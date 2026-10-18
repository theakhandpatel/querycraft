package eval

import (
	"context"
	"slices"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

type frows struct {
	cols []engine.Column
	rows [][]any
	i    int
	cur  []any
}

func (r *frows) Columns() []engine.Column { return r.cols }
func (r *frows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.cur = r.rows[r.i]
	r.i++
	return true
}
func (r *frows) Values() ([]any, error) { return r.cur, nil }
func (r *frows) Err() error             { return nil }
func (r *frows) Close()                 {}

type testErr struct{}

func (testErr) Error() string { return "execution boom" }

type fakeExec struct {
	bySQL   map[string]*frows
	def     *frows
	fail    map[string]bool
	err     error
	lastSQL string
}

func (e *fakeExec) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	e.lastSQL = sql
	if e.err != nil {
		return nil, e.err
	}
	if e.fail != nil && e.fail[sql] {
		return nil, testErr{}
	}
	if e.bySQL != nil {
		if r, ok := e.bySQL[sql]; ok {
			return r, nil
		}
	}
	if e.def == nil {
		return nil, nil
	}
	return e.def, nil
}
func (e *fakeExec) Ping(ctx context.Context) error              { return nil }
func (e *fakeExec) Version(ctx context.Context) (string, error) { return "test", nil }
func (e *fakeExec) Stats() engine.PoolStats                     { return engine.PoolStats{} }
func (e *fakeExec) Ready(ctx context.Context) (bool, string)    { return true, "" }
func (e *fakeExec) Dialect() string                             { return engine.DialectPostgres }
func (e *fakeExec) EngineLabel() string                         { return engine.EngineName }
func (e *fakeExec) ClassifyError(err error) (string, bool)      { return "", false }
func (e *fakeExec) Diagnostics(ctx context.Context) map[string]any {
	return nil
}
func (e *fakeExec) Close() error { return nil }

func colsOf(names ...string) []engine.Column {
	out := make([]engine.Column, len(names))
	for i, n := range names {
		out[i] = engine.Column{Name: n, Type: "text"}
	}
	return out
}

func makeQR(names []string, rows [][]any) engine.QueryResult {
	return engine.QueryResult{Status: "succeeded", Columns: colsOf(names...), Rows: rows, RowCount: len(rows), Engine: engine.EngineName}
}

func baseContract(names ...string) Contract {
	return Contract{Columns: names, ColumnOrder: "exact", RowOrder: "ignored", Duplicates: "preserved"}
}

func mustPass(t *testing.T, ok bool, codes []string) {
	t.Helper()
	if !ok {
		t.Fatalf("expected comparison to pass, got codes %v", codes)
	}
	if len(codes) != 0 {
		t.Fatalf("expected no codes on pass, got %v", codes)
	}
}

func mustFail(t *testing.T, ok bool, codes []string, wantCode string) {
	t.Helper()
	if ok {
		t.Fatalf("expected comparison to fail")
	}
	if wantCode != "" && !slices.Contains(codes, wantCode) {
		t.Fatalf("codes = %v, want to contain %s", codes, wantCode)
	}
}

func TestCompareEmptyResultValid(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"n"}, Rows: [][]any{}}
	qr := makeQR([]string{"n"}, [][]any{})
	cmp := CompareDetailed(exp, baseContract("n"), qr)
	mustPass(t, cmp.OK, cmp.Codes)
}

func TestCompareColumnMismatch(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"name"}, Rows: [][]any{{"a"}}}
	qr := makeQR([]string{"nombre"}, [][]any{{"a"}})
	cmp := CompareDetailed(exp, baseContract("name"), qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeColumn)
}

func TestCompareColumnOrderExactVsIgnored(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"a", "b"}, Rows: [][]any{{1, 2}}}
	qr := makeQR([]string{"b", "a"}, [][]any{{2, 1}})

	exact := baseContract("a", "b")
	exact.ColumnOrder = "exact"
	cmp := CompareDetailed(exp, exact, qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeColumnOrder)

	ignored := baseContract("a", "b")
	ignored.ColumnOrder = "ignored"
	cmp = CompareDetailed(exp, ignored, qr)
	mustPass(t, cmp.OK, cmp.Codes)
}

func TestCompareRowOrderIgnoredVsExact(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"n"}, Rows: [][]any{{"x"}, {"y"}}}
	qr := makeQR([]string{"n"}, [][]any{{"y"}, {"x"}})

	ignored := baseContract("n")
	ignored.RowOrder = "ignored"
	cmp := CompareDetailed(exp, ignored, qr)
	mustPass(t, cmp.OK, cmp.Codes)

	exact := baseContract("n")
	exact.RowOrder = "exact"
	cmp = CompareDetailed(exp, exact, qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeRowOrder)
}

func TestCompareDuplicatesPreservedVsIgnored(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"n"}, Rows: [][]any{{"x"}, {"x"}}}
	qr := makeQR([]string{"n"}, [][]any{{"x"}, {"x"}, {"x"}})

	preserved := baseContract("n")
	ignored := baseContract("n")
	ignored.Duplicates = "ignored"

	cmp := CompareDetailed(exp, preserved, qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeRowCount)

	cmp = CompareDetailed(exp, ignored, qr)
	mustPass(t, cmp.OK, cmp.Codes)
}

func TestCompareRowCount(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"n"}, Rows: [][]any{{1}, {2}}}
	qr := makeQR([]string{"n"}, [][]any{{1}})
	cmp := CompareDetailed(exp, baseContract("n"), qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeRowCount)
}

func TestCompareValueMismatch(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"n"}, Rows: [][]any{{"a"}}}
	qr := makeQR([]string{"n"}, [][]any{{"b"}})
	cmp := CompareDetailed(exp, baseContract("n"), qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeValueMismatch)
}

func TestCompareNullDistinct(t *testing.T) {
	cases := []struct {
		name     string
		expected any
		actual   any
		wantOK   bool
	}{
		{"null vs null", nil, nil, true},
		{"null vs empty", nil, "", false},
		{"null vs zero", nil, 0, false},
		{"empty vs zero", "", 0, false},
		{"empty vs empty", "", "", true},
		{"bool vs bool", true, true, true},
		{"bool vs string", true, "true", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := valueEqual(tc.expected, tc.actual, 0); got != tc.wantOK {
				t.Fatalf("valueEqual(%#v, %#v) = %v, want %v", tc.expected, tc.actual, got, tc.wantOK)
			}
		})
	}
}

func TestCompareNumericTolerance(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"p"}, Rows: [][]any{{59.0}}}
	qr := makeQR([]string{"p"}, [][]any{{59.4}})

	tight := baseContract("p")
	cmp := CompareDetailed(exp, tight, qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeValueMismatch)

	loose := baseContract("p")
	loose.NumericTolerance = 0.5
	cmp = CompareDetailed(exp, loose, qr)
	mustPass(t, cmp.OK, cmp.Codes)
}

func TestCompareNumericStringCoercionAndNoTextNormalization(t *testing.T) {
	// Expected data is often stringified; a numeric string compared to an engine
	// number is numeric. Two real strings are compared verbatim.
	exp := ExpectedResult{Columns: []string{"p"}, Rows: [][]any{{"59.00"}}}
	qr := makeQR([]string{"p"}, [][]any{{59.0}})
	cmp := CompareDetailed(exp, baseContract("p"), qr)
	mustPass(t, cmp.OK, cmp.Codes)

	// "042" as text must not equal "42" as text.
	exp2 := ExpectedResult{Columns: []string{"code"}, Rows: [][]any{{"042"}}}
	qr2 := makeQR([]string{"code"}, [][]any{{"42"}})
	cmp = CompareDetailed(exp2, baseContract("code"), qr2)
	mustFail(t, cmp.OK, cmp.Codes, CodeValueMismatch)
}

func TestCompareExecutionErrorIsNotWrongAnswer(t *testing.T) {
	exp := ExpectedResult{Columns: []string{"n"}, Rows: [][]any{{1}}}
	qr := engine.QueryResult{Status: "failed", Columns: []engine.Column{}, Rows: [][]any{}, Error: engine.NewError(engine.CodeSyntax, "syntax", "")}
	cmp := CompareDetailed(exp, baseContract("n"), qr)
	if cmp.OK {
		t.Fatalf("expected fail")
	}
	if len(cmp.Codes) != 0 {
		t.Fatalf("execution failure must not emit value/column codes, got %v", cmp.Codes)
	}
}

func TestCompareColumnsOnly(t *testing.T) {
	qr := makeQR([]string{"name", "category", "price"}, [][]any{{"a", "b", 1}})
	cmp := CompareColumnsDetailed(baseContract("name", "category", "price"), qr)
	mustPass(t, cmp.OK, cmp.Codes)

	cmp = CompareColumnsDetailed(baseContract("name", "category"), qr)
	mustFail(t, cmp.OK, cmp.Codes, CodeColumn)
}

func TestEvaluateSynthesizesContractChecklist(t *testing.T) {
	ex := &fakeExec{def: &frows{cols: colsOf("n"), rows: [][]any{{2}}}}
	req := Request{
		SQL: "SELECT n FROM t",
		Exercise: Exercise{
			Contract: baseContract("n"),
			Result:   &ExpectedResult{Columns: []string{"n"}, Rows: [][]any{{1}}},
		},
	}
	resp := Evaluate(context.Background(), ex, req)
	if resp.Passed {
		t.Fatalf("expected false")
	}
	// One row per contract rule, so a green row means the database really
	// returned the right thing.
	if len(resp.Checks) != 3 {
		t.Fatalf("expected 3 rule rows, got %+v", resp.Checks)
	}
	if !resp.Checks[0].OK || !resp.Checks[1].OK {
		t.Fatalf("columns and row count should pass: %+v", resp.Checks)
	}
	if resp.Checks[2].OK || resp.Checks[2].Code != CodeValueMismatch {
		t.Fatalf("row values should fail with %s: %+v", CodeValueMismatch, resp.Checks[2])
	}
}

func TestEvaluateFailingReferenceIsLoudNotSilent(t *testing.T) {
	ex := &fakeExec{bySQL: map[string]*frows{
		"SELECT answer": {cols: colsOf("n"), rows: [][]any{{1}}},
	}}
	ex.def = &frows{cols: colsOf("n"), rows: [][]any{{1}}}
	ex.fail = map[string]bool{"SELECT ref": true}
	req := Request{
		SQL: "SELECT answer",
		Exercise: Exercise{
			Contract:          baseContract("n"),
			ReferenceSolution: "SELECT ref",
		},
	}
	resp := Evaluate(context.Background(), ex, req)
	if resp.Passed {
		t.Fatalf("a reference that cannot run must not pass the learner")
	}
	found := false
	for _, c := range resp.Checks {
		if c.Code == "EVALUATION_ERROR" && !c.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an EVALUATION_ERROR row, got %+v", resp.Checks)
	}
}

func TestEvaluateUsesReferenceSolution(t *testing.T) {
	ex := &fakeExec{bySQL: map[string]*frows{
		"SELECT answer": {cols: colsOf("n"), rows: [][]any{{42}}},
		"SELECT ref":    {cols: colsOf("n"), rows: [][]any{{42}}},
	}}
	req := Request{
		SQL: "SELECT answer",
		Exercise: Exercise{
			Contract:          baseContract("n"),
			ReferenceSolution: "SELECT ref",
		},
	}
	resp := Evaluate(context.Background(), ex, req)
	if !resp.Passed {
		t.Fatalf("expected passed via reference solution, got %+v", resp)
	}
}

func TestEvaluateExecutionError(t *testing.T) {
	ex := &fakeExec{err: testErr{}}
	req := Request{
		SQL: "SELECT n FROM t",
		Exercise: Exercise{
			Contract: baseContract("n"),
			Result:   &ExpectedResult{Columns: []string{"n"}, Rows: [][]any{{1}}},
		},
	}
	resp := Evaluate(context.Background(), ex, req)
	if resp.Passed {
		t.Fatalf("execution failure must not pass")
	}
	if resp.QR.Status != "failed" {
		t.Fatalf("qr status = %q, want failed", resp.QR.Status)
	}
	if len(resp.Checks) == 0 || resp.Checks[0].OK {
		t.Fatalf("checks must be not ok on execution failure: %+v", resp.Checks)
	}
}

// TestEvaluateLimitAboveDefaultSurvives pins the double-merge bug in
// Evaluate: it re-merged DefaultLimits(), which clamps DOWN, so a gateway
// configured with QC_MAX_ROWS=2000 silently ran at the compiled default of
// 500. A limit above the default must survive to the executor.
func TestEvaluateLimitAboveDefaultSurvives(t *testing.T) {
	// 600 rows: more than DefaultMaxRows (500), fewer than the raised cap.
	rows := make([][]any, 600)
	for i := range rows {
		rows[i] = []any{i + 1}
	}
	ex := &fakeExec{def: &frows{cols: colsOf("n"), rows: rows}}
	req := Request{
		SQL:    "SELECT n FROM big",
		Limits: engine.Limits{MaxRows: 2000, MaxBytes: 8 << 20, TimeoutMS: 20_000},
		Exercise: Exercise{
			Contract: baseContract("n"),
			Result:   &ExpectedResult{Columns: []string{"n"}, Rows: rows},
		},
	}
	resp := Evaluate(context.Background(), ex, req)
	if resp.QR.Error != nil {
		t.Fatalf("Evaluate error = %+v", resp.QR.Error)
	}
	if resp.QR.RowCount != 600 {
		t.Errorf("RowCount = %d, want 600; the raised cap was clamped to the compiled default", resp.QR.RowCount)
	}
	if resp.QR.Truncated {
		t.Errorf("Truncated = true at 600 rows under a 2000-row cap")
	}
	if !resp.Passed {
		t.Errorf("expected the full result to pass, got %+v", resp.Checks)
	}
}
