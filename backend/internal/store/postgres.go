package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the server-mode metadata store. It is a separate database from the
// learner execution connection. The account, progress and session methods live
// in sqlStore, shared with the SQLite backend.
type Postgres struct {
	pool *pgxpool.Pool
	sqlStore
}

// AppSchema is the schema that holds gateway metadata (accounts, progress,
// events). It is deliberately distinct from the learner-visible `dataset`
// schema so a restricted execution role can be granted SELECT on `dataset`
// alone. See docs/database-layout.md.
const AppSchema = "app"

// migrateSQL is the metadata schema in the PostgreSQL dialect. It renders from
// appTables, the one description both backends share.
var migrateSQL = postgresDDL(appTables)

// ErrStaleSchema is returned when the metadata database predates the
// owner-scoped layout. There is no upgrade path by design (see migrateSQL), so
// the caller gets an actionable error instead of a store that fails later with
// a confusing "column does not exist".
var ErrStaleSchema = errors.New("metadata schema is from the pre-accounts layout")

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
	// CREATE TABLE IF NOT EXISTS is a no-op against a table that already exists
	// with the wrong columns, which would leave every scoped query failing at
	// runtime. Detect that shape up front: a cluster created before accounts
	// existed has app.progress keyed by course alone.
	stale, err := hasLegacyMetadata(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if stale {
		pool.Close()
		return nil, ErrStaleSchema
	}
	if _, err := pool.Exec(ctx, migrateSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply metadata schema: %w", err)
	}
	return &Postgres{pool: pool, sqlStore: postgresStore(pool)}, nil
}

// hasLegacyMetadata reports whether app.progress exists without the user_id
// column that owner scoping depends on.
func hasLegacyMetadata(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var exists, scoped bool
	err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM information_schema.tables
			         WHERE table_schema = $1 AND table_name = 'progress'),
			EXISTS (SELECT 1 FROM information_schema.columns
			         WHERE table_schema = $1 AND table_name = 'progress' AND column_name = 'user_id')`,
		AppSchema).Scan(&exists, &scoped)
	if err != nil {
		return false, fmt.Errorf("inspect metadata schema: %w", err)
	}
	return exists && !scoped, nil
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
