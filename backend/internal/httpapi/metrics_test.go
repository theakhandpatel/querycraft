package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* ---------- GET /metrics ---------- */

func TestMetricsExposition(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	// One request that is not the scrape, so the counter it produces is already
	// rendered by the time /metrics answers.
	do(t, s, http.MethodGet, "/health", "")

	w := do(t, s, http.MethodGet, "/metrics", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"# TYPE querycraft_requests_total counter",
		"# TYPE querycraft_request_duration_seconds histogram",
		"# TYPE querycraft_statements_total counter",
		"# TYPE querycraft_pool_connections gauge",
		"# TYPE querycraft_pool_max_connections gauge",
		"# TYPE querycraft_engine_ready gauge",
		`querycraft_requests_total{method="GET",route="/health",status="200"} 1`,
		"querycraft_pool_max_connections 4",
		`querycraft_pool_connections{state="acquired"} 1`,
		"querycraft_engine_ready 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q\n%s", want, body)
		}
	}
	// The health request's latency must be observed, not just counted.
	if !strings.Contains(body, "querycraft_request_duration_seconds_count") {
		t.Errorf("no histogram count in body:\n%s", body)
	}
	// A route label must be a bounded pattern, never an attacker-chosen path.
	do(t, s, http.MethodGet, "/no/such/path/at/all", "")
	w2 := do(t, s, http.MethodGet, "/metrics", "")
	if strings.Contains(w2.Body.String(), "/no/such/path/at/all") {
		t.Errorf("raw request path leaked into a metric label:\n%s", w2.Body.String())
	}
}

func TestMetricsRecordStatementOutcomes(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	do(t, s, http.MethodPost, "/query", `{"sql":"   "}`)

	if got, ok := s.Registry().Value("querycraft_statements_total", metrics.Labels{"status": "failed", "code": engine.CodeEmptyQuery}); !ok || got != 1 {
		t.Fatalf("empty-query counter = %v (exists %v), want 1", got, ok)
	}
	// An unreadable body never reached the database, so it is not a statement.
	do(t, s, http.MethodPost, "/query", `{`)
	if got, ok := s.Registry().Value("querycraft_statements_total", metrics.Labels{"status": "failed", "code": engine.CodeSyntax}); ok && got > 0 {
		t.Fatalf("malformed JSON counted as ar %s statement: %v", engine.CodeSyntax, got)
	}
}

/* ---------- GET /diagnostics ---------- */

func TestDiagnosticsReportsMeasuredFacts(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "diagnostics@example.test")
	w := doAs(t, s, token, http.MethodGet, "/diagnostics", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Build  string `json:"build"`
		Engine struct {
			Ready   bool   `json:"ready"`
			Reason  string `json:"reason"`
			Version string `json:"version"`
			Pool    engine.PoolStats
			Limits  struct {
				MaxConcurrent int `json:"max_concurrent"`
				TimeoutMS     int `json:"timeout_ms"`
			} `json:"limits"`
		} `json:"engine"`
		Store struct {
			Kind       string `json:"kind"`
			Persistent bool   `json:"persistent"`
		} `json:"store"`
		Dataset  struct{ Version int } `json:"dataset"`
		Progress struct{ Version int } `json:"progress"`
		Learner  struct {
			CurrentUser string `json:"current_user"`
			SearchPath  string `json:"search_path"`
			CanReadApp  bool   `json:"can_read_app"`
		} `json:"learner"`
		Metrics []string `json:"metrics"`
	}
	mustDecode(t, w, &out)

	if !out.Engine.Ready || out.Engine.Reason != "" {
		t.Errorf("engine = %+v, want ready with no reason", out.Engine)
	}
	if out.Engine.Version == "" {
		t.Error("engine version is empty")
	}
	if out.Engine.Limits.MaxConcurrent != 2 {
		t.Errorf("max_concurrent = %d, want the configured 2", out.Engine.Limits.MaxConcurrent)
	}
	if out.Store.Kind != "memory" {
		t.Errorf("store kind = %q, want memory for the in-memory test store", out.Store.Kind)
	}
	if out.Store.Persistent {
		t.Error("in-memory store reported as persistent")
	}
	if out.Dataset.Version != 1 {
		t.Errorf("dataset version = %d, want 1", out.Dataset.Version)
	}
	if out.Progress.Version != ProgressVersion {
		t.Errorf("progress version = %d, want %d", out.Progress.Version, ProgressVersion)
	}
	if out.Learner.CurrentUser != "querycraft_learner" || out.Learner.CanReadApp {
		t.Errorf("learner = %+v, want the restricted role without app access", out.Learner)
	}
	if len(out.Metrics) == 0 {
		t.Error("metrics list is empty")
	}
	for _, forbidden := range []string{"password", "://", "dsn", "user="} {
		if strings.Contains(strings.ToLower(w.Body.String()), forbidden) {
			t.Errorf("diagnostics body contains %q:\n%s", forbidden, w.Body.String())
		}
	}
}

// The diagnostics view must not claim health it cannot demonstrate. An executor
// that cannot answer a query is not ready.
func TestDiagnosticsReportsUnavailableHonestly(t *testing.T) {
	s := newTestServer(&fakeExecutor{err: errors.New("dial tcp 127.0.0.1:54329: connect: connection refused")})
	token := signupUser(t, s, "diagnostics-down@example.test")
	w := doAs(t, s, token, http.MethodGet, "/diagnostics", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var out struct {
		Engine struct {
			Ready  bool   `json:"ready"`
			Reason string `json:"reason"`
		} `json:"engine"`
	}
	mustDecode(t, w, &out)
	if out.Engine.Ready {
		t.Error("engine reported ready while every query fails")
	}
	if !strings.Contains(out.Engine.Reason, "connection refused") {
		t.Errorf("reason = %q, want the underlying failure", out.Engine.Reason)
	}
}

// A gateway with no executor at all reports the same way: not ready, and a
// reason that says what to set.
func TestDiagnosticsWithUnavailableExecutor(t *testing.T) {
	s := New(engine.UnavailableExecutor{}, store.NewMemory(), Config{MaxConcurrent: 2}, nil)
	token := signupUser(t, s, "diagnostics-unavailable@example.test")
	w := doAs(t, s, token, http.MethodGet, "/diagnostics", "")
	var out struct {
		Engine struct {
			Ready  bool   `json:"ready"`
			Reason string `json:"reason"`
			Pool   engine.PoolStats
		} `json:"engine"`
	}
	mustDecode(t, w, &out)
	if out.Engine.Ready || out.Engine.Reason == "" {
		t.Errorf("engine = %+v, want not ready with a reason", out.Engine)
	}
	if out.Engine.Pool.MaxConns != 0 {
		t.Errorf("pool max_conns = %d, want 0: there is no pool to advertise", out.Engine.Pool.MaxConns)
	}
}

/* ---------- concurrency limit ---------- */

// blockingExecutor holds the first query inside Query() until released, so a
// second request has to wait for the concurrency slot.
type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingExecutor) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &fakeRows{cols: []engine.Column{{Name: "n", Type: "int8"}}, rows: [][]any{{1}}}, nil
}
func (b *blockingExecutor) Ping(ctx context.Context) error { return nil }
func (b *blockingExecutor) Version(ctx context.Context) (string, error) {
	return "PostgreSQL test", nil
}
func (b *blockingExecutor) Stats() engine.PoolStats                  { return engine.PoolStats{MaxConns: 4} }
func (b *blockingExecutor) Ready(ctx context.Context) (bool, string) { return true, "" }
func (b *blockingExecutor) Dialect() string                          { return engine.DialectPostgres }
func (b *blockingExecutor) EngineLabel() string                      { return engine.EngineName }
func (b *blockingExecutor) ClassifyError(err error) (string, bool)   { return "", false }
func (b *blockingExecutor) Diagnostics(ctx context.Context) map[string]any {
	return nil
}
func (b *blockingExecutor) Close() error { return nil }

// MaxConcurrent is advertised in /capabilities, so it has to be enforced. A
// request that cannot get a slot within its own timeout gets the existing
// QUERY_TIMEOUT code: the result contract's error list is closed, and a request
// that waited past its deadline has timed out in the only sense it has a word
// for. It must not be queued indefinitely, and it must not be reported as a
// syntax or environment error.
func TestConcurrencyLimitIsEnforced(t *testing.T) {
	block := &blockingExecutor{started: make(chan struct{}, 1), release: make(chan struct{})}
	s := New(block, store.NewMemory(), Config{MaxRows: 100, TimeoutMS: 2000, MaxConcurrent: 1}, nil)
	if cap(s.sem) != 1 {
		t.Fatalf("semaphore capacity = %d, want the configured MaxConcurrent", cap(s.sem))
	}

	var wg sync.WaitGroup
	var first *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		first = do(t, s, http.MethodPost, "/query", `{"sql":"select 1"}`)
	}()
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first query never reached the executor")
	}

	// The holder keeps the slot until it is released. The waiter asks for a
	// much tighter budget than the holder's, so its own deadline decides the
	// outcome instead of a race between them.
	start := time.Now()
	second := do(t, s, http.MethodPost, "/query", `{"sql":"select 2","limits":{"timeout_ms":150}}`)
	waited := time.Since(start)

	close(block.release)
	wg.Wait()

	if first == nil || first.Code != http.StatusOK {
		t.Fatalf("first request = %v, want 200", first)
	}
	if second.Code != http.StatusBadRequest {
		t.Fatalf("second status = %d, want 400", second.Code)
	}
	var qr engine.QueryResult
	mustDecode(t, second, &qr)
	if qr.Status != "failed" || qr.Error == nil || qr.Error.Code != engine.CodeTimeout {
		t.Fatalf("second result = %+v, want failed/%s", qr, engine.CodeTimeout)
	}
	if !strings.Contains(qr.Error.Message, "maximum of 1") {
		t.Errorf("message = %q, want it to state the concurrency limit", qr.Error.Message)
	}
	// The wait is spent against the request's own deadline, not in addition to
	// it: the waiter gives up at roughly its own 150ms budget, long before the
	// holder's 2s budget expires.
	if waited > time.Second {
		t.Errorf("waited %s for a slot, want it bounded by the request's own 150ms timeout", waited)
	}
	if first != nil && first.Code == http.StatusOK {
		var held engine.QueryResult
		_ = json.Unmarshal(first.Body.Bytes(), &held)
		if held.DurationMS >= 2000 {
			t.Errorf("holder ran for %vms, want it released before its own budget", held.DurationMS)
		}
	}
	if got, ok := s.Registry().Value("querycraft_statements_total", metrics.Labels{"status": "failed", "code": engine.CodeTimeout}); !ok || got != 1 {
		t.Errorf("timeout counter = %v (exists %v), want 1", got, ok)
	}
}

// Grading runs learner SQL too, so it shares the same budget.
func TestConcurrencyLimitAppliesToEvaluate(t *testing.T) {
	block := &blockingExecutor{started: make(chan struct{}, 1), release: make(chan struct{})}
	s := New(block, store.NewMemory(), Config{MaxRows: 100, TimeoutMS: 2000, MaxConcurrent: 1}, nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = do(t, s, http.MethodPost, "/query", `{"sql":"select 1"}`)
	}()
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first query never reached the executor")
	}

	w := do(t, s, http.MethodPost, "/answers/evaluate", `{"sql":"select 1","limits":{"timeout_ms":150},"exercise":{"id":"x"}}`)
	close(block.release)
	wg.Wait()

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		QR struct {
			Status string             `json:"status"`
			Error  *engine.QueryError `json:"error"`
		} `json:"qr"`
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Passed {
		t.Error("a request that never ran must not be graded as passed")
	}
	if resp.QR.Error == nil || resp.QR.Error.Code != engine.CodeTimeout {
		t.Fatalf("qr error = %+v, want %s", resp.QR.Error, engine.CodeTimeout)
	}
}
