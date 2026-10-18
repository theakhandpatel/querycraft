// Per-rule contract verdicts. compare.go holds the value and row primitives;
// this file turns them into the structured Comparison the lesson checklist
// renders, keeping each §6.1 rule its own boolean so a caller can say which
// rule failed rather than only that something did.
package eval

import "github.com/querycraft/sqlquery/backend/internal/engine"

// Comparison is the structured outcome of a contract check: one boolean per
// §6.1 rule plus the feedback codes. It mirrors the client's `Comparison` in
// web/src/api/compare.ts so every adapter renders the same checklist, and so a
// green row means the database really returned the right thing.
type Comparison struct {
	OK          bool
	Codes       []string
	ColumnsOK   bool
	RowsCountOK *bool
	ValuesOK    *bool
	RowOrderOK  *bool
}

// boolPtr boxes a bool, so an unset rule stays distinct from a rule that ran
// and passed.
func boolPtr(v bool) *bool { return &v }

// CompareColumnsDetailed reports the column rule on its own instead of
// collapsing everything into one boolean.
func CompareColumnsDetailed(contract Contract, qr engine.QueryResult) Comparison {
	if qr.Status != "succeeded" || qr.Truncated {
		return Comparison{Codes: []string{CodeValueMismatch}}
	}
	contract = contract.normalized()
	if len(contract.Columns) == 0 {
		return Comparison{OK: true, ColumnsOK: true}
	}
	ok, codes := matchColumns(contract.Columns, columnNames(qr.Columns), contract.ColumnOrder)
	return Comparison{OK: ok, Codes: codes, ColumnsOK: ok}
}

// CompareDetailed reports the per-rule verdicts. RowsCountOK, ValuesOK and
// RowOrderOK are nil when the rule does not apply to this contract.
func CompareDetailed(expected ExpectedResult, contract Contract, qr engine.QueryResult) Comparison {
	if qr.Status != "succeeded" {
		return Comparison{}
	}
	if qr.Truncated {
		return Comparison{Codes: []string{CodeRowCount}, RowsCountOK: boolPtr(false)}
	}
	contract = contract.normalized()

	wantCols := contract.Columns
	if len(wantCols) == 0 {
		wantCols = expected.Columns
	}
	gotCols := columnNames(qr.Columns)
	if ok, codes := matchColumns(wantCols, gotCols, contract.ColumnOrder); !ok {
		return Comparison{Codes: dedupe(codes)}
	}

	if expected.Rows == nil {
		// Columns only: there was no reference row set to compare against.
		return Comparison{OK: true, ColumnsOK: true}
	}

	order := alignColumns(wantCols, gotCols, contract.ColumnOrder)
	actRows := projectRows(qr.Rows, order)

	cmp := Comparison{OK: true, ColumnsOK: true}
	rowsCountOK, valuesOK, rowOrderOK, ok, codes := compareRowsDetailed(expected.Rows, actRows, contract)
	cmp.RowsCountOK = rowsCountOK
	cmp.ValuesOK = valuesOK
	cmp.RowOrderOK = rowOrderOK
	if !ok {
		cmp.OK = false
		cmp.Codes = codes
	}
	return cmp
}

// compareRowsDetailed applies the row rules and returns each verdict. It is the
// single implementation of the row rules, so a §6.1 change lands once.
func compareRowsDetailed(expected, actual [][]any, contract Contract) (*bool, *bool, *bool, bool, []string) {
	tol := contract.NumericTolerance
	exact := contract.RowOrder == "exact"
	var rowOrder *bool
	if exact {
		rowOrder = boolPtr(true)
	}

	if len(expected) == 0 && len(actual) == 0 {
		return boolPtr(true), boolPtr(true), rowOrder, true, nil
	}

	if exact {
		if len(expected) != len(actual) {
			return boolPtr(false), nil, rowOrder, false, []string{CodeRowCount}
		}
		mismatch := false
		for i := range expected {
			if !rowsEqual(expected[i], actual[i], tol) {
				mismatch = true
				break
			}
		}
		if !mismatch {
			return boolPtr(true), boolPtr(true), boolPtr(true), true, nil
		}
		codes := []string{CodeValueMismatch}
		if multisetEqual(expected, actual, tol, contract.Duplicates) {
			codes = append(codes, CodeRowOrder)
		}
		return boolPtr(true), boolPtr(false), boolPtr(false), false, dedupe(codes)
	}

	if contract.Duplicates == "ignored" {
		expSet := uniqueRows(expected, tol)
		actSet := uniqueRows(actual, tol)
		if len(expSet) != len(actSet) {
			return boolPtr(false), boolPtr(false), nil, false, []string{CodeRowCount}
		}
		for _, e := range expSet {
			if !containsRow(actSet, e, tol) {
				return boolPtr(true), boolPtr(false), nil, false, []string{CodeValueMismatch}
			}
		}
		return boolPtr(true), boolPtr(true), nil, true, nil
	}

	if len(expected) != len(actual) {
		return boolPtr(false), nil, nil, false, []string{CodeRowCount}
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
			return boolPtr(true), boolPtr(false), nil, false, []string{CodeValueMismatch}
		}
	}
	return boolPtr(true), boolPtr(true), nil, true, nil
}
