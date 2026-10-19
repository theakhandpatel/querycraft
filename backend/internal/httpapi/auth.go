package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/auth"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* Authentication surface.
 *
 * Transport is an opaque session token. An API client sends it explicitly as
 * `Authorization: Bearer <token>`; a browser keeps it in the `qc_session`
 * cookie the gateway sets on sign-in, which the browser returns on its own on
 * every same-origin request. The server resolves either to an account on every
 * request. Nothing is trusted from the request body except the credentials
 * themselves. In particular, a caller can never name the user whose progress
 * it is reading or writing.
 *
 * The Authorization header wins when both are present, because it is explicit:
 * a present-but-invalid token must not fall back to the cookie.
 *
 * A cookie travels without the caller's intent, so a cookie-authenticated
 * mutating request is checked against the same-origin headers a browser sends.
 * A request with neither header is a non-browser client and stays allowed; a
 * header that names another site is refused with 403 CSRF_REFUSED. A
 * bearer-authenticated request is never checked, because the credential is
 * explicit.
 *
 * withAuth resolves the token through the store and attaches the account;
 * withRouteAuth then enforces the requirement routes.go declares for the
 * matched pattern. A handler that needs an owner calls requireUser, and the
 * admin surface calls requireAdmin, so ownership is decided once, from the
 * session, at the point of use. */

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

// withAuth resolves a session token into an account and attaches it. A request
// with no token, or one the store does not know, continues as anonymous:
// withRouteAuth decides whether the route it is heading for may serve an
// anonymous caller, and handlers that need an owner call requireUser.
//
// The Authorization header is resolved first. When it names an unknown token
// the request stays anonymous and the cookie is not consulted: an explicit
// credential that does not resolve is a mistake, not a reason to try another.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, fromCookie := sessionToken(r)
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
		if fromCookie && isMutating(r.Method) && !sameOriginRequest(r) {
			errorEnvelope(w, http.StatusForbidden, "CSRF_REFUSED",
				"The request was refused because it did not come from this site.")
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

// withRouteAuth enforces the routing table's requirement for the request, and
// attaches the table's own entry to the context so handlers and body decoding
// read the same declaration the middleware enforced.
//
// It sits inside withAuth, so a valid token is already resolved into an
// account. A required route without one answers 401 AUTH_REQUIRED and never
// reaches its handler. A pattern the table does not declare is closed too, so a
// route added to the mux without a table entry cannot serve an anonymous
// caller.
func withRouteAuth(mux *http.ServeMux, routes []route) http.Handler {
	declared := make(map[string]route, len(routes))
	for _, rt := range routes {
		declared[rt.pattern()] = rt
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			rt, ok := declared[pattern]
			if !ok || rt.auth != authPublic {
				if _, authed := userFrom(r.Context()); !authed {
					writeAuthRequired(w)
					return
				}
			}
			if ok {
				r = r.WithContext(withRoute(r.Context(), rt))
			}
		}
		// An unmatched pattern carries no route; the mux answers 404 as before.
		mux.ServeHTTP(w, r)
	})
}

// writeAuthRequired writes the 401 every session-gated route answers with: one
// sentence and one code, so the routing middleware and a handler cannot drift.
func writeAuthRequired(w http.ResponseWriter) {
	errorEnvelope(w, http.StatusUnauthorized, "AUTH_REQUIRED", "Sign in to continue.")
}

// requireUser resolves the caller or writes 401 and returns false.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	if u, ok := userFrom(r.Context()); ok {
		return u, true
	}
	writeAuthRequired(w)
	return store.User{}, false
}

// requireAdmin resolves the caller and refuses anyone without the admin role:
// 401 for no session, 403 ADMIN_REQUIRED for a signed-in learner. The admin
// surface is the course inventory; the role lives on the stored account, and
// public signups never receive it.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return store.User{}, false
	}
	if !u.IsAdmin() {
		errorEnvelope(w, http.StatusForbidden, "ADMIN_REQUIRED", "This surface is for course administrators.")
		return store.User{}, false
	}
	return u, true
}

// authBadRequest writes a 400 for a body the auth routes could not accept.
func authBadRequest(w http.ResponseWriter, err error) {
	errorEnvelope(w, http.StatusBadRequest, "AUTH_INVALID_INPUT", err.Error())
}

/* ---------- session transport ---------- */

// sessionCookieName is the cookie the gateway sets on sign-in and that the
// browser returns on every same-origin request.
const sessionCookieName = "qc_session"

// sessionToken resolves the request's session token. The Authorization header
// wins, because it is explicit and a present-but-invalid token must not fall
// back to the cookie. The second return reports whether the token came from the
// cookie, which is what decides whether the same-origin check applies.
func sessionToken(r *http.Request) (token string, fromCookie bool) {
	if token := auth.BearerToken(r.Header.Get("Authorization")); token != "" {
		return token, false
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
}

// setSessionCookie writes the cookie login and signup set. Secure is added only
// when the request arrived over TLS, so a plaintext local install still works
// while a TLS deployment never ships the cookie in the clear.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		Expires:  expires,
	})
}

// clearSessionCookie expires the cookie on logout. MaxAge -1 makes the browser
// drop it immediately rather than keep it until it would have expired.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})
}

// isMutating reports whether a method can change state, which is the only kind
// the same-origin check covers. A safe method is left alone so a cross-site
// navigation still renders.
func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// sameOriginRequest reports whether the browser headers on a mutating request
// agree that it came from this site. Origin and Sec-Fetch-Site are both
// consulted when present; neither present means a non-browser client, which is
// allowed because such a caller had to attach the cookie itself.
func sameOriginRequest(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "none":
		return false
	}
	return true
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
			errorEnvelope(w, http.StatusConflict, "AUTH_EMAIL_TAKEN", "That email is already registered.")
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
	setSessionCookie(w, r, token, expires)
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
	key := auth.LoginKey(auth.ClientIP(r.RemoteAddr), auth.NormalizeEmail(req.Email))
	if allowed, retryAfter := s.login.Allow(key); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		errorEnvelope(w, http.StatusTooManyRequests, "AUTH_RATE_LIMITED", "Too many failed sign-in attempts. Try again later.")
		return
	}
	u, err := auth.Authenticate(r.Context(), s.store, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			s.login.Fail(key)
			errorEnvelope(w, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "Email or password is incorrect.")
			return
		}
		persistenceFailed(w, err)
		return
	}
	// A successful sign-in clears the pair's failures: the caller has proven it
	// holds the password, so the budget it spent guessing is irrelevant now.
	s.login.Reset(key)
	token, expires, err := auth.StartSession(r.Context(), s.store, u.ID)
	if err != nil {
		persistenceFailed(w, err)
		return
	}
	setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, authResponse{OK: true, User: u, Token: token, ExpiresAt: expires})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token, _ := sessionToken(r)
	if token != "" {
		if err := s.store.DeleteSession(r.Context(), token); err != nil {
			persistenceFailed(w, err)
			return
		}
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
}
