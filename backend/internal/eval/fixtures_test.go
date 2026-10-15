package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// The language-neutral fixture suite (docs/technical-design.md §6.3) is the
// arbiter for result-contract comparison. The same JSON files under
// contracts/fixtures drive this test and scripts/fixtures-audit.mjs, so the Go
// and JavaScript evaluators cannot drift apart. A new rule is added as a
// fixture first.
//
// Go and JavaScript both grade every case; neither harness hardcodes an
// expected value, only the path to the shared files.

type fixtureContract struct {
	Columns          []string `json:"columns"`
	Rows             [][]any  `json:"rows"`
	ColumnOrder      string   `json:"column_order"`
	RowOrder         string   `json:"row_order"`
	Duplicates       string   `json:"duplicates"`
	NumericTolerance float64  `json:"numeric_tolerance"`
}

type fixtureActual struct {
	Status  string   `json:"status"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

type fixtureOutcome struct {
	Passed bool     `json:"passed"`
	Codes  []string `json:"codes"`
}

type contractFixture struct {
	Note            string          `json:"note"`
	Contract        fixtureContract `json:"contract"`
	ActualResult    fixtureActual   `json:"actual_result"`
	ExpectedOutcome fixtureOutcome  `json:"expected_outcome"`
}

// fixturesDir resolves contracts/fixtures relative to this package, not the
// working directory, so the test runs from anywhere.
func fixturesDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	// backend/internal/eval -> repository root
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "fixtures")
}

// codeSet normalizes feedback codes to a sorted, duplicate-free set, because
// expected_outcome.codes is order- and duplicate-insensitive.
func codeSet(codes []string) []string {
	out := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func TestContractFixtureSuite(t *testing.T) {
	dir := fixturesDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixtures from %s: %v", dir, err)
	}

	total := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// perf-*.json is a different contract family: a PerfContract graded
		// against a PlanBundle, read by backend/internal/perf and by
		// web/scripts/plan-conformance-audit.mjs. It has no columns/rows to
		// compare, so it is not a case for this comparator.
		if strings.HasPrefix(entry.Name(), "perf-") {
			continue
		}
		total++
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			var fx contractFixture
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}

			status := fx.ActualResult.Status
			if status == "" {
				status = "succeeded"
			}
			contract := Contract{
				Columns:          fx.Contract.Columns,
				ColumnOrder:      fx.Contract.ColumnOrder,
				RowOrder:         fx.Contract.RowOrder,
				Duplicates:       fx.Contract.Duplicates,
				NumericTolerance: fx.Contract.NumericTolerance,
			}
			expected := ExpectedResult{Columns: fx.Contract.Columns, Rows: fx.Contract.Rows}
			qr := engine.QueryResult{
				Status:   status,
				Columns:  colsOf(fx.ActualResult.Columns...),
				Rows:     fx.ActualResult.Rows,
				RowCount: len(fx.ActualResult.Rows),
			}

			cmp := CompareDetailed(expected, contract, qr)
			got := codeSet(cmp.Codes)
			want := codeSet(fx.ExpectedOutcome.Codes)
			if cmp.OK != fx.ExpectedOutcome.Passed {
				t.Fatalf("passed = %v, want %v (codes %v)", cmp.OK, fx.ExpectedOutcome.Passed, cmp.Codes)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("codes = %v, want %v", got, want)
			}
		})
	}
	if total < 20 {
		t.Fatalf("expected at least 20 fixtures in %s, found %d", dir, total)
	}
}
