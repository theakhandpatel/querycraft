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
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"engine":  "postgresql",
		"version": version,
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"hard_timeout":   true,
		"explain":        false,
		"max_concurrent": s.cfg.MaxConcurrent,
		"isolation":      "read_only_tx",
	})
}

// queryRequest is the POST /query body.
type queryRequest struct {
	SQL    string        `json:"sql"`
	Limits engine.Limits `json:"limits"`
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
	exec, err := s.statementExecutor(ctx, r)
	if err != nil {
		res := engine.Failing(engine.CodeEnvironmentUnavailable,
			"The learner workspace is unavailable.", engine.RedactSecrets(err.Error()),
			engine.RequestIDFromContext(ctx))
		res.DurationMS = engine.RoundMS(time.Since(start))
		s.recordStatement(res)
		writeJSON(w, http.StatusBadRequest, res)
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
	var req eval.Request
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
	// result, its reference query) on the same executor the identity routes to,
	// so a signed-in learner is graded against their own workspace.
	exec, err := s.statementExecutor(ctx, r)
	if err != nil {
		qr := engine.Failing(engine.CodeEnvironmentUnavailable,
			"The learner workspace is unavailable.", engine.RedactSecrets(err.Error()),
			engine.RequestIDFromContext(ctx))
		qr.DurationMS = engine.RoundMS(time.Since(start))
		s.recordStatement(qr)
		writeJSON(w, http.StatusOK, eval.Response{
			QR:         qr,
			Checks:     []eval.CheckResult{},
			Passed:     false,
			DurationMS: fmt.Sprintf("%.1f", engine.RoundMS(time.Since(start))),
		})
		return
	}

	resp := eval.Evaluate(ctx, exec, req)
	s.recordStatement(resp.QR)
	writeJSON(w, http.StatusOK, resp)
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

func (s *Server) handleGenerateCourse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string   `json:"title"`
		Exercises int      `json:"exercises"`
		Steps     []string `json:"steps"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req)
	writeJSON(w, http.StatusCreated, map[string]any{
		"title":          req.Title,
		"chapters":       len(req.Steps),
		"exercises":      req.Exercises,
		"datasetVersion": dataset.DatasetVersion,
	})
}

func (s *Server) handleGenerateChallenge(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxBodyBytes))
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}
