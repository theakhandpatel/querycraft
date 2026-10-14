package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Memory is a concurrency-safe in-process Store. It is used when no metadata
// database is configured so the gateway still runs. Accounts and sessions live
// in the same process, which means a restart forgets who was signed in -- the
// honest behaviour for a store that has nowhere to persist them.
type Memory struct {
	mu       sync.RWMutex
	progress map[string]json.RawMessage // key: userID + "\x00" + course
	events   map[string]json.RawMessage // key: userID + "\x00" + event id
	users    map[string]memoryUser      // key: account id
	sessions map[string]memorySession   // key: token
}

type memoryUser struct {
	user User
	hash string
}

type memorySession struct {
	userID    string
	expiresAt time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		progress: make(map[string]json.RawMessage),
		events:   make(map[string]json.RawMessage),
		users:    make(map[string]memoryUser),
		sessions: make(map[string]memorySession),
	}
}

func scopeKey(userID, key string) string { return userID + "\x00" + key }

// SaveProgress implements Store.
func (m *Memory) SaveProgress(_ context.Context, userID, course string, snapshot json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progress[scopeKey(userID, course)] = clone(snapshot)
	return nil
}

// LoadProgress implements Store.
func (m *Memory) LoadProgress(_ context.Context, userID, course string) (json.RawMessage, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap, ok := m.progress[scopeKey(userID, course)]
	if !ok {
		return nil, false, nil
	}
	return clone(snap), true, nil
}

// SaveEvent implements Store.
func (m *Memory) SaveEvent(_ context.Context, userID, id string, payload json.RawMessage) error {
	// Match Postgres: an event without an owner or an id is not storable, so the
	// degraded store reports the same failure instead of silently accepting it.
	if userID == "" {
		return errors.New("user is required")
	}
	if id == "" {
		return errors.New("event id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[scopeKey(userID, id)] = clone(payload)
	return nil
}

// CreateUser implements Store.
func (m *Memory) CreateUser(_ context.Context, u User, passwordHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if strings.EqualFold(existing.user.Email, u.Email) {
			return ErrEmailTaken
		}
	}
	if u.Role == "" {
		u.Role = RoleLearner
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	m.users[u.ID] = memoryUser{user: u, hash: passwordHash}
	return nil
}

// UserByEmail implements Store.
func (m *Memory) UserByEmail(_ context.Context, email string) (User, string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, existing := range m.users {
		if strings.EqualFold(existing.user.Email, email) {
			return existing.user, existing.hash, true, nil
		}
	}
	return User{}, "", false, nil
}

// UserByID implements Store.
func (m *Memory) UserByID(_ context.Context, id string) (User, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, false, nil
	}
	return u.user, true, nil
}

// CountUsers implements Store.
func (m *Memory) CountUsers(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users), nil
}

// SetPasswordHash implements Store.
func (m *Memory) SetPasswordHash(_ context.Context, userID, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return errors.New("unknown user")
	}
	u.hash = hash
	m.users[userID] = u
	return nil
}

// CreateSession implements Store.
func (m *Memory) CreateSession(_ context.Context, token, userID string, expiresAt time.Time) error {
	if userID == "" {
		return errors.New("user is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[token] = memorySession{userID: userID, expiresAt: expiresAt}
	return nil
}

// SessionUser implements Store.
func (m *Memory) SessionUser(_ context.Context, token string) (User, bool, error) {
	m.mu.RLock()
	sess, ok := m.sessions[token]
	u, uok := m.users[sess.userID]
	m.mu.RUnlock()
	if !ok || !uok || time.Now().After(sess.expiresAt) {
		return User{}, false, nil
	}
	return u.user, true, nil
}

// DeleteSession implements Store.
func (m *Memory) DeleteSession(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

// Close implements Store.
func (m *Memory) Close() error { return nil }

func clone(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	out := make(json.RawMessage, len(b))
	copy(out, b)
	return out
}
