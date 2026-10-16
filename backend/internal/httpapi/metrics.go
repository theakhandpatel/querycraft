package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
	"github.com/querycraft/sqlquery/backend/internal/perf"
)

// Metric names. They are constants because /diagnostics publishes the list and
// the audit script asserts against the same strings; a typo here would
// otherwise show up as a series that never appears.
const (
	metricRequests   = "querycraft_requests_total"
	metricDuration   = "querycraft_request_duration_seconds"
	metricStatements = "querycraft_statements_total"
	metricPoolConns  = "querycraft_pool_connections"
	metricPoolMax    = "querycraft_pool_max_connections"
	metricReady      = "querycraft_engine_ready"
	metricPlans      = "querycraft_plans_total"
)

// Help strings, declared once so /metrics and the diagnostics list agree.
const (
	helpRequests   = "HTTP requests handled, by route, method and response status."
	helpDuration   = "HTTP request latency in seconds."
	helpStatements = "Learner statements executed, by outcome and error code."
	helpPoolConns  = "Execution pool connections, by state."
	helpPoolMax    = "Maximum connections the execution pool opens."
	helpReady      = "1 when learner statements can run, 0 when the execution database is unreachable."
	helpPlans      = "Plans captured by POST /explain, by outcome and error code."
)

// declareMetrics publishes the instrumentation before the first request, so a
// scrape and the diagnostics view describe the system even when it is idle.
func (s *Server) declareMetrics() {
	s.metrics.DescribeCounter(metricRequests, helpRequests)
	s.metrics.DescribeHistogram(metricDuration, helpDuration)
	s.metrics.DescribeCounter(metricStatements, helpStatements)
	s.metrics.DescribeGauge(metricPoolConns, helpPoolConns)
	s.metrics.DescribeGauge(metricPoolMax, helpPoolMax)
	s.metrics.DescribeGauge(metricReady, helpReady)
	s.metrics.DescribeCounter(metricGenerations, helpGenerations)
	s.metrics.DescribeGauge(metricGenerationJobs, helpGenerationJobs)
	s.metrics.DescribeCounter(metricPlans, helpPlans)
}

// ProgressVersion is the snapshot schema the frontend writes
// (web/src/lib/progress.ts PROGRESS_VERSION). It is reported by /diagnostics so
// a snapshot written by an older build is visible instead of silent. The
// metrics audit compares this constant with the bundled frontend source, so the
// two cannot drift apart quietly.
const ProgressVersion = 5

/* ---------- middleware ---------- */

// statusRecorder captures the outcome of a request for the access log and the
// metrics layer: the status line and the stable error code, if any.
type statusRecorder struct {
	http.ResponseWriter
	status int
	code   string
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// noteError attaches a stable error code to a response so the access log can
// name the failure instead of only its status. writeJSON calls it, so a handler
// cannot forget. A response written outside writeJSON (the SPA) has no code.
func noteError(w http.ResponseWriter, code string) {
	if rec, ok := w.(*statusRecorder); ok && code != "" {
		rec.code = code
	}
}

// errorCode extracts the stable code from the two error shapes the gateway
// writes: the map envelope handlers use, and the QueryResult/eval contract.
func errorCode(v any) string {
	switch body := v.(type) {
	case engine.QueryResult:
		if body.Error != nil {
			return body.Error.Code
		}
	case *engine.QueryResult:
		if body != nil && body.Error != nil {
			return body.Error.Code
		}
	case eval.Response:
		if body.QR.Error != nil {
			return body.QR.Error.Code
		}
	case *eval.Response:
		if body != nil && body.QR.Error != nil {
			return body.QR.Error.Code
		}
	case map[string]any:
		if e, ok := body["error"].(map[string]string); ok {
			return e["code"]
		}
		if e, ok := body["error"].(map[string]any); ok {
			if s, ok := e["code"].(string); ok {
				return s
			}
		}
	}
	return ""
}

// withObservability is the one place a request is measured and logged.
//
// It is a single middleware with a single recorder on purpose: a separate
// logging layer and metrics layer would each keep their own status and error
// code, and the log line could then disagree with the counter about what
// happened. Every request — including one answered before routing, such as an
// unauthenticated /progress call — is counted and logged exactly once, after
// the response is written.
//
// routeOf resolves the label from the routing table rather than the handler
// chain, so a request that never reached the mux still gets a bounded route
// instead of its raw path (one series per probe otherwise).
func (s *Server) withObservability(next http.Handler, routeOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := newStatusRecorder(w)
		next.ServeHTTP(rec, r)

		route := routeOf(r)
		if route == "" {
			route = routeUnmatched
		}
		elapsed := time.Since(start)

		s.metrics.Inc(metricRequests,
			metrics.Labels{"route": route, "method": r.Method, "status": strconv.Itoa(rec.status)},
			helpRequests)
		s.metrics.Observe(metricDuration,
			metrics.Labels{"route": route, "method": r.Method},
			elapsed.Seconds(),
			helpDuration,
			nil)

		// The log line names the work, the outcome and how long it took. It
		// carries no request or response body: the body is where a credential
		// would hide, and an error code is the part an operator filters on.
		attrs := []any{
			"operation", operationOf(route),
			"route", route,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", engine.RoundMS(elapsed),
			"request_id", requestIDOf(r),
		}
		if rec.code != "" {
			attrs = append(attrs, "error_code", rec.code)
		}
		s.log.Info("request", attrs...)
	})
}

// routeUnmatched labels a request that no route claimed.
const routeUnmatched = "unmatched"

// operations maps a route to the stable operation name the log carries. The
// route is a path; the operation is the word a query filters on, and it is
// deliberately decoupled from the URL so renaming a path does not change how
// past logs read.
var operations = map[string]string{
	"/health":                            "health",
	"/metrics":                           "metrics",
	"/diagnostics":                       "diagnostics",
	"/capabilities":                      "capabilities",
	"/query":                             "query",
	"/explain":                           "explain",
	"/answers/evaluate":                  "evaluate",
	"/environment":                       "environment",
	"/environment/reset":                 "reset_environment",
	"/datasets/{id}/tables":              "tables",
	"/auth/signup":                       "signup",
	"/auth/login":                        "login",
	"/auth/logout":                       "logout",
	"/auth/me":                           "me",
	"/progress/{course}":                 "progress",
	"/events":                            "event",
	"/courses/generate":                  "generate_course",
	"/courses":                           "courses",
	"/courses/{id}":                      "course",
	"/courses/{id}/build":                "build_course",
	"/generation/{id}":                   "generation_job",
	"/challenges/generate":               "generate_challenge",
	"/challenges":                        "challenges",
	"/challenges/{id}":                   "challenge",
	"/challenges/{id}/reset":             "reset_challenge",
	"/challenges/{id}/critique":          "critique",
	"/challenges/{id}/report":            "report_defect",
	"/challenges/{id}/patches/{patchID}": "apply_patch",
	"/tutor":                             "tutor",
	"/runs":                              "runs",
	"/runs/{id}":                         "run",
	"/openapi.json":                      "openapi",
	"/docs":                              "docs",
	"/":                                  "spa",
	routeUnmatched:                       "unmatched",
}

// operationOf names the work behind a route, defaulting to a constant so an
// unmapped route cannot expand the log vocabulary.
func operationOf(route string) string {
	if op, ok := operations[route]; ok {
		return op
	}
	return "other"
}

// requestIDOf reads the correlation id attached by withRequest.
func requestIDOf(r *http.Request) string {
	if id := engine.RequestIDFromContext(r.Context()); id != nil {
		return *id
	}
	return ""
}

// recordStatement counts one learner statement by outcome. The error code is
// the label that makes the adversarial suite readable: the number of
// QUERY_TIMEOUT statements is a measurement, not an inference.
func (s *Server) recordStatement(res engine.QueryResult) {
	code := ""
	if res.Error != nil {
		code = res.Error.Code
	}
	s.metrics.Inc(metricStatements,
		metrics.Labels{"status": res.Status, "code": code},
		helpStatements)
}

// recordPlan counts one plan capture by outcome. It mirrors recordStatement:
// the number of QUERY_TIMEOUT plans is a measurement, not an inference, and a
// refusal (EMPTY_QUERY, ENVIRONMENT_UNAVAILABLE) is counted the same way.
func (s *Server) recordPlan(bundle *perf.PlanBundle, analyze bool) {
	status, code := "failed", ""
	if bundle != nil {
		status = bundle.Status
		if bundle.Error != nil {
			code = bundle.Error.Code
		}
	}
	mode := "estimate"
	if analyze {
		mode = "analyze"
	}
	s.metrics.Inc(metricPlans,
		metrics.Labels{"status": status, "code": code, "mode": mode},
		helpPlans)
}

/* ---------- GET /metrics ---------- */

// handleMetrics serves the Prometheus text exposition. The pool gauges are
// sampled here rather than at request time so a scrape shows the pool as it is
// at the moment of the scrape, including when nothing is running.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.sampleEngine(r.Context())
	var buf bytes.Buffer
	if err := s.metrics.WritePrometheus(&buf); err != nil {
		http.Error(w, "could not render metrics", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) sampleEngine(ctx context.Context) {
	st := s.exec.Stats()
	s.metrics.Set(metricPoolMax, nil, float64(st.MaxConns), helpPoolMax)
	help := helpPoolConns
	s.metrics.Set(metricPoolConns, metrics.Labels{"state": "total"}, float64(st.TotalConns), help)
	s.metrics.Set(metricPoolConns, metrics.Labels{"state": "idle"}, float64(st.IdleConns), help)
	s.metrics.Set(metricPoolConns, metrics.Labels{"state": "acquired"}, float64(st.AcquiredConns), help)
	s.metrics.Set(metricPoolConns, metrics.Labels{"state": "constructing"}, float64(st.ConstructingConns), help)

	ready, _ := s.exec.Ready(ctx)
	v := 0.0
	if ready {
		v = 1
	}
	s.metrics.Set(metricReady, nil, v, helpReady)
}

/* ---------- GET /diagnostics ---------- */

// handleDiagnostics answers "what is this gateway, and is it actually able to
// run learner SQL right now" with measured values and no secrets.
//
// Nothing here is a credential: the learner's DSN, the admin's DSN and the
// passwords never appear, and the learner subsection is read over the
// restricted connection so it cannot report privileges the learner does not
// have. The route needs a session, and it is deliberately not a debug dump.
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ready, reason := s.exec.Ready(ctx)
	version, verr := s.exec.Version(ctx)
	if errors.Is(verr, engine.ErrEnvironmentUnavailable) {
		version = ""
	}

	out := map[string]any{
		"build": BuildVersion,
		"engine": map[string]any{
			"name":    engine.EngineName,
			"ready":   ready,
			"reason":  reason,
			"version": version,
			"pool":    s.exec.Stats(),
			"limits": map[string]any{
				"max_rows":       s.cfg.MaxRows,
				"max_bytes":      s.cfg.MaxBytes,
				"timeout_ms":     s.cfg.TimeoutMS,
				"max_concurrent": s.cfg.MaxConcurrent,
			},
		},
		"store":    map[string]any{"kind": s.store.Kind(), "persistent": s.store.Kind() == "postgres" || s.store.Kind() == "sqlite"},
		"dataset":  map[string]any{"schema": dataset.SchemaName, "version": dataset.DatasetVersion},
		"progress": map[string]any{"version": ProgressVersion},
		"metrics":  s.metrics.Names(),
	}
	// Every executor answers Diagnostics. A nil map means this engine has no
	// learner sandbox to describe (SQLite), so the block stays out of the body
	// instead of appearing as an empty object.
	if learner := s.exec.Diagnostics(ctx); learner != nil {
		out["learner"] = learner
	}
	writeJSON(w, http.StatusOK, out)
}

/* ---------- concurrency ---------- */

// enter waits for a statement slot, spending the request's own deadline while
// it waits. A gateway at capacity answers with QUERY_TIMEOUT rather than
// queueing unboundedly: the code list in contracts/query-result.schema.json is
// closed, and a request that waited past its budget has timed out in the only
// sense the contract has a word for.
func (s *Server) enter(ctx context.Context) (func(), bool) {
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, true
	case <-ctx.Done():
		return nil, false
	}
}

// busyResult is the QUERY_TIMEOUT result for a request that never ran because
// the gateway was at its concurrency limit.
func (s *Server) busyResult(ctx context.Context, start time.Time) engine.QueryResult {
	res := engine.Failing(engine.CodeTimeout,
		fmt.Sprintf("The gateway is already running its maximum of %d statements; this request waited for a free slot and gave up.", s.cfg.MaxConcurrent),
		"", engine.RequestIDFromContext(ctx))
	res.DurationMS = engine.RoundMS(time.Since(start))
	return res
}

// routeOfMux resolves a request's route label from the routing table without
// serving it.
func routeOfMux(mux *http.ServeMux) func(*http.Request) string {
	return func(r *http.Request) string {
		if p := r.Pattern; p != "" {
			return trimPattern(p)
		}
		_, pattern := mux.Handler(r)
		return trimPattern(pattern)
	}
}

// trimPattern turns a ServeMux pattern ("GET /progress/{course}") into a route
// label, and folds the two root patterns onto "/" so the SPA catch-all does not
// produce a second series.
func trimPattern(p string) string {
	if p == "" {
		return ""
	}
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	if p == "/{$}" {
		return "/"
	}
	return p
}
