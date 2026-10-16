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

// SQLite stores app metadata in one local file. Learner SQL continues to run
// through the separately configured execution engine.
type SQLite struct {
	db *sql.DB
}

var sqliteMemoryID atomic.Uint64

// SQLiteSchema creates the greenfield metadata schema. JSON values are TEXT
// encoded by this package, and timestamps are canonical UTC RFC3339Nano text.
const SQLiteSchema = `
PRAGMA foreign_keys = ON;
-- SQLite strftime has millisecond precision; appending three zeroes gives the
-- same fixed-width microsecond UTC representation used by timestamp().
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'learner',
    password_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (lower(email));
CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now')),
    expires_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now'))
);
CREATE INDEX IF NOT EXISTS sessions_user_key ON sessions (user_id);
CREATE TABLE IF NOT EXISTS progress (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course TEXT NOT NULL,
    snapshot TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now')),
    PRIMARY KEY (user_id, course)
);
CREATE TABLE IF NOT EXISTS events (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now')),
    PRIMARY KEY (user_id, id)
);
CREATE TABLE IF NOT EXISTS challenges (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    artifact TEXT NOT NULL,
    outcome TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now'))
);
CREATE INDEX IF NOT EXISTS challenges_created_key ON challenges (created_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS courses (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    plan TEXT NOT NULL,
    outcome TEXT NOT NULL,
    request TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now'))
);
CREATE INDEX IF NOT EXISTS courses_created_key ON courses (created_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS patches (
    id TEXT PRIMARY KEY,
    challenge_id TEXT NOT NULL DEFAULT '',
    proposal TEXT NOT NULL,
    approved_at TEXT,
    approved_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now'))
);
CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL DEFAULT 'challenge',
    owner_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'failed',
    entries TEXT NOT NULL,
    entry_count INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    truncated INTEGER NOT NULL DEFAULT 0,
    error TEXT,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now')),
    finished_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000Z','now'))
);
CREATE INDEX IF NOT EXISTS runs_owner_started_key ON runs (owner_id, started_at DESC, id DESC);
`

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
	if _, err := db.ExecContext(ctx, SQLiteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create metadata schema: %w", err)
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Kind() string { return "sqlite" }
func (s *SQLite) Close() error { return s.db.Close() }

func timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}

func parseTimestamp(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode metadata timestamp: %w", err)
	}
	return t, nil
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func decodeJSON(raw string, dst any, label string) error {
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	return nil
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

func (s *SQLite) SaveProgress(ctx context.Context, userID, course string, snapshot json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	if course == "" {
		return errors.New("course is required")
	}
	raw, err := normalizeJSON(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO progress(user_id, course, snapshot, updated_at) VALUES(?, ?, ?, ?)
		ON CONFLICT(user_id, course) DO UPDATE SET snapshot=excluded.snapshot, updated_at=excluded.updated_at`, userID, course, raw, timestamp(time.Now()))
	return err
}

func (s *SQLite) LoadProgress(ctx context.Context, userID, course string) (json.RawMessage, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT snapshot FROM progress WHERE user_id=? AND course=?`, userID, course).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage([]byte(raw)), true, nil
}

func (s *SQLite) SaveEvent(ctx context.Context, userID, id string, payload json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	if id == "" {
		return errors.New("event id is required")
	}
	raw, err := normalizeJSON(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO events(user_id,id,payload,created_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id,id) DO UPDATE SET payload=excluded.payload`, userID, id, raw, timestamp(time.Now()))
	return err
}

func (s *SQLite) CreateUser(ctx context.Context, u User, passwordHash string) error {
	role := u.Role
	if role == "" {
		role = RoleLearner
	}
	created := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id,email,name,role,password_hash,created_at) VALUES(?,?,?,?,?,?)`, u.ID, u.Email, u.Name, role, passwordHash, timestamp(created))
	if isSQLiteUniqueViolation(err) {
		return ErrEmailTaken
	}
	return err
}

func (s *SQLite) UserByEmail(ctx context.Context, email string) (User, string, bool, error) {
	var u User
	var hash, created string
	err := s.db.QueryRowContext(ctx, `SELECT id,email,name,role,password_hash,created_at FROM users WHERE lower(email)=lower(?)`, email).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", false, nil
	}
	if err != nil {
		return User{}, "", false, err
	}
	u.CreatedAt, err = parseTimestamp(created)
	return u, hash, err == nil, err
}

func (s *SQLite) UserByID(ctx context.Context, id string) (User, bool, error) {
	var u User
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,email,name,role,created_at FROM users WHERE id=?`, id).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	u.CreatedAt, err = parseTimestamp(created)
	return u, err == nil, err
}

func (s *SQLite) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UserIDs implements Store: every account id, for the environment reaper.
func (s *SQLite) UserIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SQLite) SetPasswordHash(ctx context.Context, userID, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, hash, userID)
	return err
}

func (s *SQLite) CreateSession(ctx context.Context, token, userID string, expiresAt time.Time) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token,user_id,created_at,expires_at,last_seen_at) VALUES(?,?,?,?,?)`, token, userID, timestamp(now), timestamp(expiresAt), timestamp(now))
	return err
}

func (s *SQLite) SessionUser(ctx context.Context, token string) (User, bool, error) {
	var u User
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.email,u.name,u.role,u.created_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token=? AND s.expires_at>?`, token, timestamp(time.Now())).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	u.CreatedAt, err = parseTimestamp(created)
	if err != nil {
		return User{}, false, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE token=?`, timestamp(time.Now()), token)
	return u, true, nil
}

func (s *SQLite) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

func isSQLiteUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == 1555 || sqliteErr.Code() == 2067
}

var _ Store = (*SQLite)(nil)

// JSON serializers for Depot records are kept here to make the text boundary
// explicit and to ensure nil optional values remain SQL NULL.
func jsonText(v any) (string, error) { return marshalJSON(v) }
