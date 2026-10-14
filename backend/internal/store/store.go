// Package store persists opaque progress snapshots, audit events and the
// accounts that own them. When no database is configured the in-memory
// implementation keeps the service usable; callers must never report success
// when a write actually failed.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrEmailTaken is returned when a signup reuses an existing address. Handlers
// map it to 409 instead of leaking a database constraint error.
var ErrEmailTaken = errors.New("email already registered")

// Account roles. The first account a fresh database gets is an admin, so a
// local install has someone who can see the whole table.
const (
	RoleAdmin   = "admin"
	RoleLearner = "learner"
)

// User is an authenticated account. The password hash never leaves the store:
// auth is the only package that reads it, and it is never serialized to a
// client (see the json tags — there is deliberately no hash field).
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
// Progress and events are scoped by owner: every read and write names the user
// it belongs to, so one account can never observe or overwrite another's
// history. Handlers get that id from the authenticated session, not from the
// request body.
type Store interface {
	SaveProgress(ctx context.Context, userID, course string, snapshot json.RawMessage) error
	LoadProgress(ctx context.Context, userID, course string) (json.RawMessage, bool, error)
	SaveEvent(ctx context.Context, userID, id string, payload json.RawMessage) error

	CreateUser(ctx context.Context, u User, passwordHash string) error
	UserByEmail(ctx context.Context, email string) (User, string, bool, error)
	UserByID(ctx context.Context, id string) (User, bool, error)
	CountUsers(ctx context.Context) (int, error)
	SetPasswordHash(ctx context.Context, userID, hash string) error

	CreateSession(ctx context.Context, token, userID string, expiresAt time.Time) error
	SessionUser(ctx context.Context, token string) (User, bool, error)
	DeleteSession(ctx context.Context, token string) error

	// Kind names the implementation for the diagnostics view: "postgres" when
	// accounts and snapshots survive a restart, "memory" when they do not. A
	// gateway running on the memory store is not broken, but a learner looking
	// at it should be told their progress is not being kept.
	Kind() string

	Close() error
}
