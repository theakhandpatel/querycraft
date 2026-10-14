// Package httpapi is Querycraft's Adapter B gateway: the JSON HTTP surface the
// React client talks to. It owns transport concerns (routing, CORS, request
// ids, JSON, limits) and delegates execution to engine and grading to eval.
// Endpoints and payloads mirror contracts/*.schema.json and the client's
// src/api/httpApi.js.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/store"
	"github.com/querycraft/sqlquery/backend/internal/webui"
)

// BuildVersion is reported by GET /health; override with -ldflags "-X ...".
var BuildVersion = "dev"

// Config holds the gateway's tunable limits and advertised capabilities.
type Config struct {
	MaxRows       int
	MaxBytes      int
	TimeoutMS     int
	MaxConcurrent int
}

// Server wires an Executor and a Store behind the HTTP surface.
type Server struct {
	exec  engine.Executor
	store store.Store
	cfg   Config
	log   *slog.Logger
}

// New builds a Server, filling unset config from the engine defaults.
func New(exec engine.Executor, st store.Store, cfg Config, log *slog.Logger) *Server {
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = engine.DefaultMaxRows
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = engine.DefaultMaxBytes
	}
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = engine.DefaultTimeoutMS
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 4
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{exec: exec, store: st, cfg: cfg, log: log}
}

// Handler returns the routed, CORS-wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /auth/signup", s.handleSignup)
	mux.HandleFunc("POST /auth/login", s.handleLogin)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /auth/me", s.handleMe)
	mux.HandleFunc("GET /capabilities", s.handleCapabilities)
	mux.HandleFunc("POST /query", s.handleQuery)
	mux.HandleFunc("POST /answers/evaluate", s.handleEvaluate)
	mux.HandleFunc("GET /datasets/{id}/tables", s.handleTables)
	mux.HandleFunc("GET /progress/{course}", s.handleGetProgress)
	mux.HandleFunc("PUT /progress/{course}", s.handlePutProgress)
	mux.HandleFunc("POST /events", s.handleEvent)
	mux.HandleFunc("POST /courses/generate", s.handleGenerateCourse)
	mux.HandleFunc("POST /challenges/generate", s.handleGenerateChallenge)
	mux.HandleFunc("GET /openapi.json", s.handleOpenAPI)
	mux.HandleFunc("GET /docs", s.handleDocs)
	mux.HandleFunc("GET /docs/", s.handleDocs)

	// Catch-all for the embedded SPA. In Go 1.22+ ServeMux the more specific
	// patterns above (including /health, /query, /docs, /openapi.json) still
	// win, so the UI only handles paths no API route claims.
	if webui.Available() {
		mux.Handle("GET /", webui.Handler())
	} else {
		mux.Handle("GET /{$}", webui.Placeholder())
	}
	// withAuth sits inside logging/request-id so a resolved account is visible to
	// every handler and to the access log's context, and inside CORS so that an
	// OPTIONS preflight short-circuits before any database lookup.
	return withCORS(s.withRequest(s.withLogging(s.withAuth(mux))))
}

/* ---------- middleware ---------- */

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		h.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(engine.WithRequestID(r.Context(), id)))
	})
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"ms", engine.RoundMS(time.Since(start)),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

/* ---------- helpers ---------- */

func newRequestID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		// rand/v2's package-level generator is safe for concurrent use; a
		// shared *rand.Rand is not, and the gateway handles requests in
		// parallel goroutines.
		b[i] = chars[rand.IntN(len(chars))]
	}
	return "rq_" + string(b)
}

func (s *Server) limits(l engine.Limits) engine.Limits {
	return l.Merge(engine.Limits{MaxRows: s.cfg.MaxRows, MaxBytes: s.cfg.MaxBytes, TimeoutMS: s.cfg.TimeoutMS})
}

const maxBodyBytes = 8 << 20 // 8 MiB request cap

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"code": "EMPTY_QUERY", "message": "Request body is empty."},
			})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"code": "QUERY_SYNTAX_ERROR", "message": "Malformed JSON: " + err.Error()},
		})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func notFound(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": map[string]string{"code": "NOT_FOUND", "message": message},
	})
}

func persistenceFailed(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"ok":    false,
		"error": map[string]string{"code": "PERSISTENCE_FAILED", "message": err.Error()},
	})
}

func persistenceBadRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"ok":    false,
		"error": map[string]string{"code": "PERSISTENCE_FAILED", "message": message},
	})
}

func trimCourse(name string) string {
	return strings.TrimSpace(name)
}
