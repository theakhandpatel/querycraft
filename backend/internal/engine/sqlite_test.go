package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/dialect"
)

// SQLiteExecutor must satisfy the same seam as the PostgreSQL engine, including
// the two optional interfaces engine.Execute consults for the result's dialect
// and engine labels: that is the whole point of M6 step 4, and a compile-time
// assertion is the cheapest way to keep it true.
var (
	_ Executor        = (*SQLiteExecutor)(nil)
	_ Dialecter       = (*SQLiteExecutor)(nil)
	_ LabeledExecutor = (*SQLiteExecutor)(nil)
)

/* ---------- fixtures ---------- */

// sqliteDatasetFile builds a temporary SQLite database holding the authored
// commerce dataset and returns its path.
//
// The schema and the seed are applied with one Exec each. modernc.org/sqlite
// runs a whole multi-statement script from a single call — measured, and noted
// in internal/dataset/sqlite_dataset.go — so no statement splitter is involved
// and this helper is where a change in that behaviour would land first.
func sqliteDatasetFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "commerce.db")
	dsn, err := sqliteDSN(path, nil, false)
	if err != nil {
		t.Fatalf("build DSN: %v", err)
	}
	db, err := sql.Open(SQLiteDriverName, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()
	for _, step := range []struct{ name, script string }{
		{"schema", dataset.SQLiteSchemaSQL()},
		{"seed", dataset.SQLiteSeedSQL()},
	} {
		if _, err := db.Exec(step.script); err != nil {
			t.Fatalf("apply %s in one Exec: %v", step.name, err)
		}
	}
	return path
}

// sqliteExecutor opens the shared dataset the way the gateway serves it to an
// anonymous learner: read-only, with the pragmas in the DSN. The pool is one
// connection on purpose, so a test that reuses the executor afterwards proves
// the connection came back rather than that a fresh one could be opened.
func sqliteExecutor(t *testing.T, path string) *SQLiteExecutor {
	t.Helper()
	ex, err := NewSQLiteExecutor(SQLiteConfig{
		Path:         path,
		ReadOnly:     true,
		Pragmas:      SQLiteReadOnlyPragmas(),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("NewSQLiteExecutor(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = ex.Close() })
	return ex
}

// sqliteWritableExecutor opens a throwaway writable database with a tiny probe
// table: UNIQUE, NOT NULL and CHECK violations are the failure kinds the
// read-only dataset cannot produce, and they are exactly the errors a
// write-shaped lesson reads. The schema is named for the test rather than the
// course dataset because these cases are about the message the engine writes,
// not about what the dataset contains.
func sqliteWritableExecutor(t *testing.T) *SQLiteExecutor {
	t.Helper()
	ex, err := NewSQLiteExecutor(SQLiteConfig{
		Path:         filepath.Join(t.TempDir(), "writable.db"),
		Pragmas:      []string{"busy_timeout(0)"},
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("NewSQLiteExecutor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close() })
	if _, err := ex.db.Exec(`CREATE TABLE probe (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		qty INTEGER CHECK (qty >= 0)
	)`); err != nil {
		t.Fatalf("create the probe table: %v", err)
	}
	return ex
}

// sqliteQueryError runs a statement that must be refused and returns the engine
// error. It exists because a Query that unexpectedly succeeds is not a small
// flake on this executor: the rows hold the pool's single connection until they
// are closed, so the *next* call waits forever. Closing on the way out makes a
// wrong expectation fail loudly instead of hanging the suite.
func sqliteQueryError(t *testing.T, ex Executor, statement string) error {
	t.Helper()
	rows, err := ex.Query(context.Background(), statement)
	if err == nil {
		rows.Close()
		t.Fatalf("%s succeeded, want an error", statement)
	}
	return err
}

/* ---------- query helpers ---------- */

// sqliteRows runs a statement through the executor — so the assertions see what
// a learner would see, encoded by the profile — and returns the column labels
// together with the encoded rows.
func sqliteQuery(t *testing.T, ex Executor, statement string) ([]Column, [][]any) {
	t.Helper()
	rows, err := ex.Query(context.Background(), statement)
	if err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	defer rows.Close()
	cols := rows.Columns()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			t.Fatalf("%s: values: %v", statement, err)
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	return cols, out
}

// sqliteScalar returns the single encoded value of a one-row, one-column
// statement.
func sqliteScalar(t *testing.T, ex Executor, statement string) any {
	t.Helper()
	_, rows := sqliteQuery(t, ex, statement)
	if len(rows) != 1 {
		t.Fatalf("%s: %d rows, want 1", statement, len(rows))
	}
	if len(rows[0]) != 1 {
		t.Fatalf("%s: %d columns, want 1", statement, len(rows[0]))
	}
	return rows[0][0]
}

// sqliteCount is sqliteScalar for a count, asserting the encoding too: a count
// is an INTEGER and must arrive as a JSON number, never as a string.
func sqliteCount(t *testing.T, ex Executor, statement string) int64 {
	t.Helper()
	v := sqliteScalar(t, ex, statement)
	n, ok := v.(int64)
	if !ok {
		t.Fatalf("%s = %#v (%T), want a number", statement, v, v)
	}
	return n
}

// sqliteRowJSON returns the declared-type labels and the JSON text of the first
// row, which is the shape that reaches the HTTP response.
func sqliteRowJSON(t *testing.T, ex Executor, statement string) ([]Column, string) {
	t.Helper()
	cols, rows := sqliteQuery(t, ex, statement)
	if len(rows) != 1 {
		t.Fatalf("%s: %d rows, want 1", statement, len(rows))
	}
	b, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatalf("%s: marshal: %v", statement, err)
	}
	return cols, string(b)
}

/* ---------- the dataset ---------- */

// TestSQLiteDatasetLoadsInOneExecPerScript proves the two authored files apply,
// with one Exec each (the driver's measured multi-statement behaviour), and that
// they land the same commerce dataset the PostgreSQL pair does.
func TestSQLiteDatasetLoadsInOneExecPerScript(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	// The row counts are the authored dataset's, taken from the Go generator
	// that produced seed.sql so the two dialects cannot drift apart silently.
	fromGenerator := make(map[string]int)
	for _, table := range dataset.Commerce().Tables {
		fromGenerator[table.Name] = len(table.Rows)
	}
	// These are also the numbers seed.sqlite.sql carries in its `-- <table>
	// (N rows)` headers. The perf fixture (perf.sql, M5) is a different,
	// larger dataset — 2000/500/10000/20000 — and has no SQLite mirror; the
	// SQLite dataset is the commerce one, which mapping.json and
	// scripts/dataset-map-audit.mjs both check at this grain.
	want := map[string]int64{"customers": 30, "products": 20, "orders": 80, "order_items": 160}
	for table, n := range want {
		if got := sqliteCount(t, ex, "SELECT count(*) FROM "+table); got != n {
			t.Errorf("%s has %d rows, want %d", table, got, n)
		}
		if gen := fromGenerator[table]; int64(gen) != n {
			t.Errorf("seed.sqlite.sql has %d %s rows but dataset.Commerce() generates %d; the dialects have drifted", n, table, gen)
		}
	}
	if got := sqliteCount(t, ex, "SELECT version FROM dataset_meta WHERE dataset = 'commerce'"); got != int64(dataset.DatasetVersion) {
		t.Errorf("dataset_meta.version = %d, want %d", got, dataset.DatasetVersion)
	}
	if got := sqliteScalar(t, ex, "PRAGMA integrity_check"); got != "ok" {
		t.Errorf("integrity_check = %v, want ok", got)
	}
	if got := sqliteCount(t, ex, "SELECT count(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Errorf("the seed has %d foreign-key violations; the DSN's foreign_keys pragma is live, so this is a real defect", got)
	}
}

/* ---------- labels and encodings ---------- */

// TestSQLiteColumnLabelsAreTheDeclaredTypeOrExpression pins the profile's
// column-type-label rule against the driver's own sqlite3_column_decltype.
func TestSQLiteColumnLabelsAreTheDeclaredTypeOrExpression(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	cases := []struct {
		statement string
		want      []Column
	}{
		{
			statement: "SELECT id, name, email, signup_date FROM customers",
			want: []Column{
				{Name: "id", Type: "INTEGER"},
				{Name: "name", Type: "TEXT"},
				{Name: "email", Type: "TEXT"},
				// A DATE column labels DATE while every value in it is TEXT:
				// the label is a declaration, not a type.
				{Name: "signup_date", Type: "DATE"},
			},
		},
		{
			// An expression never had a declared type; claiming one would
			// report a type the engine never had.
			statement: "SELECT count(*), price * 2 FROM products",
			want: []Column{
				{Name: "count(*)", Type: "expression"},
				{Name: "price * 2", Type: "expression"},
			},
		},
		{
			statement: "SELECT p.category FROM products p GROUP BY p.category ORDER BY p.category LIMIT 1",
			want:      []Column{{Name: "category", Type: "TEXT"}},
		},
	}
	for _, tc := range cases {
		got, _ := sqliteQuery(t, ex, tc.statement)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %d columns, want %d", tc.statement, len(got), len(tc.want))
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: column %d = %+v, want %+v", tc.statement, i, got[i], tc.want[i])
			}
		}
	}
}

// TestSQLiteStorageClassesEncodeToJSONValues runs the storage-class table from
// contracts/dialects/sqlite.json against values the engine actually produced,
// comparing the JSON text rather than the Go value, because the contract is
// about the JSON.
func TestSQLiteStorageClassesEncodeToJSONValues(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	cases := []struct {
		name      string
		statement string
		wantJSON  string
	}{
		{"INTEGER is a JSON number", "SELECT id FROM customers WHERE id = 3", `[3]`},
		{"TEXT is a string verbatim", "SELECT name FROM customers WHERE id = 3", `["Chidi Okafor"]`},
		{"NULL is JSON null, not '' and not 0", "SELECT email FROM customers WHERE id = 3", `[null]`},
		{"REAL is a JSON number", "SELECT 2.5", `[2.5]`},
		{"BLOB is \\x plus lowercase hex", "SELECT x'00FFab' AS b", `["\\x00ffab"]`},
		{"an INTEGER inside 2^53-1 stays a number", "SELECT 9007199254740991", `[9007199254740991]`},
		// 2^63-1: no JSON number can hold it exactly, so it is a decimal string.
		{"an INTEGER above 2^53-1 is a decimal string", "SELECT 9223372036854775807", `["9223372036854775807"]`},
		{"a NUMERIC-declared value is the class it was stored as", "SELECT price FROM products WHERE id = 8", `[118]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := sqliteRowJSON(t, ex, tc.statement)
			if got != tc.wantJSON {
				t.Fatalf("%s encoded as %s, want %s", tc.statement, got, tc.wantJSON)
			}
		})
	}
}

// TestSQLiteDateDeclaredColumnsArriveAsTheStoredText pins the dialect contract's
// date rule: "a DATE- or TIMESTAMP-declared column stores ISO-8601 text and is
// reported verbatim".
//
// The dataset stores ISO-8601 TEXT in its DATE/TIMESTAMP columns (typeof() says
// so, and that is the dialect point). The declared type is only a *label* on the
// read path: this package's own driver reads the storage class and its accessor
// (sqlite3_column_type + sqlite3_column_text), so a DATE-declared column returns
// the stored text. That is also what the browser adapter reports, which is the
// parity the contract asks for — and it is why the read path does not go through
// modernc's database/sql driver, which would materialise the text into a
// time.Time (2025-12-01 -> 2025-12-01T00:00:00Z) before this package saw it.
func TestSQLiteDateDeclaredColumnsArriveAsTheStoredText(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	if got := sqliteScalar(t, ex, "SELECT typeof(signup_date) FROM customers WHERE id = 3"); got != "text" {
		t.Fatalf("typeof(signup_date) = %v, want text: the engine stores the date as text, whatever the driver hands back", got)
	}
	// The stored text, verbatim — not RFC3339, not a host Date.
	if got := sqliteScalar(t, ex, "SELECT signup_date FROM customers WHERE id = 3"); got != "2025-12-01" {
		t.Fatalf("signup_date = %v (%T), want the stored text 2025-12-01", got, got)
	}
	// A TIMESTAMP-declared column is the same case.
	if got := sqliteScalar(t, ex, "SELECT ordered_at FROM orders WHERE id = 1001"); got != "2026-01-04 10:12" {
		t.Fatalf("ordered_at = %v (%T), want the stored text 2026-01-04 10:12, not a materialised UTC string", got, got)
	}
	// Asking for the text explicitly returns the same thing; the label is the
	// only difference between the two columns.
	if got := sqliteScalar(t, ex, "SELECT CAST(signup_date AS TEXT) FROM customers WHERE id = 3"); got != "2025-12-01" {
		t.Fatalf("CAST(signup_date AS TEXT) = %v, want the stored text 2025-12-01", got)
	}
}

/* ---------- affinity ---------- */

// TestSQLiteNumericAffinityMakesThePriceComparisonNumeric is the reason the
// SQLite dataset declares `price NUMERIC` rather than TEXT: a declared type is
// an affinity, and the affinity is what makes `price > 100` a numeric question.
func TestSQLiteNumericAffinityMakesThePriceComparisonNumeric(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	// NUMERIC affinity converts the seed's numeric text on write, and it
	// prefers INTEGER whenever the conversion is lossless: the seed's 118.00 is
	// stored as the integer 118 (measured). That is SQLite's own rule, not a
	// quirk of this adapter, and it is why `price > 100` compares numbers.
	if got := sqliteScalar(t, ex, "SELECT typeof(price) FROM products WHERE id = 8"); got != "integer" {
		t.Errorf("typeof(price) = %v, want integer (NUMERIC affinity stores the seed's 118.00 as the integer 118)", got)
	}
	// A value NUMERIC cannot make integral keeps REAL.
	if got := sqliteScalar(t, ex, "SELECT typeof(price) FROM products WHERE id = 4"); got != "real" {
		t.Errorf("typeof(price) = %v, want real (42.50 has no integer form, so NUMERIC keeps it REAL)", got)
	}
	const numeric = int64(4) // 129, 139, 118, 210
	if got := sqliteCount(t, ex, "SELECT count(*) FROM products WHERE price > 100"); got != numeric {
		t.Errorf("count(price > 100) = %d, want %d", got, numeric)
	}
	// The same values as TEXT compare lexicographically — every price but the
	// discontinued 0.00 sorts above '100' as a string — which is why the
	// comparison would answer a different question under TEXT affinity.
	const lexicographic = int64(19)
	if got := sqliteCount(t, ex,
		"SELECT count(*) FROM (SELECT CAST(price AS TEXT) AS p FROM products) WHERE p > 100"); got != lexicographic {
		t.Errorf("count(CAST(price AS TEXT) > 100) = %d, want %d: the TEXT comparison is lexicographic", got, lexicographic)
	}
	// And the numbers themselves are stored as numbers, not as the seed's text.
	if got := sqliteScalar(t, ex, "SELECT price FROM products WHERE id = 8"); got != int64(118) {
		t.Errorf("price = %#v (%T), want the number 118", got, got)
	}
}

/* ---------- the seam: caps, deadline, errors ---------- */

// TestSQLiteExecuteOwnsTheRowCap proves the cap is engine.Execute's and not a
// second implementation inside the executor: the same statement returns every
// row through Executor.Query and is truncated by Execute.
func TestSQLiteExecuteOwnsTheRowCap(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	statement := "SELECT id FROM order_items"

	if got := len(mustRows(t, ex, statement)); got != 160 {
		t.Fatalf("the executor returned %d rows directly, want all 160 (the cap belongs to engine.Execute)", got)
	}

	qr := Execute(context.Background(), ex, statement, Limits{MaxRows: 5, MaxBytes: 1 << 20, TimeoutMS: 5000})
	if qr.Status != "succeeded" {
		t.Fatalf("status = %q (%v), want succeeded: the row cap is a soft cap", qr.Status, qr.Error)
	}
	// A result produced by SQLite must say so: engine comes from the executor
	// (LabeledExecutor) and dialect from Dialecter, not from a default.
	if !strings.HasPrefix(qr.Engine, "SQLite 3.") {
		t.Errorf("engine = %q, want the SQLite adapter's own label", qr.Engine)
	}
	if qr.Dialect != "sqlite" {
		t.Errorf("dialect = %q, want sqlite", qr.Dialect)
	}
	if !qr.Truncated {
		t.Error("truncated = false, want true")
	}
	if qr.RowCount != 5 || len(qr.Rows) != 5 {
		t.Fatalf("row_count = %d (%d rows), want 5", qr.RowCount, len(qr.Rows))
	}
	// Execute cancels the statement's context when it stops reading. The pool
	// has exactly one connection, so a query that answers now proves the
	// cancelled statement gave it back instead of holding it to the deadline.
	start := time.Now()
	if got := sqliteCount(t, ex, "SELECT count(*) FROM customers"); got != 30 {
		t.Fatalf("count after truncation = %d, want 30", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the connection took %v to come back after a truncated read", elapsed)
	}
}

// TestSQLiteDeadlineStopsALongStatement is M6 step 4's open spike question:
// whether a long WITH RECURSIVE is genuinely interrupted mid-statement, or
// merely abandoned on the client while the engine keeps working.
//
// Measured with modernc.org/sqlite v1.38.2 on this machine: the unbounded
// recursive CTE below returned after 301 ms under a 300 ms deadline, with the
// context's DeadlineExceeded, and the one pooled connection was immediately
// reusable. For scale, the same shape bounded at ten million rows takes about
// 3.1 s (~3.2M rows/s), so the unbounded statement would not have finished
// inside any request deadline: it was interrupted, not left running.
func TestSQLiteDeadlineStopsALongStatement(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	const unbounded = "WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c) SELECT count(*) FROM c"

	start := time.Now()
	qr := Execute(context.Background(), ex, unbounded, Limits{MaxRows: 10, MaxBytes: 1 << 20, TimeoutMS: 300})
	elapsed := time.Since(start)
	t.Logf("unbounded recursive CTE under a 300 ms deadline: %v, code %v", elapsed.Round(time.Millisecond), qr.Error)

	if qr.Status != "failed" || qr.Error == nil {
		t.Fatalf("status = %q, error = %v; want a failed result", qr.Status, qr.Error)
	}
	if qr.Error.Code != CodeTimeout {
		t.Fatalf("code = %s, want %s", qr.Error.Code, CodeTimeout)
	}
	// The statement is stopped near the deadline, not after it: a client-side
	// abandonment would have to wait for the statement to finish (hours).
	if elapsed > 2*time.Second {
		t.Errorf("took %v under a 300 ms deadline; the statement was not pre-empted", elapsed)
	}
	// The connection came back, and quickly. That timing is the real proof the
	// statement was interrupted rather than abandoned: this pool has exactly one
	// connection, so the next statement cannot run until the interrupted one has
	// stopped executing. A client-side abandonment would leave the engine
	// counting and this follow-up would block until the CTE ended (never).
	start = time.Now()
	if got := sqliteCount(t, ex, "SELECT count(*) FROM customers"); got != 30 {
		t.Fatalf("count after the deadline = %d, want 30", got)
	}
	elapsed = time.Since(start)
	t.Logf("the single pooled connection served the next statement %v after the deadline", elapsed)
	if elapsed > time.Second {
		t.Errorf("the connection took %v to come back after the deadline; the statement was not stopped", elapsed)
	}

	// The raw driver error behind that result, so the record names the code the
	// driver reported and not only the message the classifier matched. Query
	// under the same deadline without engine.Execute in the way; the interrupt
	// arrives while the driver is stepping the statement, so it may surface
	// either from Query or from the cursor's Err.
	rawCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	rawRows, rawErr := ex.Query(rawCtx, unbounded)
	if rawRows != nil {
		for rawRows.Next() {
		}
		if rawErr == nil {
			rawErr = rawRows.Err()
		}
		rawRows.Close()
	}
	// engine.Execute's QUERY_TIMEOUT branch keys on this identity, so it is
	// asserted rather than assumed.
	if !errors.Is(rawErr, context.DeadlineExceeded) {
		t.Errorf("raw deadline error = %v (%T), want it to wrap context.DeadlineExceeded", rawErr, rawErr)
	}
	var engineErr *sqliteError
	if errors.As(rawErr, &engineErr) {
		// SQLITE_INTERRUPT is 9 in the primary result codes; Code() reports the
		// extended value, which the profile's "interrupted" rule maps to
		// QUERY_TIMEOUT.
		t.Logf("raw engine error: %v (extended code %d)", engineErr, engineErr.Code())
	} else {
		// Measured: a deadline arrives as the context's error, not an engine
		// sentence. The raw driver's step path substitutes ctx.Err() once its
		// own interrupt fired, so the code layer is not reached for a deadline —
		// the profile's message rules cover the engine's own errors, where an
		// *engine.sqliteError (and its Code()) do appear.
		t.Logf("raw deadline error is not an engine error: %T %v", rawErr, rawErr)
	}
}

// TestSQLiteErrorClassification checks the profile's verdict on the errors the
// real driver produces, through the executor the gateway uses. Every case runs a
// real statement — no hand-written message strings — and the error the assertion
// sees is the one Query hands the learner, so this is the same text Execute puts
// in QueryResult.error.message.
//
// Each case also proves the message is the engine's own and not a driver's
// rendering: the read path calls sqlite3_errmsg directly, so the error carries
// SQLite's sentence with no class prefix and no result-code suffix, and this
// package's error type carries the result code without re-parsing the text.
func TestSQLiteErrorClassification(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	cases := []struct {
		name      string
		statement string
		want      string
	}{
		{"parser rejection", "SELEC 1", dialect.CodeSyntaxError},
		{"incomplete statement", "SELECT", dialect.CodeSyntaxError},
		{"unrecognized token", "SELECT * FROM customers WHERE #", dialect.CodeSyntaxError},
		{"missing table", "SELECT * FROM ordrs", dialect.CodeRuntimeError},
		{"missing column", "SELECT nope FROM customers", dialect.CodeRuntimeError},
		{"missing function", "SELECT nosuchfunc(1)", dialect.CodeRuntimeError},
		{"ambiguous column", "SELECT id FROM customers JOIN orders ON 1=1", dialect.CodeRuntimeError},
		{"write refused by query_only", "INSERT INTO customers (id, name, signup_date) VALUES (999, 'x', '2026-01-01')", dialect.CodeRuntimeError},
		{"blob too big", "SELECT length(zeroblob(1000000001))", dialect.CodeResultLimitExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := sqliteQueryError(t, ex, tc.statement)
			// The text under test is the engine's sentence: assert it does not
			// still carry the driver's class prefix or " (N)" result-code suffix.
			if isDriverFramed(err.Error()) {
				t.Errorf("%s: %q still carries the driver's framing", tc.statement, err)
			}
			// The error is this package's own (sqlite_raw.go's engineError),
			// reporting SQLite's sentence and its extended result code.
			var engineErr *sqliteError
			if !errors.As(err, &engineErr) {
				t.Fatalf("%s: %v is not an *engine.sqliteError", tc.statement, err)
			}
			if engineErr.Code() == 0 {
				t.Errorf("%s: result code = 0; the engine's own code should be on the error", tc.statement)
			}
			// Both doors agree: the profile's package-level classifier and the
			// executor's dialect-aware method. A caller holding the Executor can
			// use the latter without knowing the dialect's profile.
			if got := dialect.ClassifyError(err); got != tc.want {
				t.Fatalf("%s: %q classified as %s, want %s", tc.statement, err, got, tc.want)
			}
			got, ok := ex.ClassifyError(err)
			if !ok || got != tc.want {
				t.Fatalf("%s: executor.ClassifyError(%v) = (%s, %v), want (%s, true)", tc.statement, err, got, ok, tc.want)
			}
		})
	}
}

// isDriverFramed reports whether a message is still modernc's rendering of the
// engine's sentence: a trailing " (N)" result code, the " (SQLITE_BUSY)" marker,
// or the "SQL logic error: " class prefix. It is the negative half of the test
// above — the shape the learner must never read.
func isDriverFramed(msg string) bool {
	if strings.HasPrefix(msg, "SQL logic error: ") || strings.HasSuffix(msg, sqliteBusyMarker) {
		return true
	}
	if i := strings.LastIndex(msg, " ("); i >= 0 && strings.HasSuffix(msg, ")") {
		if _, err := strconv.Atoi(msg[i+2 : len(msg)-1]); err == nil {
			return true
		}
	}
	return false
}

// TestSQLiteErrorFramingFollowsTheDriverSource pins sqliteUnframe against the
// two format strings modernc's conn.errstr actually uses (read in v1.38.2,
// sqlite.go:1417-1430):
//
//	msg == sqlite3_errstr(rc)  ->  "<errstr> (<rc>)"
//	msg != sqlite3_errstr(rc)  ->  "<errstr>: <msg> (<rc>)"
//
// The frames here are real — each one was produced by the statement named beside
// it and recorded from the driver — and the expected value is what the engine
// reported. The statement column is not decoration: it is how a reader checks
// that the frame was measured rather than imagined.
func TestSQLiteErrorFramingFollowsTheDriverSource(t *testing.T) {
	cases := []struct {
		frame     string
		statement string
		want      string
		code      int
	}{
		{"SQL logic error: incomplete input (1)", "SELECT", "incomplete input", 1},
		{`SQL logic error: unrecognized token: "#" (1)`, "SELECT * FROM customers WHERE #", `unrecognized token: "#"`, 1},
		{`SQL logic error: near "SELEC": syntax error (1)`, "SELEC 1", `near "SELEC": syntax error`, 1},
		{"SQL logic error: no such table: nope (1)", "SELECT * FROM nope", "no such table: nope", 1},
		{"SQL logic error: ambiguous column name: id (1)", "SELECT id FROM customers JOIN orders ON 1=1", "ambiguous column name: id", 1},
		{"attempt to write a readonly database (8)", "INSERT INTO customers (id, name, signup_date) VALUES (999, 'x', '2026-01-01')", "attempt to write a readonly database", 8},
		{"string or blob too big (18)", "SELECT length(zeroblob(1000000001))", "string or blob too big", 18},
		{"database is locked (5) (SQLITE_BUSY)", "(an EXCLUSIVE lock held by another connection)", "database is locked", 5},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := sqliteUnframe(tc.frame, tc.code); got != tc.want {
				t.Errorf("sqliteUnframe(%q, %d) = %q, want %q", tc.frame, tc.code, got, tc.want)
			}
			// The frame is the one the driver produced for that statement, and
			// the un-framed sentence is what the profile classifies. The live
			// assertion for each statement lives in the tests that run it; this
			// table keeps the transcription honest.
			if tc.statement == "" {
				t.Errorf("case %q names no statement; frames must be measured", tc.want)
			}
		})
	}

	// A frame whose suffix is not that code's is left entirely alone: guessing
	// at a prefix would be worse than reporting the driver's own words.
	untouched := "constraint failed: UNIQUE constraint failed: t.name (2067)"
	if got := sqliteUnframe(untouched, 1299); got != untouched {
		t.Errorf("sqliteUnframe(%q, 1299) = %q, want it untouched (the suffix is not code 1299's)", untouched, got)
	}

	// The live half: for every frame above that a read-only statement can
	// produce, modernc must hand back exactly that frame.
	live := []struct {
		statement string
		frame     string
	}{
		{"SELECT", "SQL logic error: incomplete input (1)"},
		{"SELECT * FROM customers WHERE #", `SQL logic error: unrecognized token: "#" (1)`},
		{"SELEC 1", `SQL logic error: near "SELEC": syntax error (1)`},
		{"SELECT * FROM nope", "SQL logic error: no such table: nope (1)"},
		{"SELECT id FROM customers JOIN orders ON 1=1", "SQL logic error: ambiguous column name: id (1)"},
		{"INSERT INTO customers (id, name, signup_date) VALUES (999, 'x', '2026-01-01')", "attempt to write a readonly database (8)"},
		{"SELECT length(zeroblob(1000000001))", "string or blob too big (18)"},
	}
	// These frames are modernc.org/sqlite's, so they are pinned against a
	// modernc connection — the driver the *write* path (the one-time seed in
	// internal/httpapi) still uses. The executor's own read driver
	// (sqlite_raw.go) reports SQLite's sentence with no framing at all, so it
	// is the wrong instrument for this table. Opening the same file read-only
	// through the registered driver keeps the transcription honest.
	dsn, err := sqliteDSN(sqliteDatasetFile(t), []string{"busy_timeout(0)"}, true)
	if err != nil {
		t.Fatalf("build the read-only DSN: %v", err)
	}
	driver, err := sql.Open(SQLiteDriverName, dsn)
	if err != nil {
		t.Fatalf("open the driver connection: %v", err)
	}
	driver.SetMaxOpenConns(1)
	defer func() { _ = driver.Close() }()
	for _, tc := range live {
		rows, qerr := driver.Query(tc.statement)
		if rows != nil {
			rows.Close()
		}
		var driverErr *sqlite.Error
		if !errors.As(qerr, &driverErr) {
			t.Fatalf("%s: %v is not a *sqlite.Error", tc.statement, qerr)
		}
		if driverErr.Error() != tc.frame {
			t.Errorf("%s: driver frame = %q, want %q", tc.statement, driverErr.Error(), tc.frame)
		}
	}
}

// firstMatchingRule returns the profile rule that classifies msg, so a test can
// assert *which* rule fired and not only its code. Rule order is the contract's
// ("first match wins"), and two rules can share a code — QUERY_TIMEOUT for both
// "database is locked" and "is locked" — so the code alone would not prove the
// equals rule was reachable.
func firstMatchingRule(p dialect.Profile, msg string) (dialect.MatchRule, bool) {
	for _, rule := range p.ErrorClassification {
		switch rule.Match {
		case "equals":
			if msg == rule.Pattern {
				return rule, true
			}
		case "prefix":
			if strings.HasPrefix(msg, rule.Pattern) {
				return rule, true
			}
		default:
			if strings.Contains(msg, rule.Pattern) {
				return rule, true
			}
		}
	}
	return dialect.MatchRule{}, false
}

// TestSQLiteAffectedRulesMatchRealDriverErrors is the regression test for the
// framing defect: the profile's `equals` and `prefix` rules were unreachable
// while the driver's wrapper was the text being matched, so each one mutated
// into a different rule (or into the catch-all) than the contract intends.
//
// Every case runs a real statement through the executor and asserts the rule
// that classified it, by pattern, in addition to the code. QUERY_SYNTAX_ERROR
// for "SELECT" is the headline: it used to be QUERY_RUNTIME_ERROR.
func TestSQLiteAffectedRulesMatchRealDriverErrors(t *testing.T) {
	p := dialect.SQLite()
	cases := []struct {
		name      string
		statement string
		pattern   string
		code      string
	}{
		{"incomplete input is a syntax error", "SELECT", "incomplete input", dialect.CodeSyntaxError},
		{"unrecognized token is a syntax error", "SELECT * FROM customers WHERE #", "unrecognized token:", dialect.CodeSyntaxError},
		{"near-x syntax error", "SELEC 1", "syntax error", dialect.CodeSyntaxError},
	}
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := sqliteQueryError(t, ex, tc.statement)
			rule, ok := firstMatchingRule(p, err.Error())
			if !ok {
				t.Fatalf("%s: no rule matched %q", tc.statement, err)
			}
			if rule.Pattern != tc.pattern || rule.Code != tc.code {
				t.Fatalf("%s: %q matched rule %s %q -> %s, want %s %q -> %s",
					tc.statement, err, rule.Match, rule.Pattern, rule.Code, "the rule for", tc.pattern, tc.code)
			}
		})
	}
}

// TestSQLiteBusyIsTheEqualsRuleAndATimeout pins the SQLITE_BUSY rule against a
// real lock. The message matters: the read path reports SQLite's own sentence,
// "database is locked", which the profile's `equals` rule matches exactly — a
// message carrying a result-code suffix ("database is locked (5)") or a driver's
// BUSY marker would slip past that rule to the broader `contains "is locked"`,
// a different rule with the same code. The note on the equals rule ("so the
// shorter SQLITE_LOCKED message is not swallowed by it") is what the exact text
// keeps true.
//
// The lock is real: a second connection holds BEGIN EXCLUSIVE while the executor
// reads, and the executor is opened before the lock is taken because the
// constructor reads sqlite_version() and a locked-out read fails at open.
func TestSQLiteBusyIsTheEqualsRuleAndATimeout(t *testing.T) {
	ctx := context.Background()
	path := sqliteDatasetFile(t)
	ex, err := NewSQLiteExecutor(SQLiteConfig{
		Path:         path,
		Pragmas:      []string{"busy_timeout(0)"},
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("NewSQLiteExecutor(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = ex.Close() })

	locker, err := sql.Open(SQLiteDriverName, "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("open the locking connection: %v", err)
	}
	defer func() { _ = locker.Close() }()
	locker.SetMaxOpenConns(1)
	conn, err := locker.Conn(ctx)
	if err != nil {
		t.Fatalf("take the locking connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("BEGIN EXCLUSIVE: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, "ROLLBACK") }()

	err = sqliteQueryError(t, ex, "SELECT count(*) FROM customers")
	if got := err.Error(); got != "database is locked" {
		t.Fatalf("learner message = %q, want %q", got, "database is locked")
	}
	// The engine's own result code, so this case cannot pass by accident on a
	// shorter message: SQLITE_BUSY is 5, and its extended codes share that value
	// in the low byte.
	var engineErr *sqliteError
	if !errors.As(err, &engineErr) {
		t.Fatalf("%v is not an *engine.sqliteError", err)
	}
	if got := engineErr.Code() & 0xff; got != 5 {
		t.Fatalf("result code = %d, want SQLITE_BUSY (5)", got)
	}
	rule, ok := firstMatchingRule(dialect.SQLite(), err.Error())
	if !ok || rule.Match != "equals" || rule.Pattern != "database is locked" {
		t.Fatalf("%q matched %s %q, want the equals rule for %q", err, rule.Match, rule.Pattern, "database is locked")
	}
	if code := dialect.ClassifyError(err); code != dialect.CodeTimeout {
		t.Fatalf("code = %s, want %s", code, dialect.CodeTimeout)
	}
	if code, ok := ex.ClassifyError(err); !ok || code != dialect.CodeTimeout {
		t.Fatalf("executor.ClassifyError(%v) = (%s, %v), want (%s, true)", err, code, ok, dialect.CodeTimeout)
	}
}

// TestSQLiteConstraintFailuresReachTheLearnerUnframed runs the three constraint
// kinds the task named through a writable executor, because they are the errors
// a write-shaped lesson will produce and the framed text ("constraint failed:
// UNIQUE constraint failed: t.name (2067)") hides which column failed behind the
// driver's class prefix.
func TestSQLiteConstraintFailuresReachTheLearnerUnframed(t *testing.T) {
	ex := sqliteWritableExecutor(t)
	cases := []struct {
		name      string
		statement string
		want      string
	}{
		{"unique", "INSERT INTO probe (id, name, qty) VALUES (1, 'dup', 1); INSERT INTO probe (id, name, qty) VALUES (2, 'dup', 1)", "UNIQUE constraint failed: probe.name"},
		{"not null", "INSERT INTO probe (id, name, qty) VALUES (3, NULL, 1)", "NOT NULL constraint failed: probe.name"},
		{"check", "INSERT INTO probe (id, name, qty) VALUES (4, 'ok', -1)", "CHECK constraint failed: qty >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := sqliteQueryError(t, ex, tc.statement)
			if err.Error() != tc.want {
				t.Fatalf("learner message = %q, want %q", err.Error(), tc.want)
			}
			if isDriverFramed(err.Error()) {
				t.Errorf("%q still carries the driver's framing", err.Error())
			}
			if code := dialect.ClassifyError(err); code != dialect.CodeRuntimeError {
				t.Fatalf("code = %s, want %s", code, dialect.CodeRuntimeError)
			}
		})
	}
}

// TestSQLiteMidIterationErrorIsUnframed covers the second path an engine failure
// can take: a statement that produces a row and then fails on a later step
// (measured with this driver — the error arrives through rows.Err(), not
// Query). The learner must read the engine's sentence on that path too, and the
// engine.Execute envelope must classify it from the same profile.
func TestSQLiteMidIterationErrorIsUnframed(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	const overflow = "WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT abs(-9223372036854775808) FROM c WHERE i=1) SELECT i FROM c"

	rows, err := ex.Query(context.Background(), overflow)
	if err != nil {
		t.Fatalf("%s: %v, want a first row before the failure", overflow, err)
	}
	seen := 0
	for rows.Next() {
		seen++
	}
	rerr := rows.Err()
	rows.Close()
	if seen != 1 {
		t.Fatalf("rows before the failure = %d, want 1", seen)
	}
	if rerr == nil {
		t.Fatal("no error on the second step, want an engine failure")
	}
	if got := rerr.Error(); got != "integer overflow" {
		t.Fatalf("rows.Err() message = %q, want %q", got, "integer overflow")
	}
	if isDriverFramed(rerr.Error()) {
		t.Errorf("%q still carries the driver's framing", rerr.Error())
	}
	if code := dialect.ClassifyError(rerr); code != dialect.CodeRuntimeError {
		t.Fatalf("code = %s, want %s", code, dialect.CodeRuntimeError)
	}

	// The same statement through the full envelope: same sentence, same code.
	qr := Execute(context.Background(), ex, overflow, DefaultLimits())
	if qr.Status != "failed" || qr.Error == nil {
		t.Fatalf("status = %q, error = %v; want a failed result", qr.Status, qr.Error)
	}
	if qr.Error.Message != "integer overflow" || qr.Error.Code != CodeRuntime {
		t.Fatalf("Execute gave (%s, %q), want (%s, %q)", qr.Error.Code, qr.Error.Message, CodeRuntime, "integer overflow")
	}
}

// TestSQLiteBlobTooBigIsAResultLimitThroughExecute is the value-cap rule
// (SQLITE_TOOBIG(18) -> RESULT_LIMIT_EXCEEDED) with the engine's own sentence.
// The statement is cheap: sqlite3_result_zeroblob refuses a length above
// SQLITE_MAX_LENGTH before allocating, so nothing near a gigabyte is ever
// reserved (measured).
func TestSQLiteBlobTooBigIsAResultLimitThroughExecute(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	qr := Execute(context.Background(), ex, "SELECT length(zeroblob(1000000001))", DefaultLimits())
	if qr.Status != "failed" || qr.Error == nil {
		t.Fatalf("status = %q, error = %v; want a failed result", qr.Status, qr.Error)
	}
	if qr.Error.Code != CodeResultLimit {
		t.Fatalf("code = %s, want %s", qr.Error.Code, CodeResultLimit)
	}
	if qr.Error.Message != "string or blob too big" {
		t.Fatalf("message = %q, want %q", qr.Error.Message, "string or blob too big")
	}
}

// TestSQLiteInterruptRuleCannotArriveAsADriverError records why the profile's
// seventh rule is not pinned by a live failure, and pins the unframing it still
// needs.
//
// The rule matters: SQLITE_INTERRUPT(9) means the host stopped the statement, so
// it is a timeout and not a learner mistake. But on the read path a real
// interrupt never arrives as a SQLite sentence: the raw driver (sqlite_raw.go)
// arms sqlite3_interrupt from a goroutine that watches the statement context
// and, once its own interrupt fired, returns ctx.Err() rather than the engine's
// message. The deadline branch of engine.Execute answers first, so the learner
// sees QUERY_TIMEOUT either way — but the profile still has to be able to read a
// framed sentence if a driver or a wasm build ever hands one over, and that is
// what the second half asserts.
func TestSQLiteInterruptRuleCannotArriveAsADriverError(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	const endless = "WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c) SELECT count(*) FROM c"

	rows, err := ex.Query(ctx, endless)
	if err == nil {
		rows.Close()
		t.Fatal("the endless statement finished inside 150 ms; the fixture no longer proves anything")
	}
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		t.Fatalf("the deadline arrived as %v; if this driver ever changes, the profile's interrupted rule becomes reachable and this test should assert it", driverErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v, want it to be context.DeadlineExceeded", err)
	}
	qr := Execute(ctx, ex, endless, DefaultLimits())
	if qr.Error == nil || qr.Error.Code != CodeTimeout {
		t.Fatalf("Execute code = %v, want %s", qr.Error, CodeTimeout)
	}

	// The frame a driver would write for code 9, in the shape measured for codes
	// 8, 18 and 20 ("<errstr> (<rc>)"): the unframer must reduce it to the
	// sentence the profile's equals rule is written against.
	if got := sqliteUnframe("interrupted (9)", 9); got != "interrupted" {
		t.Fatalf("sqliteUnframe(%q, 9) = %q, want %q", "interrupted (9)", got, "interrupted")
	}
	if code := dialect.SQLite().ClassifyError(errors.New("interrupted")); code != dialect.CodeTimeout {
		t.Fatalf("ClassifyError(%q) = %s, want %s", "interrupted", code, dialect.CodeTimeout)
	}
	rule, ok := firstMatchingRule(dialect.SQLite(), "interrupted")
	if !ok || rule.Match != "equals" || rule.Pattern != "interrupted" || rule.Code != dialect.CodeTimeout {
		t.Fatalf("the first rule for %q is %s %q -> %s, want the equals rule for %q", "interrupted", rule.Match, rule.Pattern, rule.Code, "interrupted")
	}
}

// TestSQLiteExecuteShowsTheEngineSentenceNotTheDriverWrapper is the learner's-eye
// case the defect was reported from: the message in QueryResult.error is
// SQLite's own sentence, with no class prefix and no result-code suffix, and with
// it the code is the one the profile means.
//
// "SELECT" is the headline row: it is a parse failure ("incomplete input" is
// what sqlite3_errmsg reports), so the learner needs "look at your SQL" — not
// the runtime-error advice the framed text produced.
func TestSQLiteExecuteShowsTheEngineSentenceNotTheDriverWrapper(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	cases := []struct {
		statement string
		wantCode  string
		wantMsg   string
	}{
		{"SELECT", CodeSyntax, "incomplete input"},
		{"SELEC 1", CodeSyntax, `near "SELEC": syntax error`},
		{"SELECT * FROM customers WHERE #", CodeSyntax, `unrecognized token: "#"`},
		{"SELECT * FROM nope", CodeRuntime, "no such table: nope"},
		{"INSERT INTO customers (id, name, signup_date) VALUES (999, 'x', '2026-01-01')", CodeRuntime, "attempt to write a readonly database"},
	}
	for _, tc := range cases {
		t.Run(tc.statement, func(t *testing.T) {
			qr := Execute(context.Background(), ex, tc.statement, DefaultLimits())
			if qr.Status != "failed" || qr.Error == nil {
				t.Fatalf("status = %q, error = %v; want a failed result", qr.Status, qr.Error)
			}
			if qr.Error.Code != tc.wantCode {
				t.Fatalf("code = %s, want %s (%s)", qr.Error.Code, tc.wantCode, qr.Error.Message)
			}
			// Exact, not a substring: the point is that nothing was added.
			if qr.Error.Message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", qr.Error.Message, tc.wantMsg)
			}
			if qr.Dialect != "sqlite" {
				t.Errorf("dialect = %q, want sqlite", qr.Dialect)
			}
		})
	}
}

// TestSQLiteExecuteReportsRuntimeFailuresWithTheProfileCode runs the cases
// where engine.Execute and the profile already agree through the full path.
func TestSQLiteExecuteReportsRuntimeFailuresWithTheProfileCode(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	cases := []struct {
		name      string
		statement string
		want      string
	}{
		{"missing table", "SELECT * FROM ordrs", CodeRuntime},
		{"write refused by query_only", "INSERT INTO customers (id, name, signup_date) VALUES (999, 'x', '2026-01-01')", CodeRuntime},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qr := Execute(context.Background(), ex, tc.statement, DefaultLimits())
			if qr.Status != "failed" || qr.Error == nil {
				t.Fatalf("status = %q, error = %v; want a failed result", qr.Status, qr.Error)
			}
			if qr.Error.Code != tc.want {
				t.Fatalf("code = %s, want %s (%s)", qr.Error.Code, tc.want, qr.Error.Message)
			}
			if qr.Dialect != "sqlite" {
				t.Errorf("dialect = %q, want sqlite", qr.Dialect)
			}
		})
	}
}

// TestSQLiteMultiStatementSubmissionCannotWrite records two measured facts
// about a statement string that holds several statements, because both differ
// from PostgreSQL.
//
// The driver runs every statement in the string and returns the last one's
// rows; PostgreSQL's extended protocol refuses the second statement outright.
// The read-only anonymous path stays safe anyway — query_only refuses each write
// — and the test proves the refusal happens rather than assuming it. A writable
// environment (M6 step 5) inherits the open question of whether the HTTP layer
// should reject a multi-statement submission before it reaches the engine.
func TestSQLiteMultiStatementSubmissionCannotWrite(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	// The second statement is refused, and the failure is the write refusal.
	_, err := ex.Query(context.Background(),
		"SELECT 1; INSERT INTO customers (id, name, signup_date) VALUES (999, 'x', '2026-01-01')")
	if err == nil {
		t.Fatal("a multi-statement submission with a write succeeded, want a refusal")
	}
	if code := dialect.ClassifyError(err); code != dialect.CodeRuntimeError {
		t.Fatalf("%v classified as %s, want %s", err, code, dialect.CodeRuntimeError)
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Errorf("error %q does not name the read-only refusal", err)
	}
	if got := sqliteCount(t, ex, "SELECT count(*) FROM customers"); got != 30 {
		t.Fatalf("customers has %d rows after the refused write, want 30: something was written", got)
	}

	// Several reads in one string run in order; the driver returns the last
	// result, which is the behaviour a caller must not rely on.
	cols, rows := sqliteQuery(t, ex, "SELECT 1 AS a; SELECT 2 AS b;")
	if len(cols) != 1 || cols[0].Name != "b" {
		t.Fatalf("columns = %+v, want the last statement's single column b", cols)
	}
	if len(rows) != 1 || rows[0][0] != int64(2) {
		t.Fatalf("rows = %v, want [[2]]", rows)
	}
}

// TestSQLiteSyntaxErrorThroughExecuteIsSyntaxError closes the classification
// gap the SQLite adapter used to document.
//
// execute.go's classify is the PostgreSQL SQLSTATE table with a runtime
// catch-all, so before the ErrorClassifier hook a SQLite syntax error reached
// the learner as QUERY_RUNTIME_ERROR — advice about the wrong thing, because
// "look at your SQL" is only useful for a parse failure. Execute now asks the
// executor first (engine.ErrorClassifier), so the dialect profile's verdict is
// what the learner reads, and this test asserts the two agree.
func TestSQLiteSyntaxErrorThroughExecuteIsSyntaxError(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))

	qr := Execute(context.Background(), ex, "SELEC 1", DefaultLimits())
	if qr.Status != "failed" || qr.Error == nil {
		t.Fatalf("status = %q, error = %v; want a failed result", qr.Status, qr.Error)
	}
	if qr.Error.Code != CodeSyntax {
		t.Fatalf("Execute code = %s, want %s from the dialect classifier", qr.Error.Code, CodeSyntax)
	}
	// The executor's own answer agrees with what Execute reported, so the two
	// doors cannot drift apart.
	if got, ok := ex.ClassifyError(errors.New(qr.Error.Message)); !ok || got != dialect.CodeSyntaxError {
		t.Fatalf("executor.ClassifyError(%q) = (%s, %v), want (%s, true)", qr.Error.Message, got, ok, dialect.CodeSyntaxError)
	}
}

/* ---------- pragmas, pool, health ---------- */

// TestSQLitePragmasComeFromTheDSNAndNotFromAGlobal proves SQLite's defaults are
// the danger the DSN is there to close: a plain connection to the same file
// reports foreign_keys OFF while the executor's does not, so nothing is being
// inherited from a process-wide setting.
func TestSQLitePragmasComeFromTheDSNAndNotFromAGlobal(t *testing.T) {
	path := sqliteDatasetFile(t)
	ex := sqliteExecutor(t, path)

	plain, err := sql.Open(SQLiteDriverName, "file:"+path)
	if err != nil {
		t.Fatalf("plain open: %v", err)
	}
	plain.SetMaxOpenConns(1)
	defer func() { _ = plain.Close() }()

	defaults := map[string]any{
		"foreign_keys": mustScalar(t, plain, "PRAGMA foreign_keys"),
		"busy_timeout": mustScalar(t, plain, "PRAGMA busy_timeout"),
		"query_only":   mustScalar(t, plain, "PRAGMA query_only"),
	}
	if defaults["foreign_keys"] != int64(0) {
		t.Errorf("a connection with no pragmas reports foreign_keys = %v, want 0 (SQLite's default)", defaults["foreign_keys"])
	}
	if defaults["query_only"] != int64(0) {
		t.Errorf("a connection with no pragmas reports query_only = %v, want 0", defaults["query_only"])
	}

	if got := sqliteScalar(t, ex, "PRAGMA foreign_keys"); got != int64(1) {
		t.Errorf("the executor reports foreign_keys = %v, want 1", got)
	}
	if got := sqliteScalar(t, ex, "PRAGMA busy_timeout"); got != int64(2000) {
		t.Errorf("the executor reports busy_timeout = %v, want 2000", got)
	}
	if got := sqliteScalar(t, ex, "PRAGMA query_only"); got != int64(1) {
		t.Errorf("the executor reports query_only = %v, want 1", got)
	}
	if !ex.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
}

// TestSQLiteDSNEscapesThePathAndCarriesThePragmas checks the two things a
// hand-built URI gets wrong: a relative path must stay relative, and a path or
// pragma containing URI punctuation must not change the DSN's meaning.
func TestSQLiteDSNEscapesThePathAndCarriesThePragmas(t *testing.T) {
	got, err := sqliteDSN("relative/p.db", []string{"foreign_keys(1)"}, false)
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	if want := "file:relative/p.db?_pragma=foreign_keys%281%29"; got != want {
		t.Errorf("sqliteDSN = %q, want %q", got, want)
	}
	got, err = sqliteDSN("/tmp/x.db", []string{"foreign_keys(1)"}, true)
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	for _, want := range []string{"mode=ro", "_pragma=query_only%281%29", "_pragma=foreign_keys%281%29"} {
		if !strings.Contains(got, want) {
			t.Errorf("read-only DSN %q is missing %q", got, want)
		}
	}
	if _, err := sqliteDSN("  ", nil, false); err == nil {
		t.Error("sqliteDSN accepted an empty path")
	}

	// The escaped path round-trips: the file that gets created is the file with
	// the punctuation in its name.
	dir := t.TempDir()
	odd := filepath.Join(dir, "a b?c.db")
	dsn, err := sqliteDSN(odd, []string{"foreign_keys(1)"}, false)
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	db, err := sql.Open(SQLiteDriverName, dsn)
	if err != nil {
		t.Fatalf("open %q: %v", dsn, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE t(i INTEGER); INSERT INTO t VALUES (7)"); err != nil {
		t.Fatalf("use the odd path: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT i FROM t").Scan(&n); err != nil || n != 7 {
		t.Fatalf("odd path read = %d, %v", n, err)
	}
	if _, err := os.Stat(odd); err != nil {
		t.Errorf("the database file is not at %q: %v", odd, err)
	}
}

// TestSQLiteMissingFileIsUnavailableNotEmpty proves the read-only open refuses
// to invent a database: a path that does not exist is an environment fault, not
// an empty schema every lesson then fails against.
func TestSQLiteMissingFileIsUnavailableNotEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.db")
	_, err := NewSQLiteExecutor(SQLiteConfig{
		Path:     missing,
		ReadOnly: true,
		Pragmas:  SQLiteReadOnlyPragmas(),
	})
	if err == nil {
		t.Fatal("opening a missing file read-only succeeded, want an environment error")
	}
	if !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatalf("error %v is not ErrEnvironmentUnavailable", err)
	}
	if _, statErr := os.Stat(missing); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the failed open created %s anyway", missing)
	}

	// A writable open does create the file, which is SQLite's own behaviour and
	// the reason the read-only path has to be the one the anonymous dataset
	// uses.
	ex, err := NewSQLiteExecutor(SQLiteConfig{Path: missing})
	if err != nil {
		t.Fatalf("writable open: %v", err)
	}
	defer func() { _ = ex.Close() }()
	if _, statErr := os.Stat(missing); statErr != nil {
		t.Errorf("a writable open did not create %s: %v", missing, statErr)
	}
}

// TestSQLiteVersionReadyAndStatsChecks the reporting methods, including the
// honest zeroes Stats returns: SQLite has no pool whose saturation could be
// published.
func TestSQLiteVersionReadyAndStatsChecks(t *testing.T) {
	ex := sqliteExecutor(t, sqliteDatasetFile(t))
	ctx := context.Background()

	version, err := ex.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if !strings.HasPrefix(version, "SQLite 3.") {
		t.Errorf("Version = %q, want a SQLite 3.x version", version)
	}
	if ok, reason := ex.Ready(ctx); !ok {
		t.Errorf("Ready = (false, %q), want ready", reason)
	}
	if err := ex.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if got := ex.Stats(); got != (PoolStats{}) {
		t.Errorf("Stats = %+v, want zeroes: SQLite has no pool numbers to report", got)
	}
	if got, want := ex.Dialect(), "sqlite"; got != want {
		t.Errorf("Dialect = %q, want %q", got, want)
	}
	// EngineLabel is what engine.Execute puts in QueryResult.engine; without it
	// the result would be labelled "postgresql". It carries the version the
	// engine itself reported, read at open.
	if got := ex.EngineLabel(); !strings.HasPrefix(got, "SQLite 3.") {
		t.Errorf("EngineLabel = %q, want a SQLite 3.x label", got)
	}
	if got, want := EngineLabelOf(ex), ex.EngineLabel(); got != want {
		t.Errorf("EngineLabelOf = %q, want the executor's own label %q", got, want)
	}
	if got, want := DialectOf(ex), "sqlite"; got != want {
		t.Errorf("DialectOf = %q, want %q", got, want)
	}

	if err := ex.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if ok, reason := ex.Ready(ctx); ok || reason == "" {
		t.Errorf("Ready after Close = (%v, %q), want not ready with a reason", ok, reason)
	}
}

/* ---------- small helpers ---------- */

// mustRows runs a statement and returns every encoded row, failing the test on
// any error.
func mustRows(t *testing.T, ex Executor, statement string) [][]any {
	t.Helper()
	_, rows := sqliteQuery(t, ex, statement)
	return rows
}

// mustScalar reads one value straight from a *sql.DB, for the assertions that
// are about the engine's own defaults rather than about encoding.
func mustScalar(t *testing.T, db *sql.DB, statement string) any {
	t.Helper()
	var v any
	if err := db.QueryRow(statement).Scan(&v); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	return v
}
