// Result-contract comparison (§6.1). This file holds the value and row rules:
// column matching, per-column alignment, row comparison, and the set and
// multiset operations the duplicate rule needs. eval.go resolves the contract
// and runs the SQL; nothing here executes anything.
package eval

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Result-contract feedback codes (contracts/evaluation.schema.json).
const (
	// CodeValueMismatch: at least one compared value differs.
	CodeValueMismatch = "VALUE_MISMATCH"
	// CodeColumn: the set or the count of column names differs.
	CodeColumn = "COLUMN_MISMATCH"
	// CodeColumnOrder: the same columns appear in a different order.
	CodeColumnOrder = "COLUMN_ORDER_MISMATCH"
	// CodeRowCount: the row counts differ.
	CodeRowCount = "ROW_COUNT_MISMATCH"
	// CodeRowOrder: the rows match as a multiset but not in order.
	CodeRowOrder = "ROW_ORDER_MISMATCH"
)

// matchColumns reports whether got satisfies the contract's column names. A
// length difference is a plain column mismatch. When order is "exact" the
// comparison is positional, and a wanted column found elsewhere in got is
// reported as CodeColumnOrder as well. Otherwise the columns match as a
// multiset: every wanted name must claim one unused actual name.
func matchColumns(want, got []string, order string) (bool, []string) {
	if len(want) != len(got) {
		return false, []string{CodeColumn}
	}
	if order == "exact" {
		var codes []string
		for i := range want {
			if want[i] != got[i] {
				codes = append(codes, CodeColumn)
				if slices.Contains(got, want[i]) {
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

// projectRows reorders each row so its columns line up with the expected
// order. A wanted column that got does not carry (index -1) projects to nil,
// which the value rule then reports as a mismatch.
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

// rowsEqual reports whether two rows hold the same values, column for column,
// under the value rule and the contract's numeric tolerance. A length
// difference is a mismatch, because some column has nothing to compare against.
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

// toFloat coerces a numeric or numeric-looking value to float64. A string is
// accepted here because the caller reaches this path only when the other side
// is already numeric, which is what keeps two strings from being normalized.
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

// toBool coerces a bool, or a string strconv.ParseBool accepts.
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

// toStr returns the string value, when v holds one.
func toStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// uniqueRows keeps the first copy of each distinct row, so the "ignored"
// duplicate rule can compare the two sides as row sets.
func uniqueRows(rows [][]any, tol float64) [][]any {
	var out [][]any
	for _, r := range rows {
		if !containsRow(out, r, tol) {
			out = append(out, r)
		}
	}
	return out
}

// containsRow reports whether want appears in rows under the value rule.
func containsRow(rows [][]any, want []any, tol float64) bool {
	for _, r := range rows {
		if rowsEqual(r, want, tol) {
			return true
		}
	}
	return false
}

// multisetEqual applies the duplicate rule for the exact-row-order path, where
// it decides whether a value mismatch is only an ordering difference. With
// duplicates ignored it compares the two row sets; otherwise it matches every
// expected row to an unused actual row.
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

// dedupe removes repeated codes while preserving first-seen order, so a
// multi-column mismatch does not report CodeColumn twice.
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
