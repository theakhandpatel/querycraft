package store

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

// Both backends render their metadata DDL from appTables. These tests read the
// rendered scripts back and compare them with the description and with the
// catalogs of the engines that ran them, so a table or column that reaches one
// dialect only fails here rather than in production.

var (
	createTableRE = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ([A-Za-z_.]+) \((.+?)\n\)`)
	createIndexRE = regexp.MustCompile(`CREATE (?:UNIQUE )?INDEX IF NOT EXISTS ([A-Za-z_]+) ON ([A-Za-z_.]+)`)
)

// describedTables is the description as a test fixture: one entry per table.
func describedTables() map[string][]string {
	out := make(map[string][]string, len(appTables))
	for _, t := range appTables {
		names := make([]string, 0, len(t.columns))
		for _, c := range t.columns {
			names = append(names, c.name)
		}
		out[t.name] = names
	}
	return out
}

// describedIndexes is the description's index names, mapped to their table.
func describedIndexes() map[string]string {
	out := map[string]string{}
	for _, t := range appTables {
		for _, ix := range t.indexes {
			out[ix.name] = t.name
		}
	}
	return out
}

// renderedTables reads a rendered script back into table names and their
// columns. A renderer that drops a column, or invents one, shows up here.
func renderedTables(t *testing.T, label, ddl string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, m := range createTableRE.FindAllStringSubmatch(ddl, -1) {
		name := bareName(m[1])
		if _, dup := out[name]; dup {
			t.Fatalf("%s: table %s is created twice", label, name)
		}
		var columns []string
		for _, line := range strings.Split(m[2], "\n") {
			line = strings.TrimSuffix(strings.TrimSpace(line), ",")
			if line == "" || strings.HasPrefix(line, "PRIMARY KEY") {
				continue
			}
			columns = append(columns, strings.Fields(line)[0])
		}
		out[name] = columns
	}
	return out
}

// renderedIndexes reads a rendered script back into index names, mapped to
// their table.
func renderedIndexes(ddl string) map[string]string {
	out := map[string]string{}
	for _, m := range createIndexRE.FindAllStringSubmatch(ddl, -1) {
		out[m[1]] = bareName(m[2])
	}
	return out
}

// bareName strips a schema qualifier.
func bareName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// compareTables reports every table and column set two descriptions disagree
// on. Column order is not part of the comparison, because neither dialect
// depends on it.
func compareTables(t *testing.T, label string, want, got map[string][]string) {
	t.Helper()
	for name, wantColumns := range want {
		gotColumns, ok := got[name]
		if !ok {
			t.Errorf("%s: table %s is missing", label, name)
			continue
		}
		wantColumns = slices.Sorted(slices.Values(wantColumns))
		gotColumns = slices.Sorted(slices.Values(gotColumns))
		if !slices.Equal(gotColumns, wantColumns) {
			t.Errorf("%s: table %s has columns %v, want %v", label, name, gotColumns, wantColumns)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: table %s is not described", label, name)
		}
	}
}

// TestMetadataSchemaRendersTheSameTablesInBothDialects renders the description
// into both dialects and reads the output back. The description is the only
// input, so a renderer that skips a table, a column or an index fails here.
func TestMetadataSchemaRendersTheSameTablesInBothDialects(t *testing.T) {
	postgresScript := postgresDDL(appTables)
	sqliteScript := sqliteDDL(appTables)

	want := describedTables()
	compareTables(t, "postgres", want, renderedTables(t, "postgres", postgresScript))
	compareTables(t, "sqlite", want, renderedTables(t, "sqlite", sqliteScript))

	wantIndexes := describedIndexes()
	if got := renderedIndexes(postgresScript); !maps.Equal(got, wantIndexes) {
		t.Errorf("postgres indexes = %v, want %v", got, wantIndexes)
	}
	if got := renderedIndexes(sqliteScript); !maps.Equal(got, wantIndexes) {
		t.Errorf("sqlite indexes = %v, want %v", got, wantIndexes)
	}
}

// TestEveryDDLKindRendersATypeInBothDialects guards the type name switches: a
// kind added without a name in one dialect would render an empty type.
func TestEveryDDLKindRendersATypeInBothDialects(t *testing.T) {
	for _, k := range []ddlKind{kindText, kindJSON, kindInteger, kindBoolean, kindTimestamp} {
		if k.postgresType() == "" || k.sqliteType() == "" {
			t.Errorf("kind %d renders postgres %q, sqlite %q", k, k.postgresType(), k.sqliteType())
		}
	}
}

// TestMetadataSchemaMatchesBothCatalogs opens both backends for real and reads
// the tables and columns the engines recorded. The static test above pins the
// text of the scripts; this one pins what the engines did with them.
func TestMetadataSchemaMatchesBothCatalogs(t *testing.T) {
	ctx := context.Background()
	srv := pgtest.Start(t)

	postgres, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer postgres.Close()
	sqlite, err := NewSQLite(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlite.Close()

	want := describedTables()
	compareTables(t, "information_schema", want, postgresCatalog(t, ctx, postgres))
	compareTables(t, "pragma_table_info", want, sqliteCatalog(t, ctx, sqlite))
}

// postgresCatalog reads the tables and columns of the app schema.
func postgresCatalog(t *testing.T, ctx context.Context, st *Postgres) map[string][]string {
	t.Helper()
	rows, err := st.pool.Query(ctx, `
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = $1
		 ORDER BY table_name, ordinal_position`, AppSchema)
	if err != nil {
		t.Fatalf("read information_schema: %v", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("scan information_schema: %v", err)
		}
		out[table] = append(out[table], column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read information_schema: %v", err)
	}
	return out
}

// sqliteCatalog reads the columns of each described table from the file.
func sqliteCatalog(t *testing.T, ctx context.Context, st *SQLite) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range appTables {
		columns, err := sqliteColumns(ctx, st, table.name)
		if err != nil {
			t.Fatalf("read pragma_table_info(%s): %v", table.name, err)
		}
		out[table.name] = columns
	}
	return out
}

func sqliteColumns(ctx context.Context, st *SQLite, table string) ([]string, error) {
	rows, err := st.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}
