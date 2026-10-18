package httpapi

import (
	"net/http"

	"github.com/querycraft/sqlquery/backend/internal/ai/transcript"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* The agent-run audit trail, read over HTTP.
 *
 * A generation run leaves a transcript in app.runs (see
 * backend/internal/ai/transcript). These two routes expose it to the account
 * that owns the run:
 *
 *   GET /runs       -> the caller's runs, newest first, as summaries
 *   GET /runs/{id}  -> one run's whole transcript
 *
 * Both require a session, and both are owner-scoped. A run that belongs to
 * another account is reported as unknown (404, never 403), the same rule the
 * stored challenge and course routes follow: an id must not be probeable for
 * existence. The list filters on the session's own id rather than trusting
 * anything in the request.
 *
 * Reading uses the configured depot directly rather than the owner-scoped
 * wrapper, for the same reason GET /courses does: the wrapper scopes writes
 * only, and ownership is enforced here, in the handler a request reaches with
 * an id of its own choosing.
 */

// runView is a stored transcript as a client sees it. The owner is the session's
// own account, so it does not travel in the body.
func runView(rec schema.RunTranscript) schema.RunTranscript {
	rec.OwnerID = ""
	return rec
}

// runSummaryView is the list shape with the same owner rule.
func runSummaryView(s schema.RunSummary) schema.RunSummary {
	s.OwnerID = ""
	return s
}

// handleListRuns implements GET /runs.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	list(w, r, listSpec[schema.RunSummary]{
		require: s.requireUser,
		key:     "runs",
		view:    runSummaryView,
		limit:   transcript.MaxRunList,
		fetch: func(u store.User) ([]schema.RunSummary, bool) {
			depot := s.aiCourseDepot()
			if depot == nil {
				aiOff(w, "runs", []schema.RunSummary{})
				return nil, false
			}
			summaries, err := depot.RunsFor(r.Context(), u.ID)
			if err != nil {
				writeAIError(w, err)
				return nil, false
			}
			/* The store filters by owner in SQL, so this list never loads another
			   learner's runs only to discard them; the row bound is applied over
			   this owner's rows. */
			return summaries, true
		},
	})
}

// handleGetRun implements GET /runs/{id}.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	u, depot, ok := s.requireCourseDepot(w, r)
	if !ok {
		return
	}
	rec, found, err := depot.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	// 404, never 403: a learner must not be able to confirm that another
	// learner's run exists.
	if !found || rec.OwnerID != u.ID {
		notFound(w, "unknown run: "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": runView(rec)})
}
