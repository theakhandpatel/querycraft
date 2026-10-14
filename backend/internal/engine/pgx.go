package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxExecutor is the real Adapter B engine: pgx against native PostgreSQL.
//
// Learner statements run inside a read-only transaction that is always rolled
// back, with per-session statement, lock, temp-file and idle timeouts. The
// statement_timeout is derived from the context deadline set by the gateway, so
// a runaway query is stopped server-side as well as client-side. The
// administrative connection is never used here.
type PgxExecutor struct {
	pool *pgxpool.Pool
	tm   *pgtype.Map
}

// NewPgxExecutor opens a bounded connection pool for learner statements.
func NewPgxExecutor(ctx context.Context, dsn string) (*PgxExecutor, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	if cfg.MaxConns < 4 {
		cfg.MaxConns = 4
	}
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	return &PgxExecutor{pool: pool, tm: pgtype.NewMap()}, nil
}

// Query implements Executor.
func (e *PgxExecutor) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	tx, err := e.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
	}
	for _, stmt := range e.sessionSettings(ctx) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("%w: %v", ErrEnvironmentUnavailable, err)
		}
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return &pgxRows{rows: rows, tx: tx, m: e.tm}, nil
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

// Ping implements Executor.
func (e *PgxExecutor) Ping(ctx context.Context) error { return e.pool.Ping(ctx) }

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

type pgxRows struct {
	rows   pgx.Rows
	tx     pgx.Tx
	m      *pgtype.Map
	cols   []Column
	loaded bool
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

func (r *pgxRows) Values() ([]any, error) {
	vals, err := r.rows.Values()
	if err != nil {
		return nil, err
	}
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = normalizeValue(v)
	}
	return out, nil
}

func (r *pgxRows) Err() error { return r.rows.Err() }

func (r *pgxRows) Close() {
	r.rows.Close()
	if r.tx != nil {
		_ = r.tx.Rollback(context.Background())
	}
}

func typeName(m *pgtype.Map, oid uint32) string {
	if t, ok := m.TypeForOID(oid); ok && t != nil && t.Name != "" {
		return t.Name
	}
	return fmt.Sprintf("oid:%d", oid)
}
