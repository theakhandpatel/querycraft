package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

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
func (s *Server) handleEnvironmentReset(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if s.envs == nil {
		environmentUnavailable(w, fmt.Errorf("%w: no writable workspace manager is configured", engine.ErrEnvironmentUnavailable))
		return
	}
	took, err := s.envs.Reset(r.Context(), u.ID)
	if err != nil {
		environmentUnavailable(w, err)
		return
	}
	body := environmentBody{Account: u.ID, Exists: true, Writable: true}
	if facts, ferr := s.envs.Facts(r.Context(), u.ID); ferr == nil {
		body = factsBody(facts)
	}
	// The manager discards its record on Reset, so the fresh copy has no reset
	// time of its own; the moment we reset it is the honest value.
	body.ResetAt = time.Now().UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"duration_ms": float64(took) / float64(time.Millisecond),
		"environment": body,
	})
}
