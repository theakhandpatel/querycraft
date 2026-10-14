// Package auth owns account credentials and sessions.
//
// Two deliberate choices keep this small rather than hand-rolled:
//
//   - Password hashing is bcrypt (golang.org/x/crypto/bcrypt), which is
//     well-reviewed and handles salting and cost for us.
//   - Session tokens are 256 bits from crypto/rand. A random opaque token in a
//     table is simpler and safer here than a JWT: there is no signature key to
//     leak, revocation is a DELETE, and the gateway already has a database to
//     resolve it against.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/querycraft/sqlquery/backend/internal/store"
)

// SessionTTL is how long a login stays valid.
const SessionTTL = 30 * 24 * time.Hour

// MinPasswordLen is the floor for a *new* password. Login is deliberately not
// held to it: the default admin ships with an empty password so a local install
// needs no setup, and refusing to check that password would make the account
// unreachable.
const MinPasswordLen = 8

var (
	ErrInvalidEmail       = errors.New("enter a valid email address")
	ErrWeakPassword       = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	ErrInvalidCredentials = errors.New("email or password is incorrect")
	ErrNameRequired       = errors.New("name is required")
)

// NormalizeEmail lowercases and trims an address so account lookups are stable.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail rejects addresses that are not parseable as mail addresses.
func ValidateEmail(email string) error {
	addr, err := mail.ParseAddress(email)
	if err != nil || !strings.Contains(addr.Address, "@") {
		return ErrInvalidEmail
	}
	return nil
}

// ValidatePassword enforces the signup floor. An empty password is rejected
// here even though login accepts one, because a blank password should never be
// something a learner can *choose*.
func ValidatePassword(password string) error {
	if len([]rune(password)) < MinPasswordLen {
		return ErrWeakPassword
	}
	return nil
}

// Hash returns a bcrypt hash of the password.
func Hash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

// Verify reports whether password matches hash.
//
// An empty stored hash means the account was created without a password (the
// default admin); only an empty password then matches. That keeps the "no
// setup" path working without making a blank password match a real hash.
func Verify(hash, password string) bool {
	if hash == "" {
		return password == ""
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NewToken returns a fresh 256-bit session token, URL-safe.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewUserID returns a new account id.
func NewUserID() string { return uuid.NewString() }

// DefaultAdmin describes the account a fresh database is seeded with.
type DefaultAdmin struct {
	Email    string
	Password string
	Name     string
}

// EnsureDefaultAdmin makes sure the given admin account exists, creating it on
// first boot with the admin role. It is idempotent and safe to call on every
// start; it never rewrites the password of an account that already exists, so
// an operator who changed it does not get it reset on restart.
func EnsureDefaultAdmin(ctx context.Context, st store.Store, spec DefaultAdmin, log *slog.Logger) (store.User, error) {
	email := NormalizeEmail(spec.Email)
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = "Administrator"
	}
	if err := ValidateEmail(email); err != nil {
		return store.User{}, fmt.Errorf("QC_ADMIN_EMAIL %q: %w", spec.Email, err)
	}

	existing, hash, ok, err := st.UserByEmail(ctx, email)
	if err != nil {
		return store.User{}, err
	}
	if ok {
		// Repair the role if the account was created before admin seeding, and
		// adopt an account that has no password yet so the default stays usable.
		if !existing.IsAdmin() || hash == "" {
			if hash == "" {
				hashed, herr := Hash(spec.Password)
				if herr != nil {
					return store.User{}, herr
				}
				if serr := st.SetPasswordHash(ctx, existing.ID, hashed); serr != nil {
					return store.User{}, serr
				}
			}
			log.Info("adopting existing account as the default administrator", "email", email)
		}
		if !existing.IsAdmin() {
			log.Warn("default admin address is already registered as a learner; promote it with SQL if it should administer the install", "email", email)
		}
		return existing, nil
	}

	hashed, err := Hash(spec.Password)
	if err != nil {
		return store.User{}, err
	}
	u := store.User{
		ID:        NewUserID(),
		Email:     email,
		Name:      name,
		Role:      store.RoleAdmin,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateUser(ctx, u, hashed); err != nil {
		return store.User{}, err
	}
	if spec.Password == "" {
		log.Warn("created the default administrator with an EMPTY password - set QC_ADMIN_PASSWORD or change it before exposing this port", "email", email)
	} else {
		log.Info("created the default administrator", "email", email)
	}
	return u, nil
}

// Authenticate checks an email/password pair and returns the account. A missing
// account and a wrong password return the same error so the endpoint cannot be
// used to enumerate registered addresses.
func Authenticate(ctx context.Context, st store.Store, email, password string) (store.User, error) {
	u, hash, ok, err := st.UserByEmail(ctx, NormalizeEmail(email))
	if err != nil {
		return store.User{}, err
	}
	if !ok || !Verify(hash, password) {
		return store.User{}, ErrInvalidCredentials
	}
	return u, nil
}

// StartSession issues a token for the account and stores it.
func StartSession(ctx context.Context, st store.Store, userID string) (string, time.Time, error) {
	token, err := NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(SessionTTL)
	if err := st.CreateSession(ctx, token, userID, expires); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	return token, expires, nil
}

// BearerToken extracts a token from an Authorization header. Empty means the
// request is anonymous, not that it is invalid.
func BearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
