package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* ---------- fakes ---------- */

type fakeRows struct {
	cols []engine.Column
	rows [][]any
	i    int
}

func (r *fakeRows) Columns() []engine.Column { return r.cols }
func (r *fakeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}
func (r *fakeRows) Values() ([]any, error) { return r.rows[r.i-1], nil }
func (r *fakeRows) Err() error             { return nil }
func (r *fakeRows) Close()                 {}

type fakeExecutor struct{ err error }

func (f *fakeExecutor) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	if strings.Contains(sql, "SYNTAXERR") {
		return nil, &pgconn.PgError{Code: "42601", Message: `syntax error at or near "SYNTAXERR"`}
	}
	if strings.Contains(sql, "BOOM") {
		return nil, errors.New("boom")
	}
	if f.err != nil {
		return nil, f.err
	}
	return &fakeRows{
		cols: []engine.Column{{Name: "status", Type: "text"}, {Name: "order_count", Type: "int8"}},
		rows: [][]any{{"delivered", 36}, {"pending", 13}},
	}, nil
}
func (f *fakeExecutor) Ping(ctx context.Context) error { return nil }
func (f *fakeExecutor) Version(ctx context.Context) (string, error) {
	return "PostgreSQL 16.3 (test)", nil
}
func (f *fakeExecutor) Stats() engine.PoolStats {
	return engine.PoolStats{MaxConns: 4, TotalConns: 2, IdleConns: 1, AcquiredConns: 1}
}

// Ready mirrors the real executor: an executor that cannot answer a query is
// not ready, and /diagnostics has to say so rather than report a healthy pool.
func (f *fakeExecutor) Ready(ctx context.Context) (bool, string) {
	if f.err != nil {
		return false, f.err.Error()
	}
	return true, ""
}

// Diagnostics makes the fake a Diagnostician so the /diagnostics test covers
// the learner subsection without a live database.
func (f *fakeExecutor) Diagnostics(ctx context.Context) map[string]any {
	return map[string]any{
		"current_user": "querycraft_learner",
		"search_path":  "dataset",
		"database":     "querycraft",
		"can_read_app": false,
	}
}

func (f *fakeExecutor) Close() error { return nil }

func newTestServer(exec engine.Executor) *Server {
	return New(exec, store.NewMemory(), Config{MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2}, nil)
}

func do(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// doAs is do with a bearer token attached, which the owner-scoped endpoints
// require. An empty token is the anonymous case.
func doAs(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// signupUser creates an account through the real endpoint and returns its
// session token, so tests exercise the same path a browser does.
func signupUser(t *testing.T, s *Server, email string) string {
	t.Helper()
	w := do(t, s, http.MethodPost, "/auth/signup",
		`{"email":"`+email+`","name":"Test Learner","password":"correct-horse"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	mustDecode(t, w, &out)
	if out.Token == "" {
		t.Fatalf("signup returned no token")
	}
	return out.Token
}

/* ---------- tests ---------- */

func TestHealth(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodGet, "/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var out map[string]any
	mustDecode(t, w, &out)
	if out["status"] != "ok" {
		t.Fatalf("status field = %v", out["status"])
	}
}

func TestCapabilities(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodGet, "/capabilities", "")
	var out map[string]any
	mustDecode(t, w, &out)
	if out["isolation"] != "read_only_tx" {
		t.Fatalf("isolation = %v", out["isolation"])
	}
	if out["max_concurrent"].(float64) != 2 {
		t.Fatalf("max_concurrent = %v", out["max_concurrent"])
	}
}

func TestQuerySuccess(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/query", `{"sql":"SELECT status, count(*) FROM orders GROUP BY 1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Status != "succeeded" || qr.RowCount != 2 || len(qr.Columns) != 2 {
		t.Fatalf("unexpected result: %+v", qr)
	}
	if qr.Engine != engine.EngineName {
		t.Fatalf("engine = %q", qr.Engine)
	}
}

func TestQueryEmpty(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/query", `{"sql":"   -- just a comment "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Error == nil || qr.Error.Code != engine.CodeEmptyQuery {
		t.Fatalf("error = %+v", qr.Error)
	}
}

func TestQuerySyntaxError(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/query", `{"sql":"SELECT SYNTAXERR"}`)
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Error == nil || qr.Error.Code != engine.CodeSyntax {
		t.Fatalf("want syntax error, got %+v", qr.Error)
	}
}

func TestQueryRuntimeError(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/query", `{"sql":"SELECT BOOM"}`)
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Error == nil || qr.Error.Code != engine.CodeRuntime {
		t.Fatalf("want runtime error, got %+v", qr.Error)
	}
}

func TestQueryEnvironmentUnavailable(t *testing.T) {
	s := newTestServer(engine.UnavailableExecutor{})
	w := do(t, s, http.MethodPost, "/query", `{"sql":"SELECT 1"}`)
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Error == nil || qr.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("want ENVIRONMENT_UNAVAILABLE, got %+v", qr.Error)
	}
}

func TestEvaluatePass(t *testing.T) {
	body := `{"sql":"SELECT status, count(*) FROM orders GROUP BY 1","exercise":{
		"id":"ex.1","version":1,"result_contract":{"columns":["status","order_count"],"column_order":"exact","row_order":"exact","duplicates":"preserved","numeric_tolerance":0},
		"result":{"columns":["status","order_count"],"rows":[["delivered",36],["pending",13]]}}}`
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/answers/evaluate", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Passed bool `json:"passed"`
		QR     engine.QueryResult
	}
	mustDecode(t, w, &resp)
	if !resp.Passed {
		t.Fatalf("expected pass, got %+v", w.Body.String())
	}
}

func TestEvaluateWrongValues(t *testing.T) {
	body := `{"sql":"SELECT status, count(*) FROM orders GROUP BY 1","exercise":{
		"id":"ex.1","version":1,"result_contract":{"columns":["status","order_count"],"column_order":"exact","row_order":"exact"},
		"result":{"columns":["status","order_count"],"rows":[["delivered",999],["pending",13]]}}}`
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/answers/evaluate", body)
	var resp struct {
		Passed bool `json:"passed"`
	}
	mustDecode(t, w, &resp)
	if resp.Passed {
		t.Fatalf("expected fail for wrong values")
	}
}

func TestEvaluateColumnMismatch(t *testing.T) {
	body := `{"sql":"SELECT 1","exercise":{"id":"ex.1","version":1,
		"result_contract":{"columns":["month","revenue"],"column_order":"exact","row_order":"exact"},
		"result":{"columns":["month","revenue"],"rows":[["2026-01-01",1]]}}}`
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/answers/evaluate", body)
	var resp struct {
		Passed bool `json:"passed"`
	}
	mustDecode(t, w, &resp)
	if resp.Passed {
		t.Fatalf("expected fail for column mismatch")
	}
}

func TestTablesKnownAndUnknown(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	w := do(t, s, http.MethodGet, "/datasets/commerce/tables", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var out struct {
		Dataset string `json:"dataset"`
		Tables  []struct {
			Name string `json:"name"`
		}
		Rows int `json:"rows"`
	}
	mustDecode(t, w, &out)
	if out.Dataset != "commerce" || len(out.Tables) != 4 || out.Rows < 250 {
		t.Fatalf("unexpected tables response: %+v", out)
	}
	if do(t, s, http.MethodGet, "/datasets/nope/tables", "").Code != http.StatusNotFound {
		t.Fatalf("unknown dataset should 404")
	}
}

func TestProgressRoundTrip(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "roundtrip@example.test")
	put := doAs(t, s, token, http.MethodPut, "/progress/sql-analytics", `{"version":3,"passed":{"ex.1":true}}`)
	if put.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", put.Code, put.Body.String())
	}
	get := doAs(t, s, token, http.MethodGet, "/progress/sql-analytics", "")
	var out struct {
		OK       bool           `json:"ok"`
		Snapshot map[string]any `json:"snapshot"`
	}
	mustDecode(t, get, &out)
	if !out.OK || out.Snapshot == nil {
		t.Fatalf("snapshot not returned: %s", get.Body.String())
	}
}

func TestProgressRejectsBadJSON(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "badjson@example.test")
	w := doAs(t, s, token, http.MethodPut, "/progress/sql-analytics", `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestEvents(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	w := doAs(t, s, signupUser(t, s, "events@example.test"), http.MethodPost, "/events", `{"id":"ex.1","kind":"passed"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d", w.Code)
	}
	var out map[string]any
	mustDecode(t, w, &out)
	if out["id"] != "ex.1" {
		t.Fatalf("id = %v", out["id"])
	}
}

// A bad event body must be a 400, never a silent 2xx: previously the decode
// error was dropped and, with the in-memory store, garbage was "saved".
func TestEventValidation(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "eventvalidation@example.test")
	cases := []struct {
		name string
		body string
		want int
	}{
		{"valid", `{"id":"ex.1","kind":"passed"}`, http.StatusAccepted},
		{"not json", `not json`, http.StatusBadRequest},
		{"json array", `[1,2]`, http.StatusBadRequest},
		{"missing id", `{"kind":"passed"}`, http.StatusBadRequest},
		{"blank id", `{"id":"  "}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doAs(t, s, token, http.MethodPost, "/events", tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusBadRequest && !strings.Contains(w.Body.String(), "PERSISTENCE_FAILED") {
				t.Fatalf("400 body missing the PERSISTENCE_FAILED envelope: %s", w.Body.String())
			}
		})
	}
}

// newRequestID runs in every request goroutine; its random source must be safe
// for concurrent use (a shared *rand.Rand is not).
func TestRequestIDConcurrent(t *testing.T) {
	const goroutines, each = 16, 200
	var wg sync.WaitGroup
	ids := make(chan string, goroutines*each)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				ids <- newRequestID()
			}
		}()
	}
	wg.Wait()
	close(ids)
	for id := range ids {
		if len(id) != len("rq_")+6 || !strings.HasPrefix(id, "rq_") {
			t.Fatalf("malformed request id %q", id)
		}
	}
}

// The request middleware path (which mints ids) must be race-free under load.
func TestHandlerConcurrentRequests(t *testing.T) {
	h := newTestServer(&fakeExecutor{}).Handler()
	var wg sync.WaitGroup
	failures := make(chan string, 64)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
				if w.Code != http.StatusOK {
					failures <- w.Result().Status
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for status := range failures {
		t.Errorf("/health status = %s", status)
	}
}

// nonFiniteExecutor returns values with no JSON representation.
type nonFiniteExecutor struct{}

func (nonFiniteExecutor) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	return &fakeRows{
		cols: []engine.Column{{Name: "v", Type: "float8"}},
		rows: [][]any{{math.NaN()}, {math.Inf(1)}},
	}, nil
}
func (nonFiniteExecutor) Ping(ctx context.Context) error              { return nil }
func (nonFiniteExecutor) Version(ctx context.Context) (string, error) { return "PostgreSQL test", nil }
func (nonFiniteExecutor) Stats() engine.PoolStats                     { return engine.PoolStats{} }
func (nonFiniteExecutor) Ready(ctx context.Context) (bool, string)    { return true, "" }
func (nonFiniteExecutor) Close() error                                { return nil }

// An executor value with no JSON representation must still produce a well-formed
// JSON body with a stable error code — never the headers-only 200 that the
// swallowed encoder error used to yield. (The real PgxExecutor normalizes
// non-finite floats; this guards the boundary for any executor.)
func TestQueryUnencodableValueBodyIsJSON(t *testing.T) {
	w := do(t, newTestServer(nonFiniteExecutor{}), http.MethodPost, "/query", `{"sql":"select v from t"}`)
	if !json.Valid(w.Body.Bytes()) {
		t.Fatalf("body is not valid JSON: %q", w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Error == nil || qr.Error.Code != engine.CodeRuntime {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeRuntime)
	}
}

// bigExecutor returns more rows than the test server's row cap (100).
type bigExecutor struct{}

func (bigExecutor) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	rows := make([][]any, 1000)
	for i := range rows {
		rows[i] = []any{i}
	}
	return &fakeRows{cols: []engine.Column{{Name: "n", Type: "int8"}}, rows: rows}, nil
}
func (bigExecutor) Ping(ctx context.Context) error              { return nil }
func (bigExecutor) Version(ctx context.Context) (string, error) { return "PostgreSQL test", nil }
func (bigExecutor) Stats() engine.PoolStats                     { return engine.PoolStats{} }
func (bigExecutor) Ready(ctx context.Context) (bool, string)    { return true, "" }
func (bigExecutor) Close() error                                { return nil }

// A request may lower a cap but never raise it: asking for max_rows beyond the
// server's cap must not let the gateway return more than the cap.
func TestQueryCannotRaiseServerCap(t *testing.T) {
	body := `{"sql":"select n from t","limits":{"max_rows":100000,"max_bytes":100000000,"timeout_ms":100000000}}`
	w := do(t, newTestServer(bigExecutor{}), http.MethodPost, "/query", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.RowCount > 100 || !qr.Truncated {
		t.Fatalf("row_count = %d truncated = %v, want the server cap (100) to win", qr.RowCount, qr.Truncated)
	}
}

func TestGenerateCourse(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodPost, "/courses/generate", `{"title":"SQL Joins","exercises":9,"steps":["a","b","c"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d", w.Code)
	}
	var out map[string]any
	mustDecode(t, w, &out)
	if out["chapters"].(float64) != 3 || out["datasetVersion"].(float64) != 1 {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestCORSPreflight(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodOptions, "/query", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header")
	}
}

func mustDecode(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
}
