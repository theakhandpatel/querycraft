// PgxExecutor: the PostgreSQL engine behind the Executor seam (engine.go), built
// on pgx and pgxpool. It runs each learner statement in a transaction with
// session timeouts, read-only for the shared dataset and writable for a copied
// environment. execute.go supplies the caps; normalize.go decides how a value is
// encoded.
package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxExecutor is the real Adapter B engine: pgx against native PostgreSQL.
//
// A learner statement runs inside a transaction that is always ended by the
// driver, with per-session statement, lock, temp-file and idle timeouts. The
// statement_timeout is derived from the context deadline set by the gateway, so
// a runaway query is stopped server-side as well as client-side.
//
// The transaction mode is per-executor. The shared dataset is read-only: a
// SELECT is the only thing the learner role may run against it, and the
// read-only access mode is a second, server-side lock on the same door. A
// challenge environment is writable, because a course that only ever runs
// SELECT cannot teach INSERT, UPDATE, DELETE, DDL or transactions. The
// administrative connection is never used here in either mode.
type PgxExecutor struct {
	pool     *pgxpool.Pool
	tm       *pgtype.Map
	writable bool
}

// PgxExecutor answers the whole Executor interface, capability methods
// included: the assertions for the adapters live beside their types.
var _ Executor = (*PgxExecutor)(nil)

// PgxConfig tunes the executor's pool and transaction mode.
type PgxConfig struct {
	// MaxConns is the pool's hard ceiling. Zero keeps the DSN's pool_max_conns,
	// floored at 4 (the read-only default). A per-environment executor passes a
	// smaller number, because one challenge needs a connection or two.
	MaxConns int32
	// MinConns is how many connections stay warm. Zero means 1.
	MinConns int32
	// Writable selects a writing transaction. False keeps the read-only default.
	Writable bool
}

// minimumPoolConns and minimumPoolMinConns are the floors NewPgxExecutor has
// always applied: at least four connections, one kept warm.
const (
	minimumPoolConns    = 4
	minimumPoolMinConns = 1
)

// NewPgxExecutor opens a bounded connection pool for read-only learner
// statements against the shared dataset.
func NewPgxExecutor(ctx context.Context, dsn string) (*PgxExecutor, error) {
	return NewPgxExecutorWithConfig(ctx, dsn, PgxConfig{})
}

// NewPgxExecutorWithConfig opens a pool with an explicit size and transaction
// mode. A zero MaxConns keeps today's floor of four; a zero MinConns keeps one
// warm connection.
func NewPgxExecutorWithConfig(ctx context.Context, dsn string, config PgxConfig) (*PgxExecutor, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	maxConns := config.MaxConns
	if maxConns <= 0 {
		maxConns = cfg.MaxConns
		if maxConns < minimumPoolConns {
			maxConns = minimumPoolConns
		}
	}
	if maxConns < 1 {
		maxConns = 1
	}
	minConns := config.MinConns
	if minConns <= 0 {
		minConns = minimumPoolMinConns
	}
	if minConns > maxConns {
		minConns = maxConns
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = minConns
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	return &PgxExecutor{pool: pool, tm: pgtype.NewMap(), writable: config.Writable}, nil
}

// Query implements Executor.
func (e *PgxExecutor) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	// A read-only transaction is a server-side guarantee, not a convention: it
	// refuses an INSERT before the statement runs, even if a grant somewhere
	// widened. The default stays read-only; only an explicit writable executor
	// asks for the other mode.
	txOpts := pgx.TxOptions{AccessMode: pgx.ReadOnly}
	if e.writable {
		txOpts = pgx.TxOptions{}
	}
	tx, err := e.pool.BeginTx(ctx, txOpts)
	if err != nil {
		return nil, connectErr(err)
	}
	for _, stmt := range e.sessionSettings(ctx) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			if isContextErr(err) {
				// The deadline passed during setup: the request ran out of time,
				// which classify already knows how to describe.
				return nil, err
			}
			return nil, fmt.Errorf("%w: %w", ErrEnvironmentUnavailable, err)
		}
	}
	// pgResultFormats asks the server for the text form of the value types in
	// textOIDs (see normalize.go). pgx resolves a QueryResultFormatsByOID by
	// position and a missing OID falls back to the zero value, which is
	// TextFormatCode — so this also puts every other column in text form. That
	// is deliberate and welcome here: text is what psql would show the learner,
	// and it removes the "binary codec vs browser parser" mismatch for the
	// whole result instead of patching it type by type. The result set is
	// capped at a few hundred rows, so the larger wire form is not a cost.
	queryArgs := make([]any, 0, len(args)+1)
	queryArgs = append(queryArgs, pgResultFormats)
	queryArgs = append(queryArgs, args...)
	rows, err := tx.Query(ctx, sql, queryArgs...)
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return &pgxRows{rows: rows, tx: tx, m: e.tm, ctx: ctx, commit: e.writable}, nil
}

// pgResultFormats is the result-format request shared by every learner query.
// Only the OIDs that must arrive as text are listed; pgx's zero value makes the
// rest text as well (see the comment in Query).
var pgResultFormats = func() pgx.QueryResultFormatsByOID {
	out := make(pgx.QueryResultFormatsByOID, len(textOIDs))
	for oid := range textOIDs {
		out[oid] = pgx.TextFormatCode
	}
	return out
}()

// isContextErr reports whether the request itself was cancelled or timed out.
func isContextErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// connectErr separates "the pool was busy until the deadline" from "the database
// could not be reached". Both arrive here as a non-nil error from BeginTx, and
// only the second is a fault.
func connectErr(err error) error {
	if isContextErr(err) {
		return fmt.Errorf("%w: %w", errPoolTimeout, err)
	}
	return fmt.Errorf("%w: %w", ErrEnvironmentUnavailable, err)
}

func (e *PgxExecutor) sessionSettings(ctx context.Context) []string {
	settings := []string{
		// Learner SQL sees the commerce tables unqualified (FROM orders) while
		// gateway metadata in `app` stays off the search path, so `progress`
		// and `events` are not reachable from a learner statement. `public` is
		// omitted on purpose: it is the default creation schema, and leaving it
		// on the path would let a stray object there shadow or extend the
		// dataset without an explicit grant.
		"SET LOCAL search_path = dataset",
		"SET LOCAL lock_timeout = '2s'",
		"SET LOCAL idle_in_transaction_session_timeout = '10s'",
		// temp_file_limit is deliberately absent: it is superuser-only, so a
		// restricted learner role cannot set it and would fail every query.
		// It is applied as a role default when the learner role is created.
		"SET LOCAL work_mem = '16MB'",
	}
	if dl, ok := ctx.Deadline(); ok {
		ms := time.Until(dl).Milliseconds()
		if ms < 1 {
			ms = 1
		}
		// statement_timeout is int4 milliseconds; a larger value (from a
		// misconfigured limit) would be rejected by the server and wrongly
		// surfaced as ENVIRONMENT_UNAVAILABLE.
		if ms > math.MaxInt32 {
			ms = math.MaxInt32
		}
		settings = append(settings, fmt.Sprintf("SET LOCAL statement_timeout = %d", ms))
	}
	return settings
}

// readyProbeTimeout bounds the health probe behind Ready and /diagnostics. It
// is deliberately shorter than the query deadline: a diagnostics request that
// hangs for the full 5 s is itself a bug.
const readyProbeTimeout = 2 * time.Second

// Ping implements Executor.
func (e *PgxExecutor) Ping(ctx context.Context) error { return e.pool.Ping(ctx) }

// Stats implements Executor.
func (e *PgxExecutor) Stats() PoolStats {
	st := e.pool.Stat()
	return PoolStats{
		MaxConns:          int(st.MaxConns()),
		TotalConns:        int(st.TotalConns()),
		IdleConns:         int(st.IdleConns()),
		AcquiredConns:     int(st.AcquiredConns()),
		ConstructingConns: int(st.ConstructingConns()),
	}
}

// Ready implements Executor. A pool tracks connections, not liveness, so a
// pool whose server disappeared still reports idle connections; the only
// honest answer comes from asking the server.
func (e *PgxExecutor) Ready(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, readyProbeTimeout)
	defer cancel()
	if err := e.pool.Ping(ctx); err != nil {
		return false, RedactSecrets(err.Error())
	}
	return true, ""
}

// Diagnostics implements Executor. Every field is read from the server
// over the learner's own connection, so it reports what a learner statement
// actually sees and holds no privilege the learner does not.
func (e *PgxExecutor) Diagnostics(ctx context.Context) map[string]any {
	out := map[string]any{}
	var user, searchPath, database string
	if err := e.pool.QueryRow(ctx,
		"SELECT current_user, current_setting('search_path'), current_database()",
	).Scan(&user, &searchPath, &database); err == nil {
		out["current_user"] = user
		out["search_path"] = searchPath
		out["database"] = database
	} else {
		out["error"] = RedactSecrets(err.Error())
	}
	// has_schema_privilege answers with the caller's own privileges, so a
	// learner connection can run this check without reading the catalog. If
	// this ever reports true, learner SQL can reach gateway metadata.
	var canReadApp bool
	if err := e.pool.QueryRow(ctx, "SELECT has_schema_privilege($1, 'USAGE')", "app").Scan(&canReadApp); err == nil {
		out["can_read_app"] = canReadApp
	} else {
		out["can_read_app"] = "unknown"
	}
	return out
}

// Version implements Executor.
func (e *PgxExecutor) Version(ctx context.Context) (string, error) {
	var v string
	if err := e.pool.QueryRow(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "", err
	}
	if v == "" {
		return "", nil
	}
	return "PostgreSQL " + v, nil
}

// Close implements Executor.
func (e *PgxExecutor) Close() error {
	e.pool.Close()
	return nil
}

// Dialect implements Executor. This executor runs the default dialect.
func (e *PgxExecutor) Dialect() string { return DialectPostgres }

// EngineLabel implements Executor. The label names the default engine, which is
// what QueryResult.engine carries for a statement this executor answered.
func (e *PgxExecutor) EngineLabel() string { return EngineName }

// ClassifyError implements Executor with PostgreSQL's SQLSTATE rules.
//
// It answers rather than declining, and it answers with the very table
// Execute's fallback uses: two copies of the same rules would eventually
// disagree, and the learner would see one dialect's mistake described two ways
// depending on which path reached the classifier.
func (e *PgxExecutor) ClassifyError(err error) (string, bool) { return pgCodeFor(err) }

type pgxRows struct {
	rows pgx.Rows
	tx   pgx.Tx
	m    *pgtype.Map

	// ctx is the request context the query ran under. Close consults it to tell
	// "the result was fully read" from "the result was stopped": execute.go
	// cancels this same context the moment a row or byte cap is hit.
	ctx context.Context
	// commit is true for a writable executor and false for the read-only one.
	commit bool
	// aborted records that iterating the result reported an error, so a
	// half-consumed result is never committed even if the driver's own error
	// is empty.
	aborted bool

	cols   []Column
	loaded bool

	// oidList is the column type OIDs, cached the first time they are needed.
	// Deciding how to encode a value needs the OID, not just the decoded value.
	oidList []uint32
}

func (r *pgxRows) Columns() []Column {
	if !r.loaded {
		fds := r.rows.FieldDescriptions()
		cols := make([]Column, len(fds))
		for i, fd := range fds {
			cols[i] = Column{Name: fd.Name, Type: typeName(r.m, fd.DataTypeOID)}
		}
		r.cols = cols
		r.loaded = true
	}
	return r.cols
}

func (r *pgxRows) Next() bool { return r.rows.Next() }

// oids returns the result column type OIDs, one per column.
func (r *pgxRows) oids() []uint32 {
	if r.oidList == nil {
		fds := r.rows.FieldDescriptions()
		list := make([]uint32, len(fds))
		for i, fd := range fds {
			list[i] = fd.DataTypeOID
		}
		r.oidList = list
	}
	return r.oidList
}

func (r *pgxRows) Values() ([]any, error) {
	vals, err := r.rows.Values()
	if err != nil {
		// A value-level failure is an iteration failure even when the driver
		// does not surface it through Err(); a writable transaction must not
		// commit after one.
		r.aborted = true
		return nil, err
	}
	// raw is the value as the server sent it, which Query asked to be text for
	// the types whose contract value is text. Two columns are encoded from it:
	// `numeric` and the date/time family, where a decoded Go value cannot carry
	// the scale or the wall-clock time the engine printed (normalize.go).
	raw := r.rows.RawValues()
	oids := r.oids()
	out := make([]any, len(vals))
	for i, v := range vals {
		var oid uint32
		if i < len(oids) {
			oid = oids[i]
		}
		var rawValue []byte
		if i < len(raw) {
			rawValue = raw[i]
		}
		out[i] = normalizeColumn(oid, rawValue, v)
	}
	return out, nil
}

func (r *pgxRows) Err() error { return r.rows.Err() }

func (r *pgxRows) Close() {
	r.rows.Close()
	if r.tx == nil {
		return
	}
	// The transaction is ended here, once, for both modes.
	//
	// Writable commits only when the statement ran to completion: the result was
	// drained, no iteration error was reported, and the request context is still
	// live. Everything else rolls back. In particular a truncated result rolls
	// back, because execute.go cancels the request context the moment a row or
	// byte cap is hit, and a cancelled statement is not a finished one: the
	// learner was told the result exceeded the limit, so persisting the writes
	// it managed to perform before the cap would be a change they were never
	// shown. Rollback is the only outcome the response can honestly describe.
	//
	// WithoutCancel detaches the commit/rollback from the request's own
	// cancellation; the transaction has already been decided by this point, and
	// a rollback issued on a cancelled context would fail and leave the
	// connection dirty.
	if r.commit && !r.aborted && r.rows.Err() == nil && r.ctx.Err() == nil {
		_ = r.tx.Commit(context.WithoutCancel(r.ctx))
		return
	}
	_ = r.tx.Rollback(context.WithoutCancel(r.ctx))
}

// typeName resolves a PostgreSQL type OID to the name pgx knows it by, so a
// column reports "int4" rather than a bare number. An OID pgx cannot name is
// reported as "oid:<n>" so the label still identifies the type.
func typeName(m *pgtype.Map, oid uint32) string {
	if t, ok := m.TypeForOID(oid); ok && t != nil && t.Name != "" {
		return t.Name
	}
	return fmt.Sprintf("oid:%d", oid)
}
