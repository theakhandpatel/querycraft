// Package store persists opaque progress snapshots, audit events and the
// accounts that own them. Three implementations satisfy Store: Postgres and
// SQLite (both embed sqlStore over their own dialect) and Memory, the SQLite
// backend over an in-memory database.
//
// When no database is configured the in-memory implementation keeps the service
// usable, but it never reports a write as durable. Callers must not report
// success when a write failed.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// ErrEmailTaken is returned when a signup reuses an existing address. Handlers
// map it to 409 instead of leaking a database constraint error.
var ErrEmailTaken = errors.New("email already registered")

// Account roles. auth.EnsureDefaultAdmin seeds one account with RoleAdmin, so a
// fresh install has someone who can administer it.
const (
	RoleAdmin   = "admin"
	RoleLearner = "learner"
)

// User is an authenticated account. The password hash never leaves the store:
// auth is the only package that reads it, and it is never serialized to a
// client (see the json tags: there is no hash field).
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// IsAdmin reports whether the account holds the admin role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// Store is the persistence surface for accounts, progress and events.
//
// The account, progress and session methods run from one implementation
// (metadata.go) on both SQL backends; Memory runs those same statements against
// an in-memory database.
//
// Progress and events are scoped by owner: every read and write names the user
// it belongs to, so one account can never observe or overwrite another's
// history. Handlers get that id from the authenticated session, not from the
// request body.
type Store interface {
	// SaveProgress replaces the calling account's snapshot for one course. The
	// write names the user, so two accounts can hold independent snapshots of
	// the same course.
	SaveProgress(ctx context.Context, userID, course string, snapshot json.RawMessage) error
	// LoadProgress returns one account's snapshot for a course. found is false
	// when that account has never saved the course.
	LoadProgress(ctx context.Context, userID, course string) (json.RawMessage, bool, error)
	// SaveEvent upserts one audit event under the calling account.
	SaveEvent(ctx context.Context, userID, id string, payload json.RawMessage) error

	// CreateUser inserts an account. A duplicate address returns ErrEmailTaken.
	CreateUser(ctx context.Context, u User, passwordHash string) error
	// UserByEmail looks up an account by address, ignoring case. The second
	// result is the stored password hash; found is false when nothing matches.
	UserByEmail(ctx context.Context, email string) (User, string, bool, error)
	// SetPasswordHash replaces one account's stored hash, for a password change.
	SetPasswordHash(ctx context.Context, userID, hash string) error
	// UserSetting reads one client preference row for an account. found is
	// false when the account has never saved that key.
	UserSetting(ctx context.Context, userID, key string) (value string, found bool, err error)

	// SaveUserSetting writes one client preference row for an account,
	// replacing any previous value.
	SaveUserSetting(ctx context.Context, userID, key, value string) error

	// Setting reads one runtime setting by key. found is false when the key
	// has never been saved; value is the raw JSON text it was saved with.
	Setting(ctx context.Context, key string) (value string, found bool, err error)

	// SaveSetting writes one runtime setting, replacing any previous value.
	SaveSetting(ctx context.Context, key, value string) error

	// UserIDs lists every account id, which is what the environment reaper uses
	// to decide which per-account databases the store still references. It is the
	// honest input for "drop the ones not named here": an orphan is a database
	// whose account was never recorded, so the set of recorded accounts is
	// exactly the keep-set.
	UserIDs(ctx context.Context) ([]string, error)

	// CreateSession stores a bearer token for an account. The token resolves to
	// the account until expiresAt passes.
	CreateSession(ctx context.Context, token, userID string, expiresAt time.Time) error
	// SessionUser resolves a live token to its account. found is false for an
	// unknown or expired token.
	SessionUser(ctx context.Context, token string) (User, bool, error)
	// DeleteSession removes one token, which is what sign-out does.
	DeleteSession(ctx context.Context, token string) error

	// Kind names the implementation for the diagnostics view: "postgres" and
	// "sqlite" persist accounts and snapshots across restarts; "memory" does not.
	// A gateway running on the memory store is not broken, but a learner looking
	// at it should be told their progress is not being kept.
	Kind() string

	// Close releases the backend's connections.
	Close() error

	// schema.Depot is the M4 challenge store. A generated challenge and the
	// evidence that admitted it are persisted together, so a later reader can
	// see why an artifact was allowed in front of learners. Patch proposals
	// live here too, unapplied until the learner approves them. The agent-run
	// transcripts (app.runs) are also part of this seam: schema.Depot carries
	// SaveRun/Run/RunsFor, so the audit trail is owner-scoped by the same store
	// that scopes everything else.
	schema.Depot
}
