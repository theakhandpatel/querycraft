// Package eval implements result-contract comparison (technical design §6.1)
// and the /answers/evaluate operation. It never trusts SQL text for grading:
// correctness comes from running the query and comparing its result.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
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

// Setup names the dataset an exercise runs against.
type Setup struct {
	Dataset       string `json:"dataset"`
	SchemaVersion int    `json:"schema_version"`
	SeedVersion   int    `json:"seed_version"`
	/* Dialect is the engine this exercise's contract was written for. Grading a
	   statement the *other* dialect produced against it would compare
	   engine-specific values (a boolean against 0/1, a numeric against text,
	   a timestamp's rendering) and call a correct answer wrong. */
	Dialect string `json:"dialect"`
}

// ExpectedResult is the reference output that rows are compared against.
type ExpectedResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// Check is one author-supplied grading check. A check may carry a RE2 `pattern`
// (or a `test` field holding a regex string); when it does, the check is ok only
// if the SQL text also matches. Checks without a pattern are ok exactly when the
// result contract passes.
type Check struct {
	Label   string          `json:"label"`
	Code    string          `json:"code"`
	Pattern string          `json:"pattern,omitempty"`
	Test    json.RawMessage `json:"test,omitempty"`
}

// Exercise is the subset of ExerciseSpec the evaluator needs, plus the optional
// expected result and reference solution used to derive one. Both `contract`
// and the schema's `result_contract` are accepted.
type Exercise struct {
	ID                string          `json:"id"`
	Version           int             `json:"version"`
	LessonID          string          `json:"lesson_id"`
	Type              string          `json:"type"`
	Prompt            string          `json:"prompt"`
	Setup             Setup           `json:"setup"`
	Contract          Contract        `json:"result_contract"`
	ContractAlt       *Contract       `json:"contract"`
	ReferenceSolution string          `json:"reference_solution"`
	Solution          string          `json:"solution"`
	Hints             []string        `json:"hints"`
	Difficulty        string          `json:"difficulty"`
	Result            *ExpectedResult `json:"result"`
	ExpectedResult    *ExpectedResult `json:"expected_result"`
	ExpectedRows      [][]any         `json:"expected_rows"`
	Checks            []Check         `json:"checks"`
}

func (e Exercise) effectiveContract() Contract {
	if e.ContractAlt != nil {
		c := *e.ContractAlt
		if len(c.Columns) == 0 {
			c.Columns = e.Contract.Columns
		}
		if c.ColumnOrder == "" {
			c.ColumnOrder = e.Contract.ColumnOrder
		}
		if c.RowOrder == "" {
			c.RowOrder = e.Contract.RowOrder
		}
		if c.Duplicates == "" {
			c.Duplicates = e.Contract.Duplicates
		}
		return c.normalized()
	}
	return e.Contract.normalized()
}

func (e Exercise) expected() (ExpectedResult, bool) {
	if e.Result != nil {
		return *e.Result, true
	}
	if e.ExpectedResult != nil {
		return *e.ExpectedResult, true
	}
	if e.ExpectedRows != nil {
		return ExpectedResult{Columns: e.effectiveContract().Columns, Rows: e.ExpectedRows}, true
	}
	return ExpectedResult{}, false
}

func (e Exercise) reference() string {
	if strings.TrimSpace(e.ReferenceSolution) != "" {
		return e.ReferenceSolution
	}
	return e.Solution
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

// ErrorResponse is returned when the evaluator itself fails (§6.1 rule 9).
type ErrorResponse struct {
	Error *engine.QueryError `json:"error"`
}

// Evaluate runs the learner's SQL, compares the real result to the exercise's
// contract, and grades each check. An execution error is an execution failure
// (reported through qr.status), never a "wrong answer".
func Evaluate(ctx context.Context, ex engine.Executor, req Request) Response {
	start := time.Now()
	limits := req.Limits.Merge(engine.DefaultLimits())

	qr := engine.Execute(ctx, ex, req.SQL, limits)
	resp := Response{QR: qr, Checks: []CheckResult{}}

	contract := req.Exercise.effectiveContract()
	expected, hasExpected := req.Exercise.expected()
	referenceFailed := false
	if !hasExpected {
		if ref := req.Exercise.reference(); strings.TrimSpace(ref) != "" {
			refQR := engine.Execute(ctx, ex, ref, limits)
			if refQR.Status == "succeeded" && !refQR.Truncated {
				expected = ExpectedResult{Columns: columnNames(refQR.Columns), Rows: refQR.Rows}
				hasExpected = true
			} else {
				// A reference that does not run is our bug, not the learner's.
				// Say so instead of silently degrading to a column check.
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

	for _, chk := range req.Exercise.Checks {
		ok := contractOK
		if ok {
			if pat, has := chk.patternString(); has {
				if matched, err := matchPattern(pat, req.SQL); err == nil {
					ok = matched
				}
			}
		}
		resp.Checks = append(resp.Checks, CheckResult{Label: chk.Label, Code: NormalizeCode(chk.Code), OK: ok})
	}
	if len(req.Exercise.Checks) == 0 {
		resp.Checks = comparisonChecks(cmp, contract)
	}
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
	if contains(cmp.Codes, CodeColumnOrder) {
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

func primaryCode(codes []string) string {
	if len(codes) == 0 {
		return ""
	}
	return codes[0]
}
