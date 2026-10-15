package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
	"github.com/querycraft/sqlquery/backend/internal/perf"
)

/* The plan route is exercised against a fake that answers the four questions a
   plan capture asks (the EXPLAIN, the row counts, the index list, the planner
   settings), so the handler's routing and its refusals are tested without a
   database. The engine's own plan parsing is tested in internal/engine. */

const httpSamplePlan = `[{"Plan":{
  "Node Type":"Aggregate","Plan Rows":1,"Total Cost":57.02,
  "Plans":[{
    "Node Type":"Index Only Scan","Parent Relationship":"Outer",
    "Relation Name":"orders","Alias":"orders","Index Name":"orders_status_idx",
    "Plan Rows":50,"Total Cost":56.89,"Index Cond":"(status = 'refunded'::text)"
  }]
}}]`

type planHTTPFake struct {
	plan string

	mu      sync.Mutex
	queries []string
}

func (f *planHTTPFake) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	f.mu.Lock()
	f.queries = append(f.queries, sql)
	f.mu.Unlock()
	switch {
	case strings.HasPrefix(sql, "EXPLAIN"):
		return &fakeRows{cols: []engine.Column{{Name: "QUERY PLAN", Type: "json"}}, rows: [][]any{{f.plan}}}, nil
	case strings.Contains(sql, "pg_class"):
		return &fakeRows{
			cols: []engine.Column{{Name: "relname", Type: "text"}, {Name: "reltuples", Type: "float8"}},
			rows: [][]any{{"customers", float64(2000)}, {"orders", float64(10000)}, {"order_items", float64(20000)}, {"products", float64(500)}},
		}, nil
	case strings.Contains(sql, "pg_indexes"):
		return &fakeRows{
			cols: []engine.Column{{Name: "tablename", Type: "text"}, {Name: "indexname", Type: "text"}, {Name: "indexdef", Type: "text"}},
			rows: [][]any{{"orders", "orders_status_idx", "CREATE INDEX orders_status_idx ON dataset.orders USING btree (status)"}},
		}, nil
	case strings.Contains(sql, "current_setting"):
		return &fakeRows{cols: []engine.Column{{Name: "setting", Type: "text"}}, rows: [][]any{{
			"18.3", "4", "1", "4GB", "16MB", "on", "on", "on", "on", "on", "on",
		}}}, nil
	}
	return nil, nil
}

func (f *planHTTPFake) Ping(ctx context.Context) error { return nil }
func (f *planHTTPFake) Version(ctx context.Context) (string, error) {
	return "PostgreSQL 18.3 (test)", nil
}
func (f *planHTTPFake) Stats() engine.PoolStats                  { return engine.PoolStats{} }
func (f *planHTTPFake) Ready(ctx context.Context) (bool, string) { return true, "" }
func (f *planHTTPFake) Close() error                             { return nil }

func (f *planHTTPFake) ranExplain() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, q := range f.queries {
		if strings.HasPrefix(q, "EXPLAIN") {
			out = append(out, q)
		}
	}
	return out
}

func explainBody(t *testing.T, s *Server, body string) (int, perf.PlanBundle) {
	t.Helper()
	w := do(t, s, http.MethodPost, "/explain", body)
	var bundle perf.PlanBundle
	if err := json.Unmarshal(w.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("response is not a PlanBundle: %v\nbody: %s", err, w.Body.String())
	}
	return w.Code, bundle
}

// EXPLAIN ANALYZE executes the learner's statement, so an anonymous request is
// refused with the contract's ENVIRONMENT_UNAVAILABLE — never a 5xx, and never
// a silent downgrade to a plain EXPLAIN.
func TestExplainRefusesAnonymousAnalyze(t *testing.T) {
	fake := &planHTTPFake{plan: httpSamplePlan}
	s := newTestServer(fake)

	status, bundle := explainBody(t, s, `{"sql":"SELECT count(*) FROM orders","analyze":true}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; bundle = %+v", status, bundle)
	}
	if bundle.Status != perf.StatusFailed || bundle.Error == nil {
		t.Fatalf("bundle = %+v, want a failed envelope", bundle)
	}
	if bundle.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("error code = %q, want %s", bundle.Error.Code, engine.CodeEnvironmentUnavailable)
	}
	if !strings.Contains(strings.ToLower(bundle.Error.Message), "analyze") {
		t.Errorf("message %q does not name the reason", bundle.Error.Message)
	}
	// The refusal is honest: no plan, no timing claim, and nothing was run.
	if bundle.Plan != nil || bundle.Analyze || bundle.TimingPrecision != perf.TimingNone {
		t.Errorf("refusal = plan %v analyze %v precision %q, want no plan / false / none",
			bundle.Plan, bundle.Analyze, bundle.TimingPrecision)
	}
	if ran := fake.ranExplain(); len(ran) != 0 {
		t.Fatalf("the refusal still ran %v", ran)
	}
}

// A plain EXPLAIN only plans, so an anonymous caller may use the shared
// read-only dataset — and gets the honest `estimate` precision.
func TestExplainAnonymousEstimate(t *testing.T) {
	fake := &planHTTPFake{plan: httpSamplePlan}
	s := newTestServer(fake)

	status, bundle := explainBody(t, s, `{"sql":"SELECT count(*) FROM orders"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; bundle = %+v", status, bundle)
	}
	if bundle.Status != perf.StatusSucceeded || bundle.Error != nil {
		t.Fatalf("bundle = %+v, want a succeeded plan", bundle)
	}
	if bundle.Analyze || bundle.TimingPrecision != perf.TimingEstimate {
		t.Errorf("analyze = %v precision = %q, want false/estimate", bundle.Analyze, bundle.TimingPrecision)
	}
	if bundle.Summary.NodeCount != 2 || len(bundle.Summary.IndexScans) != 1 {
		t.Errorf("summary = %+v, want the captured plan", bundle.Summary)
	}
	ran := fake.ranExplain()
	if len(ran) != 1 || strings.Contains(ran[0], "ANALYZE") {
		t.Fatalf("EXPLAIN statements = %v, want one plain EXPLAIN", ran)
	}
}

// The perf dataset lives in a writable per-account environment, so an anonymous
// request for it is refused rather than planned against the wrong fixture.
func TestExplainPerfDatasetRequiresAnEnvironment(t *testing.T) {
	s := newTestServer(&planHTTPFake{plan: httpSamplePlan})
	status, bundle := explainBody(t, s, `{"sql":"SELECT 1","dataset":"perf"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if bundle.Error == nil || bundle.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("error = %+v, want %s", bundle.Error, engine.CodeEnvironmentUnavailable)
	}
}

func TestExplainRefusesUnknownDataset(t *testing.T) {
	s := newTestServer(&planHTTPFake{plan: httpSamplePlan})
	status, bundle := explainBody(t, s, `{"sql":"SELECT 1","dataset":"nope"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if bundle.Error == nil || bundle.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("error = %+v, want %s", bundle.Error, engine.CodeEnvironmentUnavailable)
	}
	if !strings.Contains(bundle.Error.Message, "perf") {
		t.Errorf("message %q does not name the datasets the gateway serves", bundle.Error.Message)
	}
}

// A SQLite chapter cannot be graded against a PostgreSQL plan; the refusal says
// so instead of comparing two dialects' facts.
func TestExplainRefusesAnotherDialect(t *testing.T) {
	s := newTestServer(&planHTTPFake{plan: httpSamplePlan})
	status, bundle := explainBody(t, s,
		`{"sql":"SELECT 1","exercise":{"perf":{"engine":"sqlite","require_indexes":["orders_status_idx"]}}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if bundle.Error == nil || bundle.Error.Code != engine.CodeEvaluationError {
		t.Fatalf("error = %+v, want %s", bundle.Error, engine.CodeEvaluationError)
	}
	if !strings.Contains(bundle.Error.Message, "sqlite") {
		t.Errorf("message %q does not name the dialect", bundle.Error.Message)
	}
}

// With no database the route reports ENVIRONMENT_UNAVAILABLE and never invents
// a plan.
func TestExplainWithoutADatabase(t *testing.T) {
	s := newTestServer(engine.UnavailableExecutor{})
	for _, body := range []string{
		`{"sql":"SELECT 1"}`,
		`{"sql":"SELECT 1","analyze":true}`,
		`{"sql":"SELECT 1","dataset":"perf"}`,
	} {
		status, bundle := explainBody(t, s, body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", body, status)
		}
		if bundle.Error == nil || bundle.Error.Code != engine.CodeEnvironmentUnavailable {
			t.Fatalf("%s error = %+v, want %s", body, bundle.Error, engine.CodeEnvironmentUnavailable)
		}
		if bundle.Plan != nil {
			t.Fatalf("%s invented a plan: %v", body, bundle.Plan)
		}
	}
}

// A blank statement is EMPTY_QUERY, the same code POST /query reports.
func TestExplainEmptyQuery(t *testing.T) {
	s := newTestServer(&planHTTPFake{plan: httpSamplePlan})
	status, bundle := explainBody(t, s, `{"sql":"   -- nothing here\n"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if bundle.Error == nil || bundle.Error.Code != engine.CodeEmptyQuery {
		t.Fatalf("error = %+v, want %s", bundle.Error, engine.CodeEmptyQuery)
	}
}

// Every plan is counted, so "how many plans timed out" is a measurement rather
// than an inference.
func TestExplainRecordsPlanMetrics(t *testing.T) {
	s := newTestServer(&planHTTPFake{plan: httpSamplePlan})
	do(t, s, http.MethodPost, "/explain", `{"sql":"SELECT count(*) FROM orders"}`)
	do(t, s, http.MethodPost, "/explain", `{"sql":"SELECT 1","analyze":true}`)

	ok := metrics.Labels{"status": "succeeded", "code": "", "mode": "estimate"}
	if got, found := s.Registry().Value(metricPlans, ok); !found || got != 1 {
		t.Errorf("plans_total%v = %v (found %v), want 1", ok, got, found)
	}
	refused := metrics.Labels{"status": "failed", "code": engine.CodeEnvironmentUnavailable, "mode": "analyze"}
	if got, found := s.Registry().Value(metricPlans, refused); !found || got != 1 {
		t.Errorf("plans_total%v = %v (found %v), want 1", refused, got, found)
	}
}

// The capability block tells a client what the lab offers before it offers an
// Explain button, and says why it is unavailable when it is.
func TestCapabilitiesPlanBlock(t *testing.T) {
	s := newTestServer(&planHTTPFake{plan: httpSamplePlan})
	w := do(t, s, http.MethodGet, "/capabilities", "")
	var caps map[string]any
	mustDecode(t, w, &caps)

	if caps["explain"] != true {
		t.Errorf("explain = %v, want true", caps["explain"])
	}
	plan, ok := caps["plan"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities has no plan block: %v", caps)
	}
	if plan["available"] != true {
		t.Errorf("plan.available = %v, want true", plan["available"])
	}
	if plan["analyze_requires_auth"] != true {
		t.Errorf("plan.analyze_requires_auth = %v, want true", plan["analyze_requires_auth"])
	}
	datasets, _ := plan["datasets"].([]any)
	found := false
	for _, d := range datasets {
		if d == "perf" {
			found = true
		}
	}
	if !found {
		t.Errorf("plan.datasets = %v, want it to include perf", datasets)
	}

	// No database: the route is still described, but availability is honest.
	s = newTestServer(engine.UnavailableExecutor{})
	w = do(t, s, http.MethodGet, "/capabilities", "")
	mustDecode(t, w, &caps)
	plan, _ = caps["plan"].(map[string]any)
	if plan["available"] != false {
		t.Errorf("plan.available = %v with no database, want false", plan["available"])
	}
	if reason, _ := plan["reason"].(string); reason == "" {
		t.Error("plan.reason is empty with no database; the refusal must name the reason")
	}
}
