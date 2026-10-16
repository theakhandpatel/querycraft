package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
)

/* ADR 0003: learner SQL may write, but only inside a per-account database.
 *
 * A signed-in learner is routed to their own environment database, copied from
 * the sealed template and created on first use; an anonymous visitor keeps the
 * shared, read-only dataset. The two routes here report and restore that
 * workspace. They never invent one: an anonymous caller is told they have no
 * environment, and a gateway with no database (or no environment manager)
 * answers ENVIRONMENT_UNAVAILABLE instead of pretending.
 */

// UseEnvironments attaches the per-account writable-workspace manager. It must
// be called before Handler() serves requests; a nil manager (the default) means
// the gateway cannot offer a workspace, and a signed-in learner is refused with
// ENVIRONMENT_UNAVAILABLE. Anonymous visitors are unaffected either way: they
// stay on the shared, read-only dataset.
func (s *Server) UseEnvironments(m *envdb.Manager) { s.envs = m }

// statementExecutor returns the executor that should run the caller's SQL.
//
// A signed-in learner runs in their own writable environment (created on first
// use); an anonymous caller runs on the shared, read-only dataset. An error
// means no workspace could be provided, which the caller reports honestly
// rather than falling back to a read-only connection that would silently refuse
// INSERT.
func (s *Server) statementExecutor(ctx context.Context, r *http.Request) (engine.Executor, error) {
	u, ok := userFrom(r.Context())
	if !ok {
		return s.exec, nil
	}
	if s.envs == nil {
		return nil, fmt.Errorf("%w: no writable workspace manager is configured", engine.ErrEnvironmentUnavailable)
	}
	return s.envs.Ensure(ctx, u.ID)
}

// environmentBody is the wire shape the client agreed on. exists and writable
// are always present; the rest are facts read from the environment when it
// exists. Times are RFC 3339 strings so an absent value is omitted rather than
// serialised as the zero time.
type environmentBody struct {
	Account   string `json:"account,omitempty"`
	Database  string `json:"database,omitempty"`
	Exists    bool   `json:"exists"`
	Writable  bool   `json:"writable"`
	Tables    int    `json:"tables,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	ResetAt   string `json:"reset_at,omitempty"`
	IdleUntil string `json:"idle_until,omitempty"`
}

func factsBody(facts envdb.Facts) environmentBody {
	body := environmentBody{
		Account:  facts.Account,
		Database: facts.Database,
		Exists:   facts.Exists,
		Writable: facts.Writable,
	}
	if facts.Exists {
		body.Tables = facts.Tables
	}
	body.CreatedAt = rfc3339(facts.CreatedAt)
	body.ResetAt = rfc3339(facts.ResetAt)
	body.IdleUntil = rfc3339(facts.IdleUntil)
	return body
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// errorEnvelope writes the {ok:false,error} shape the workspace routes use, for
// a refusal that is the route's own rather than a statement's. It exists so a
// non-statement route does not answer a 400 with a bare QueryResult, which is a
// different contract shape (and the OpenAPI documents these as ErrorEnvelope).
func errorEnvelope(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"ok": false,
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// environmentUnavailable reports that no workspace can be provided. It uses the
// contract's closed error enum, so the code is never invented.
func environmentUnavailable(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"ok": false,
		"error": map[string]string{
			"code":    engine.CodeEnvironmentUnavailable,
			"message": "The learner workspace is unavailable.",
			"detail":  engine.RedactSecrets(err.Error()),
		},
	})
}

// workspaceRefusal is the one shape a statement route answers with when no
// workspace can be prepared: the contract's closed ENVIRONMENT_UNAVAILABLE code,
// a learner-facing sentence, and the redacted machine detail underneath it. It
// is shared by the commerce route, the SQLite route and the perf route so the
// three cannot drift apart.
func workspaceRefusal(ctx context.Context, message string, err error) engine.QueryResult {
	return engine.Failing(engine.CodeEnvironmentUnavailable,
		message, engine.RedactSecrets(err.Error()), engine.RequestIDFromContext(ctx))
}

// handleEnvironment reports the caller's workspace facts.
//
// A signed-in learner's environment is created lazily here on first use, so the
// answer is the truth about a database that now exists. An anonymous caller has
// no workspace and is told so — exists=false, writable=false — rather than being
// shown the shared dataset as if it were theirs.
func (s *Server) handleEnvironment(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"environment": environmentBody{},
		})
		return
	}
	if s.envs == nil {
		environmentUnavailable(w, fmt.Errorf("%w: no writable workspace manager is configured", engine.ErrEnvironmentUnavailable))
		return
	}
	if _, err := s.envs.Ensure(r.Context(), u.ID); err != nil {
		environmentUnavailable(w, err)
		return
	}
	facts, err := s.envs.Facts(r.Context(), u.ID)
	if err != nil {
		environmentUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"environment": factsBody(facts),
	})
}

// handleEnvironmentReset drops the caller's environment and copies the sealed
// template again, reporting the measured duration. Reset is destructive to the
// caller's own workspace only. Anonymous callers have nothing to reset and are
// refused with AUTH_REQUIRED; the refusal says writes require an account.
//
// A course may be named (?course=<id>): its setup is what the exercises were
// verified against, so the learner's environment would not be the verified one
// without it. The course is resolved owner-scoped through the same rule
// GET /courses/{id} uses, and a request that names no course applies none.
//
// The dialect travels with the request (?dialect=sqlite) for the same reason it
// travels with a statement: a SQLite chapter's workspace is a file, not a
// database, and resetting the PostgreSQL workspace while the learner is looking
// at a SQLite chapter would answer `ok` for a restore that did not happen. An
// absent dialect is PostgreSQL, so a client that has never heard of `dialect`
// keeps the behaviour it had.
func (s *Server) handleEnvironmentReset(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	dialect := requestedDialect(r.URL.Query().Get("dialect"))
	if dialect == engine.DialectSQLite {
		s.resetSQLiteEnvironment(w, r, u.ID)
		return
	}
	if dialect != engine.DialectPostgres {
		// The reset route answers in the {ok,error} envelope, not a QueryResult:
		// a reset is not a statement, so a refusal is the route's own shape. The
		// 400 used to carry a bare QueryResult (dialect+engine+request_id), which
		// is the shape this route never returns elsewhere — and put
		// DIALECT_MISMATCH in a body the OpenAPI documents as ErrorEnvelope.
		errorEnvelope(w, http.StatusBadRequest, engine.CodeDialectMismatch,
			fmt.Sprintf("This gateway cannot reset a %s workspace; it serves %s here.", dialect, engine.DialectPostgres))
		return
	}
	if s.envs == nil {
		environmentUnavailable(w, fmt.Errorf("%w: no writable workspace manager is configured", engine.ErrEnvironmentUnavailable))
		return
	}
	// M5. A plan lesson runs in its own database, so "restore my data" means a
	// different database depending on the course the learner is looking at. The
	// dataset is resolved by the same rules a statement uses, and a name this
	// gateway does not serve is refused by name rather than silently resetting
	// the challenge workspace the learner did not ask about.
	requested := resolveDataset(r.URL.Query().Get("dataset"), nil)
	perf := requested == dataset.PerfDatasetID
	// Resolve the course before touching the workspace: a request that names a
	// course this caller does not own is refused without destroying the data
	// they asked to restore.
	courseID := strings.TrimSpace(r.URL.Query().Get("course"))
	var setup ai.SetupPlan
	if courseID != "" {
		if perf {
			errorEnvelope(w, http.StatusBadRequest, engine.CodeEnvironmentUnavailable,
				"A course runs in its own workspace, so it cannot be combined with the "+dataset.PerfDatasetID+" dataset.")
			return
		}
		found, err := s.courseSetup(r.Context(), u.ID, courseID, &setup)
		if err != nil {
			environmentUnavailable(w, err)
			return
		}
		// 404, never 403: a learner must not be able to confirm that another
		// learner's course exists.
		if !found {
			notFound(w, "unknown course: "+courseID)
			return
		}
	}
	var took time.Duration
	var err error
	if perf {
		took, err = s.envs.ResetPerf(r.Context(), u.ID)
	} else if requested != "" {
		errorEnvelope(w, http.StatusBadRequest, engine.CodeEnvironmentUnavailable, unknownDatasetMessage(requested))
		return
	} else {
		took, err = s.envs.Reset(r.Context(), u.ID)
	}
	if err != nil {
		environmentUnavailable(w, err)
		return
	}
	// The stored plan was verified in a scratch environment that had its setup
	// applied; the learner's environment is the same database, so it must have
	// the same setup or an exercise that verified green dies with "relation
	// does not exist". A statement that fails is reported, never swallowed: a
	// reset that did not take must not be reported as one.
	applied := 0
	if courseID != "" {
		exec, execErr := s.envs.Ensure(r.Context(), u.ID)
		if execErr != nil {
			environmentUnavailable(w, execErr)
			return
		}
		applied, err = applyCourseSetup(r.Context(), exec, setup)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok": false,
				"error": map[string]string{
					"code":    engine.CodeEnvironmentUnavailable,
					"message": "The course setup could not be applied to the learner workspace.",
					"detail":  engine.RedactSecrets(err.Error()),
				},
			})
			return
		}
	}
	body := environmentBody{Account: u.ID, Exists: true, Writable: true}
	/* Facts describes the challenge workspace, so it is only the right report
	   when that is what was reset; for a plan environment the honest body is the
	   one fact this handler can state without asking the engine anything. */
	if !perf {
		if facts, ferr := s.envs.Facts(r.Context(), u.ID); ferr == nil {
			body = factsBody(facts)
		}
	}
	// The manager discards its record on Reset, so the fresh copy has no reset
	// time of its own; the moment we reset it is the honest value.
	body.ResetAt = time.Now().UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"duration_ms":      float64(took) / float64(time.Millisecond),
		"setup_statements": applied,
		"environment":      body,
	})
}

// resetSQLiteEnvironment restores a signed-in learner's SQLite workspace file
// to the committed fixture. It is the SQLite half of ADR 0003's reset, and it
// resolves the course and applies its setup by the same rules the PostgreSQL
// path does, so a SQLite course that was verified with scratch objects behaves
// the same for the learner.
//
// A course whose setup plan is written for PostgreSQL is not applied here: the
// setup statements were verified against the course's own dialect, and a
// SQLite chapter's course carries SQLite setup. Applying a statement the engine
// cannot parse would report a reset that did not take, so a failing statement
// surfaces rather than being swallowed — identical to the PostgreSQL path.
func (s *Server) resetSQLiteEnvironment(w http.ResponseWriter, r *http.Request, ownerID string) {
	if s.sqlite == nil {
		environmentUnavailable(w, fmt.Errorf("%w: no SQLite workspace store is configured", engine.ErrEnvironmentUnavailable))
		return
	}
	courseID := strings.TrimSpace(r.URL.Query().Get("course"))
	var setup ai.SetupPlan
	if courseID != "" {
		found, err := s.courseSetup(r.Context(), ownerID, courseID, &setup)
		if err != nil {
			environmentUnavailable(w, err)
			return
		}
		// 404, never 403: a learner must not be able to confirm that another
		// learner's course exists.
		if !found {
			notFound(w, "unknown course: "+courseID)
			return
		}
	}
	ex, took, err := s.sqlite.Reset(r.Context(), ownerID)
	if err != nil {
		environmentUnavailable(w, err)
		return
	}
	applied := 0
	if courseID != "" {
		applied, err = applyCourseSetup(r.Context(), ex, setup)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok": false,
				"error": map[string]string{
					"code":    engine.CodeEnvironmentUnavailable,
					"message": "The course setup could not be applied to the learner workspace.",
					"detail":  engine.RedactSecrets(err.Error()),
				},
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"duration_ms":      float64(took) / float64(time.Millisecond),
		"setup_statements": applied,
		"environment": environmentBody{
			Account:  ownerID,
			Exists:   true,
			Writable: true,
			ResetAt:  time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// courseSetup resolves the named course owner-scoped and copies its setup plan
// into out. It is the same rule GET /courses/{id} applies: the record must
// exist and belong to the caller, and anything else is reported as missing.
func (s *Server) courseSetup(ctx context.Context, ownerID, courseID string, out *ai.SetupPlan) (bool, error) {
	depot := s.aiCourseDepot()
	if depot == nil {
		return false, nil
	}
	rec, found, err := depot.Course(ctx, courseID)
	if err != nil {
		return false, err
	}
	if !found || rec.OwnerID != ownerID {
		return false, nil
	}
	*out = rec.Plan.Setup
	return true, nil
}

// applyCourseSetup runs a course's setup statements in the learner's freshly
// reset environment, in order, and returns how many were applied. It mirrors
// the pipeline's own prepare step over the same engine seam, so the two cannot
// disagree about what "the setup applied" means.
func applyCourseSetup(ctx context.Context, ex engine.Executor, setup ai.SetupPlan) (int, error) {
	for i, stmt := range setup.Statements {
		res := engine.Execute(ctx, ex, stmt, engine.DefaultLimits())
		if res.Status != "succeeded" || res.Truncated {
			detail := "the engine gave no reason"
			if res.Error != nil {
				detail = res.Error.Code + ": " + res.Error.Message
				if res.Error.Detail != nil && *res.Error.Detail != "" {
					detail += " (" + *res.Error.Detail + ")"
				}
			}
			return i, fmt.Errorf("setup statement %d failed: %s", i, detail)
		}
	}
	return len(setup.Statements), nil
}
