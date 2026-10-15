package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/perf"
)

/*
The plan fixtures are language-neutral documents: the same files are graded by
this package's walk and by the browser's (web/src/api/planContract.ts, checked by
web/scripts/plan-conformance-audit.mjs). What makes them evidence is that their
`summary` is not a claim someone typed next to a plan — it is exactly what the
walk makes of `plan`. A stored summary that no walk could produce is a fixture
that pins a number the app never emits, and a walk that disagrees with the stored
one is a bug in one of the two languages.

This is the check that catches an under-counted access path: a Bitmap Index Scan
prints no "Relation Name", the relation lives on the Bitmap Heap Scan above it,
and reading the attribute literally drops the index from `index_scans` on both
sides while the fixture says otherwise.
*/
func TestPlanFixturesSummarizeTheirOwnPlan(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "fixtures")
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
			var fx struct {
				Bundle struct {
					Plan    perf.PlanRoot    `json:"plan"`
					Summary perf.PlanSummary `json:"summary"`
				} `json:"bundle"`
			}
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			if fx.Bundle.Plan == nil {
				t.Fatalf("%s carries no bundle.plan to walk", name)
			}

			got, _ := summarizePlan(fx.Bundle.Plan, DefaultPlanNodeCap)
			want := fx.Bundle.Summary

			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("derived summary does not marshal: %v", err)
			}
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("stored summary does not marshal: %v", err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("%s: the walk of bundle.plan is not bundle.summary\n derived: %s\n stored:  %s",
					name, shortJSON(gotJSON), shortJSON(wantJSON))
			}
		})
	}
	if total < 8 {
		t.Fatalf("found %d perf fixtures in %s, want at least 8: an empty or shrunken fixture set proves nothing", total, dir)
	}
}

// shortJSON keeps a mismatch readable: the summaries differ in a field or two,
// and a whole plan dumped into a test log buries the difference it is reporting.
func shortJSON(raw []byte) string {
	const limit = 2000
	if len(raw) <= limit {
		return string(raw)
	}
	return string(raw[:limit]) + "…(truncated)"
}
