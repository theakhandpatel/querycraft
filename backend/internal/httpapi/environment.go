package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/schema"
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

// errorObject is the one error object the gateway's refusals carry: a stable
// code, a learner-facing message, and optional redacted detail.
func errorObject(code, message, detail string) map[string]any {
	obj := map[string]any{"code": code, "message": message}
	if detail != "" {
		obj["detail"] = detail
	}
	return obj
}

// writeErrorEnvelope writes the {ok:false,error} shape the workspace routes use,
// for a refusal that is the route's own rather than a statement's. It exists so a
// non-statement route does not answer a 400 with a bare QueryResult, which is a
// different contract shape (and the OpenAPI documents these as ErrorEnvelope).
func writeErrorEnvelope(w http.ResponseWriter, status int, code, message, detail string) {
	noteError(w, code)
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": errorObject(code, message, detail),
	})
}

// errorEnvelope writes the same shape without the machine detail.
func errorEnvelope(w http.ResponseWriter, status int, code, message string) {
	writeErrorEnvelope(w, status, code, message, "")
}

// courseSetupFailed reports a reset whose course setup did not apply. It carries
// the redacted machine detail the plain errorEnvelope has no room for.
func courseSetupFailed(w http.ResponseWriter, err error) {
	writeErrorEnvelope(w, http.StatusInternalServerError, engine.CodeEnvironmentUnavailable,
		"The course setup could not be applied to the learner workspace.", engine.RedactSecrets(err.Error()))
}

// environmentUnavailable reports that no workspace can be provided. It uses the
// contract's closed error enum, so the code is never invented.
func environmentUnavailable(w http.ResponseWriter, err error) {
	writeErrorEnvelope(w, http.StatusServiceUnavailable, engine.CodeEnvironmentUnavailable,
		"The learner workspace is unavailable.", engine.RedactSecrets(err.Error()))
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
// no workspace and is told so (exists=false, writable=false) rather than being
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
// caller's own workspace only. Anonymous callers have nothing to reset, and the
// routing table refuses them with AUTH_REQUIRED before this handler runs.
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
		// a reset is not a statement, so a refusal is the route's own shape. A
		// bare QueryResult (dialect+engine+request_id) is the shape this route
		// never returns elsewhere, and DIALECT_MISMATCH belongs in the body the
		// OpenAPI documents as ErrorEnvelope.
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
	if courseID != "" && perf {
		errorEnvelope(w, http.StatusBadRequest, engine.CodeEnvironmentUnavailable,
			"A course runs in its own workspace, so it cannot be combined with the "+dataset.PerfDatasetID+" dataset.")
		return
	}
	setup, ok := s.resolveCourseSetup(w, r, u.ID, courseID)
	if !ok {
		return
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
	// does not exist". The apply goes through the same once-per-generation path
	// the prepare route uses, so it also leaves the mark that makes a later
	// course open a no-op. A statement that fails is reported, never swallowed:
	// a reset that did not take must not be reported as one.
	applied := 0
	if courseID != "" {
		_, err = s.envs.PrepareCourse(r.Context(), u.ID, courseID, func(exec engine.Executor) error {
			var applyErr error
			applied, applyErr = applyCourseSetup(r.Context(), exec, setup)
			return applyErr
		})
		if err != nil {
			if errors.Is(err, engine.ErrEnvironmentUnavailable) {
				environmentUnavailable(w, err)
				return
			}
			courseSetupFailed(w, err)
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
	// time of its own; the reset time is the moment of this call.
	body.ResetAt = rfc3339(time.Now())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"duration_ms":      float64(took) / float64(time.Millisecond),
		"setup_statements": applied,
		"environment":      body,
	})
}

// handleEnvironmentPrepare ensures the caller's workspace exists and applies a
// stored course's own setup to it once per environment generation.
//
// A stored course's exercises were verified in a scratch environment that had
// the course's setup applied. Opening the course issued no such request before,
// so the learner's first exercise died with "relation does not exist" until
// they reset by hand. This route is what the course-open warm-up calls: it
// resolves the course owner-scoped, ensures the workspace, and applies the
// setup through the manager's once-per-generation mark.
//
// The route is idempotent. A second call for the same course and workspace
// reports `setup_statements: 0` because it applied nothing, not because the
// setup is missing. A `reset` clears the mark, so the course is prepared again.
//
// When no workspace can be provided the answer is the contract's
// ENVIRONMENT_UNAVAILABLE and nothing is invented; the course still opens and
// the first Run reports the same code.
func (s *Server) handleEnvironmentPrepare(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	courseID := strings.TrimSpace(r.URL.Query().Get("course"))
	if courseID == "" {
		errorEnvelope(w, http.StatusBadRequest, engine.CodeEnvironmentUnavailable,
			"Name a stored course to prepare its workspace.")
		return
	}
	// Resolve the course before touching the workspace, the same rule the reset
	// route uses: a course this caller does not own is refused with 404 and
	// prepares nothing, so it cannot be probed for existence.
	setup, ok := s.resolveCourseSetup(w, r, u.ID, courseID)
	if !ok {
		return
	}
	dialect := requestedDialect(r.URL.Query().Get("dialect"))
	if dialect == engine.DialectSQLite {
		s.prepareSQLiteEnvironment(w, r, u.ID, courseID, setup)
		return
	}
	if dialect != engine.DialectPostgres {
		errorEnvelope(w, http.StatusBadRequest, engine.CodeDialectMismatch,
			fmt.Sprintf("This gateway cannot prepare a %s workspace; it serves %s here.", dialect, engine.DialectPostgres))
		return
	}
	if s.envs == nil {
		environmentUnavailable(w, fmt.Errorf("%w: no writable workspace manager is configured", engine.ErrEnvironmentUnavailable))
		return
	}
	applied := 0
	_, err := s.envs.PrepareCourse(r.Context(), u.ID, courseID, func(exec engine.Executor) error {
		var applyErr error
		applied, applyErr = applyCourseSetup(r.Context(), exec, setup)
		return applyErr
	})
	if err != nil {
		if errors.Is(err, engine.ErrEnvironmentUnavailable) {
			environmentUnavailable(w, err)
			return
		}
		courseSetupFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"setup_statements": applied,
	})
}

// prepareSQLiteEnvironment is the SQLite half of prepare. It resolves nothing
// itself: the caller has already resolved the course and its setup, and this
// applies it once per SQLite file generation.
func (s *Server) prepareSQLiteEnvironment(w http.ResponseWriter, r *http.Request, ownerID, courseID string, setup schema.SetupPlan) {
	if s.sqlite == nil {
		environmentUnavailable(w, fmt.Errorf("%w: no SQLite workspace store is configured", engine.ErrEnvironmentUnavailable))
		return
	}
	applied := 0
	_, err := s.sqlite.PrepareCourse(r.Context(), ownerID, courseID, func(exec engine.Executor) error {
		var applyErr error
		applied, applyErr = applyCourseSetup(r.Context(), exec, setup)
		return applyErr
	})
	if err != nil {
		if errors.Is(err, engine.ErrEnvironmentUnavailable) {
			environmentUnavailable(w, err)
			return
		}
		courseSetupFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"setup_statements": applied,
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
// surfaces rather than being swallowed, identical to the PostgreSQL path.
func (s *Server) resetSQLiteEnvironment(w http.ResponseWriter, r *http.Request, ownerID string) {
	if s.sqlite == nil {
		environmentUnavailable(w, fmt.Errorf("%w: no SQLite workspace store is configured", engine.ErrEnvironmentUnavailable))
		return
	}
	courseID := strings.TrimSpace(r.URL.Query().Get("course"))
	setup, ok := s.resolveCourseSetup(w, r, ownerID, courseID)
	if !ok {
		return
	}
	_, took, err := s.sqlite.Reset(r.Context(), ownerID)
	if err != nil {
		environmentUnavailable(w, err)
		return
	}
	applied := 0
	if courseID != "" {
		_, err = s.sqlite.PrepareCourse(r.Context(), ownerID, courseID, func(exec engine.Executor) error {
			var applyErr error
			applied, applyErr = applyCourseSetup(r.Context(), exec, setup)
			return applyErr
		})
		if err != nil {
			if errors.Is(err, engine.ErrEnvironmentUnavailable) {
				environmentUnavailable(w, err)
				return
			}
			courseSetupFailed(w, err)
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
			ResetAt:  rfc3339(time.Now()),
		},
	})
}

// resolveCourseSetup resolves the named course owner-scoped and returns its
// setup plan. A request that names no course applies no setup. It writes the
// refusal and reports false when the course is unknown or belongs to another
// account, the same rule GET /courses/{id} applies.
func (s *Server) resolveCourseSetup(w http.ResponseWriter, r *http.Request, ownerID, courseID string) (schema.SetupPlan, bool) {
	if courseID == "" {
		return schema.SetupPlan{}, true
	}
	var setup schema.SetupPlan
	found, err := s.courseSetup(r.Context(), ownerID, courseID, &setup)
	if err != nil {
		environmentUnavailable(w, err)
		return schema.SetupPlan{}, false
	}
	// 404, never 403: a learner must not be able to confirm that another
	// learner's course exists.
	if !found {
		notFound(w, "unknown course: "+courseID)
		return schema.SetupPlan{}, false
	}
	return setup, true
}

// courseSetup resolves the named course owner-scoped and copies its setup plan
// into out. It is the same rule GET /courses/{id} applies: the record must
// exist and belong to the caller, and anything else is reported as missing.
func (s *Server) courseSetup(ctx context.Context, ownerID, courseID string, out *schema.SetupPlan) (bool, error) {
	depot := s.aiCourseDepot()
	if depot == nil {
		return false, nil
	}
	rec, owned, err := ownedCourse(ctx, depot, ownerID, courseID)
	if err != nil {
		return false, err
	}
	if !owned {
		return false, nil
	}
	*out = rec.Plan.Setup
	return true, nil
}

// applyCourseSetup runs a course's setup statements in the learner's freshly
// reset environment, in order, and returns how many were applied. It mirrors
// the pipeline's own prepare step over the same engine seam, so the two cannot
// disagree about what "the setup applied" means.
func applyCourseSetup(ctx context.Context, ex engine.Executor, setup schema.SetupPlan) (int, error) {
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
