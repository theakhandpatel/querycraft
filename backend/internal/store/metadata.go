package store

/* The account, progress and session statements, written once for both SQL
 * backends. Postgres and SQLite embed sqlStore, so every method below runs on
 * both of them, and the dialect next to it supplies what the two engines spell
 * differently: the table schema, the placeholder style, the timestamp value,
 * the JSON column, and the errors each driver raises for a missing row and for
 * a duplicate key.
 *
 * The statements use ? placeholders and name their tables without a schema, so
 * one text reads for both engines. stmt() fills in the dialect: it qualifies
 * the table and writes the placeholders the way the driver wants them.
 */

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* ---------- the query surface ---------- */

// querier is the part of a driver these statements need. Postgres and SQLite
// name and return their calls differently, so each driver reaches this
// interface through the small adapter below.
type querier interface {
	exec(ctx context.Context, statement string, args ...any) error
	queryRow(ctx context.Context, statement string, args ...any) scanner
	query(ctx context.Context, statement string, args ...any) (cursor, error)
}

// scanner reads one row.
type scanner interface {
	Scan(dest ...any) error
}

// cursor reads the rows of a multi-row result. pgx.Rows satisfies it as it
// stands; database/sql's rows need the adapter below, because its Close
// returns an error this interface does not carry.
type cursor interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// pgxQuerier runs statements through a pgx pool, which stays the Postgres
// driver.
type pgxQuerier struct {
	pool *pgxpool.Pool
}

func (q pgxQuerier) exec(ctx context.Context, statement string, args ...any) error {
	_, err := q.pool.Exec(ctx, statement, args...)
	return err
}

func (q pgxQuerier) queryRow(ctx context.Context, statement string, args ...any) scanner {
	return q.pool.QueryRow(ctx, statement, args...)
}

func (q pgxQuerier) query(ctx context.Context, statement string, args ...any) (cursor, error) {
	return q.pool.Query(ctx, statement, args...)
}

// sqlQuerier runs statements through database/sql, which is the SQLite driver.
type sqlQuerier struct {
	db *sql.DB
}

func (q sqlQuerier) exec(ctx context.Context, statement string, args ...any) error {
	_, err := q.db.ExecContext(ctx, statement, args...)
	return err
}

func (q sqlQuerier) queryRow(ctx context.Context, statement string, args ...any) scanner {
	return q.db.QueryRowContext(ctx, statement, args...)
}

func (q sqlQuerier) query(ctx context.Context, statement string, args ...any) (cursor, error) {
	rows, err := q.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	return sqlCursor{rows}, nil
}

// sqlCursor closes a database/sql row set, whose Close returns an error the
// cursor interface drops.
type sqlCursor struct {
	*sql.Rows
}

func (c sqlCursor) Close() { _ = c.Rows.Close() }

/* ---------- the shared implementation ---------- */

// sqlStore holds one backend's query surface and its dialect. Postgres and
// SQLite embed it, so the twelve methods it declares arrive on both types.
type sqlStore struct {
	querier
	dialect sqlDialect
}

// postgresStore wires the shared statements to a pgx pool.
func postgresStore(pool *pgxpool.Pool) sqlStore {
	return sqlStore{querier: pgxQuerier{pool: pool}, dialect: postgresDialect}
}

// sqliteStore wires the shared statements to a database/sql handle.
func sqliteStore(db *sql.DB) sqlStore {
	return sqlStore{querier: sqlQuerier{db: db}, dialect: sqliteDialect}
}

// stmt renders a shared statement for this backend: every %s is a table name
// to qualify and every ? is a placeholder to write in the driver's style.
func (s *sqlStore) stmt(format string, tables ...string) string {
	names := make([]any, len(tables))
	for i, name := range tables {
		names[i] = s.dialect.table(name)
	}
	return s.dialect.bind(fmt.Sprintf(format, names...))
}

/* ---------- the dialect ---------- */

// sqlDialect is what the two backends do differently, in one place.
type sqlDialect struct {
	// postgres selects the PostgreSQL spelling. SQLite stores JSON and
	// timestamps as TEXT, and its driver takes ? placeholders.
	postgres bool
}

var (
	postgresDialect = sqlDialect{postgres: true}
	sqliteDialect   = sqlDialect{}
)

// table qualifies a table name with the schema PostgreSQL keeps its metadata
// in. SQLite's tables are unqualified.
func (d sqlDialect) table(name string) string {
	if d.postgres {
		return AppSchema + "." + name
	}
	return name
}

// bind writes the shared ? placeholders for the driver: pgx numbers them
// ($1, $2), database/sql takes them as they stand. A shared statement must not
// hold a ? inside a string literal, because bind cannot tell the two apart.
func (d sqlDialect) bind(statement string) string {
	if !d.postgres {
		return statement
	}
	var b strings.Builder
	b.Grow(len(statement) + 8)
	n := 0
	for i := 0; i < len(statement); i++ {
		if statement[i] != '?' {
			b.WriteByte(statement[i])
			continue
		}
		n++
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
	}
	return b.String()
}

// stamp prepares a timestamp for its column. PostgreSQL takes the time.Time
// and stores it as timestamptz; SQLite takes the canonical UTC text that
// timestamp() writes.
func (d sqlDialect) stamp(t time.Time) any {
	if d.postgres {
		return t
	}
	return timestamp(t)
}

// encodeJSON prepares a JSON value for its column. PostgreSQL takes the text
// and jsonb validates and compacts it; SQLite stores TEXT, so the value is
// compacted here to read back the same.
func (d sqlDialect) encodeJSON(raw json.RawMessage) (any, error) {
	if d.postgres {
		return string(raw), nil
	}
	return normalizeJSON(raw)
}

// noRows reports the driver's "the query matched nothing" error.
func (d sqlDialect) noRows(err error) bool {
	if d.postgres {
		return errors.Is(err, pgx.ErrNoRows)
	}
	return errors.Is(err, sql.ErrNoRows)
}

// emailTaken reports the driver's duplicate-key error.
func (d sqlDialect) emailTaken(err error) bool {
	if d.postgres {
		return isUniqueViolation(err)
	}
	return isSQLiteUniqueViolation(err)
}

// timeColumn reads one timestamp column. PostgreSQL hands pgx a time.Time and
// SQLite hands database/sql the text timestamp() wrote, so one destination
// decodes both.
type timeColumn struct {
	dst *time.Time
}

func (c timeColumn) Scan(src any) error {
	switch raw := src.(type) {
	case time.Time:
		*c.dst = raw
		return nil
	case string:
		t, err := parseTimestamp(raw)
		if err != nil {
			return err
		}
		*c.dst = t
		return nil
	case []byte:
		t, err := parseTimestamp(string(raw))
		if err != nil {
			return err
		}
		*c.dst = t
		return nil
	}
	return fmt.Errorf("scan timestamp: unsupported source %T", src)
}

/* ---------- the statements ---------- */

const (
	saveProgressSQL = `INSERT INTO %s (user_id, course, snapshot, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (user_id, course) DO UPDATE
		 SET snapshot = excluded.snapshot, updated_at = excluded.updated_at`

	loadProgressSQL = `SELECT snapshot FROM %s WHERE user_id = ? AND course = ?`

	saveEventSQL = `INSERT INTO %s (user_id, id, payload, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (user_id, id) DO UPDATE SET payload = excluded.payload`

	createUserSQL = `INSERT INTO %s (id, email, name, role, password_hash, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`

	userByEmailSQL = `SELECT id, email, name, role, password_hash, created_at
		 FROM %s WHERE lower(email) = lower(?)`

	userByIDSQL = `SELECT id, email, name, role, created_at FROM %s WHERE id = ?`

	countUsersSQL = `SELECT count(*) FROM %s`

	userIDsSQL = `SELECT id FROM %s`

	setPasswordHashSQL = `UPDATE %s SET password_hash = ? WHERE id = ?`

	saveSettingSQL = `INSERT INTO %s (key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`

	settingSQL = `SELECT value FROM %s WHERE key = ?`

	createSessionSQL = `INSERT INTO %s (token, user_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`

	sessionUserSQL = `SELECT u.id, u.email, u.name, u.role, u.created_at
		 FROM %s s JOIN %s u ON u.id = s.user_id
		 WHERE s.token = ? AND s.expires_at > ?`

	deleteSessionSQL = `DELETE FROM %s WHERE token = ?`
)

/* ---------- progress ---------- */

// SaveProgress implements Store. The write is scoped to userID, so the same
// course can hold an independent snapshot per account.
func (s *sqlStore) SaveProgress(ctx context.Context, userID, course string, snapshot json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	if course == "" {
		return errors.New("course is required")
	}
	raw, err := s.dialect.encodeJSON(snapshot)
	if err != nil {
		return err
	}
	return s.exec(ctx, s.stmt(saveProgressSQL, "progress"), userID, course, raw, s.dialect.stamp(time.Now()))
}

// LoadProgress implements Store. Returning nothing for another account's row is
// the point: the query filters on the owner rather than trusting the caller.
func (s *sqlStore) LoadProgress(ctx context.Context, userID, course string) (json.RawMessage, bool, error) {
	var raw []byte
	err := s.queryRow(ctx, s.stmt(loadProgressSQL, "progress"), userID, course).Scan(&raw)
	if s.dialect.noRows(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(raw), true, nil
}

// SaveEvent implements Store.

/* ---------- settings ---------- */

// SaveSetting implements Store. The value is the feature's own JSON text,
// stored as jsonb on PostgreSQL and text on SQLite; updated_at moves on every
// write, so an operator can see when a setting last changed.
func (s *sqlStore) SaveSetting(ctx context.Context, key, value string) error {
	if key == "" {
		return errors.New("setting key is required")
	}
	raw, err := s.dialect.encodeJSON(json.RawMessage(value))
	if err != nil {
		return err
	}
	return s.exec(ctx, s.stmt(saveSettingSQL, "settings"), key, raw, s.dialect.stamp(time.Now()))
}

// Setting implements Store. found is false when the key has never been saved;
// an empty value is data, not absence.
func (s *sqlStore) Setting(ctx context.Context, key string) (string, bool, error) {
	var raw []byte
	err := s.queryRow(ctx, s.stmt(settingSQL, "settings"), key).Scan(&raw)
	if s.dialect.noRows(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

// SaveEvent implements Store.
func (s *sqlStore) SaveEvent(ctx context.Context, userID, id string, payload json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	if id == "" {
		return errors.New("event id is required")
	}
	raw, err := s.dialect.encodeJSON(payload)
	if err != nil {
		return err
	}
	return s.exec(ctx, s.stmt(saveEventSQL, "events"), userID, id, raw, s.dialect.stamp(time.Now()))
}

/* ---------- accounts ---------- */

// CreateUser implements Store. A duplicate address is reported as
// ErrEmailTaken rather than a raw constraint error, so the handler can answer
// 409 without inspecting either driver's error codes.
func (s *sqlStore) CreateUser(ctx context.Context, u User, passwordHash string) error {
	role := u.Role
	if role == "" {
		role = RoleLearner
	}
	err := s.exec(ctx, s.stmt(createUserSQL, "users"),
		u.ID, u.Email, u.Name, role, passwordHash, s.dialect.stamp(time.Now()))
	if err != nil && s.dialect.emailTaken(err) {
		return ErrEmailTaken
	}
	return err
}

// UserByEmail implements Store. The match is case-insensitive on the address,
// which is why the unique index is on lower(email).
func (s *sqlStore) UserByEmail(ctx context.Context, email string) (User, string, bool, error) {
	var u User
	var hash string
	err := s.queryRow(ctx, s.stmt(userByEmailSQL, "users"), email).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &hash, timeColumn{&u.CreatedAt})
	if s.dialect.noRows(err) {
		return User{}, "", false, nil
	}
	if err != nil {
		return User{}, "", false, err
	}
	return u, hash, true, nil
}

// UserByID implements Store.
func (s *sqlStore) UserByID(ctx context.Context, id string) (User, bool, error) {
	var u User
	err := s.queryRow(ctx, s.stmt(userByIDSQL, "users"), id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, timeColumn{&u.CreatedAt})
	if s.dialect.noRows(err) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

// CountUsers implements Store.
func (s *sqlStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.queryRow(ctx, s.stmt(countUsersSQL, "users")).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// UserIDs implements Store: every account id, for the environment reaper.
func (s *sqlStore) UserIDs(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx, s.stmt(userIDsSQL, "users"))
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

// SetPasswordHash implements Store.
func (s *sqlStore) SetPasswordHash(ctx context.Context, userID, hash string) error {
	return s.exec(ctx, s.stmt(setPasswordHashSQL, "users"), hash, userID)
}

/* ---------- sessions ---------- */

// CreateSession implements Store. created_at is bound on both backends rather
// than left to the column default, so both statements bind the same arguments.
func (s *sqlStore) CreateSession(ctx context.Context, token, userID string, expiresAt time.Time) error {
	return s.exec(ctx, s.stmt(createSessionSQL, "sessions"),
		token, userID, s.dialect.stamp(time.Now()), s.dialect.stamp(expiresAt))
}

// SessionUser resolves a token to its account, ignoring expired sessions.
func (s *sqlStore) SessionUser(ctx context.Context, token string) (User, bool, error) {
	var u User
	err := s.queryRow(ctx, s.stmt(sessionUserSQL, "sessions", "users"), token, s.dialect.stamp(time.Now())).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, timeColumn{&u.CreatedAt})
	if s.dialect.noRows(err) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

// DeleteSession implements Store.
func (s *sqlStore) DeleteSession(ctx context.Context, token string) error {
	return s.exec(ctx, s.stmt(deleteSessionSQL, "sessions"), token)
}
