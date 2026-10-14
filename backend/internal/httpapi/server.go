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

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
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

	// metrics is the instrumentation behind GET /metrics. It is created in New,
	// never nil, so no handler needs a nil check.
	metrics *metrics.Registry

	// sem enforces Config.MaxConcurrent. It bounds statements only: reading
	// progress, listing tables and the SPA must stay available while the
	// database is saturated, which is exactly when an operator looks at them.
	sem chan struct{}

	// envs is the per-account writable-workspace manager (ADR 0003). It is nil
	// when the gateway has no database — or no administrative DSN to copy the
	// sealed template — in which case a signed-in learner is refused with
	// ENVIRONMENT_UNAVAILABLE rather than handed an invented workspace. It is
	// set once, before the handler is served, with UseEnvironments.
	envs *envdb.Manager
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
	srv := &Server{
		exec:    exec,
		store:   st,
		cfg:     cfg,
		log:     log,
		metrics: metrics.New(),
		sem:     make(chan struct{}, cfg.MaxConcurrent),
	}
	srv.declareMetrics()
	return srv
}

// Registry exposes the instrumentation so a test can read a counter directly
// instead of scraping it back out of the text format.
func (s *Server) Registry() *metrics.Registry { return s.metrics }

// Handler returns the routed, CORS-wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /diagnostics", s.handleDiagnostics)
	mux.HandleFunc("POST /auth/signup", s.handleSignup)
	mux.HandleFunc("POST /auth/login", s.handleLogin)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /auth/me", s.handleMe)
	mux.HandleFunc("GET /capabilities", s.handleCapabilities)
	mux.HandleFunc("POST /query", s.handleQuery)
	mux.HandleFunc("POST /answers/evaluate", s.handleEvaluate)
	mux.HandleFunc("GET /environment", s.handleEnvironment)
	mux.HandleFunc("POST /environment/reset", s.handleEnvironmentReset)
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
	// OPTIONS preflight short-circuits before any database lookup. withMetrics is
	// outside withAuth so an unauthenticated request is still measured, and it
	// resolves the route from the mux rather than the handler chain so that
	// requests answered before routing carry a route label from a bounded set
	// instead of an attacker-chosen path.
	return withCORS(s.withRequest(s.withObservability(s.withAuth(mux), routeOfMux(mux))))
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

// writeJSON writes a JSON body, and — when the body carries a stable error code
// — records that code for the access log. Doing it here rather than in each
// handler means a new endpoint cannot quietly ship without an error_code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	noteError(w, errorCode(v))
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
