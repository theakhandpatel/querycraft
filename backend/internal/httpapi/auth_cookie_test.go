package httpapi

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/store"
)

/*
auth_cookie_test.go covers the cookie transport: the cookie sign-in sets and
its attributes, the resolution order between the Authorization header and the
cookie, the same-origin check a cookie-authenticated mutating request passes,
and the log-out clear. The bearer path is exercised by auth_test.go.

Every case drives the real Handler() with httptest, so the middleware chain the
gateway serves is the one under test.
*/

// sessionCookieFrom returns the qc_session cookie a response set, or fails the
// test when the response set none.
func sessionCookieFrom(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie among %v", sessionCookieName, w.Result().Cookies())
	return nil
}

// signupCookie creates an account through the real endpoint and returns the
// qc_session cookie it set.
func signupCookie(t *testing.T, s *Server, email string) *http.Cookie {
	t.Helper()
	w := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "Cookie Learner", "correct-horse"))
	if w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d body = %s", w.Code, w.Body.String())
	}
	return sessionCookieFrom(t, w)
}

// doCookieReq is do with a session cookie attached and extra headers set. A nil
// cookie sends none, which is the anonymous case.
func doCookieReq(t *testing.T, s *Server, cookie *http.Cookie, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// assertSessionCookie checks the attributes every sign-in cookie carries and
// returns it for an expiry comparison.
func assertSessionCookie(t *testing.T, w *httptest.ResponseRecorder, token string) *http.Cookie {
	t.Helper()
	c := sessionCookieFrom(t, w)
	if c.Value != token {
		t.Fatalf("cookie value = %q, want the session token", c.Value)
	}
	if !c.HttpOnly {
		t.Fatalf("cookie is not HttpOnly: %+v", c)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Fatalf("Path = %q, want /", c.Path)
	}
	if c.Secure {
		t.Fatalf("Secure set on a plaintext request")
	}
	if c.Expires.IsZero() {
		t.Fatalf("cookie carries no expiry")
	}
	return c
}

// Signup and login both set the cookie, and its expiry is the session TTL the
// body reports. The token stays in the body for API clients.
func TestSignInSetsSessionCookie(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	email, password := "cookie@example.test", "correct-horse"

	signup := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "Cookie Learner", password))
	if signup.Code != http.StatusCreated {
		t.Fatalf("signup status = %d body = %s", signup.Code, signup.Body.String())
	}
	assertSessionCookie(t, signup, decodeAuthResponse(t, signup.Body.Bytes()).Token)

	login := do(t, s, http.MethodPost, "/auth/login", `{"email":"`+email+`","password":"`+password+`"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", login.Code, login.Body.String())
	}
	out := decodeAuthResponse(t, login.Body.Bytes())
	c := assertSessionCookie(t, login, out.Token)

	wantExpires, err := time.Parse(time.RFC3339Nano, out.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiresAt %q: %v", out.ExpiresAt, err)
	}
	// Set-Cookie truncates to whole seconds, so compare at that resolution.
	if !c.Expires.Truncate(time.Second).Equal(wantExpires.Truncate(time.Second)) {
		t.Fatalf("cookie expiry %s does not match the session expiry %s", c.Expires, wantExpires)
	}
}

// Secure is added only when the request arrived over TLS, so a plaintext local
// install still receives a cookie.
func TestSessionCookieIsSecureOverTLS(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	email, password := "tls@example.test", "correct-horse"
	if w := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "TLS", password)); w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d body = %s", w.Code, w.Body.String())
	}

	r := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"email":"`+email+`","password":"`+password+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", w.Code, w.Body.String())
	}
	if c := sessionCookieFrom(t, w); !c.Secure {
		t.Fatalf("Secure not set on a TLS request: %+v", c)
	}
}

// The cookie alone resolves the account: no Authorization header is needed.
func TestCookieAloneAuthorizesGet(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	email := "cookie.get@example.test"
	cookie := signupCookie(t, s, email)

	w := doCookieReq(t, s, cookie, http.MethodGet, "/auth/me", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("me status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		OK   bool       `json:"ok"`
		User store.User `json:"user"`
	}
	mustDecode(t, w, &out)
	if !out.OK || out.User.Email != email {
		t.Fatalf("unexpected me response: %+v", out)
	}
}

// Logout revokes the session and clears the cookie in the response.
func TestLogoutClearsSessionCookie(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	cookie := signupCookie(t, s, "cookie.logout@example.test")

	w := doCookieReq(t, s, cookie, http.MethodPost, "/auth/logout", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("logout status = %d body = %s", w.Code, w.Body.String())
	}
	cleared := sessionCookieFrom(t, w)
	if cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("logout cookie not cleared: value=%q maxAge=%d", cleared.Value, cleared.MaxAge)
	}

	after := doCookieReq(t, s, cookie, http.MethodGet, "/auth/me", "", nil)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie still resolved: %d %s", after.Code, after.Body.String())
	}
}

// A cookie-authenticated mutating request is refused when the browser headers
// say it came from another site, and allowed when they say it is same-origin.
// Neither header is a non-browser client, which is allowed.
func TestCookieMutatingRequestSameOriginCheck(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	cookie := signupCookie(t, s, "cookie.csrf@example.test")
	body := `{"id":"ex.1","kind":"passed"}`

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"cross-site Origin", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"cross-site Sec-Fetch-Site", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"Sec-Fetch-Site none", map[string]string{"Sec-Fetch-Site": "none"}, http.StatusForbidden},
		{"same-origin Origin", map[string]string{"Origin": "http://example.com"}, http.StatusAccepted},
		{"same-origin Sec-Fetch-Site", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusAccepted},
		{"no browser headers", nil, http.StatusAccepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doCookieReq(t, s, cookie, http.MethodPost, "/events", body, tc.headers)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d body = %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden && !containsAny(w.Body.String(), "CSRF_REFUSED") {
				t.Fatalf("403 body missing CSRF_REFUSED: %s", w.Body.String())
			}
		})
	}

	// A safe method is exempt: a cross-site navigation still renders.
	t.Run("GET is exempt", func(t *testing.T) {
		w := doCookieReq(t, s, cookie, http.MethodGet, "/auth/me", "",
			map[string]string{"Origin": "http://evil.example", "Sec-Fetch-Site": "cross-site"})
		if w.Code != http.StatusOK {
			t.Fatalf("cross-site GET status = %d, want 200 body = %s", w.Code, w.Body.String())
		}
	})
}

// A bearer-authenticated request is explicit, so the same-origin check never
// applies, even when a cookie is also present and the Origin is cross-site.
func TestBearerIgnoresSameOriginCheck(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	w := do(t, s, http.MethodPost, "/auth/signup", signupBody("bearer.csrf@example.test", "Bearer", "correct-horse"))
	if w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d body = %s", w.Code, w.Body.String())
	}
	token := decodeAuthResponse(t, w.Body.Bytes()).Token
	cookie := sessionCookieFrom(t, w)

	got := doCookieReq(t, s, cookie, http.MethodPost, "/events", `{"id":"ex.1"}`,
		map[string]string{"Authorization": "Bearer " + token, "Origin": "http://evil.example", "Sec-Fetch-Site": "cross-site"})
	if got.Code != http.StatusAccepted {
		t.Fatalf("bearer request status = %d, want 202 body = %s", got.Code, got.Body.String())
	}
}

// A present-but-invalid Bearer must not fall back to a valid cookie.
func TestInvalidBearerDoesNotFallBackToCookie(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	cookie := signupCookie(t, s, "badtoken@example.test")

	w := doCookieReq(t, s, cookie, http.MethodGet, "/auth/me", "",
		map[string]string{"Authorization": "Bearer not-a-real-token"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 body = %s", w.Code, w.Body.String())
	}
	if !containsAny(w.Body.String(), "AUTH_REQUIRED") {
		t.Fatalf("401 body missing AUTH_REQUIRED: %s", w.Body.String())
	}
}
