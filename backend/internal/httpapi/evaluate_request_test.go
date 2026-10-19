package httpapi

import (
	"encoding/json"
	"testing"
)

// TestEvaluateRequestReadsOnlyTheGradingFields pins the accepted exercise field
// set to the one eval.Request and evaluateRequest.UnmarshalJSON decode:
// result_contract, reference_solution and result. The stale aliases travel but
// are not read, so a body that sends them is graded as if they were absent.
func TestEvaluateRequestReadsOnlyTheGradingFields(t *testing.T) {
	body := `{
	  "sql": "SELECT 1",
	  "exercise": {
	    "id": "ex.1",
	    "result_contract": {"columns": ["kept"], "column_order": "exact", "row_order": "exact", "duplicates": "preserved", "numeric_tolerance": 0},
	    "reference_solution": "SELECT 1 AS kept",
	    "result": {"columns": ["result_col"], "rows": [[1]]},
	    "contract": {"columns": ["ignored_contract"]},
	    "solution": "SELECT 2",
	    "expected_result": {"columns": ["ignored_expected"], "rows": []},
	    "expected_rows": [[1]],
	    "checks": [{"label": "ignored"}]
	  }
	}`
	var req evaluateRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	exercise := req.Request.Exercise
	if got := exercise.Contract.Columns; len(got) != 1 || got[0] != "kept" {
		t.Errorf("contract columns = %v, want the result_contract column", got)
	}
	if exercise.ReferenceSolution != "SELECT 1 AS kept" {
		t.Errorf("reference = %q, want the reference_solution value", exercise.ReferenceSolution)
	}
	if exercise.Result == nil || len(exercise.Result.Columns) != 1 || exercise.Result.Columns[0] != "result_col" {
		t.Errorf("result = %+v, want the result value", exercise.Result)
	}
}
