// SQLite is Querycraft's second dialect. This file is the gateway's engine for
// it: a real SQLite behind the same Executor seam as PgxExecutor, so
// engine.Execute supplies the row cap, the byte cap, the deadline, the truncation
// flag and the RESULT_LIMIT_EXCEEDED envelope through exactly one code path for
// both dialects. Nothing about the caps is reimplemented here.
//
// Four dialect decisions frame the file and each was measured, not assumed; a
// fifth states the transaction scope.
//
//  1. modernc.org/sqlite, never mattn/go-sqlite3. The latter needs cgo, and the
//     single binary is cgo-free by contract (`CGO_ENABLED=0 go build ./...`,
//     plus the check-cgo guard in `make test`). modernc carries SQLite compiled
//     to Go, with no cgo and no build tag.
//
//  2. Reads run over the raw sqlite3 API through this package's own
//     database/sql/driver (sqlite_raw.go), not over modernc's database/sql
//     driver. That driver materialises any TEXT result column whose declared
//     type is DATE, DATETIME or TIMESTAMP into a time.Time before the value
//     reaches this package: `SELECT signup_date` for a stored '2025-11-02' came
//     back as 2025-11-02T00:00:00Z (measured). The dialect contract says the
//     stored text is the answer — the browser adapter reads sqlite3_column_text
//     directly and reports exactly that — so this package reads each column by
//     its storage class (sqlite3_column_type) and returns that class's accessor
//     result verbatim, consulting no declared type for the value. The declared
//     type still supplies the column *label* (sqlite3_column_decltype), so the
//     labels and the values cannot drift apart, and database/sql is kept for the
//     pool and for the one-time multi-statement seed in internal/envdb.
//
//  3. Pragmas are statements, run per connection at open, and never come from a
//     global. SQLite's defaults are the dangerous part: foreign_keys is OFF
//     (measured on a fresh connection with this driver: PRAGMA foreign_keys
//     = 0), so a lesson about referential integrity silently teaches nothing
//     unless the chapter turns it on. A `PRAGMA` issued once over a pool would
//     land on whichever connection happened to run it, so applyPragmas
//     (sqlite_raw.go) runs the configured set on every connection as the
//     connector hands it out. The anonymous learner's shared dataset is
//     query_only(1) *and* mode=ro: the first is the engine-level refusal a
//     write receives (SQLITE_READONLY, which the profile classifies as
//     QUERY_RUNTIME_ERROR — a mistake the learner can see, not a broken
//     environment), the second is the open-level guard that also makes a
//     missing file an ENVIRONMENT_UNAVAILABLE fault rather than an empty
//     database that answers "no such table" to every lesson.
//
//  4. The deadline really does stop the statement. The raw driver arranges, via
//     interruptOnDone (sqlite_raw.go), for a goroutine to call
//     sqlite3_interrupt when the statement context is done; SQLite's VDBE
//     checks the interrupt flag as it runs, so a statement that never
//     terminates on its own is genuinely pre-empted mid-statement. Measured
//     with this driver: a 300 ms context over `WITH RECURSIVE c(i) AS (SELECT 1
//     UNION ALL SELECT i+1 FROM c) SELECT count(*) FROM c` returned at 301 ms,
//     and the one pooled connection was immediately reusable (the follow-up
//     count returned in well under a second), which is the proof that matters —
//     a single-connection pool cannot serve the next statement until the
//     interrupted one has stopped executing. For scale, the same recursive CTE
//     bounded at ten million rows takes about 3.1 s (~3.2M rows/s), so the
//     unbounded statement would not have finished inside any request deadline:
//     it was stopped, not merely abandoned by the client.
//
//     One measured detail about *how* that error arrives: it is
//     context.DeadlineExceeded, not an engine error, because the raw driver's
//     step path returns the context's error once its own interrupt fired. That
//     identity is what engine.Execute's errors.Is(err, context.DeadlineExceeded)
//     branch turns into QUERY_TIMEOUT (measured: the test logs the concrete
//     type). A SQLite error of its own, reported as the engine's sentence, is
//     what the profile classifies when the engine itself fails — e.g.
//     SQLITE_READONLY for a refused write — and the profile's "interrupted"
//     rule still covers an interrupt raised outside a context.
//
//  5. (Scope, not a measurement.) A statement is its own transaction: this
//     executor never issues BEGIN itself, so under SQLite's autocommit each
//     statement is atomic on its own and there is nothing for Rows.Close to
//     commit or roll back. Both paths run this way today: the anonymous shared
//     dataset refuses every write through query_only, and the writable
//     per-account environment (envdb.forAccountLocked) commits each statement
//     as it finishes. A transaction spanning statements would belong to one
//     learner's own file, which is the file this path already runs.
//
// One measured behaviour is worth stating because it is a dialect difference a
// reader will otherwise trip over:
//
//   - A statement string containing several statements runs all of them, in
//     order, and returns the last one's rows (SQLite's parser advances through
//     the string; the raw driver's stepScript reproduces modernc's measured
//     loop). PostgreSQL's extended protocol refuses a second statement, so this
//     is a real divergence. The anonymous read-only path stays safe: query_only
//     refuses every write. The writable path carries the question of whether the
//     HTTP layer should reject a multi-statement submission outright.
package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dialect"
)

// SQLiteDriverName is the driver name modernc.org/sqlite registers. It is worth
// naming because the near miss is a different engine: "sqlite3" is
// mattn/go-sqlite3, which needs cgo and would break the cgo-free binary.
const SQLiteDriverName = "sqlite"

// sqliteOpenTimeout bounds the single connection the constructor opens to prove
// the file can be opened and every pragma applied.
const sqliteOpenTimeout = 5 * time.Second

// SQLiteConfig configures a SQLiteExecutor over one SQLite database file.
type SQLiteConfig struct {
	// Path is the database file. ":memory:" opens a private in-memory database
	// and is meant for tests: keep MaxOpenConns at 1, because a second pooled
	// connection would open its own empty database.
	Path string

	// ReadOnly opens the file with SQLite's own mode=ro and adds the
	// query_only(1) pragma. The two are deliberately not separate switches:
	// the anonymous learner's shared dataset is read-only, and a caller asking
	// for ReadOnly cannot accidentally get a writable engine by forgetting a
	// pragma.
	ReadOnly bool

	// Pragmas are run as statements on every connection at open, e.g.
	// "foreign_keys(1)", "busy_timeout(2000)", "journal_mode(WAL)". They are
	// explicit and per-connection on purpose: SQLite has no global to inherit
	// from, and inheriting one would be wrong even if it did.
	Pragmas []string

	// MaxOpenConns and MaxIdleConns bound the database/sql pool. A zero keeps
	// database/sql's default, which is "no ceiling" for MaxOpenConns: the
	// gateway already bounds concurrency, and inventing a tighter ceiling here
	// would serialise readers for no reason.
	MaxOpenConns int
	MaxIdleConns int
}

// SQLiteReadOnlyPragmas returns the pragma set for the shared, anonymous
// dataset the gateway serves.
//
// foreign_keys is here because SQLite defaults it OFF, so leaving it out would
// mean a foreign-key lesson teaches nothing. busy_timeout is here so a lock is
// waited out briefly instead of failing at once; the profile classifies
// SQLITE_BUSY as QUERY_TIMEOUT, not as an environment fault.
//
// query_only is deliberately absent: SQLiteConfig.ReadOnly adds it together
// with mode=ro, so it cannot be forgotten. journal_mode is deliberately absent
// too, and that is measured rather than an oversight: a journal mode belongs to
// the file, a read-only connection cannot change it, and asking anyway fails
// the open — a read-only open of a WAL file with PRAGMA journal_mode(DELETE)
// fails with "disk I/O error (3850)". The template that creates the file sets
// its journal mode once, when the file is seeded.
func SQLiteReadOnlyPragmas() []string {
	return []string{
		"foreign_keys(1)",
		"busy_timeout(2000)",
	}
}

// SQLiteExecutor runs learner statements against one SQLite database file.
//
// It implements the same Executor interface as PgxExecutor, so the two differ
// only where the dialects genuinely differ: value encoding and column labels
// come from the embedded profile (dialect.SQLite()), and error classification
// is the profile's message table (dialect.ClassifyError).
type SQLiteExecutor struct {
	db      *sql.DB
	profile dialect.Profile
	// version is the engine's own sqlite_version() string, read once at open.
	// EngineLabel needs it without a context (Executor.EngineLabel answers
	// synchronously), and it is not invented: a constructor that cannot read
	// the version fails rather than publishing a placeholder.
	version string
}

// NewSQLiteExecutor opens a pool over one SQLite database and proves it works.
//
// The open is eager on purpose. SQLite creates a missing file on open, so a
// mistyped path would otherwise look healthy and answer "no such table:
// customers" for every lesson — a runtime error that blames the learner for a
// configuration fault. Opening one connection now also runs every pragma as a
// statement, so a pragma the engine rejects fails here once instead of on the
// first request.
func NewSQLiteExecutor(config SQLiteConfig) (*SQLiteExecutor, error) {
	// The pool is database/sql's, but each connection is this package's driver
	// over the raw sqlite3 API (sqlite_raw.go), which is what makes a read value
	// the storage class's verbatim result instead of modernc's parsed row.
	connector, err := sqliteConnectorFor(config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	db := sql.OpenDB(connector)
	if config.MaxOpenConns > 0 {
		db.SetMaxOpenConns(config.MaxOpenConns)
	}
	if config.MaxIdleConns > 0 {
		db.SetMaxIdleConns(config.MaxIdleConns)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteOpenTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	// The version is read here, not lazily, for two reasons: EngineLabel has no
	// context to query with, and a file that opens but cannot answer its own
	// version is not a usable engine, so it should fail at construction instead
	// of at the first lesson.
	var version string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	return &SQLiteExecutor{db: db, profile: dialect.SQLite(), version: version}, nil
}

// Dialect names the dialect this executor runs. It comes from the embedded
// profile rather than a literal here, so QueryResult.dialect and the contract
// file cannot drift, and a caller can ask the executor what it is instead of
// hard-coding "sqlite" at the call site.
func (e *SQLiteExecutor) Dialect() string { return e.profile.Dialect }

// EngineLabel implements Executor, which is how engine.Execute fills
// QueryResult.engine: without it, a result produced by SQLite would be labelled
// "postgresql" because that is EngineName's default. The value carries the
// engine's own version ("SQLite 3.50.3") so the label names a real string from
// the running binary, not a constant this file picked.
func (e *SQLiteExecutor) EngineLabel() string {
	if e.version == "" {
		return e.profile.EngineLabel
	}
	return "SQLite " + e.version
}

// ClassifyError maps a driver error to this dialect's closed QueryResult code
// with the embedded profile — the same rule table contracts/dialects/sqlite.json
// publishes and the Node audit reads — and implements Executor.ClassifyError,
// which is what makes Execute ask this executor first instead of sending a
// SQLite syntax error through the PostgreSQL SQLSTATE table and reporting it as
// QUERY_RUNTIME_ERROR.
//
// The text it classifies is the engine's own sentence: the raw driver
// (sqlite_raw.go) reports sqlite3_errmsg verbatim, so the profile's `equals` and
// `prefix` rules see the text they were written for ("incomplete input",
// "unrecognized token: …", "database is locked", "interrupted"). A
// database/sql driver's framing would hide those words, and the catch-all rule
// would then report a parse failure as a runtime error.
//
// The profile returns "" only for a nil error, so the second value is "the
// profile had a message to match": a real error always classifies, and the
// catch-all rule is what guarantees it (TestClassifyErrorNeverEmitsExecutorCodes).
func (e *SQLiteExecutor) ClassifyError(err error) (string, bool) {
	code := e.profile.ClassifyError(err)
	if strings.TrimSpace(code) == "" {
		return "", false
	}
	return code, true
}

// Diagnostics implements Executor. SQLite has no server-side session and no
// second role to check, so this returns nil and /diagnostics leaves its learner
// block out rather than publishing fields it cannot measure.
func (e *SQLiteExecutor) Diagnostics(ctx context.Context) map[string]any { return nil }

/* ---------- the engine's error ---------- */

// sqliteError is an engine failure as the learner reads it: SQLite's own
// sentence from sqlite3_errmsg. It marks an error as the engine's own rather
// than a host or driver failure, and engine.Execute takes its Error() text as
// the message (execute.go, engineWordsFor); that text is what the profile's
// message rules are written against.
type sqliteError struct {
	sentence string
}

// Error reports the engine's sentence.
func (e *sqliteError) Error() string { return e.sentence }

// Query implements Executor.
//
// The error is the engine's own sentence: the words are SQLite's, not a
// paraphrase and not a driver's wrapper. Classifying it is the profile's job
// (ClassifyError above), not this method's: the code has to be one
// contracts/dialects/sqlite.json can produce, and a literal here could invent
// one.
func (e *SQLiteExecutor) Query(ctx context.Context, statement string, args ...any) (Rows, error) {
	rows, err := e.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	return &sqliteRows{rows: rows, profile: e.profile}, nil
}

// Ping implements Executor.
func (e *SQLiteExecutor) Ping(ctx context.Context) error { return e.db.PingContext(ctx) }

// Version implements Executor. It reports the engine's own answer, prefixed so
// /health reads the same shape as PostgreSQL's "PostgreSQL 16.3".
func (e *SQLiteExecutor) Version(ctx context.Context) (string, error) {
	var v string
	if err := e.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v); err != nil {
		return "", err
	}
	if v == "" {
		return "", nil
	}
	return "SQLite " + v, nil
}

// Stats implements Executor. SQLite has no pool of its own to report, so this
// returns zeroes rather than a plausible-looking maximum — the same rule
// UnavailableExecutor follows. database/sql does keep host-side pool counters,
// but those describe this process's connections, not the engine, and
// publishing them under the names PoolStats gives pgxpool would invent a
// meaning they do not have.
func (e *SQLiteExecutor) Stats() PoolStats { return PoolStats{} }

// Ready implements Executor. A pooled handle tracks connections, not liveness,
// so the honest answer comes from asking the engine for a connection; the probe
// is deliberately shorter than the query deadline, because a health check that
// hangs for the full 5 s is itself a bug.
func (e *SQLiteExecutor) Ready(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, readyProbeTimeout)
	defer cancel()
	if err := e.db.PingContext(ctx); err != nil {
		return false, RedactSecrets(err.Error())
	}
	return true, ""
}

// Close implements Executor.
func (e *SQLiteExecutor) Close() error { return e.db.Close() }

// sqliteRows is a forward-only cursor over one database/sql result set.
type sqliteRows struct {
	rows    *sql.Rows
	profile dialect.Profile

	loaded bool
	cols   []Column
	// decls is the driver's declared type per column. The encoding call and the
	// label both need it: the label reports it, and EncodeValue receives it so
	// the call site shows what the column claimed — even though the profile
	// deliberately does not apply it, because SQLite applies affinity on write,
	// not on read.
	decls []string
	// raw and ptrs are the Scan scratch: one value per column, scanned through
	// pointers, so each value keeps the Go type the driver produced for its
	// storage class instead of being funnelled through a string.
	raw  []any
	ptrs []any
}

func (r *sqliteRows) Columns() []Column {
	if r.loaded {
		return r.cols
	}
	r.loaded = true
	names, err := r.rows.Columns()
	if err != nil {
		// The Rows interface has nowhere to report a metadata failure, and the
		// row count is already known to database/sql; Next reports the same
		// failure again if the result cannot be read.
		return nil
	}
	types, _ := r.rows.ColumnTypes()
	r.cols = make([]Column, len(names))
	r.decls = make([]string, len(names))
	r.raw = make([]any, len(names))
	r.ptrs = make([]any, len(names))
	for i, name := range names {
		decl := ""
		if i < len(types) && types[i] != nil {
			decl = types[i].DatabaseTypeName()
		}
		r.decls[i] = decl
		r.cols[i] = Column{Name: name, Type: r.columnLabel(decl)}
		r.ptrs[i] = &r.raw[i]
	}
	return r.cols
}

// columnLabel applies the profile's column-type-label rule: the declared type
// when the column has one, otherwise the profile's fallback label
// ("expression"). A label is not a type — a DATE column labels DATE while every
// value in it is TEXT — which is exactly why the fallback must say "expression"
// rather than invent a type for COUNT(*) or price * 2.
//
// The raw driver reads sqlite3_column_decltype itself and reports it verbatim
// (sqlite_raw.go), so the dataset's INTEGER/TEXT/NUMERIC/DATE arrive as the
// schema authored them.
func (r *sqliteRows) columnLabel(decl string) string {
	if decl != "" {
		return decl
	}
	if fallback := r.profile.ValueEncoding.ColumnTypeLabel.Fallback; fallback != "" {
		return fallback
	}
	return "expression"
}

func (r *sqliteRows) Next() bool { return r.rows.Next() }

// Values implements Rows by handing each driver value to the profile's encoder.
//
// Scanning into []any is what preserves the storage class: database/sql gives
// back what the driver put there — int64 for INTEGER, float64 for REAL, string
// for TEXT, []byte for BLOB, nil for NULL. Nothing here turns a 0/1 into a
// boolean, parses a date-shaped string into a time, or re-applies the column's
// declared affinity: EncodeValue reads the class from the value, because by the
// time a value is read its class is already fixed.
func (r *sqliteRows) Values() ([]any, error) {
	if !r.loaded {
		r.Columns()
	}
	// A statement that returned no result columns (a write, say) has no values
	// to scan; Scan with no destinations is not a call this should make.
	if len(r.ptrs) == 0 {
		return []any{}, nil
	}
	for i := range r.raw {
		r.raw[i] = nil
	}
	if err := r.rows.Scan(r.ptrs...); err != nil {
		// The engine's own sentence, exactly as Query and Err hand it over, so
		// ClassifyError reads SQLite's words and not a database/sql rendering.
		return nil, err
	}
	out := make([]any, len(r.raw))
	for i, v := range r.raw {
		out[i] = r.profile.EncodeValue(v, r.decls[i])
	}
	return out, nil
}

// Err implements Rows. An engine failure that lands on a later step (measured:
// an integer overflow on the second row of a recursive CTE) arrives here, and
// the learner reads the engine's own sentence on this path too.
func (r *sqliteRows) Err() error { return r.rows.Err() }

// Close implements Rows. There is no transaction to end: this executor never
// opens one, so a statement is its own transaction under SQLite's autocommit
// and its effect is already decided. Closing releases the statement and returns
// the connection to the pool — which is why engine.Execute cancelling the
// request context on a cap is enough to stop the statement (sqlite3_interrupt)
// and why the connection is reusable immediately afterwards (measured).
func (r *sqliteRows) Close() { _ = r.rows.Close() }
