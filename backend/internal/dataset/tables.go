// The introspection response behind GET /datasets/{id}/tables, plus the shared
// row and column types the generator builds. Tables() describes commerce or
// perf; the perf description walks perf.go's perfRelations, which the template
// loader walks too, so the drawer and the fixture cannot disagree.
package dataset

import (
	"fmt"
	"strconv"
)

// DatasetVersion is shared by the schema and the seed.
const DatasetVersion = 1

// ColumnSpec is one column's name, SQL type and flags (PK/FK/nullable).
type ColumnSpec struct {
	Name  string
	Type  string
	Flags string
}

// TableSpec is one table: its columns, the columns shown in a preview, and its
// full rows (in column order).
type TableSpec struct {
	Name        string
	Columns     []ColumnSpec
	PreviewCols []string
	Rows        [][]any
}

// Dataset is a named, versioned set of tables.
type Dataset struct {
	ID      string
	Version int
	Tables  []TableSpec
}

// TableInfo is the JSON shape returned by GET /datasets/{id}/tables.
type TableInfo struct {
	Name    string     `json:"name"`
	Rows    int        `json:"rows"`
	Cols    [][]string `json:"cols"`
	Preview [][]string `json:"preview"`
}

// TablesResponse is the /datasets/{id}/tables body.
type TablesResponse struct {
	Dataset string      `json:"dataset"`
	Version int         `json:"version"`
	Tables  []TableInfo `json:"tables"`
	Rows    int         `json:"rows"`
}

// Tables builds the introspection response for a dataset id.
func Tables(id string) (TablesResponse, bool) {
	if id == PerfDatasetID {
		return perfTablesResponse(), true
	}
	// commerce is the only other dataset this build ships, and it is generated
	// rather than loaded, so there is nothing to look up by id.
	if id != "commerce" {
		return TablesResponse{}, false
	}
	ds := Commerce()
	out := TablesResponse{Dataset: ds.ID, Version: ds.Version}
	for _, t := range ds.Tables {
		out.Tables = append(out.Tables, t.Info())
		out.Rows += len(t.Rows)
	}
	return out, true
}

// perfTablesResponse describes the perf fixture for the tables drawer.
//
// The four relations are the four perfRelations: perf.sql's own schema at the
// scale the plan lessons are about. The drawer reads the same list
// transferPerfTablesToLearner walks, so the description and the ownership
// transfer cannot disagree about which relations exist; the columns are the ones
// perf.sql declares, and the row counts are the counts the seed generates.
//
// There is deliberately no row *preview*. A preview is learner data, and the
// perf rows live in the caller's own copy of the sealed template — an anonymous
// caller has no such copy, and a signed-in one may have changed it. Returning
// the header alone says "these are the columns" and shows no row, which is the
// only honest answer available without an environment; inventing five rows
// would be exactly the fabrication this endpoint must never commit.
func perfTablesResponse() TablesResponse {
	out := TablesResponse{Dataset: PerfDatasetID, Version: PerfVersion}
	for _, rel := range perfRelations {
		info := TableInfo{Name: rel.name, Rows: rel.rows}
		info.Cols = make([][]string, len(rel.columns))
		for i, c := range rel.columns {
			info.Cols[i] = []string{c.Name, c.Type, c.Flags}
		}
		previewCols := rel.preview
		if len(previewCols) == 0 {
			for _, c := range rel.columns {
				previewCols = append(previewCols, c.Name)
			}
		}
		info.Preview = [][]string{append([]string{}, previewCols...)}
		out.Tables = append(out.Tables, info)
		out.Rows += rel.rows
	}
	return out
}

// Info renders a table's metadata and a preview (header plus up to five rows).
func (t TableSpec) Info() TableInfo {
	info := TableInfo{Name: t.Name, Rows: len(t.Rows)}
	info.Cols = make([][]string, len(t.Columns))
	colType := make(map[string]string, len(t.Columns))
	for i, c := range t.Columns {
		info.Cols[i] = []string{c.Name, c.Type, c.Flags}
		colType[c.Name] = c.Type
	}
	previewCols := t.PreviewCols
	if len(previewCols) == 0 {
		for _, c := range t.Columns {
			previewCols = append(previewCols, c.Name)
		}
	}
	idx := make([]int, len(previewCols))
	for i, name := range previewCols {
		idx[i] = t.columnIndex(name)
	}
	header := append([]string{}, previewCols...)
	info.Preview = append(info.Preview, header)
	limit := 5
	if len(t.Rows) < limit {
		limit = len(t.Rows)
	}
	for r := 0; r < limit; r++ {
		row := make([]string, len(idx))
		for k, ci := range idx {
			if ci < 0 || ci >= len(t.Rows[r]) {
				row[k] = "NULL"
				continue
			}
			row[k] = stringify(t.Rows[r][ci], colType[previewCols[k]])
		}
		info.Preview = append(info.Preview, row)
	}
	return info
}

func (t TableSpec) columnIndex(name string) int {
	for i, c := range t.Columns {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// stringify renders one preview cell as the learner reads it. A numeric column
// keeps two decimals ("118.00"), matching the PostgreSQL text form the query
// result carries, while other floats print their shortest exact form.
func stringify(v any, typ string) string {
	if v == nil {
		return "NULL"
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if typ == "numeric" {
			return strconv.FormatFloat(x, 'f', 2, 64)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", v)
}
