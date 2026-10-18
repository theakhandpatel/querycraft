package httpapi

/* SQLite table introspection for the schema drawer.
 *
 * GET /datasets/{id}/tables serves the drawer. A SQLite chapter must describe
 * the SQLite workspace, not the PostgreSQL one, so handlers.go routes a
 * ?dialect=sqlite request here. Each table is read over the executor seam with
 * sqlite_master and PRAGMA table_info, against the caller's own file when
 * signed in and the shared fixture otherwise. The response carries declared
 * types verbatim and a column-name preview, never invented rows.
 */

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// handleSQLiteTables introspects the SQLite workspace over the executor seam: the
// live relations, their declared types verbatim, the row counts, and a header
// preview (column names, no invented rows, the same honesty rule the perf
// tables response follows).
func (s *Server) handleSQLiteTables(w http.ResponseWriter, r *http.Request, datasetID string) {
	if s.sqlite == nil {
		errorEnvelope(w, http.StatusBadRequest, engine.CodeDialectMismatch,
			"This gateway does not serve the sqlite dialect.")
		return
	}
	ctx := r.Context()
	ex, err := s.sqliteExecutor(ctx, r)
	if err != nil {
		environmentUnavailable(w, err)
		return
	}

	names, qerr := sqliteTableNames(ctx, ex)
	if qerr != nil {
		writeSQLiteTablesError(w, qerr)
		return
	}
	out := dataset.TablesResponse{Dataset: datasetID, Version: 1}
	for _, name := range names {
		cols, qerr := sqliteTableColumns(ctx, ex, name)
		if qerr != nil {
			writeSQLiteTablesError(w, qerr)
			return
		}
		info := dataset.TableInfo{Name: name, Cols: cols}
		previewCols := make([]string, 0, len(cols))
		for _, c := range cols {
			previewCols = append(previewCols, c[0])
		}
		info.Preview = [][]string{previewCols}
		info.Rows = sqliteRowCount(ctx, ex, name)
		out.Rows += info.Rows
		out.Tables = append(out.Tables, info)
	}
	writeJSON(w, http.StatusOK, out)
}

// sqliteTableNames lists the learner-visible relations, excluding SQLite's own
// bookkeeping tables.
func sqliteTableNames(ctx context.Context, ex engine.Executor) ([]string, *engine.QueryError) {
	res := engine.Execute(ctx, ex,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
		engine.DefaultLimits())
	if res.Status != "succeeded" {
		return nil, res.Error
	}
	names := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) == 0 {
			continue
		}
		names = append(names, fmt.Sprint(row[0]))
	}
	return names, nil
}

// sqliteTableColumns reads PRAGMA table_info for one table: name, declared type,
// and whether the column is part of the primary key.
func sqliteTableColumns(ctx context.Context, ex engine.Executor, table string) ([][]string, *engine.QueryError) {
	res := engine.Execute(ctx, ex, "PRAGMA table_info("+quoteSQLiteIdent(table)+")", engine.DefaultLimits())
	if res.Status != "succeeded" {
		return nil, res.Error
	}
	// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk.
	cols := make([][]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 6 {
			continue
		}
		name := fmt.Sprint(row[1])
		declType := fmt.Sprint(row[2])
		flags := ""
		if fmt.Sprint(row[5]) != "0" {
			flags = "pk"
		}
		cols = append(cols, []string{name, declType, flags})
	}
	return cols, nil
}

// sqliteRowCount counts one table's rows; a count failure is reported as 0 rather
// than failing the whole drawer, because a missing count is not a missing schema.
func sqliteRowCount(ctx context.Context, ex engine.Executor, table string) int {
	res := engine.Execute(ctx, ex, "SELECT count(*) FROM "+quoteSQLiteIdent(table), engine.DefaultLimits())
	if res.Status != "succeeded" || len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		return 0
	}
	switch n := res.Rows[0][0].(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// quoteSQLiteIdent wraps an identifier in double quotes, escaping any embedded
// quote, so a table name that is a keyword still resolves.
func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func writeSQLiteTablesError(w http.ResponseWriter, qerr *engine.QueryError) {
	errorEnvelope(w, http.StatusBadRequest, qerr.Code, qerr.Message)
}
