package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* Postgres is the server-mode metadata backend. It keeps accounts, progress and
 * audit events in their own PostgreSQL database, separate from the learner
 * execution connection. The statements live in metadata.go and the DDL comes
 * from tables.go; this file owns connecting, the app schema name, and
 * duplicate-key detection.
 */

// Postgres is the server-mode metadata store. It is a separate database from the
// learner execution connection. The account, progress and session methods live
// in sqlStore, shared with the SQLite backend.
type Postgres struct {
	pool *pgxpool.Pool
	sqlStore
}

// appSchema is the schema that holds gateway metadata (accounts, progress,
// events). It is deliberately distinct from the learner-visible `dataset`
// schema so a restricted execution role can be granted SELECT on `dataset`
// alone. See docs/database-layout.md.
const appSchema = "app"

// migrateSQL is the metadata schema in the PostgreSQL dialect. It renders from
// appTables, the one description both backends share.
var migrateSQL = postgresDDL(appTables)

// NewPostgres connects, verifies the connection and applies the metadata schema.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, migrateSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply metadata schema: %w", err)
	}
	// CREATE TABLE IF NOT EXISTS cannot widen a table that exists; the additive
	// migrations do. Without this, a store created before a nullable column was
	// declared fails every query that reads it.
	if _, err := pool.Exec(ctx, postgresMigrations(appTables)); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply metadata migrations: %w", err)
	}
	return &Postgres{pool: pool, sqlStore: postgresStore(pool)}, nil
}

// Kind implements Store.
func (p *Postgres) Kind() string { return "postgres" }

// Close implements Store.
func (p *Postgres) Close() error {
	if p.pool != nil {
		p.pool.Close()
	}
	return nil
}

var _ Store = (*Postgres)(nil)

// isUniqueViolation reports PostgreSQL's duplicate-key error, which the shared
// CreateUser maps to ErrEmailTaken.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
