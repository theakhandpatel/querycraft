package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
	res := engine.Execute(r.Context(), s.exec, req.SQL, s.limits(req.Limits))
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
	req.Limits = s.limits(req.Limits)
	resp := eval.Evaluate(r.Context(), s.exec, req)
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
