// Package eval implements result-contract comparison (technical design §6.1)
// and the /answers/evaluate operation. It never trusts SQL text for grading:
// correctness comes from running the query and comparing its result.
package eval

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// EvaluatorVersion identifies this evaluator implementation.
const EvaluatorVersion = "go-1"

// Contract is the result contract that governs comparison. Unknown or empty
// enum values default per §6.1: order ignored, duplicates preserved.
type Contract struct {
	Columns          []string `json:"columns"`
	ColumnOrder      string   `json:"column_order"`
	RowOrder         string   `json:"row_order"`
	Duplicates       string   `json:"duplicates"`
	NumericTolerance float64  `json:"numeric_tolerance"`
}

func (c Contract) normalized() Contract {
	if c.ColumnOrder != "exact" {
		c.ColumnOrder = "ignored"
	}
	if c.RowOrder != "exact" {
		c.RowOrder = "ignored"
	}
	if c.Duplicates != "ignored" {
		c.Duplicates = "preserved"
	}
	if c.NumericTolerance < 0 {
		c.NumericTolerance = 0
	}
	return c
}

// ExpectedResult is the reference output that rows are compared against.
type ExpectedResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// Exercise is the subset of ExerciseSpec the evaluator needs: the result
// contract, the expected result, and the optional reference solution used to
// derive an expected result when none is given.
type Exercise struct {
	Contract          Contract        `json:"result_contract"`
	ReferenceSolution string          `json:"reference_solution"`
	Result            *ExpectedResult `json:"result"`
}

func (e Exercise) expected() (ExpectedResult, bool) {
	if e.Result != nil {
		return *e.Result, true
	}
	return ExpectedResult{}, false
}

func (e Exercise) reference() string {
	return e.ReferenceSolution
}

// Request is the /answers/evaluate body.
type Request struct {
	SQL      string        `json:"sql"`
	Limits   engine.Limits `json:"limits"`
	Exercise Exercise      `json:"exercise"`
}

// CheckResult is one graded check in the response.
type CheckResult struct {
	Label string `json:"label"`
	Code  string `json:"code"`
	OK    bool   `json:"ok"`
}

// Response is the /answers/evaluate body: the real QueryResult for the learner's
// SQL, the per-check verdicts, the overall pass flag, and a duration string.
type Response struct {
	QR         engine.QueryResult `json:"qr"`
	Checks     []CheckResult      `json:"checks"`
	Passed     bool               `json:"passed"`
	DurationMS string             `json:"duration_ms"`
}

// Evaluate runs the learner's SQL, compares the real result to the exercise's
// contract, and reports the contract checklist. An execution error is an
// execution failure (reported through qr.status), never a "wrong answer".
func Evaluate(ctx context.Context, ex engine.Executor, req Request) Response {
	start := time.Now()
	/* Fill, not Merge: the HTTP handler already resolved request-versus-server
	   with Merge, where the server's configured caps are the ceiling. Merging
	   the compiled DefaultLimits() again here would clamp down, so a gateway
	   configured above the default (QC_MAX_ROWS=2000) would silently run at the
	   default 500. */
	limits := req.Limits.Fill(engine.DefaultLimits())

	qr := engine.Execute(ctx, ex, req.SQL, limits)
	resp := Response{QR: qr}

	contract := req.Exercise.Contract.normalized()
	expected, hasExpected := req.Exercise.expected()
	referenceFailed := false
	if !hasExpected {
		if ref := req.Exercise.reference(); strings.TrimSpace(ref) != "" {
			refQR := engine.Execute(ctx, ex, ref, limits)
			if refQR.Status == "succeeded" && !refQR.Truncated {
				expected = ExpectedResult{Columns: columnNames(refQR.Columns), Rows: refQR.Rows}
				hasExpected = true
			} else {
				// A reference that does not run is an authoring bug, not the
				// learner's. Say so instead of silently degrading to a column
				// check.
				referenceFailed = true
			}
		}
	}

	var cmp Comparison
	if hasExpected {
		cmp = CompareDetailed(expected, contract, qr)
	} else {
		cmp = CompareColumnsDetailed(contract, qr)
	}
	contractOK := cmp.OK

	resp.Checks = comparisonChecks(cmp, contract)
	if referenceFailed {
		resp.Checks = append(resp.Checks, CheckResult{
			Label: "Reference query could not be executed",
			Code:  "EVALUATION_ERROR",
			OK:    false,
		})
	}

	passed := qr.Status == "succeeded" && !qr.Truncated && contractOK && !referenceFailed
	for _, c := range resp.Checks {
		if !c.OK {
			passed = false
		}
	}
	resp.Passed = passed
	resp.DurationMS = fmt.Sprintf("%.1f", engine.RoundMS(time.Since(start)))
	return resp
}

func columnNames(cols []engine.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// comparisonChecks turns a structured comparison into the checklist the lesson
// UI renders. Labels match web/src/api/compare.ts, so all three adapters show
// the same rows and a green row means the database returned the right thing.
func comparisonChecks(cmp Comparison, contract Contract) []CheckResult {
	contract = contract.normalized()
	columnCode := CodeColumn
	if slices.Contains(cmp.Codes, CodeColumnOrder) {
		columnCode = CodeColumnOrder
	}
	columnLabel := "Column names"
	if contract.ColumnOrder == "exact" {
		columnLabel = "Columns and column order"
	}

	out := []CheckResult{{Label: columnLabel, Code: columnCode, OK: cmp.ColumnsOK}}
	if cmp.RowsCountOK != nil {
		out = append(out, CheckResult{Label: "Row count", Code: CodeRowCount, OK: *cmp.RowsCountOK})
	}
	if cmp.ValuesOK != nil {
		out = append(out, CheckResult{Label: "Row values against the reference result", Code: CodeValueMismatch, OK: *cmp.ValuesOK})
	}
	if cmp.RowOrderOK != nil {
		out = append(out, CheckResult{Label: "Row order", Code: CodeRowOrder, OK: *cmp.RowOrderOK})
	}
	return out
}
