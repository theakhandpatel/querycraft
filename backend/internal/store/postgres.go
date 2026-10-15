package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the server-mode metadata store. It is a separate database from the
// learner execution connection.
type Postgres struct {
	pool *pgxpool.Pool
}

// AppSchema is the schema that holds gateway metadata (accounts, progress,
// events). It is deliberately distinct from the learner-visible `dataset`
// schema so a restricted execution role can be granted SELECT on `dataset`
// alone. See docs/database-layout.md.
const AppSchema = "app"

/* The metadata schema, in its final shape.
 *
 * This is greenfield: there is no pre-accounts layout to migrate from. A
 * database created before this shape existed is simply reset (`make db-reset`),
 * which is why there is no ALTER-based upgrade path here. Progress and events
 * are keyed by the account that produced them. */
const migrateSQL = `
CREATE SCHEMA IF NOT EXISTS app;

CREATE TABLE IF NOT EXISTS app.users (
    id            text PRIMARY KEY,
    email         text NOT NULL,
    name          text NOT NULL,
    role          text NOT NULL DEFAULT 'learner',
    password_hash text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON app.users (lower(email));

CREATE TABLE IF NOT EXISTS app.sessions (
    token        text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_user_key ON app.sessions (user_id);

CREATE TABLE IF NOT EXISTS app.progress (
    user_id    text NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
    course     text NOT NULL,
    snapshot   jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, course)
);

CREATE TABLE IF NOT EXISTS app.events (
    user_id    text NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
    id         text NOT NULL,
    payload    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);

/* Generated challenges (M4). The status column is 'active' only for a challenge that
 * passed every pipeline stage; the outcome column is the evidence captured at
 * activation, kept beside the artifact so the two cannot drift. There is no
 * foreign key to app.users: a challenge generated before sign-in is still a
 * valid artifact, and owner_id records who asked for it when there was one. */
CREATE TABLE IF NOT EXISTS app.challenges (
    id         text PRIMARY KEY,
    owner_id   text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT 'active',
    artifact   jsonb NOT NULL,
    outcome    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS challenges_created_key ON app.challenges (created_at DESC, id DESC);

/* Generated courses (M7), the multi-chapter counterpart of app.challenges. The
 * plan is stored whole: the chapters, the markdown and every exercise's
 * expected rows are one artifact, and the outcome that admitted it sits beside
 * it so a later reader can see what was measured before the course existed.
 * request is the generation request that produced the plan; it is kept because
 * a course can pause after its outline and be continued later, and the chapter
 * phase rebuilds its own prompt from the learner's own words. A row written
 * before the request was kept carries none, which the resume path handles. */
CREATE TABLE IF NOT EXISTS app.courses (
    id         text PRIMARY KEY,
    owner_id   text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT 'active',
    plan       jsonb NOT NULL,
    outcome    jsonb NOT NULL,
    request    jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS courses_created_key ON app.courses (created_at DESC, id DESC);

/* Patch proposals (M4). approved_at is null until a learner approves, so a
 * proposal can never be mistaken for something that was applied. */
CREATE TABLE IF NOT EXISTS app.patches (
    id           text PRIMARY KEY,
    challenge_id text NOT NULL DEFAULT '',
    proposal     jsonb NOT NULL,
    approved_at  timestamptz,
    approved_by  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

/* Agent-run transcripts (the audit trail). One row per generation run: the
 * conversation the model had, stored as one JSONB array with the run's
 * attribution beside it. The entries are bounded before they arrive here
 * (ai.MaxTranscriptBytes / ai.MaxTranscriptEntries), so a runaway run cannot
 * grow the row without limit, and the whole record is redacted at the seam, so
 * a credential never reaches this table. owner_id records who asked for the
 * run; there is no foreign key, for the same reason app.challenges has none: a
 * run made before sign-in is still a real record. */
CREATE TABLE IF NOT EXISTS app.runs (
    id          text PRIMARY KEY,
    kind        text NOT NULL DEFAULT 'challenge',
    owner_id    text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'failed',
    entries     jsonb NOT NULL,
    entry_count integer NOT NULL DEFAULT 0,
    bytes       integer NOT NULL DEFAULT 0,
    truncated   boolean NOT NULL DEFAULT false,
    error       jsonb,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS runs_owner_started_key ON app.runs (owner_id, started_at DESC, id DESC);
`

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
	return &Postgres{pool: pool}, nil
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

/* ---------- progress ---------- */

// SaveProgress implements Store. The write is scoped to userID, so the same
// course can hold an independent snapshot per account.
func (p *Postgres) SaveProgress(ctx context.Context, userID, course string, snapshot json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	if course == "" {
		return errors.New("course is required")
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO app.progress (user_id, course, snapshot, updated_at) VALUES ($1, $2, $3, now())
		 ON CONFLICT (user_id, course) DO UPDATE SET snapshot = EXCLUDED.snapshot, updated_at = now()`,
		userID, course, string(snapshot))
	return err
}

// LoadProgress implements Store. Returning nothing for another account's row is
// the point: the query filters on the owner rather than trusting the caller.
func (p *Postgres) LoadProgress(ctx context.Context, userID, course string) (json.RawMessage, bool, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx,
		`SELECT snapshot FROM app.progress WHERE user_id = $1 AND course = $2`, userID, course).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(raw), true, nil
}

// SaveEvent implements Store.
func (p *Postgres) SaveEvent(ctx context.Context, userID, id string, payload json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	if id == "" {
		return errors.New("event id is required")
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO app.events (user_id, id, payload) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, id) DO UPDATE SET payload = EXCLUDED.payload`,
		userID, id, string(payload))
	return err
}

/* ---------- accounts ---------- */

// CreateUser implements Store. A duplicate address is reported as
// ErrEmailTaken rather than a raw constraint error, so the handler can answer
// 409 without inspecting PostgreSQL error codes.
func (p *Postgres) CreateUser(ctx context.Context, u User, passwordHash string) error {
	role := u.Role
	if role == "" {
		role = RoleLearner
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO app.users (id, email, name, role, password_hash, created_at)
		 VALUES ($1, $2, $3, $4, $5, now())`,
		u.ID, u.Email, u.Name, role, passwordHash)
	if err != nil && isUniqueViolation(err) {
		return ErrEmailTaken
	}
	return err
}

// UserByEmail implements Store. The match is case-insensitive on the address,
// which is why the unique index is on lower(email).
func (p *Postgres) UserByEmail(ctx context.Context, email string) (User, string, bool, error) {
	var u User
	var hash string
	err := p.pool.QueryRow(ctx,
		`SELECT id, email, name, role, password_hash, created_at FROM app.users WHERE lower(email) = lower($1)`,
		email).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &hash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", false, nil
	}
	if err != nil {
		return User{}, "", false, err
	}
	return u, hash, true, nil
}

// UserByID implements Store.
func (p *Postgres) UserByID(ctx context.Context, id string) (User, bool, error) {
	var u User
	err := p.pool.QueryRow(ctx,
		`SELECT id, email, name, role, created_at FROM app.users WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

// CountUsers implements Store.
func (p *Postgres) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM app.users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// SetPasswordHash implements Store.
func (p *Postgres) SetPasswordHash(ctx context.Context, userID, hash string) error {
	_, err := p.pool.Exec(ctx, `UPDATE app.users SET password_hash = $2 WHERE id = $1`, userID, hash)
	return err
}

/* ---------- sessions ---------- */

// CreateSession implements Store.
func (p *Postgres) CreateSession(ctx context.Context, token, userID string, expiresAt time.Time) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO app.sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, expiresAt)
	return err
}

// SessionUser resolves a token to its account, ignoring expired sessions.
// last_seen_at is refreshed opportunistically; a failure there must not log the
// learner out, so its error is discarded.
func (p *Postgres) SessionUser(ctx context.Context, token string) (User, bool, error) {
	var u User
	err := p.pool.QueryRow(ctx,
		`SELECT u.id, u.email, u.name, u.role, u.created_at
		   FROM app.sessions s JOIN app.users u ON u.id = s.user_id
		  WHERE s.token = $1 AND s.expires_at > now()`, token).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	_, _ = p.pool.Exec(ctx, `UPDATE app.sessions SET last_seen_at = now() WHERE token = $1`, token)
	return u, true, nil
}

// DeleteSession implements Store.
func (p *Postgres) DeleteSession(ctx context.Context, token string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM app.sessions WHERE token = $1`, token)
	return err
}

// Close implements Store.
// Kind implements Store.
func (p *Postgres) Kind() string { return "postgres" }

func (p *Postgres) Close() error {
	if p.pool != nil {
		p.pool.Close()
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
