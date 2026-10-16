package perf

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

/* ---------- the rule cases, hand-built ---------- */

func requireNode(nodeType, relation string) PerfRequireNode {
	n := PerfRequireNode{NodeType: nodeType}
	if relation != "" {
		r := relation
		n.Relation = &r
	}
	return n
}

func forbidNode(nodeType, relation string, above float64) PerfForbidNode {
	return PerfForbidNode{NodeType: nodeType, Relation: relation, AbovePlanRows: &above}
}

func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }

// planNode builds the least a node needs for the grader.
func planNode(nodeType, relation string, rows float64, index string) PlanNode {
	n := PlanNode{NodeType: nodeType, EstimatedRows: floatPtr(rows), Warnings: []string{}}
	if relation != "" {
		r := relation
		n.Relation = &r
	}
	if index != "" {
		i := index
		n.Index = &i
	}
	return n
}

// planBundle builds a minimal succeeded bundle around a handful of nodes.
func planBundle(engine string, nodes ...PlanNode) *PlanBundle {
	summary := EmptySummary()
	summary.Nodes = nodes
	summary.NodeCount = len(nodes)
	for i := range nodes {
		n := &nodes[i]
		summary.NodeTypes = appendUniqueString(summary.NodeTypes, n.NodeType)
		if n.Relation != nil {
			summary.Relations = appendUniqueString(summary.Relations, *n.Relation)
		}
		if n.Index != nil {
			summary.IndexesUsed = appendUniqueString(summary.IndexesUsed, *n.Index)
		}
		if n.NodeType == "Seq Scan" && n.Relation != nil {
			var rows float64
			if n.EstimatedRows != nil {
				rows = *n.EstimatedRows
			}
			summary.SeqScans = append(summary.SeqScans, PlanSeqScan{Relation: *n.Relation, PlanRows: rows})
		}
		if n.Index != nil && n.Relation != nil && strings.Contains(strings.ToLower(n.NodeType), "index") {
			summary.IndexScans = append(summary.IndexScans, PlanIndexScan{
				Relation: *n.Relation, Index: *n.Index, NodeType: n.NodeType,
			})
		}
		if n.EstimatedRows != nil && *n.EstimatedRows > summary.MaxEstimatedRows {
			summary.MaxEstimatedRows = *n.EstimatedRows
		}
	}
	if nodes[0].EstimatedRows != nil {
		summary.PlanRows = *nodes[0].EstimatedRows
	}
	return &PlanBundle{
		Status:          StatusSucceeded,
		TimingPrecision: TimingEstimate,
		Engine:          engine,
		Plan:            PlanRoot{"Node Type": nodes[0].NodeType},
		Summary:         summary,
		Environment:     EmptyEnvironment(engine),
		Warnings:        []PlanWarning{},
	}
}

func appendUniqueString(list []string, value string) []string {
	for _, s := range list {
		if s == value {
			return list
		}
	}
	return append(list, value)
}

func codesOf(v Verdict) []string {
	out := make([]string, 0, len(v.Failures))
	for _, f := range v.Failures {
		out = append(out, f.Code)
	}
	sort.Strings(out)
	return out
}

func TestGradeRuleCases(t *testing.T) {
	indexed := planBundle("postgresql",
		planNode("Index Scan", "orders", 50, "orders_pkey"),
	)
	scanned := planBundle("postgresql",
		planNode("Seq Scan", "orders", 10000, ""),
	)
	small := planBundle("postgresql",
		planNode("Seq Scan", "currencies", 12, ""),
	)
	atThreshold := planBundle("postgresql",
		planNode("Seq Scan", "orders", 1000, ""),
	)

	cases := []struct {
		name     string
		bundle   *PlanBundle
		contract PerfContract
		want     []string
	}{
		{
			name:   "pass",
			bundle: indexed,
			contract: PerfContract{
				Engine:         EnginePostgres,
				RequireNodes:   []PerfRequireNode{requireNode("Index Scan", "orders")},
				RequireIndexes: []string{"orders_pkey"},
				MinIndexScans:  intPtr(1),
				MaxPlanRows:    floatPtr(100),
			},
		},
		{
			name:     "node missing",
			bundle:   scanned,
			contract: PerfContract{RequireNodes: []PerfRequireNode{requireNode("Index Scan", "orders")}},
			want:     []string{CodeNodeMissing},
		},
		{
			name:     "node forbidden above the threshold",
			bundle:   scanned,
			contract: PerfContract{ForbidNodes: []PerfForbidNode{forbidNode("Seq Scan", "orders", 1000)}},
			want:     []string{CodeNodeForbidden},
		},
		{
			name:   "a small table scan stays legal",
			bundle: small,
			contract: PerfContract{
				ForbidNodes: []PerfForbidNode{forbidNode("Seq Scan", "currencies", 1000)},
			},
		},
		{
			name:   "at the threshold stays legal",
			bundle: atThreshold,
			contract: PerfContract{
				ForbidNodes: []PerfForbidNode{forbidNode("Seq Scan", "orders", 1000)},
			},
		},
		{
			name:     "index not used",
			bundle:   scanned,
			contract: PerfContract{RequireIndexes: []string{"orders_ordered_at_idx"}},
			want:     []string{CodeIndexNotUsed},
		},
		{
			name:     "too few index scans",
			bundle:   scanned,
			contract: PerfContract{MinIndexScans: intPtr(2)},
			want:     []string{CodeIndexNotUsed},
		},
		{
			name:     "plan rows exceeded",
			bundle:   scanned,
			contract: PerfContract{MaxPlanRows: floatPtr(100)},
			want:     []string{CodePlanRowsExceeded},
		},
		{
			name:     "dialect mismatch",
			bundle:   scanned,
			contract: PerfContract{Engine: "mysql"},
			want:     []string{CodeDialectMismatch},
		},
		{
			name:     "a plan that was never captured",
			bundle:   FailedBundle(EnginePostgres, "ENVIRONMENT_UNAVAILABLE", "no database", ""),
			contract: PerfContract{RequireIndexes: []string{"orders_pkey"}},
			want:     []string{"ENVIRONMENT_UNAVAILABLE"},
		},
		{
			name:     "a nil bundle",
			bundle:   nil,
			contract: PerfContract{},
			want:     []string{"ENVIRONMENT_UNAVAILABLE"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Grade(tc.bundle, &tc.contract)
			want := tc.want
			if want == nil {
				want = []string{}
			}
			sort.Strings(want)
			if got.Passed != (len(want) == 0) {
				t.Fatalf("passed = %v (failures %v), want %v", got.Passed, codesOf(got), want)
			}
			if !equalStrings(codesOf(got), want) {
				t.Fatalf("failures = %v, want %v", codesOf(got), want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

/* ---------- the shared fixtures ---------- */

// perfFixture is one contracts/fixtures/perf-*.json file: a PerfContract, the
// PlanBundle it grades, and the verdict both languages must reach.
type perfFixture struct {
	Note            string        `json:"note"`
	Contract        PerfContract  `json:"contract"`
	Bundle          PlanBundle    `json:"bundle"`
	ExpectedOutcome fixtureResult `json:"expected_outcome"`
}

type fixtureResult struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`
}

func fixturesDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	// backend/internal/perf -> repository root
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "fixtures")
}

// TestGradeFixtures grades every pinned fixture. The files are language-neutral:
// this test and the schema audit in web/scripts plan-conformance-audit.mjs read
// the same documents, so a rule cannot be implemented in one language only.
func TestGradeFixtures(t *testing.T) {
	dir := fixturesDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixtures from %s: %v", dir, err)
	}

	total := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "perf-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		total++
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			var fx perfFixture
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}

			got := Grade(&fx.Bundle, &fx.Contract)
			want := fx.ExpectedOutcome.Failures
			if want == nil {
				want = []string{}
			}
			sort.Strings(want)
			if got.Passed != fx.ExpectedOutcome.Passed {
				t.Fatalf("passed = %v (failures %v), want %v", got.Passed, codesOf(got), fx.ExpectedOutcome.Passed)
			}
			if !equalStrings(codesOf(got), want) {
				t.Fatalf("failures = %v, want %v", codesOf(got), want)
			}

			// The fixture is also the one place a bundle is checked as JSON: a
			// nil slice marshals as null and violates "type":"array".
			encoded, err := json.Marshal(&fx.Bundle)
			if err != nil {
				t.Fatalf("bundle does not marshal: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("bundle does not unmarshal: %v", err)
			}
			for _, path := range [][]string{
				{"summary", "node_types"}, {"summary", "relations"}, {"summary", "indexes_used"},
				{"summary", "seq_scans"}, {"summary", "index_scans"}, {"summary", "nodes"},
				{"environment", "indexes"}, {"warnings"},
			} {
				node := any(decoded)
				for _, key := range path {
					obj, ok := node.(map[string]any)
					if !ok {
						t.Fatalf("path %v: %q is not an object", path, key)
					}
					node = obj[key]
				}
				if _, ok := node.([]any); !ok {
					t.Errorf("path %v is %T, want an array (a nil slice marshals as null)", path, node)
				}
			}
		})
	}
	if total < 8 {
		t.Fatalf("expected at least 8 perf fixtures in %s, found %d", dir, total)
	}
}

/* ---------- the honesty guard ---------- */

// TestGradeReadsNoTiming is the M5 exit gate's "no graded assertion reads a
// timing" clause, enforced against the package's own source rather than against
// a code review: it parses every non-test file in this package and fails on any
// selector read of a field the contract deliberately excludes. A json-tag
// line declares the field (that is plan.go describing the wire shape); a
// selector is a read.
//
// explain.go is exempt: the plan capture builds those measurements and writes
// duration_ms, so it touches them by design. The clause is about grading, and
// Grade lives in grade.go, which this guard still reads in full.
func TestGradeReadsNoTiming(t *testing.T) {
	forbidden := map[string]string{
		"ActualTimeMS": "actual_time_ms",
		"DurationMS":   "duration_ms",
		"ActualRows":   "actual_rows",
		"Loops":        "loops",
		"BuffersRead":  "buffers_read",
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "explain.go" {
			// The capture builds the measurements by design.
			continue
		}
		scanned++
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			jsonName, bad := forbidden[sel.Sel.Name]
			if !bad {
				return true
			}
			pos := fset.Position(sel.Pos())
			t.Errorf("%s reads %s (a graded assertion must never read a timing or a measurement)",
				pos, jsonName)
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no package source was scanned; the guard proved nothing")
	}
}
