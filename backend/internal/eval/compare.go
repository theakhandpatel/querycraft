package eval

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// Result-contract feedback codes (contracts/evaluation.schema.json).
const (
	CodeValueMismatch = "VALUE_MISMATCH"
	CodeColumn        = "COLUMN_MISMATCH"
	CodeColumnOrder   = "COLUMN_ORDER_MISMATCH"
	CodeRowCount      = "ROW_COUNT_MISMATCH"
	CodeRowOrder      = "ROW_ORDER_MISMATCH"
)

// validFeedbackCodes is the feedback-code enum from evaluation.schema.json.
// Keep in lockstep with that file — a code the contract declares but this set
// omits would be silently clamped to VALUE_MISMATCH by NormalizeCode, changing
// what the learner sees. The mock adapter emits these same authored codes, so
// the two sides must agree on the whole set.
var validFeedbackCodes = map[string]bool{
	"VALUE_MISMATCH":         true,
	"COLUMN_MISMATCH":        true,
	"COLUMN_ORDER_MISMATCH":  true,
	"ROW_COUNT_MISMATCH":     true,
	"ROW_ORDER_MISMATCH":     true,
	"FILTER_MISSING":         true,
	"AGGREGATION_MISSING":    true,
	"GROUPING_MISSING":       true,
	"JOIN_MISSING":           true,
	"JOIN_TYPE_MISMATCH":     true,
	"CTE_MISSING":            true,
	"WINDOW_MISSING":         true,
	"PARTITION_MISSING":      true,
	"WINDOW_ORDER_MISSING":   true,
	"RANK_FUNCTION_MISMATCH": true,
	"GRAIN_ERROR":            true,
	"EXPLANATION_MISSING":    true,
	"CASE_MISSING":           true,
	"DISTINCT_MISSING":       true,
	"NULL_SAFETY":            true,
	"NULL_TRAP":              true,
	"RECURSION_MISSING":      true,
	"SET_OP_MISSING":         true,
	"SUBQUERY_MISSING":       true,
	"TABLE_MISSING":          true,
	"EVALUATION_ERROR":       true,
}

// NormalizeCode keeps emitted feedback codes inside the schema enum.
func NormalizeCode(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	if validFeedbackCodes[c] {
		return c
	}
	return CodeValueMismatch
}

// CompareColumns checks only the column names of a successful result. It is the
// fallback when an exercise ships no expected rows and no reference solution.
func CompareColumns(contract Contract, qr engine.QueryResult) (bool, []string) {
	if qr.Status != "succeeded" || qr.Truncated {
		return false, nil
	}
	contract = contract.normalized()
	if len(contract.Columns) == 0 {
		return true, nil
	}
	return matchColumns(contract.Columns, columnNames(qr.Columns), contract.ColumnOrder)
}

// Compare applies every §6.1 rule:
//
//  1. execution errors are execution failures (handled by the caller),
//  2. column names are checked; order matters only when column_order is exact,
//  3. row order is ignored unless row_order is exact,
//  4. duplicates are preserved unless duplicates is ignored,
//  5. NULL is distinct from "" and 0,
//  6. numeric comparison uses the contract's tolerance,
//  7. no implicit text normalization,
//  8. an empty result is compared like any other.
func Compare(expected ExpectedResult, contract Contract, qr engine.QueryResult) (bool, []string) {
	if qr.Status != "succeeded" {
		return false, nil
	}
	if qr.Truncated {
		return false, []string{CodeRowCount}
	}
	contract = contract.normalized()

	wantCols := contract.Columns
	if len(wantCols) == 0 {
		wantCols = expected.Columns
	}
	gotCols := columnNames(qr.Columns)

	if ok, codes := matchColumns(wantCols, gotCols, contract.ColumnOrder); !ok {
		return false, codes
	}

	order := alignColumns(wantCols, gotCols, contract.ColumnOrder)
	actRows := projectRows(qr.Rows, order)

	if expected.Rows == nil {
		return true, nil
	}
	if ok, codes := compareRows(expected.Rows, actRows, contract); !ok {
		return false, dedupe(codes)
	}
	return true, nil
}

func matchColumns(want, got []string, order string) (bool, []string) {
	if len(want) != len(got) {
		return false, []string{CodeColumn}
	}
	if order == "exact" {
		var codes []string
		for i := range want {
			if want[i] != got[i] {
				codes = append(codes, CodeColumn)
				if contains(got, want[i]) {
					codes = append(codes, CodeColumnOrder)
				}
			}
		}
		if len(codes) > 0 {
			return false, dedupe(codes)
		}
		return true, nil
	}
	used := make([]bool, len(got))
	for _, w := range want {
		found := false
		for j, g := range got {
			if !used[j] && g == w {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false, []string{CodeColumn}
		}
	}
	return true, nil
}

// alignColumns returns, for each expected column, the index of the matching
// actual column, so values can be compared per named column even when the
// contract ignores column order.
func alignColumns(want, got []string, order string) []int {
	idx := make([]int, len(want))
	if order == "exact" {
		for i := range want {
			idx[i] = i
		}
		return idx
	}
	used := make([]bool, len(got))
	for i, w := range want {
		idx[i] = -1
		for j, g := range got {
			if !used[j] && g == w {
				used[j] = true
				idx[i] = j
				break
			}
		}
	}
	return idx
}

func projectRows(rows [][]any, order []int) [][]any {
	out := make([][]any, len(rows))
	for r, row := range rows {
		proj := make([]any, len(order))
		for k, src := range order {
			if src >= 0 && src < len(row) {
				proj[k] = row[src]
			}
		}
		out[r] = proj
	}
	return out
}

func compareRows(expected, actual [][]any, contract Contract) (bool, []string) {
	tol := contract.NumericTolerance

	if contract.RowOrder == "exact" {
		if len(expected) != len(actual) {
			return false, []string{CodeRowCount}
		}
		var codes []string
		for i := range expected {
			if !rowsEqual(expected[i], actual[i], tol) {
				codes = append(codes, CodeValueMismatch)
			}
		}
		if len(codes) == 0 {
			return true, nil
		}
		if multisetEqual(expected, actual, tol, contract.Duplicates) {
			codes = append(codes, CodeRowOrder)
		}
		return false, codes
	}

	if contract.Duplicates == "ignored" {
		expSet := uniqueRows(expected, tol)
		actSet := uniqueRows(actual, tol)
		if len(expSet) != len(actSet) {
			return false, []string{CodeRowCount}
		}
		for _, e := range expSet {
			if !containsRow(actSet, e, tol) {
				return false, []string{CodeValueMismatch}
			}
		}
		return true, nil
	}

	if len(expected) != len(actual) {
		return false, []string{CodeRowCount}
	}
	used := make([]bool, len(actual))
	for _, e := range expected {
		found := false
		for j := range actual {
			if !used[j] && rowsEqual(e, actual[j], tol) {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false, []string{CodeValueMismatch}
		}
	}
	return true, nil
}

func rowsEqual(a, b []any, tol float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !valueEqual(a[i], b[i], tol) {
			return false
		}
	}
	return true
}

// valueEqual is the value-level rule (§6.1 rules 5-7). NULL only equals NULL
// (never "" or 0); numeric values compare with tolerance; other values compare
// by exact type/value with no text normalization. A string is coerced to a
// number only when the other side is already a real number, so genuine text is
// never normalized ("042" != "42").
func valueEqual(a, b any, tol float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if isNumericValue(a) || isNumericValue(b) {
		if af, aok := toFloat(a); aok {
			if bf, bok := toFloat(b); bok {
				if tol > 0 {
					return math.Abs(af-bf) <= tol
				}
				return af == bf
			}
		}
	}
	if ab, aok := toBool(a); aok {
		if bb, bok := toBool(b); bok {
			return ab == bb
		}
	}
	if as, aok := toStr(a); aok {
		if bs, bok := toStr(b); bok {
			return as == bs
		}
	}
	return reflect.DeepEqual(a, b)
}

// isNumericValue reports whether v is a real numeric type (not a string). A
// string is only coerced to a number when the value it is compared against is
// itself numeric, so two strings are never normalized away.
func isNumericValue(v any) bool {
	switch v.(type) {
	case float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		json.Number:
		return true
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	return 0, false
}

func toBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		return b, err == nil
	}
	return false, false
}

func toStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func uniqueRows(rows [][]any, tol float64) [][]any {
	var out [][]any
	for _, r := range rows {
		if !containsRow(out, r, tol) {
			out = append(out, r)
		}
	}
	return out
}

func containsRow(rows [][]any, want []any, tol float64) bool {
	for _, r := range rows {
		if rowsEqual(r, want, tol) {
			return true
		}
	}
	return false
}

func multisetEqual(expected, actual [][]any, tol float64, duplicates string) bool {
	if duplicates == "ignored" {
		expSet := uniqueRows(expected, tol)
		actSet := uniqueRows(actual, tol)
		if len(expSet) != len(actSet) {
			return false
		}
		for _, e := range expSet {
			if !containsRow(actSet, e, tol) {
				return false
			}
		}
		return true
	}
	if len(expected) != len(actual) {
		return false
	}
	used := make([]bool, len(actual))
	for _, e := range expected {
		found := false
		for j := range actual {
			if !used[j] && rowsEqual(e, actual[j], tol) {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func dedupe(codes []string) []string {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
