package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

/* SQLite is one of the two SQL metadata backends. It stores accounts, progress
 * and audit events in one local file, or in memory. The statements live in
 * metadata.go and the DDL comes from tables.go; this file owns opening and
 * migrating the file, plus the SQLite-only helpers: the timestamp text format,
 * JSON compaction, and duplicate-key detection.
 */

// SQLite stores app metadata in one local file. Learner SQL continues to run
// through the separately configured execution engine. The account, progress and
// session methods live in sqlStore, shared with the Postgres backend.
type SQLite struct {
	db *sql.DB
	sqlStore
}

// sqliteMemoryID names each in-memory database. The DSN uses shared cache, so
// two Memory stores in one process must not collide on the same name.
var sqliteMemoryID atomic.Uint64

// sqliteSchema is the metadata schema in the SQLite dialect. It renders from
// appTables, the one description both backends share.
var sqliteSchema = sqliteDDL(appTables)

// NewSQLite opens or creates path and applies the metadata schema. The path's
// parent is created with private permissions; SQLite itself is pure Go through
// modernc.org/sqlite, so the gateway remains cgo-free.
func NewSQLite(ctx context.Context, path string) (*SQLite, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("metadata sqlite path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create metadata directory: %w", err)
		}
	}
	dsn := path
	if path == ":memory:" {
		query := url.Values{}
		query.Set("mode", "memory")
		query.Set("cache", "shared")
		query.Add("_pragma", "foreign_keys(1)")
		query.Add("_pragma", "busy_timeout(5000)")
		dsn = fmt.Sprintf("file:querycraft-metadata-%d?%s", sqliteMemoryID.Add(1), query.Encode())
	} else {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve metadata sqlite path: %w", err)
		}
		query := url.Values{}
		query.Add("_pragma", "foreign_keys(1)")
		query.Add("_pragma", "busy_timeout(5000)")
		dsn = (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs), OmitHost: true, RawQuery: query.Encode()}).String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open metadata sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping metadata sqlite: %w", err)
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("secure metadata sqlite file: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create metadata schema: %w", err)
	}
	if err := migrateAdditiveColumns(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply metadata migrations: %w", err)
	}
	return &SQLite{db: db, sqlStore: sqliteStore(db)}, nil
}

// Kind implements Store.
func (s *SQLite) Kind() string { return "sqlite" }

// migrateAdditiveColumns adds declared-but-missing columns to tables that
// already exist. SQLite has no ADD COLUMN IF NOT EXISTS, so the columns each
// table actually has are read from table_info and compared with the
// declaration; the additive set (see additiveColumns) is appended. A table
// name here comes from this file's own declarations, never from input, so the
// interpolated PRAGMA is safe.
func migrateAdditiveColumns(ctx context.Context, db *sql.DB) error {
	for _, t := range appTables {
		existing := map[string]bool{}
		rows, err := db.QueryContext(ctx, "PRAGMA table_info("+t.name+")")
		if err != nil {
			return fmt.Errorf("inspect %s: %w", t.name, err)
		}
		for rows.Next() {
			var (
				cid     int
				name    string
				kind    string
				notNull int
				dflt    *string
				pk      int
			)
			if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				return fmt.Errorf("read %s columns: %w", t.name, err)
			}
			existing[name] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("read %s columns: %w", t.name, err)
		}
		rows.Close()

		// A table that does not exist yet has no columns; CREATE TABLE IF
		// NOT EXISTS, run just before, has handled it.
		if len(existing) == 0 {
			continue
		}
		for _, c := range additiveColumns(t) {
			if existing[c.name] {
				continue
			}
			stmt := "ALTER TABLE " + t.name + " ADD COLUMN " + c.name + " " + c.kind.sqliteType()
			if c.notNull {
				stmt += " NOT NULL"
			}
			if d := c.sqliteDefault(); d != "" {
				stmt += " DEFAULT " + d
			}
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("add %s.%s: %w", t.name, c.name, err)
			}
		}
	}
	return nil
}

// Close implements Store.
func (s *SQLite) Close() error { return s.db.Close() }

// timestamp writes the canonical UTC text both dialects store: fixed-width
// microseconds and a Z suffix, which is what sqliteNow produces in SQL and what
// parseTimestamp reads back.
func timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}

// parseTimestamp reads the text form timestamp writes. It accepts any RFC3339
// precision, because the layout is the text's, not this package's.
func parseTimestamp(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode metadata timestamp: %w", err)
	}
	return t, nil
}

// normalizeJSON matches PostgreSQL jsonb's validation and whitespace behavior.
func normalizeJSON(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", errors.New("multiple JSON values")
		}
		return "", err
	}
	b, err := json.Marshal(value)
	return string(b), err
}

// isSQLiteUniqueViolation reports SQLite's duplicate-key errors, which the
// shared CreateUser maps to ErrEmailTaken.
func isSQLiteUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == 1555 || sqliteErr.Code() == 2067
}

var _ Store = (*SQLite)(nil)
