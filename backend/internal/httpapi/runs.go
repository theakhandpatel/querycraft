package httpapi

import (
	"net/http"

	"github.com/querycraft/sqlquery/backend/internal/ai"
)

/* The agent-run audit trail, read over HTTP.
 *
 * A generation run leaves a transcript in app.runs (see backend/internal/ai
 * transcript.go). These two routes expose it to the account that owns the run:
 *
 *   GET /runs       -> the caller's runs, newest first, as summaries
 *   GET /runs/{id}  -> one run's whole transcript
 *
 * Both require a session, and both are owner-scoped. A run that belongs to
 * another account is reported as unknown — 404, never 403 — the same rule the
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
func runView(rec ai.RunTranscript) ai.RunTranscript {
	rec.OwnerID = ""
	return rec
}

// runSummaryView is the list shape with the same owner rule.
func runSummaryView(s ai.RunSummary) ai.RunSummary {
	s.OwnerID = ""
	return s
}

// runsOff writes the honest "no transcript store" answer, which is the same
// answer the other generation routes give when the lab is off.
func runsOff(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"ok":   false,
		"runs": []ai.RunSummary{},
		"error": map[string]string{
			"code":    ai.CodeUnavailable,
			"message": "Generation is off: no generation service is configured.",
		},
	})
}

// handleListRuns implements GET /runs.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		runsOff(w)
		return
	}
	summaries, err := depot.Runs(r.Context())
	if err != nil {
		writeAIError(w, err)
		return
	}
	// The depot is scoped on write, not on read, so a list is filtered here to
	// the caller's own runs: a list is never another learner's evidence.
	mine := make([]ai.RunSummary, 0, len(summaries))
	for _, sm := range summaries {
		if sm.OwnerID != u.ID {
			continue
		}
		mine = append(mine, runSummaryView(sm))
		if len(mine) >= ai.MaxRunList {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runs": mine})
}

// handleGetRun implements GET /runs/{id}.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		writeAIError(w, &ai.Error{Code: ai.CodeUnavailable, Message: "Generation is off: no generation service is configured."})
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
