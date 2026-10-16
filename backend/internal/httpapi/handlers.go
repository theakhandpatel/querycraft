package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	version := BuildVersion
	if v, err := s.exec.Version(r.Context()); err == nil && strings.TrimSpace(v) != "" {
		version = v
	}
	// `engine` is the dialect of the executor that answers `/query` with no
	// dialect field (the default), and `dialects` is every dialect this gateway
	// can actually route to. Hard-coding "postgresql" would be a lie the moment
	// a second dialect is attached, and the client uses this list to decide
	// which chapters it may offer.
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"engine":   engine.DialectOf(s.exec),
		"version":  version,
		"dialects": s.Dialects(),
		// True only when the seeded default administrator can be signed in with
		// an empty password. A signed-in learner cannot read the gateway's
		// config, so the client asks here rather than guessing; the sign-in
		// screen shows the local-admin hint for this boolean and no other
		// reason. Additive and non-secret: it is a boolean, never a password or
		// a hash.
		"admin_password_empty": s.adminPasswordEmpty,
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"hard_timeout":   true,
		"explain":        true,
		"max_concurrent": s.cfg.MaxConcurrent,
		// What isolation a statement actually runs under depends on the caller,
		// so the honest answer names both halves rather than the weaker one: an
		// anonymous statement runs read-only against the shared dataset, and a
		// signed-in learner's runs in their own writable workspace (a PostgreSQL
		// environment per ADR 0003, or their own SQLite file per ADR 0006).
		// Reporting "read_only_tx" flatly was false for every signed-in learner.
		"isolation": map[string]any{
			"anonymous": "read_only_tx",
			"signed_in": "writable_workspace",
			"writable":  s.envs != nil || s.sqlite != nil,
		},
		// The dialects this gateway can run, so a chapter written for another
		// engine is refused by the UI before the learner types anything (M6
		// step 1). PostgreSQL is always present and always the default.
		"dialects": s.Dialects(),
		// Whether a signed-in learner gets a writable workspace on this
		// gateway. The client cannot know this on its own — a session is not a
		// workspace — so the gateway answers the half of the question it owns
		// and the client answers the other half (does it have a session).
		// A writable workspace is either a PostgreSQL environment (ADR 0003) or
		// the learner's own SQLite file, so either one makes this true.
		"writable": s.envs != nil || s.sqlite != nil,
		// The M5 plan lab (design 14): what a client needs to know before it
		// offers an Explain button, and why it is off when it is off.
		"plan": s.planCapability(),
		// AI is a capability, not an assumption (technical design 11.2). A
		// client hides the lab when `available` is false and shows `reason`
		// instead, so "AI is off" is never mistaken for a broken feature.
		"ai": s.aiAvailability(),
	})
}

// statementRequest is the routing half the three statement routes share
// (backend/internal/httpapi/dataset.go resolves it): the dialect to run in and
// the dataset to run against. /explain declares the same two fields in its own
// body and resolves them through the same helper, so the rules cannot drift.
type statementRequest struct {
	Dialect string `json:"dialect"`
	Dataset string `json:"dataset"`
}

// perfMarker is the exercise's PerfContract, read only to route a plan lesson
// to the perf dataset when the request names no dataset itself. It is the same
// "exercise" object the evaluator reads; see evaluateRequest.UnmarshalJSON.
type perfMarker struct {
	Exercise *planExercise `json:"exercise"`
}

// queryRequest is the POST /query body.
type queryRequest struct {
	statementRequest
	SQL      string        `json:"sql"`
	Limits   engine.Limits `json:"limits"`
	Exercise *planExercise `json:"exercise"`
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if !readJSON(w, r, &req) {
		return
	}
	start := time.Now()
	limits := s.limits(req.Limits)
	// The concurrency budget is spent out of the same deadline the query gets,
	// so a gateway at capacity fails fast with QUERY_TIMEOUT instead of either
	// queueing forever or letting a request wait longer than its own timeout.
	ctx, cancel := context.WithTimeout(r.Context(), limits.Timeout())
	defer cancel()
	release, admitted := s.enter(ctx)
	if !admitted {
		res := s.busyResult(ctx, start)
		s.recordStatement(res)
		writeJSON(w, http.StatusBadRequest, res)
		return
	}
	defer release()

	// ADR 0003: a signed-in learner's statement runs in their own writable
	// environment; an anonymous caller stays on the shared, read-only dataset.
	// The dialect routes to the executor that speaks it (M6 step 4).
	exec, refusal := s.statementExecutorFor(ctx, r, requestedDialect(req.Dialect), resolveDataset(req.Dataset, req.Exercise))
	if refusal != nil {
		refusal.DurationMS = engine.RoundMS(time.Since(start))
		s.recordStatement(*refusal)
		writeJSON(w, http.StatusBadRequest, refusal)
		return
	}

	res := engine.Execute(ctx, exec, req.SQL, limits)
	s.recordStatement(res)
	status := http.StatusOK
	if res.Status != "succeeded" {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, res)
}

func (s *Server) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	var req evaluateRequest
	if !readJSON(w, r, &req) {
		return
	}
	start := time.Now()
	req.Limits = s.limits(req.Limits)
	ctx, cancel := context.WithTimeout(r.Context(), req.Limits.Timeout())
	defer cancel()
	// Grading shares the statement budget with /query: it runs the learner's
	// SQL (and, for an exercise with no expected result, its reference query)
	// on the same connection pool.
	release, admitted := s.enter(ctx)
	if !admitted {
		qr := s.busyResult(ctx, start)
		s.recordStatement(qr)
		writeJSON(w, http.StatusOK, eval.Response{
			QR:         qr,
			Checks:     []eval.CheckResult{},
			Passed:     false,
			DurationMS: fmt.Sprintf("%.1f", engine.RoundMS(time.Since(start))),
		})
		return
	}
	defer release()

	// Grading runs the learner's SQL (and, for an exercise without an expected
	// result, its reference query) on the same executor the identity and the
	// dialect route to, so a signed-in learner is graded against their own
	// workspace and a SQLite chapter is graded by SQLite.
	exec, refusal := s.statementExecutorFor(ctx, r, requestedDialect(req.Dialect), resolveDataset(req.Dataset, req.Exercise))
	if refusal != nil {
		refusal.DurationMS = engine.RoundMS(time.Since(start))
		s.recordStatement(*refusal)
		writeJSON(w, http.StatusOK, eval.Response{
			QR:         *refusal,
			Checks:     []eval.CheckResult{},
			Passed:     false,
			DurationMS: fmt.Sprintf("%.1f", engine.RoundMS(time.Since(start))),
		})
		return
	}

	resp := eval.Evaluate(ctx, exec, req.Request)
	s.recordStatement(resp.QR)
	writeJSON(w, http.StatusOK, resp)
}

// evaluateRequest is the POST /answers/evaluate body: the evaluator's own
// request plus the routing half the three statement routes share. The embedding
// inlines eval.Request's fields, so an existing client's body decodes unchanged
// and a new one may add "dialect" and "dataset".
//
// The body's single "exercise" key carries two things at once — the evaluator's
// exercise spec and the perf marker that routes the statement to the perf
// dataset — so the request is decoded twice, each pass reading the shape it
// knows. A single reflection-based decode cannot hold both, because the two
// fields would share one JSON key.
type evaluateRequest struct {
	eval.Request
	statementRequest
	// Exercise is the perf marker, filled by UnmarshalJSON. It has no JSON key
	// of its own precisely because that key is read into eval.Request.Exercise.
	Exercise *planExercise `json:"-"`
}

// UnmarshalJSON fills the evaluator request and the routing half from the one
// body, each from the shape it knows.
func (req *evaluateRequest) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &req.Request); err != nil {
		return err
	}
	var routing struct {
		statementRequest
		perfMarker
	}
	if err := json.Unmarshal(data, &routing); err != nil {
		return err
	}
	req.statementRequest = routing.statementRequest
	req.Exercise = routing.perfMarker.Exercise
	return nil
}

func (s *Server) handleTables(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, ok := dataset.Tables(id)
	if !ok {
		notFound(w, "unknown dataset: "+id)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	course := trimCourse(r.PathValue("course"))
	snap, ok, err := s.store.LoadProgress(r.Context(), u.ID, course)
	if err != nil {
		persistenceFailed(w, err)
		return
	}
	out := map[string]any{"ok": true, "snapshot": nil}
	if ok && len(snap) > 0 {
		var decoded any
		if err := json.Unmarshal(snap, &decoded); err == nil {
			out["snapshot"] = decoded
		} else {
			out["snapshot"] = string(snap)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutProgress(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	course := trimCourse(r.PathValue("course"))
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": map[string]string{"code": "PERSISTENCE_FAILED", "message": "could not read body"},
		})
		return
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || !json.Valid(raw) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": map[string]string{"code": "PERSISTENCE_FAILED", "message": "progress snapshot must be valid JSON"},
		})
		return
	}
	if err := s.store.SaveProgress(r.Context(), u.ID, course, json.RawMessage(raw)); err != nil {
		persistenceFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		persistenceBadRequest(w, "could not read body")
		return
	}
	var meta struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		persistenceBadRequest(w, "event body must be a JSON object")
		return
	}
	id := strings.TrimSpace(meta.ID)
	if id == "" {
		persistenceBadRequest(w, "event id is required")
		return
	}
	if err := s.store.SaveEvent(r.Context(), u.ID, id, json.RawMessage(raw)); err != nil {
		persistenceFailed(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "id": id})
}
