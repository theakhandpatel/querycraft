// This file is the SQLite read path's engine, and it exists because of one
// measured defect in the database/sql driver underneath it.
//
// modernc.org/sqlite (read in v1.38.2, sqlite.go:265-275) converts any TEXT
// result column whose declared type is DATE, DATETIME or TIMESTAMP into a
// time.Time before database/sql ever sees it: `SELECT signup_date` for a stored
// '2025-11-02' returns 2025-11-02T00:00:00Z. That is a *driver* decision, not
// the engine's: sqlite3_column_text reports the stored text verbatim, and the
// browser adapter — @sqlite.org/sqlite-wasm, which reads columns through
// sqlite3_column_* directly — reports exactly that text. The dialect contract
// says the stored text is the answer (contracts/dialects/sqlite.json,
// value_encoding.date), so the gateway is the side that must change, and the
// conversion is not reachable to disable: the driver's connection type is
// unexported, there is no result-time hook, and `_time_format` is write-side
// only. Hence a driver of our own.
//
// What this driver is:
//
//   - A database/sql/driver over modernc.org/sqlite/lib — the same pure-Go,
//     cgo-free SQLite the modernc driver wraps, reached through its generated
//     bindings (lib.Xsqlite3_*). It is registered nowhere and opened by no name:
//     SQLiteExecutor builds a sqliteConnector from its SQLiteConfig and hands it
//     to sql.OpenDB, so database/sql still owns the pool, the idle ceiling, the
//     lifetime and the blocking when the pool is exhausted. What changes is only
//     what a row value *is*.
//
//   - Verbatim by construction. Each column is read with
//     sqlite3_column_type (the storage class), then the accessor that class
//     names — sqlite3_column_int64/double/text/blob — and sqlite3_column_bytes
//     for the exact byte length, so a TEXT value reaches the profile as the
//     string the file holds (embedded NULs included) and a BLOB arrives as
//     bytes. No declared type is consulted for the value: DATE, DATETIME and
//     TIMESTAMP are labels here, exactly as they are in the browser.
//
//   - The storage class and the declared type are read from the same statement.
//     sqlite3_column_decltype still supplies the column *label* (the profile's
//     column_type_label rule, "the declared type verbatim"), which is why the
//     labels and the values cannot drift apart.
//
// Three mechanics were measured rather than assumed, and each shaped the code:
//
//  1. sqlite3_prepare_v2's out-parameters and sqlite3_open_v2's must live in C
//     memory, not in a Go stack slot. The generated bindings write through the
//     uintptr they are given, and the Go runtime can relocate a goroutine stack
//     when a call grows it; a write into the abandoned stack is simply lost.
//     Measured with a stack slot: the open returned SQLITE_OK with a valid db
//     handle but the prepare returned a *zero* statement handle, and the first
//     step then answered SQLITE_MISUSE(21) — which reads as a missing table,
//     not as a bug in the caller. modernc itself mallocs both slots; this file
//     allocates them once per call from the connection's TLS.
//
//  2. A deadline really does stop the statement, and it is sqlite3_interrupt
//     that does it. SQLite honours the interrupt flag only *while a VDBE is
//     executing* — sqlite3_step resets it at entry when no statement is running
//     ("This prevents a call to sqlite3_interrupt from interrupting a statement
//     that has not yet started") — so the interrupt is armed for the whole
//     query and lands inside a long step, which is where a recursive CTE or a
//     scan spends its time. A deadline that arrives between two rows is the
//     cursor's own context check: the next step is never started, and the
//     statement is finalized (rolled back) by Close, which is what
//     engine.Execute's cap-and-cancel path relies on.
//
//  3. The error sentence is the engine's own. sqlite3_errmsg on the raw handle
//     is exactly the text the profile's rules were written against — "no such
//     table: customers", "incomplete input", "attempt to write a readonly
//     database" — so nothing has to be un-framed here; modernc's
//     "<errstr>: <msg> (<rc>)" rendering is the driver artefact this path does
//     not produce (sqliteErrorOf in sqlite.go still strips it, because that
//     framing belongs to whichever driver produced an error and a caller may
//     still hand us one of modernc's from the write path).
//
// The flags are the ones modernc opens with — READWRITE|CREATE for a writable
// file, READONLY for the shared dataset, FULLMUTEX because the interrupt
// goroutine touches the handle while the stepping goroutine runs, and URI so a
// path is a path and not a coincidence of punctuation.
package engine

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	lib "modernc.org/sqlite/lib"
)

// sqlitePtrSize is the width of a C pointer on this build. The generated
// bindings pass pointers as uintptr, so this is the size of every out-parameter
// slot this file allocates.
const sqlitePtrSize = int(unsafe.Sizeof(uintptr(0)))

// loadUintptr reads one pointer-sized word from C memory at p.
//
// The out-parameters of sqlite3_open_v2 and sqlite3_prepare_v2 must live in C
// memory (see the file comment, mechanic 1), but reading one back must not turn
// into an `unsafe.Pointer(p)` conversion that go vet rightly flags as
// unprovable. libc.GoBytes views the C bytes without that conversion, and the
// word is decoded in native byte order, so the read is portable and the vet
// check stays meaningful for every pointer conversion this package does make.
func loadUintptr(p uintptr) uintptr {
	b := libc.GoBytes(p, sqlitePtrSize)
	if sqlitePtrSize == 8 {
		return uintptr(binary.NativeEndian.Uint64(b))
	}
	return uintptr(binary.NativeEndian.Uint32(b))
}

/* ---------- the connector database/sql pools ---------- */

// sqliteRawDriver is the driver.Driver half of the connector.
//
// It has no Open: the executor never asks database/sql for the driver *by name*,
// which is the point — modernc's "sqlite" registration stays untouched for the
// write path (the one-time seed in internal/httpapi, which uses a multi-statement
// Exec that this file deliberately does not reimplement), and this read path
// cannot be reached by an accidental sql.Open("...") elsewhere.
type sqliteRawDriver struct{}

// Open is unreachable through SQLiteExecutor, which opens the connector
// directly. It reports that rather than returning a half-open connection.
func (*sqliteRawDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("querycraft sqlite: the raw read driver opens through its connector, never by name")
}

// sqliteConnector opens one connection per call, with every pragma already
// applied, so a connection database/sql takes from the idle pool carries the
// same settings as one it has just created.
type sqliteConnector struct {
	// path is the database file, or ":memory:".
	path string
	// pragmas are applied in order (busy_timeout first) at every open, e.g.
	// "foreign_keys(1)", "busy_timeout(2000)", "query_only(1)".
	pragmas []string
	// readOnly opens with SQLite's own mode=ro, which is what makes a missing
	// file an open failure instead of a fresh empty database.
	readOnly bool

	driver *sqliteRawDriver
}

func (c *sqliteConnector) Driver() driver.Driver { return c.driver }

func (c *sqliteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return openSQLiteRawConn(c.path, c.pragmas, c.readOnly)
}

// sqliteConnectorFor resolves a SQLiteConfig into the connector the executor
// pools.
//
// SQLiteConfig.DSN is accepted verbatim, pragmas included, and parsed back into
// the fields above: the DSN's escaping is url.Values', the pragma syntax is
// SQLite's, and everything the connector cannot honour (a custom VFS, mode
// abbreviations other than ro/rw/rwc, unknown parameters) is refused by name
// rather than silently ignored.
func sqliteConnectorFor(config SQLiteConfig) (*sqliteConnector, error) {
	connector := &sqliteConnector{driver: &sqliteRawDriver{}}
	if dsn := strings.TrimSpace(config.DSN); dsn != "" {
		path, pragmas, readOnly, err := parseSQLiteDSN(dsn)
		if err != nil {
			return nil, err
		}
		connector.path, connector.pragmas, connector.readOnly = path, pragmas, readOnly
		return connector, nil
	}
	if strings.TrimSpace(config.Path) == "" {
		return nil, errors.New("sqlite: no database path")
	}
	connector.path = config.Path
	connector.readOnly = config.ReadOnly
	connector.pragmas = append(connector.pragmas, config.Pragmas...)
	if config.ReadOnly {
		// ReadOnly is deliberately not two switches: mode=ro and query_only(1)
		// travel together, so a caller cannot ask for the read-only dataset and
		// get a writable engine by forgetting a pragma.
		connector.pragmas = append(connector.pragmas, "query_only(1)")
	}
	return connector, nil
}

// parseSQLiteDSN splits a modernc-style DSN into its path, pragmas and access
// mode. It understands the two shapes that are actually built for this engine —
// `file:relative/path.db?_pragma=...&mode=ro` (internal/engine.sqliteDSN and
// internal/httpapi.sqliteSeedDSN) and a bare path (":memory:") — and refuses the
// rest, because a DSN parameter that is quietly dropped is a connection that
// quietly differs from the one that was asked for.
func parseSQLiteDSN(dsn string) (path string, pragmas []string, readOnly bool, err error) {
	raw := strings.TrimPrefix(dsn, "file:")
	query := ""
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		query, raw = raw[i+1:], raw[:i]
	}
	if raw == "" {
		return "", nil, false, fmt.Errorf("sqlite: DSN %q names no database file", dsn)
	}
	if decoded, derr := url.PathUnescape(raw); derr == nil {
		raw = decoded
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", nil, false, fmt.Errorf("sqlite: bad DSN query in %q: %w", dsn, err)
	}
	for _, p := range q["_pragma"] {
		if strings.TrimSpace(p) != "" {
			pragmas = append(pragmas, p)
		}
	}
	switch mode := q.Get("mode"); mode {
	case "", "rw", "rwc":
	case "ro":
		readOnly = true
	case "memory":
		return "", nil, false, errors.New("sqlite: mode=memory is not supported by the read path; open a file or use Path \":memory:\"")
	default:
		return "", nil, false, fmt.Errorf("sqlite: unknown DSN mode %q", mode)
	}
	for key := range q {
		if key != "_pragma" && key != "mode" {
			return "", nil, false, fmt.Errorf("sqlite: DSN parameter %q is not supported by the read path", key)
		}
	}
	return raw, pragmas, readOnly, nil
}

/* ---------- one connection ---------- */

// sqliteRawConn is one SQLite handle plus the TLS its generated bindings need.
//
// mu guards Close against the context-driven interrupt: sqlite3_interrupt takes
// the rare path of being called from the goroutine that watches a deadline,
// while the statement runs on the caller's goroutine, and a handle that is
// closed underneath the watcher is a use-after-free. It is the same defence
// modernc's conn takes for its own interrupt goroutine.
type sqliteRawConn struct {
	tls *libc.TLS
	db  uintptr

	mu     sync.Mutex
	closed bool
}

// openSQLiteRawConn opens one connection and applies every pragma, so a pragma
// the engine rejects fails the open rather than the first lesson.
func openSQLiteRawConn(path string, pragmas []string, readOnly bool) (*sqliteRawConn, error) {
	tls := libc.NewTLS()
	c := &sqliteRawConn{tls: tls}

	zPath, err := libc.CString(path)
	if err != nil {
		tls.Close()
		return nil, fmt.Errorf("sqlite: %v", err)
	}
	defer libc.Xfree(tls, zPath)

	flags := int32(lib.SQLITE_OPEN_URI | lib.SQLITE_OPEN_FULLMUTEX)
	if readOnly {
		flags |= int32(lib.SQLITE_OPEN_READONLY)
	} else {
		flags |= int32(lib.SQLITE_OPEN_READWRITE | lib.SQLITE_OPEN_CREATE)
	}

	// C memory, not a Go stack slot: see the file comment, mechanic 1.
	ppDb := tls.Alloc(sqlitePtrSize)
	defer tls.Free(sqlitePtrSize)
	clear(libc.GoBytes(ppDb, sqlitePtrSize))

	rc := lib.Xsqlite3_open_v2(tls, zPath, ppDb, flags, 0)
	db := loadUintptr(ppDb)
	if rc != int32(lib.SQLITE_OK) {
		msg := fmt.Sprintf("sqlite3_open_v2 code %d", rc)
		if db != 0 {
			// errmsg before close: closing frees the handle the sentence is
			// read from.
			msg = libc.GoString(lib.Xsqlite3_errmsg(tls, db))
			_ = lib.Xsqlite3_close_v2(tls, db)
		}
		tls.Close()
		return nil, fmt.Errorf("sqlite: open %s: %s", path, msg)
	}
	if db == 0 {
		tls.Close()
		return nil, fmt.Errorf("sqlite: open %s: no database handle", path)
	}
	c.db = db

	// Extended result codes so sqlite3_errcode names the exact failure (a
	// constraint rule rather than "constraint failed"), the way modernc's own
	// driver configures its connections.
	lib.Xsqlite3_extended_result_codes(tls, db, 1)

	if err := c.applyPragmas(pragmas); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// applyPragmas runs each pragma as its own statement. busy_timeout goes first —
// the order modernc applies its DSN pragmas in — because the busy handler has to
// exist before a pragma that may have to wait for a lock is executed.
func (c *sqliteRawConn) applyPragmas(pragmas []string) error {
	ordered := make([]string, 0, len(pragmas))
	for _, p := range pragmas {
		if isBusyTimeoutPragma(p) {
			ordered = append(ordered, p)
		}
	}
	for _, p := range pragmas {
		if !isBusyTimeoutPragma(p) {
			ordered = append(ordered, p)
		}
	}
	for _, p := range ordered {
		if strings.TrimSpace(p) == "" {
			continue
		}
		rows, err := c.stepScript(context.Background(), "PRAGMA "+p, nil)
		if rows != nil {
			// The cursor owns the pragma's statement and its interrupt watch;
			// dropping it would leak the statement handle (never finalized) and,
			// for a cancellable context, leave a watcher armed to fire against
			// whatever statement runs next on this connection.
			_ = rows.Close()
		}
		if err != nil {
			return fmt.Errorf("sqlite: apply pragma %s: %w", p, err)
		}
	}
	return nil
}

func isBusyTimeoutPragma(p string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), "busy_timeout")
}

// Close implements driver.Conn. Closing is idempotent, and it is serialised
// against the interrupt goroutine.
func (c *sqliteRawConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var err error
	if c.db != 0 {
		if rc := lib.Xsqlite3_close_v2(c.tls, c.db); rc != int32(lib.SQLITE_OK) {
			err = fmt.Errorf("sqlite: close: %s", libc.GoString(lib.Xsqlite3_errmsg(c.tls, c.db)))
		}
		c.db = 0
	}
	if c.tls != nil {
		c.tls.Close()
		c.tls = nil
	}
	return err
}

// Begin implements driver.Conn. A statement is its own transaction here: the
// executor never issues BEGIN, and engine.Execute's truncation rule relies on
// SQLite's autocommit (a capped read leaves nothing to roll back).
func (c *sqliteRawConn) Begin() (driver.Tx, error) {
	return nil, errors.New("querycraft sqlite: transactions are not supported; a statement is its own transaction")
}

// Prepare implements driver.Conn. The connector answers QueryerContext and
// ExecerContext, so database/sql never prepares through this path; a caller that
// asks for a prepared statement is told so instead of getting a half-working
// one.
func (c *sqliteRawConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("querycraft sqlite: prepared statements are not supported; statements run through QueryContext/ExecContext")
}

// Ping implements driver.Pinger with a real statement, so a health check
// exercises the handle rather than the pool's bookkeeping.
func (c *sqliteRawConn) Ping(ctx context.Context) error {
	rows, err := c.stepScript(ctx, "SELECT 1", nil)
	if rows != nil {
		// Close the cursor rather than dropping it: it owns the statement and
		// the interrupt watch, and a watcher left armed for a context that the
		// caller cancels (the constructor's open deadline, say) would interrupt
		// the *next* statement on this connection.
		_ = rows.Close()
	}
	return err
}

// ResetSession implements driver.SessionResetter: a connection database/sql is
// about to reuse is only usable while its handle is open.
func (c *sqliteRawConn) ResetSession(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.db == 0 {
		return driver.ErrBadConn
	}
	return nil
}

// CheckNamedValue satisfies database/sql as-is: the executor's callers pass no
// arguments, and the values it may pass are the ones bindValue knows.
func (c *sqliteRawConn) CheckNamedValue(*driver.NamedValue) error { return nil }

// interrupt asks SQLite to stop whatever statement is running on this handle.
func (c *sqliteRawConn) interrupt() {
	c.mu.Lock()
	tls, db := c.tls, c.db
	c.mu.Unlock()
	if tls != nil && db != 0 {
		lib.Xsqlite3_interrupt(tls, db)
	}
}

/* ---------- the deadline ---------- */

// sqliteInterruptWatch is the interrupt-on-context-done arrangement: exactly one
// goroutine waits for the context, and Close stops it. Close is idempotent
// because both the cursor and an error path may reach it.
type sqliteInterruptWatch struct {
	stop func()
	once sync.Once
}

func (w *sqliteInterruptWatch) Close() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		if w.stop != nil {
			w.stop()
		}
	})
}

// interruptOnDone arranges for sqlite3_interrupt to be called if ctx is done
// while the statement runs, and returns the watch that stops it.
//
// fired records that the context was seen, and its compare-and-swap is what
// makes the arrangement safe in both directions: a context that fires as the
// query finishes cannot interrupt the *next* statement on this connection
// (a later call's own watch would have to lose the race), and stopping the
// watch cannot leave an interrupt pending for someone else.
func (c *sqliteRawConn) interruptOnDone(ctx context.Context, fired *atomic.Bool) *sqliteInterruptWatch {
	if ctx == nil || ctx.Done() == nil {
		return &sqliteInterruptWatch{stop: func() {}}
	}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if fired.CompareAndSwap(false, true) {
				c.interrupt()
			}
		case <-stopped:
		}
	}()
	return &sqliteInterruptWatch{stop: func() {
		fired.Store(true)
		close(stopped)
	}}
}

/* ---------- statements ---------- */

// stepScript prepares and steps every statement in the string once, then returns
// the cursor that survives: the last statement that produced a row, or an empty
// cursor for the last statement when none did. This is modernc's measured
// multi-statement behaviour (sqlite.go stmt.query: each statement is prepared
// and stepped once, the previous cursor is closed as the next row-producing one
// is found, and what comes back is the last result) — the behaviour a lesson
// sees when someone pastes two statements into the editor, and the reason
// `SELECT 1; INSERT ...` runs the INSERT (and is refused) rather than stopping
// at the semicolon.
func (c *sqliteRawConn) stepScript(ctx context.Context, statement string, args []driver.NamedValue) (*sqliteRawRows, error) {
	if err := c.checkOpen(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// The script itself must be C memory too: sqlite3_prepare_v2 hands back a
	// tail pointer into the buffer it was given, and the loop walks that pointer
	// statement by statement.
	zSQL, err := libc.CString(statement)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %v", err)
	}
	defer libc.Xfree(c.tls, zSQL)

	var fired atomic.Bool
	watch := c.interruptOnDone(ctx, &fired)

	// One C-memory scratch block for the two out-parameters of every prepare in
	// the script: the handle and the tail. Reused rather than reallocated, and
	// never a Go stack slot.
	scratch := c.tls.Alloc(2 * sqlitePtrSize)
	defer c.tls.Free(2 * sqlitePtrSize)

	rows := &sqliteRawRows{conn: c, ctx: ctx, watch: watch, done: true}
	// end is the address of the C string's NUL terminator: the loop walks the
	// script by address and must know where it stops without dereferencing a
	// uintptr, which is the conversion go vet cannot check.
	end := zSQL + uintptr(len(statement))
	psql := zSQL
	for psql < end && !fired.Load() {
		stmt, tail, perr := c.prepare(psql, scratch)
		if perr != nil {
			rows.finalize()
			watch.Close()
			return nil, perr
		}
		if stmt == 0 {
			if tail == psql {
				break
			}
			psql = tail
			continue
		}
		if berr := c.bind(stmt, args); berr != nil {
			_ = lib.Xsqlite3_finalize(c.tls, stmt)
			rows.finalize()
			watch.Close()
			return nil, berr
		}

		rc := lib.Xsqlite3_step(c.tls, stmt)
		switch rc & 0xff {
		case int32(lib.SQLITE_ROW):
			// The previous result is replaced, exactly as modernc replaces it;
			// its statement is finalized, and the shared watch stays with the
			// surviving cursor.
			rows.finalize()
			rows = newSQLiteRawRows(c, ctx, stmt, watch, true)
			stmt = 0
		case int32(lib.SQLITE_DONE):
			if rows.ncols == 0 && rows.stmt == 0 {
				rows = newSQLiteRawRows(c, ctx, stmt, watch, false)
				stmt = 0
			}
		default:
			// Read the engine's sentence *before* finalizing: sqlite3_finalize
			// clears the handle's error state, so calling it first would make
			// sqlite3_errmsg answer "not an error" for a real constraint
			// violation (measured with an INSERT that breaks a UNIQUE index).
			stepErr := c.stepError(ctx, rc, fired.Load())
			_ = lib.Xsqlite3_finalize(c.tls, stmt)
			rows.finalize()
			watch.Close()
			return nil, stepErr
		}
		if stmt != 0 {
			_ = lib.Xsqlite3_finalize(c.tls, stmt)
		}
		if tail == psql {
			break
		}
		psql = tail
	}
	return rows, nil
}

// QueryContext implements driver.QueryerContext.
func (c *sqliteRawConn) QueryContext(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.stepScript(ctx, statement, args)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ExecContext implements driver.ExecerContext: the statements run and the
// cursor is discarded, with the engine's own counts reported.
func (c *sqliteRawConn) ExecContext(ctx context.Context, statement string, args []driver.NamedValue) (driver.Result, error) {
	rows, err := c.stepScript(ctx, statement, args)
	if err != nil {
		return nil, err
	}
	_ = rows.Close()
	return sqliteRawResult{
		changes:      int64(lib.Xsqlite3_changes(c.tls, c.db)),
		lastInsertID: lib.Xsqlite3_last_insert_rowid(c.tls, c.db),
	}, nil
}

// sqliteRawResult is ExecerContext's answer: SQLite's own change count and last
// insert rowid.
type sqliteRawResult struct {
	changes      int64
	lastInsertID int64
}

func (r sqliteRawResult) LastInsertId() (int64, error) { return r.lastInsertID, nil }
func (r sqliteRawResult) RowsAffected() (int64, error) { return r.changes, nil }

// prepare calls sqlite3_prepare_v2 for the statement at psql and returns the
// handle together with the tail that follows it. Both out-parameters come from
// scratch, which the caller owns.
func (c *sqliteRawConn) prepare(psql, scratch uintptr) (stmt, tail uintptr, err error) {
	ppStmt, ppTail := scratch, scratch+uintptr(sqlitePtrSize)
	clear(libc.GoBytes(scratch, 2*sqlitePtrSize))

	rc := lib.Xsqlite3_prepare_v2(c.tls, c.db, psql, -1, ppStmt, ppTail)
	if rc != int32(lib.SQLITE_OK) {
		return 0, 0, c.engineError("prepare")
	}
	stmt = loadUintptr(ppStmt)
	tail = loadUintptr(ppTail)
	if tail == 0 {
		// SQLite always fills the tail on SQLITE_OK; treating a missing one as
		// "no progress" ends the loop instead of spinning on the same statement.
		tail = psql
	}
	return stmt, tail, nil
}

// bind binds the caller's arguments. The executor's own callers pass none — a
// learner's statement is text — so the supported set is exactly what
// database/sql can hand a driver (its driver.Value types), and anything else is
// refused by name rather than bound as something the learner did not write.
func (c *sqliteRawConn) bind(stmt uintptr, args []driver.NamedValue) error {
	if len(args) == 0 {
		return nil
	}
	count := int(lib.Xsqlite3_bind_parameter_count(c.tls, stmt))
	if count != len(args) {
		return fmt.Errorf("sqlite: statement has %d parameter(s) but %d value(s) were supplied", count, len(args))
	}
	for i, arg := range args {
		index := int32(i + 1)
		var rc int32
		switch v := arg.Value.(type) {
		case nil:
			rc = lib.Xsqlite3_bind_null(c.tls, stmt, index)
		case int64:
			rc = lib.Xsqlite3_bind_int64(c.tls, stmt, index, v)
		case float64:
			rc = lib.Xsqlite3_bind_double(c.tls, stmt, index, v)
		case bool:
			n := int64(0)
			if v {
				n = 1
			}
			rc = lib.Xsqlite3_bind_int64(c.tls, stmt, index, n)
		case string:
			rc = c.bindText(stmt, index, v)
		case []byte:
			rc = c.bindBlob(stmt, index, v)
		default:
			// database/sql checks the value first (CheckNamedValue), so this is
			// the belt to that brace: the type is named rather than coerced.
			return fmt.Errorf("sqlite: cannot bind a %T as a statement parameter", arg.Value)
		}
		if rc != int32(lib.SQLITE_OK) {
			return c.engineError("bind")
		}
	}
	return nil
}

// bindText binds a string with SQLITE_TRANSIENT, so SQLite copies it and the C
// buffer can be freed as soon as the bind returns.
func (c *sqliteRawConn) bindText(stmt uintptr, index int32, s string) int32 {
	z, err := libc.CString(s)
	if err != nil {
		return int32(lib.SQLITE_NOMEM)
	}
	defer libc.Xfree(c.tls, z)
	return lib.Xsqlite3_bind_text(c.tls, stmt, index, z, int32(len(s)), lib.SQLITE_TRANSIENT)
}

// bindBlob is bindText for bytes.
func (c *sqliteRawConn) bindBlob(stmt uintptr, index int32, b []byte) int32 {
	if len(b) == 0 {
		return lib.Xsqlite3_bind_blob(c.tls, stmt, index, 0, 0, lib.SQLITE_TRANSIENT)
	}
	p := c.tls.Alloc(len(b))
	defer c.tls.Free(len(b))
	// libc.GoBytes views the freshly allocated C buffer, so this copy lands in
	// the memory the engine reads, while SQLITE_TRANSIENT below has SQLite copy
	// it again and lets this buffer be freed as soon as the bind returns.
	copy(libc.GoBytes(p, len(b)), b)
	return lib.Xsqlite3_bind_blob(c.tls, stmt, index, p, int32(len(b)), lib.SQLITE_TRANSIENT)
}

// checkOpen refuses to run a statement on a handle that is going away.
func (c *sqliteRawConn) checkOpen() error {
	c.mu.Lock()
	closed := c.closed || c.db == 0
	c.mu.Unlock()
	if closed {
		return driver.ErrBadConn
	}
	return nil
}

/* ---------- errors ---------- */

// engineError reads SQLite's own sentence and result code off the connection.
//
// There is no framing to remove: sqlite3_errmsg is the text the dialect
// profile's rules were written against, which is why the learner reads "no such
// table: customers" and not a driver's rendering of it. The code is what
// sqlite3_errcode reports with extended result codes enabled.
func (c *sqliteRawConn) engineError(what string) error {
	msg := libc.GoString(lib.Xsqlite3_errmsg(c.tls, c.db))
	if strings.TrimSpace(msg) == "" {
		msg = what + " failed"
	}
	return &sqliteError{sentence: msg, code: int(lib.Xsqlite3_extended_errcode(c.tls, c.db))}
}

// stepError is engineError for a failed step, with one exception: when this
// process's own deadline landed on the statement, the returned error is the
// context's, because that is the identity engine.Execute (execute.go,
// classifyFor) turns into QUERY_TIMEOUT and it must survive both doors — the
// learner's result and a raw Query under the same context.
func (c *sqliteRawConn) stepError(ctx context.Context, rc int32, fired bool) error {
	if fired {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return c.engineError("step")
}

/* ---------- the cursor ---------- */

// sqliteRawRows is a forward-only cursor over one prepared statement.
//
// pending is the first row: stepScript steps each statement once to learn
// whether it produces rows, so the row that step returned is already waiting
// when the cursor is created.
type sqliteRawRows struct {
	conn  *sqliteRawConn
	ctx   context.Context
	watch *sqliteInterruptWatch
	stmt  uintptr

	names []string
	decls []string
	ncols int

	pending bool
	done    bool
	err     error
}

func newSQLiteRawRows(c *sqliteRawConn, ctx context.Context, stmt uintptr, watch *sqliteInterruptWatch, pending bool) *sqliteRawRows {
	r := &sqliteRawRows{conn: c, ctx: ctx, watch: watch, stmt: stmt, pending: pending}
	r.ncols = int(lib.Xsqlite3_column_count(c.tls, stmt))
	if r.ncols > 0 {
		r.names = make([]string, r.ncols)
		r.decls = make([]string, r.ncols)
		for i := 0; i < r.ncols; i++ {
			r.names[i] = libc.GoString(lib.Xsqlite3_column_name(c.tls, stmt, int32(i)))
			// decltype is NULL for an expression (COUNT(*), price * 2), and the
			// profile's column_type_label rule turns that into "expression".
			if p := lib.Xsqlite3_column_decltype(c.tls, stmt, int32(i)); p != 0 {
				r.decls[i] = libc.GoString(p)
			}
		}
	}
	r.done = !pending
	return r
}

// Columns implements driver.Rows.
func (r *sqliteRawRows) Columns() []string { return r.names }

// ColumnTypeDatabaseTypeName implements driver.RowsColumnTypeDatabaseTypeName,
// which is what database/sql's ColumnTypes() reads. The declared type reaches
// the profile verbatim — the engine's own text, the same string the browser
// adapter reports — while the *values* below never consult it.
func (r *sqliteRawRows) ColumnTypeDatabaseTypeName(i int) string {
	if i < 0 || i >= len(r.decls) {
		return ""
	}
	return r.decls[i]
}

// Next implements driver.Rows. A context that has already fired ends the
// iteration with the context's error: the statement is stopped at a row
// boundary and Close finalizes (rolls back) it, which is the cheap path
// engine.Execute's cap-and-cancel rule depends on.
func (r *sqliteRawRows) Next(dest []driver.Value) error {
	if r.err != nil {
		return r.err
	}
	if r.done {
		return io.EOF
	}
	if err := r.ctx.Err(); err != nil {
		r.done = true
		r.err = err
		return err
	}
	if r.pending {
		r.pending = false
		return r.values(dest)
	}
	rc := lib.Xsqlite3_step(r.conn.tls, r.stmt)
	switch rc & 0xff {
	case int32(lib.SQLITE_ROW):
		return r.values(dest)
	case int32(lib.SQLITE_DONE):
		r.done = true
		return io.EOF
	default:
		r.done = true
		r.err = r.conn.engineError("step")
		if firedByContext(rc, r.ctx) {
			r.err = r.ctx.Err()
		}
		return r.err
	}
}

// firedByContext reports whether a failed step was stopped by this process's
// deadline rather than by the engine.
func firedByContext(rc int32, ctx context.Context) bool {
	return rc&0xff == int32(lib.SQLITE_INTERRUPT) && ctx.Err() != nil
}

// values fills dst from the current row, one column at a time, by storage class.
//
// This is the whole point of the file: the class decides the accessor, and the
// accessor's result is what the profile encodes. TEXT stays the stored text (a
// DATE-declared column is TEXT), BLOB stays bytes, INTEGER/REAL stay numbers and
// NULL stays nil — no declared type is read, no string is parsed, and no
// time.Time can appear.
func (r *sqliteRawRows) values(dst []driver.Value) error {
	tls := r.conn.tls
	for i := 0; i < r.ncols && i < len(dst); i++ {
		index := int32(i)
		switch lib.Xsqlite3_column_type(tls, r.stmt, index) {
		case int32(lib.SQLITE_INTEGER):
			dst[i] = lib.Xsqlite3_column_int64(tls, r.stmt, index)
		case int32(lib.SQLITE_FLOAT):
			dst[i] = lib.Xsqlite3_column_double(tls, r.stmt, index)
		case int32(lib.SQLITE_TEXT):
			dst[i] = r.columnText(index)
		case int32(lib.SQLITE_BLOB):
			dst[i] = r.columnBlob(index)
		default: // SQLITE_NULL
			dst[i] = nil
		}
	}
	return nil
}

// columnText copies a TEXT value by its byte length rather than through
// libc.GoString, because a TEXT value may contain a NUL: the length comes from
// sqlite3_column_bytes *after* the text accessor, which is the order SQLite
// documents (the accessor fixes the representation the length then measures).
func (r *sqliteRawRows) columnText(i int32) string {
	tls := r.conn.tls
	p := lib.Xsqlite3_column_text(tls, r.stmt, i)
	n := int(lib.Xsqlite3_column_bytes(tls, r.stmt, i))
	if p == 0 || n == 0 {
		return ""
	}
	return string(libc.GoBytes(p, n))
}

// columnBlob is columnText for bytes. An empty BLOB is an empty slice rather
// than nil: NULL is a storage class of its own and reporting an empty blob as
// null would lose the class the dialect is teaching.
func (r *sqliteRawRows) columnBlob(i int32) []byte {
	tls := r.conn.tls
	p := lib.Xsqlite3_column_blob(tls, r.stmt, i)
	n := int(lib.Xsqlite3_column_bytes(tls, r.stmt, i))
	if p == 0 || n == 0 {
		return []byte{}
	}
	return libc.GoBytes(p, n)
}

// Close implements driver.Rows: the statement is finalized and the interrupt
// watch is stopped. A statement read to the end was already released by SQLite,
// so this is a no-op then; a statement abandoned early (the row cap, the byte
// cap, a deadline) is rolled back here, which is what hands the connection back
// immediately instead of at the deadline.
func (r *sqliteRawRows) Close() error {
	r.finalize()
	r.watch.Close()
	r.watch = nil
	return nil
}

// finalize releases the statement without stopping the watch: a cursor replaced
// by a later statement in the same script is discarded while the surviving
// cursor still owns the shared interrupt.
func (r *sqliteRawRows) finalize() {
	if r.stmt != 0 {
		_ = lib.Xsqlite3_finalize(r.conn.tls, r.stmt)
		r.stmt = 0
	}
	r.done = true
}
