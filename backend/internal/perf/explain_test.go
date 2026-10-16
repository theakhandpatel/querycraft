package perf

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/*
A purpose-built fake rather than the one in the engine package's tests: the plan
capture asks several different questions (EXPLAIN, row counts, indexes,
settings) in one request, and each answer has to be a fresh cursor.
*/
type planFake struct {
	mu      sync.Mutex
	queries []string
	answer  func(ctx context.Context, sql string) (engine.Rows, error)
}

func (f *planFake) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	f.mu.Lock()
	f.queries = append(f.queries, sql)
	f.mu.Unlock()
	return f.answer(ctx, sql)
}

func (f *planFake) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

func (f *planFake) Ping(ctx context.Context) error              { return nil }
func (f *planFake) Version(ctx context.Context) (string, error) { return "PostgreSQL 16.3 (test)", nil }
func (f *planFake) Stats() engine.PoolStats                     { return engine.PoolStats{} }
func (f *planFake) Ready(ctx context.Context) (bool, string)    { return true, "" }
func (f *planFake) Dialect() string                             { return engine.DialectPostgres }
func (f *planFake) EngineLabel() string                         { return engine.EngineName }
func (f *planFake) ClassifyError(err error) (string, bool)      { return "", false }
func (f *planFake) Diagnostics(ctx context.Context) map[string]any {
	return nil
}
func (f *planFake) Close() error { return nil }

// fakeRows and cols are the smallest cursor a plan capture needs. A test helper
// is not a symbol to share across packages, so plan capture keeps its own.
type fakeRows struct {
	cols []engine.Column
	rows [][]any
	i    int
	cur  []any
}

func newFakeRows(cols []engine.Column, rows [][]any) *fakeRows {
	return &fakeRows{cols: cols, rows: rows, i: -1}
}

func (r *fakeRows) Columns() []engine.Column { return r.cols }
func (r *fakeRows) Next() bool {
	r.i++
	if r.i >= len(r.rows) {
		return false
	}
	r.cur = r.rows[r.i]
	return true
}
func (r *fakeRows) Values() ([]any, error) { return r.cur, nil }
func (r *fakeRows) Err() error             { return nil }
func (r *fakeRows) Close()                 {}

func cols(names ...string) []engine.Column {
	out := make([]engine.Column, len(names))
	for i, n := range names {
		out[i] = engine.Column{Name: n, Type: "text"}
	}
	return out
}

const samplePlanJSON = `[{
  "Plan": {
    "Node Type": "Aggregate",
    "Strategy": "Hashed",
    "Plan Rows": 5,
    "Total Cost": 1234.5,
    "Actual Rows": 5,
    "Actual Total Time": 3.2,
    "Actual Loops": 1,
    "Shared Read Blocks": 120,
    "Plans": [
      {
        "Node Type": "Seq Scan",
        "Parent Relationship": "Outer",
        "Relation Name": "orders",
        "Alias": "orders",
        "Plan Rows": 9000,
        "Total Cost": 900.0,
        "Actual Rows": 9000,
        "Actual Total Time": 2.1,
        "Actual Loops": 1,
        "Filter": "(status = 'delivered'::text)",
        "Shared Read Blocks": 120
      }
    ]
  },
  "Planning Time": 0.5,
  "Execution Time": 4.0
}]`

// explainFake answers the EXPLAIN with planResult and every environment query
// with a small, fixed result set.
func explainFake(planResult string, planErr error) *planFake {
	return &planFake{answer: func(ctx context.Context, sql string) (engine.Rows, error) {
		switch {
		case strings.HasPrefix(sql, "EXPLAIN"):
			if planErr != nil {
				return nil, planErr
			}
			return newFakeRows(cols("QUERY PLAN"), [][]any{{planResult}}), nil
		case strings.Contains(sql, "pg_class"):
			return newFakeRows(cols("relname", "reltuples"), [][]any{
				{"customers", float64(2000)}, {"orders", float64(10000)},
				{"order_items", float64(20000)}, {"products", float64(500)},
			}), nil
		case strings.Contains(sql, "pg_indexes"):
			return newFakeRows(cols("tablename", "indexname", "indexdef"), [][]any{
				{"orders", "orders_customer_id_idx", "CREATE INDEX orders_customer_id_idx ON dataset.orders USING btree (customer_id)"},
			}), nil
		case strings.Contains(sql, "current_setting"):
			return newFakeRows(cols("setting"), [][]any{{
				"16.3", "4", "1", "524288", "16MB", "on", "on", "on", "on", "on", "on",
			}}), nil
		}
		return nil, nil
	}}
}

func explainOnce(t *testing.T, fake *planFake, sql string, opts ExplainOptions) *PlanBundle {
	t.Helper()
	bundle, err := Explain(context.Background(), fake, sql, opts)
	if err != nil {
		t.Fatalf("Explain returned an error: %v", err)
	}
	if bundle == nil {
		t.Fatal("Explain returned a nil bundle")
	}
	return bundle
}

func decodeBundle(t *testing.T, bundle *PlanBundle) map[string]any {
	t.Helper()
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("bundle does not marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("bundle does not unmarshal: %v", err)
	}
	return out
}

// planArrayPaths are every field contracts/plan.schema.json declares as an
// array. A nil slice marshals as null and violates "type":"array" — the M4
// lesson — so each one must be present and be a JSON array.
var planArrayPaths = [][]string{
	{"summary", "node_types"},
	{"summary", "relations"},
	{"summary", "indexes_used"},
	{"summary", "seq_scans"},
	{"summary", "index_scans"},
	{"summary", "nodes"},
	{"environment", "indexes"},
	{"warnings"},
}

func assertBundleShape(t *testing.T, decoded map[string]any) {
	t.Helper()
	for _, key := range []string{
		"status", "analyze", "timing_precision", "engine", "plan", "summary",
		"environment", "warnings", "duration_ms", "error",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("bundle is missing required field %q", key)
		}
	}
	for _, path := range planArrayPaths {
		node := any(decoded)
		ok := true
		for _, key := range path {
			obj, isObj := node.(map[string]any)
			if !isObj {
				t.Fatalf("path %v: %q is not an object", path, key)
			}
			node, ok = obj[key]
			if !ok {
				t.Fatalf("path %v is missing", path)
			}
		}
		if node == nil {
			t.Errorf("path %v marshalled as null, want []", path)
			continue
		}
		if _, isArr := node.([]any); !isArr {
			t.Errorf("path %v is %T, want an array", path, node)
		}
	}
	env, _ := decoded["environment"].(map[string]any)
	if env == nil {
		t.Fatal("environment is not an object")
	}
	for _, key := range []string{"engine", "engine_version", "scale_factor", "row_counts", "indexes", "settings"} {
		if _, ok := env[key]; !ok {
			t.Errorf("environment is missing required field %q", key)
		}
	}
	for _, key := range []string{"row_counts", "settings"} {
		if _, ok := env[key].(map[string]any); !ok {
			t.Errorf("environment.%s is %T, want an object", key, env[key])
		}
	}
}

// queryResultErrorCodes is the closed error enum from
// contracts/query-result.schema.json. A plan failure must use one of these: the
// plan contract shares the taxonomy rather than inventing a second one.
var queryResultErrorCodes = map[string]bool{
	"EMPTY_QUERY": true, "QUERY_SYNTAX_ERROR": true, "QUERY_RUNTIME_ERROR": true,
	"QUERY_TIMEOUT": true, "RESULT_LIMIT_EXCEEDED": true, "ENVIRONMENT_UNAVAILABLE": true,
	"PERSISTENCE_FAILED": true, "EVALUATION_ERROR": true,
}

func bundleWarningCodes(b *PlanBundle) []string {
	out := make([]string, 0, len(b.Warnings))
	for _, w := range b.Warnings {
		out = append(out, w.Code)
	}
	return out
}

func TestExplainSucceededBundleMarshals(t *testing.T) {
	fake := explainFake(samplePlanJSON, nil)
	bundle := explainOnce(t, fake, "select count(*) from orders",
		ExplainOptions{Analyze: true, Dataset: "perf", ScaleFactor: 125})

	if bundle.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", bundle.Status)
	}
	if bundle.TimingPrecision != TimingAnalyze || !bundle.Analyze {
		t.Fatalf("timing_precision = %q analyze = %v, want analyze/true", bundle.TimingPrecision, bundle.Analyze)
	}
	if bundle.Error != nil {
		t.Fatalf("error = %+v, want nil", bundle.Error)
	}

	decoded := decodeBundle(t, bundle)
	assertBundleShape(t, decoded)
	if decoded["error"] != nil {
		t.Errorf("error marshalled as %v, want null", decoded["error"])
	}
	if _, ok := decoded["plan"].(map[string]any); !ok {
		t.Errorf("plan is %T, want the root node object", decoded["plan"])
	}

	// The walk is the one planContract.ts performs.
	if bundle.Summary.NodeCount != 2 {
		t.Errorf("node_count = %d, want 2", bundle.Summary.NodeCount)
	}
	if len(bundle.Summary.SeqScans) != 1 || bundle.Summary.SeqScans[0].Relation != "orders" {
		t.Errorf("seq_scans = %+v, want orders", bundle.Summary.SeqScans)
	}
	if bundle.Summary.PlanRows != 5 || bundle.Summary.TotalCost != 1234.5 {
		t.Errorf("plan_rows/total_cost = %v/%v, want 5/1234.5", bundle.Summary.PlanRows, bundle.Summary.TotalCost)
	}
	if bundle.Summary.MaxEstimatedRows != 9000 {
		t.Errorf("max_estimated_rows = %v, want 9000", bundle.Summary.MaxEstimatedRows)
	}
	if !planHasWarning(bundleWarningCodes(bundle), WarnSeqScanLargeTable) {
		t.Errorf("warnings = %+v, want a large-seq-scan warning", bundle.Warnings)
	}

	// The environment is read, not invented.
	if bundle.Environment.EngineVersion != "16.3" {
		t.Errorf("engine_version = %q, want 16.3", bundle.Environment.EngineVersion)
	}
	if bundle.Environment.RowCounts["orders"] != 10000 {
		t.Errorf("row_counts[orders] = %v, want 10000", bundle.Environment.RowCounts["orders"])
	}
	if len(bundle.Environment.Indexes) != 1 || bundle.Environment.Indexes[0].Name != "orders_customer_id_idx" {
		t.Errorf("indexes = %+v", bundle.Environment.Indexes)
	}
	if bundle.Environment.Settings["work_mem"] != "16MB" {
		t.Errorf("settings[work_mem] = %q, want 16MB", bundle.Environment.Settings["work_mem"])
	}
	if bundle.Environment.Dataset == nil || *bundle.Environment.Dataset != "perf" {
		t.Errorf("dataset = %v, want perf", bundle.Environment.Dataset)
	}
	if bundle.Environment.ScaleFactor != 125 {
		t.Errorf("scale_factor = %d, want 125", bundle.Environment.ScaleFactor)
	}
}

// A bundle that never captured a plan still marshals as a complete PlanBundle,
// with the arrays as [] and no invented numbers.
func TestExplainFailedBundleMarshals(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"deadline", context.DeadlineExceeded, engine.CodeTimeout},
		{"canceled", context.Canceled, engine.CodeTimeout},
		{"syntax", &pgconn.PgError{Code: "42601", Message: `syntax error at or near "selct"`}, engine.CodeSyntax},
		{"runtime", &pgconn.PgError{Code: "42P01", Message: `relation "nope" does not exist`}, engine.CodeRuntime},
		{"environment", errors.Join(engine.ErrEnvironmentUnavailable, errors.New("dial tcp refused")), engine.CodeEnvironmentUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := explainFake("", tc.err)
			bundle := explainOnce(t, fake, "select 1", ExplainOptions{})

			if bundle.Status != StatusFailed {
				t.Fatalf("status = %q, want failed", bundle.Status)
			}
			if bundle.TimingPrecision != TimingNone {
				t.Errorf("timing_precision = %q, want none", bundle.TimingPrecision)
			}
			if bundle.Plan != nil {
				t.Errorf("plan = %v, want null", bundle.Plan)
			}
			if bundle.Error == nil || bundle.Error.Code != tc.wantCode {
				t.Fatalf("error = %+v, want %s", bundle.Error, tc.wantCode)
			}
			if !queryResultErrorCodes[bundle.Error.Code] {
				t.Errorf("error code %q is not in the QueryResult taxonomy", bundle.Error.Code)
			}

			decoded := decodeBundle(t, bundle)
			assertBundleShape(t, decoded)
			if decoded["plan"] != nil {
				t.Errorf("plan marshalled as %v, want null", decoded["plan"])
			}
			// The failure envelope is the same one a QueryResult carries.
			errObj, ok := decoded["error"].(map[string]any)
			if !ok {
				t.Fatalf("error marshalled as %T, want an object", decoded["error"])
			}
			if errObj["code"] != tc.wantCode {
				t.Errorf("error.code = %v, want %s", errObj["code"], tc.wantCode)
			}
			if msg, _ := errObj["message"].(string); strings.TrimSpace(msg) == "" {
				t.Errorf("error.message is empty")
			}
		})
	}
}

func TestExplainEmptyQuery(t *testing.T) {
	fake := explainFake("", nil)
	bundle := explainOnce(t, fake, "  \n -- nothing", ExplainOptions{})
	if bundle.Error == nil || bundle.Error.Code != engine.CodeEmptyQuery {
		t.Fatalf("code = %+v, want %s", bundle.Error, engine.CodeEmptyQuery)
	}
	if len(fake.recorded()) != 0 {
		t.Fatalf("the executor was asked to plan blank SQL: %v", fake.recorded())
	}
}

// The analyze flag must reach the engine: a plain EXPLAIN for estimate, an
// EXPLAIN ANALYZE (with buffers) when the caller asked for one.
func TestExplainUsesAnalyzeForm(t *testing.T) {
	fake := explainFake(samplePlanJSON, nil)
	explainOnce(t, fake, "select 1", ExplainOptions{Analyze: true})

	var explainSQL string
	for _, q := range fake.recorded() {
		if strings.HasPrefix(q, "EXPLAIN") {
			explainSQL = q
		}
	}
	if !strings.Contains(explainSQL, "ANALYZE") || !strings.Contains(explainSQL, "BUFFERS") {
		t.Fatalf("analyze EXPLAIN = %q, want ANALYZE and BUFFERS", explainSQL)
	}
	if !strings.Contains(explainSQL, "FORMAT JSON") {
		t.Fatalf("EXPLAIN = %q, want FORMAT JSON", explainSQL)
	}

	fake = explainFake(samplePlanJSON, nil)
	explainOnce(t, fake, "select 1", ExplainOptions{})
	for _, q := range fake.recorded() {
		if strings.HasPrefix(q, "EXPLAIN") && strings.Contains(q, "ANALYZE") {
			t.Fatalf("plain EXPLAIN = %q, want no ANALYZE", q)
		}
	}
}

// A runaway ANALYZE is stopped by the deadline and surfaces QUERY_TIMEOUT, the
// same code a runaway query gets.
func TestExplainRunawayAnalyzeTimesOut(t *testing.T) {
	fake := &planFake{answer: func(ctx context.Context, sql string) (engine.Rows, error) {
		if strings.HasPrefix(sql, "EXPLAIN") {
			return nil, context.DeadlineExceeded
		}
		return nil, nil
	}}
	bundle := explainOnce(t, fake, "select pg_sleep(60)", ExplainOptions{Analyze: true})
	if bundle.Error == nil || bundle.Error.Code != engine.CodeTimeout {
		t.Fatalf("code = %+v, want %s", bundle.Error, engine.CodeTimeout)
	}
}

// The byte cap is the one POST /query applies: a plan larger than it is a hard
// failure rather than a half-reported document.
func TestExplainByteCap(t *testing.T) {
	huge := "[" + strings.Repeat(" ", 4096) + "]"
	fake := explainFake(huge, nil)
	bundle := explainOnce(t, fake, "select 1", ExplainOptions{Limits: engine.Limits{MaxBytes: 128}})
	if bundle.Error == nil || bundle.Error.Code != engine.CodeResultLimit {
		t.Fatalf("code = %+v, want %s", bundle.Error, engine.CodeResultLimit)
	}
}

// TestExplainLimitAboveDefaultSurvives pins the double-merge bug in Explain:
// it re-merged DefaultLimits(), which clamps DOWN, so a gateway configured
// with a byte cap above the compiled default (1 MiB) silently planned at the
// default. A plan larger than the default but within the raised cap must
// survive.
func TestExplainLimitAboveDefaultSurvives(t *testing.T) {
	// A JSON string of 1 MiB + 1 KiB: larger than DefaultMaxBytes (1 MiB),
	// smaller than the raised cap (2 MiB).
	big := `"` + strings.Repeat("a", (1<<20)+1024) + `"`
	fake := explainFake(big, nil)
	bundle := explainOnce(t, fake, "select 1", ExplainOptions{Limits: engine.Limits{MaxBytes: 2 << 20}})
	if bundle.Error != nil {
		t.Fatalf("Explain error = %+v, want the raised byte cap to survive", bundle.Error)
	}
}

func TestExplainNodeCap(t *testing.T) {
	node := map[string]any{"Node Type": "Result"}
	for i := 0; i < 6; i++ {
		node = map[string]any{"Node Type": "Result", "Plans": []any{node}}
	}
	raw, err := json.Marshal([]any{map[string]any{"Plan": node}})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	fake := explainFake(string(raw), nil)
	bundle := explainOnce(t, fake, "select 1", ExplainOptions{NodeCap: 3})
	if bundle.Summary.NodeCount != 3 {
		t.Errorf("node_count = %d, want the cap of 3", bundle.Summary.NodeCount)
	}
	if !planHasWarning(bundleWarningCodes(bundle), WarnNodeCapReached) {
		t.Errorf("warnings = %+v, want %s", bundle.Warnings, WarnNodeCapReached)
	}
}

// An estimate that is off by orders of magnitude is only flagged when the
// statement was executed: without ANALYZE there is no actual_rows to compare.
func TestExplainEstimateOffOnlyWhenAnalyzed(t *testing.T) {
	off := `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"orders","Plan Rows":10,"Total Cost":1,"Actual Rows":50000,"Actual Total Time":1}}]`
	fake := explainFake(off, nil)
	estimate := explainOnce(t, fake, "select 1", ExplainOptions{})
	if planHasWarning(bundleWarningCodes(estimate), WarnEstimateOff) {
		t.Errorf("estimate-only bundle carries an estimate-off warning")
	}

	fake = explainFake(off, nil)
	analyze := explainOnce(t, fake, "select 1", ExplainOptions{Analyze: true})
	if !planHasWarning(bundleWarningCodes(analyze), WarnEstimateOff) {
		t.Errorf("analyze bundle warnings = %+v, want %s", analyze.Warnings, WarnEstimateOff)
	}
	if !planHasWarning(analyze.Summary.Nodes[0].Warnings, WarnEstimateOff) {
		t.Errorf("node warnings = %+v, want %s", analyze.Summary.Nodes[0].Warnings, WarnEstimateOff)
	}
}

// The plan is captured under the same deadline the handler sets for a query:
// sessionSettings turns it into a server-side statement_timeout, so this is
// what stops a runaway ANALYZE.
func TestExplainRunsUnderTheRequestDeadline(t *testing.T) {
	var sawDeadline bool
	fake := &planFake{answer: func(ctx context.Context, sql string) (engine.Rows, error) {
		if strings.HasPrefix(sql, "EXPLAIN") {
			_, sawDeadline = ctx.Deadline()
		}
		return nil, errors.New("stop after the first call")
	}}
	_, _ = Explain(context.Background(), fake, "select 1", ExplainOptions{
		Limits: engine.Limits{TimeoutMS: 1234},
	})
	if !sawDeadline {
		t.Fatal("the EXPLAIN ran without a deadline")
	}
}

// A relation that was never analyzed reports reltuples = -1; a negative row
// count is worse than an honest zero.
func TestExplainClampsNegativeRowCounts(t *testing.T) {
	fake := &planFake{answer: func(ctx context.Context, sql string) (engine.Rows, error) {
		switch {
		case strings.HasPrefix(sql, "EXPLAIN"):
			return newFakeRows(cols("QUERY PLAN"), [][]any{{samplePlanJSON}}), nil
		case strings.Contains(sql, "pg_class"):
			return newFakeRows(cols("relname", "reltuples"), [][]any{{"orders", float64(-1)}}), nil
		}
		return nil, errors.New("unavailable")
	}}
	bundle := explainOnce(t, fake, "select 1", ExplainOptions{})
	if got := bundle.Environment.RowCounts["orders"]; got != 0 {
		t.Fatalf("row_counts[orders] = %v, want 0", got)
	}
}

/*
A bitmap access is two nodes: a Bitmap Heap Scan that names the relation and a
Bitmap Index Scan underneath that names only the index. PostgreSQL prints no
"Relation Name" on the child, so a walk that reads the attribute literally
counts the index in IndexesUsed but drops it from IndexScans and Relations —
which silently under-counts the access path a selective predicate really takes.
A contract that says "at least two index scans" is then graded against a
summary that sees one. The relation is read back from the parent instead, and
this is the same walk web/src/api/planContract.ts performs.
*/
const bitmapPlanJSON = `[{
  "Plan": {
    "Node Type": "Limit",
    "Plan Rows": 5,
    "Total Cost": 210.4,
    "Plans": [
      {
        "Node Type": "Sort",
        "Parent Relationship": "Outer",
        "Plan Rows": 184,
        "Total Cost": 209.9,
        "Sort Key": ["ordered_at"],
        "Plans": [
          {
            "Node Type": "Bitmap Heap Scan",
            "Parent Relationship": "Outer",
            "Relation Name": "orders",
            "Alias": "orders",
            "Plan Rows": 184,
            "Total Cost": 106.2,
            "Recheck Cond": "(customer_id = 7)",
            "Plans": [
              {
                "Node Type": "Bitmap Index Scan",
                "Parent Relationship": "Outer",
                "Index Name": "orders_customer_id_idx",
                "Plan Rows": 184,
                "Total Cost": 0.0,
                "Index Cond": "(customer_id = 7)"
              }
            ]
          }
        ]
      }
    ]
  }
}]`

func TestExplainCountsABitmapIndexScanAsAnIndexAccess(t *testing.T) {
	fake := explainFake(bitmapPlanJSON, nil)
	bundle := explainOnce(t, fake, "select id from orders where customer_id = 7 order by ordered_at limit 5",
		ExplainOptions{Analyze: false, Dataset: "perf", ScaleFactor: 10000})

	summary := bundle.Summary
	if len(summary.IndexScans) != 1 {
		t.Fatalf("index_scans = %+v, want exactly the one bitmap access", summary.IndexScans)
	}
	scan := summary.IndexScans[0]
	if scan.Relation != "orders" || scan.Index != "orders_customer_id_idx" || scan.NodeType != "Bitmap Index Scan" {
		t.Fatalf("index_scans[0] = %+v, want the bitmap scan read back against its parent's relation", scan)
	}
	if !slices.Contains(summary.IndexesUsed, "orders_customer_id_idx") {
		t.Fatalf("indexes_used = %v, want orders_customer_id_idx", summary.IndexesUsed)
	}
	if !slices.Contains(summary.Relations, "orders") {
		t.Fatalf("relations = %v, want orders: the table a bitmap access reads is a relation the plan touches", summary.Relations)
	}

	// The inherited relation reaches the node the viewer renders, so the tree
	// reads "Bitmap Index Scan on orders" rather than an index with no table.
	var bitmap *PlanNode
	for i := range summary.Nodes {
		if summary.Nodes[i].NodeType == "Bitmap Index Scan" {
			bitmap = &summary.Nodes[i]
		}
	}
	if bitmap == nil {
		t.Fatal("summary has no Bitmap Index Scan node")
	}
	if bitmap.Relation == nil || *bitmap.Relation != "orders" {
		t.Fatalf("bitmap node relation = %v, want orders", bitmap.Relation)
	}

	// A node with no relation and no relation-bearing ancestor must stay
	// relationless: the walk inherits, it does not invent.
	for i := range summary.Nodes {
		if summary.Nodes[i].NodeType == "Limit" || summary.Nodes[i].NodeType == "Sort" {
			if summary.Nodes[i].Relation != nil {
				t.Fatalf("%s relation = %v, want nil", summary.Nodes[i].NodeType, *summary.Nodes[i].Relation)
			}
		}
	}

	// The point of the fix: the contract the lesson states is now satisfiable.
	verdict := Grade(bundle, &PerfContract{
		Engine:         "postgresql",
		RequireIndexes: []string{"orders_customer_id_idx"},
		MinIndexScans:  perfIntPtr(1),
	})
	if !verdict.Passed {
		t.Fatalf("grade = %+v, want passed", verdict.Failures)
	}
}

func perfIntPtr(v int) *int { return &v }
