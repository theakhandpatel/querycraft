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
	"sort"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/ai"
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

	// LoginMaxFailures, LoginWindowMS and LoginMaxTracked bound failed sign-in
	// attempts (see login_throttle.go). A non-positive value falls back to the
	// shipped default, so leaving them unset cannot disable the limit or leave
	// the table unbounded.
	LoginMaxFailures int
	LoginWindowMS    int
	LoginMaxTracked  int
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

	// aiCfg is the M4 generation configuration (ADR 0005). It is nil when the
	// lab is off, which is the default and a supported mode: every other
	// feature works without it. It is set once, before the handler is served,
	// with UseAI.
	aiCfg *ai.Config

	// jobs is the asynchronous-generation registry. It lives on the server
	// rather than being built per request, because a job outlives the request
	// that submitted it: a service built for one request would be gone before
	// the run it started had a chance to store anything.
	jobs *ai.Jobs

	// dialects is the list of dialects this gateway can route a statement to.
	// It is PostgreSQL alone until an adapter registers another one with
	// UseDialect: a client must never assume SQLite is served just because the
	// gateway speaks the dialect contract, and a chapter on a dialect this
	// gateway cannot run has to be refused with DIALECT_MISMATCH rather than
	// silently executed on the default engine.
	dialects []string

	// sqlite is the SQLite dialect's per-account file store (M6 step 4). It is
	// nil when no SQLite data root is configured, in which case "sqlite" is not
	// registered as a served dialect and a request for it is refused with
	// DIALECT_MISMATCH rather than answered by PostgreSQL.
	sqlite *SQLiteEnvironments

	// adminPasswordEmpty reports that the install's default administrator has no
	// password, so an empty password can sign it in. It is a boolean — never
	// the password or its hash — and it is advertised on /health so the sign-in
	// screen shows the local-admin hint only when it is actually true. It is
	// set once, before the handler is served, with UseAdminPasswordEmpty.
	adminPasswordEmpty bool

	// login bounds failed sign-in attempts per (client address, account). It is
	// created in New, never nil, so handleLogin needs no nil check. See
	// login_throttle.go.
	login *loginThrottle
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
		exec:     exec,
		store:    st,
		cfg:      cfg,
		log:      log,
		metrics:  metrics.New(),
		sem:      make(chan struct{}, cfg.MaxConcurrent),
		dialects: []string{engine.DialectPostgres},
		login:    newLoginThrottle(cfg.LoginMaxFailures, cfg.LoginWindowMS, cfg.LoginMaxTracked),
	}
	srv.declareMetrics()
	return srv
}

// UseDialect records that the gateway can serve another dialect, once and
// before the handler is served. It is idempotent and keeps the list in a stable
// order, so /capabilities does not flap between requests.
func (s *Server) UseDialect(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for _, have := range s.dialects {
		if have == name {
			return
		}
	}
	s.dialects = append(s.dialects, name)
	sort.Strings(s.dialects)
}

// Dialects reports what this gateway can serve, in stable order.
func (s *Server) Dialects() []string {
	out := make([]string, len(s.dialects))
	copy(out, s.dialects)
	return out
}

// UseSQLite attaches the SQLite dialect's file store and registers the dialect.
// It must be called before Handler() serves requests.
//
// The dialect is registered here, next to the store that serves it, and not by
// a separate UseDialect call: a gateway that advertises SQLite in
// /capabilities but cannot open a file for it is exactly the dishonesty this
// seam exists to prevent.
func (s *Server) UseSQLite(env *SQLiteEnvironments) {
	if env == nil {
		return
	}
	s.sqlite = env
	s.UseDialect(engine.DialectSQLite)
}

// Registry exposes the instrumentation so a test can read a counter directly
// instead of scraping it back out of the text format.
func (s *Server) Registry() *metrics.Registry { return s.metrics }

// UseAdminPasswordEmpty records that the seeded default administrator has no
// password. It is advertised on /health as `admin_password_empty` so a client
// can show the local-admin sign-in hint only when it is true. Call it once,
// before Handler() serves requests. It carries a boolean, never a credential.
func (s *Server) UseAdminPasswordEmpty(empty bool) { s.adminPasswordEmpty = empty }

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
	mux.HandleFunc("POST /explain", s.handleExplain)
	mux.HandleFunc("POST /answers/evaluate", s.handleEvaluate)
	mux.HandleFunc("GET /environment", s.handleEnvironment)
	mux.HandleFunc("POST /environment/reset", s.handleEnvironmentReset)
	mux.HandleFunc("GET /datasets/{id}/tables", s.handleTables)
	mux.HandleFunc("GET /progress/{course}", s.handleGetProgress)
	mux.HandleFunc("PUT /progress/{course}", s.handlePutProgress)
	mux.HandleFunc("POST /events", s.handleEvent)
	mux.HandleFunc("POST /courses/generate", s.handleGenerateCourse)
	mux.HandleFunc("GET /generation/{id}", s.handleGenerationJob)
	mux.HandleFunc("GET /courses", s.handleListCourses)
	mux.HandleFunc("GET /courses/{id}", s.handleGetCourse)
	mux.HandleFunc("POST /courses/{id}/build", s.handleBuildCourse)
	mux.HandleFunc("DELETE /courses/{id}", s.handleDeleteCourse)
	mux.HandleFunc("POST /challenges/generate", s.handleGenerateChallenge)
	mux.HandleFunc("GET /challenges", s.handleListChallenges)
	mux.HandleFunc("GET /challenges/{id}", s.handleGetChallenge)
	mux.HandleFunc("POST /challenges/{id}/reset", s.handleResetChallenge)
	mux.HandleFunc("POST /challenges/{id}/critique", s.handleCritique)
	mux.HandleFunc("POST /challenges/{id}/report", s.handleReportDefect)
	mux.HandleFunc("POST /challenges/{id}/patches/{patchID}", s.handlePatch)
	mux.HandleFunc("POST /tutor", s.handleTutor)
	mux.HandleFunc("GET /runs", s.handleListRuns)
	mux.HandleFunc("GET /runs/{id}", s.handleGetRun)
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
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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

// requestErrorCode returns the code a route answers for an unreadable body.
// A statement route's body IS a statement, so it keeps the query vocabulary
// (EMPTY_QUERY / QUERY_SYNTAX_ERROR). Every other route has no statement, and
// answering e.g. /tutor with EMPTY_QUERY put a query code on a route whose
// client branches on the AI error enum. The codes chosen already exist in the
// gateway's closed set, so no new code is invented.
func requestErrorCode(r *http.Request, empty bool) string {
	switch {
	case isStatementRoute(r):
		if empty {
			return "EMPTY_QUERY"
		}
		return "QUERY_SYNTAX_ERROR"
	case strings.HasPrefix(r.URL.Path, "/courses/"), strings.HasPrefix(r.URL.Path, "/challenges/"), r.URL.Path == "/tutor":
		return "AI_INVALID_REQUEST"
	case strings.HasPrefix(r.URL.Path, "/auth/"):
		return "AUTH_INVALID_INPUT"
	default:
		return "INVALID_REQUEST"
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		empty := errors.Is(err, io.EOF)
		message := "Malformed JSON: " + err.Error()
		if empty {
			message = "Request body is empty."
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": map[string]string{"code": requestErrorCode(r, empty), "message": message},
		})
		return false
	}
	return true
}

// isStatementRoute reports whether the request's route is one whose body is
// itself a SQL statement, and therefore one whose unreadable-body refusal should
// keep the query error vocabulary.
func isStatementRoute(r *http.Request) bool {
	switch r.URL.Path {
	case "/query", "/answers/evaluate", "/explain":
		return true
	}
	return false
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
	// ok:false like every other error envelope on this gateway. It used to omit
	// the field, so a client that consistently reads `ok` saw undefined here.
	writeJSON(w, http.StatusNotFound, map[string]any{
		"ok":    false,
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
