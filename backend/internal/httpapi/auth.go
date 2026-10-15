package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/auth"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* Authentication surface.
 *
 * Transport is an opaque bearer token. The browser stores it and sends
 * `Authorization: Bearer <token>`; the server resolves it to an account on
 * every request. Nothing is trusted from the request body except the
 * credentials themselves -- in particular a caller can never name the user
 * whose progress it is reading or writing. */

type authCtxKey struct{}

func withUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, authCtxKey{}, u)
}

// userFrom returns the authenticated account, if the request carried a valid
// token. An anonymous request is not an error at this layer.
func userFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(authCtxKey{}).(store.User)
	return u, ok
}

// withAuth resolves a bearer token into an account and attaches it.
//
// An absent token is allowed through untouched so the public surface (health,
// query, dataset introspection) keeps working; handlers that need an owner call
// requireUser, which answers 401 with a stable code.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := auth.BearerToken(r.Header.Get("Authorization"))
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, ok, err := s.store.SessionUser(r.Context(), token)
		if err != nil {
			persistenceFailed(w, err)
			return
		}
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

// requireUser resolves the caller or writes 401 and returns false.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	if u, ok := userFrom(r.Context()); ok {
		return u, true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"ok":    false,
		"error": map[string]string{"code": "AUTH_REQUIRED", "message": "Sign in to continue."},
	})
	return store.User{}, false
}

func authBadRequest(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"ok":    false,
		"error": map[string]string{"code": "AUTH_INVALID_INPUT", "message": err.Error()},
	})
}

// authResponse is the body returned by signup and login. The token is the only
// secret in it; the user object deliberately has no hash field to leak.
type authResponse struct {
	OK        bool       `json:"ok"`
	User      store.User `json:"user"`
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expiresAt"`
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	email := auth.NormalizeEmail(req.Email)
	name := strings.TrimSpace(req.Name)
	if err := auth.ValidateEmail(email); err != nil {
		authBadRequest(w, err)
		return
	}
	if name == "" {
		authBadRequest(w, auth.ErrNameRequired)
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		authBadRequest(w, err)
		return
	}
	hash, err := auth.Hash(req.Password)
	if err != nil {
		persistenceFailed(w, err)
		return
	}
	// Signups are learners. Admin is not self-service: it belongs to the
	// account the install seeds, and granting it from a public form would make
	// the role meaningless.
	u := store.User{
		ID:        auth.NewUserID(),
		Email:     email,
		Name:      name,
		Role:      store.RoleLearner,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateUser(r.Context(), u, hash); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"ok":    false,
				"error": map[string]string{"code": "AUTH_EMAIL_TAKEN", "message": "That email is already registered."},
			})
			return
		}
		persistenceFailed(w, err)
		return
	}
	token, expires, err := auth.StartSession(r.Context(), s.store, u.ID)
	if err != nil {
		persistenceFailed(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, authResponse{OK: true, User: u, Token: token, ExpiresAt: expires})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	// Throttle before touching credentials, and key the budget on the caller's
	// socket address and the account it named. A refusal carries no field from
	// the request and makes no distinction between a known and an unknown
	// address, so it leaks neither whether the account exists nor whether the
	// password was ever compared.
	key := loginKey(clientIP(r), auth.NormalizeEmail(req.Email))
	if allowed, retryAfter := s.login.allow(key); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"ok":    false,
			"error": map[string]string{"code": "AUTH_RATE_LIMITED", "message": "Too many failed sign-in attempts. Try again later."},
		})
		return
	}
	u, err := auth.Authenticate(r.Context(), s.store, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			s.login.fail(key)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"ok":    false,
				"error": map[string]string{"code": "AUTH_INVALID_CREDENTIALS", "message": "Email or password is incorrect."},
			})
			return
		}
		persistenceFailed(w, err)
		return
	}
	// A successful sign-in clears the pair's failures: the caller has proven it
	// holds the password, so the budget it spent guessing is irrelevant now.
	s.login.reset(key)
	token, expires, err := auth.StartSession(r.Context(), s.store, u.ID)
	if err != nil {
		persistenceFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, authResponse{OK: true, User: u, Token: token, ExpiresAt: expires})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := auth.BearerToken(r.Header.Get("Authorization"))
	if token != "" {
		if err := s.store.DeleteSession(r.Context(), token); err != nil {
			persistenceFailed(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
}
