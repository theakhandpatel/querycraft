package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/auth"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

func signupBody(email, name, password string) string {
	b, _ := json.Marshal(map[string]string{"email": email, "name": name, "password": password})
	return string(b)
}

type authOut struct {
	OK        bool       `json:"ok"`
	User      store.User `json:"user"`
	Token     string     `json:"token"`
	ExpiresAt string     `json:"expiresAt"`
}

func decodeAuthResponse(t *testing.T, body []byte) authOut {
	t.Helper()
	var out authOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode auth response: %v (body %s)", err, body)
	}
	return out
}

func TestSignupCreatesLearner(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	w := do(t, s, http.MethodPost, "/auth/signup", signupBody("New.User@Example.TEST", "New User", "a-good-passphrase"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	out := decodeAuthResponse(t, w.Body.Bytes())
	if !out.OK || out.Token == "" || out.ExpiresAt == "" {
		t.Fatalf("incomplete auth response: %+v", out)
	}
	if out.User.Role != store.RoleLearner {
		t.Fatalf("role = %q, want learner", out.User.Role)
	}
	if out.User.Email != "new.user@example.test" {
		t.Fatalf("email was not normalized: %q", out.User.Email)
	}
	if out.User.ID == "" || out.User.CreatedAt.IsZero() {
		t.Fatalf("id/createdAt missing: %+v", out.User)
	}
	// The response must never carry credentials.
	if got := w.Body.String(); containsAny(got, "password", "hash") {
		t.Fatalf("auth response leaks credential material: %s", got)
	}
}

func TestSignupCannotSelfGrantAdmin(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	body := `{"email":"climber@example.test","name":"Climber","password":"a-good-passphrase","role":"admin"}`
	w := do(t, s, http.MethodPost, "/auth/signup", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if out := decodeAuthResponse(t, w.Body.Bytes()); out.User.Role != store.RoleLearner {
		t.Fatalf("a public signup granted role %q", out.User.Role)
	}
}

func TestSignupValidationAndDuplicates(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	email := "dupe@example.test"
	if w := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "First", "a-good-passphrase")); w.Code != http.StatusCreated {
		t.Fatalf("first signup status = %d body = %s", w.Code, w.Body.String())
	}

	cases := []struct {
		name string
		body string
		want int
	}{
		{"duplicate", signupBody("DUPE@example.test", "Second", "a-good-passphrase"), http.StatusConflict},
		{"bad email", signupBody("not-an-email", "N", "a-good-passphrase"), http.StatusBadRequest},
		{"blank name", signupBody("named@example.test", "   ", "a-good-passphrase"), http.StatusBadRequest},
		{"short password", signupBody("short@example.test", "S", "short"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, s, http.MethodPost, "/auth/signup", tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusConflict && !containsAny(w.Body.String(), "AUTH_EMAIL_TAKEN") {
				t.Fatalf("409 body missing AUTH_EMAIL_TAKEN: %s", w.Body.String())
			}
		})
	}
}

func TestLoginAndLogout(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	email, password := "login@example.test", "a-good-passphrase"
	if w := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "Login", password)); w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d", w.Code)
	}

	// A wrong password and an unknown address must be indistinguishable.
	wrong := do(t, s, http.MethodPost, "/auth/login", `{"email":"`+email+`","password":"wrong-password"}`)
	unknown := do(t, s, http.MethodPost, "/auth/login", `{"email":"nobody@example.test","password":"wrong-password"}`)
	if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("wrong=%d unknown=%d, want 401 both", wrong.Code, unknown.Code)
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("login leaks whether an account exists:\n wrong: %s\nunknown: %s", wrong.Body.String(), unknown.Body.String())
	}
	if !containsAny(wrong.Body.String(), "AUTH_INVALID_CREDENTIALS") {
		t.Fatalf("missing AUTH_INVALID_CREDENTIALS: %s", wrong.Body.String())
	}

	ok := do(t, s, http.MethodPost, "/auth/login", `{"email":"`+email+`","password":"`+password+`"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", ok.Code, ok.Body.String())
	}
	token := decodeAuthResponse(t, ok.Body.Bytes()).Token

	me := doAs(t, s, token, http.MethodGet, "/auth/me", "")
	if me.Code != http.StatusOK {
		t.Fatalf("me status = %d body = %s", me.Code, me.Body.String())
	}
	var meOut struct {
		OK   bool       `json:"ok"`
		User store.User `json:"user"`
	}
	mustDecode(t, me, &meOut)
	if !meOut.OK || meOut.User.Email != email {
		t.Fatalf("unexpected me response: %+v", meOut)
	}

	if out := doAs(t, s, token, http.MethodPost, "/auth/logout", ""); out.Code != http.StatusOK {
		t.Fatalf("logout status = %d body = %s", out.Code, out.Body.String())
	}
	if after := doAs(t, s, token, http.MethodGet, "/auth/me", ""); after.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still resolved: %d %s", after.Code, after.Body.String())
	}
	// Logout needs the session it revokes: a revoked or absent token is
	// anonymous, and an anonymous caller gets the same 401 as on every other
	// required route.
	if out := doAs(t, s, token, http.MethodPost, "/auth/logout", ""); out.Code != http.StatusUnauthorized {
		t.Fatalf("revoked-token logout status = %d, want 401; body = %s", out.Code, out.Body.String())
	}
	if out := doAs(t, s, "", http.MethodPost, "/auth/logout", ""); out.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous logout status = %d, want 401; body = %s", out.Code, out.Body.String())
	}
}

func TestAuthRequiredOnScopedEndpoints(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/progress/sql-analytics", ""},
		{http.MethodPut, "/progress/sql-analytics", `{"version":1}`},
		{http.MethodPost, "/events", `{"id":"e1"}`},
		{http.MethodGet, "/auth/me", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := do(t, s, tc.method, tc.path, tc.body)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %s)", w.Code, w.Body.String())
			}
			if !containsAny(w.Body.String(), "AUTH_REQUIRED") {
				t.Fatalf("missing AUTH_REQUIRED: %s", w.Body.String())
			}
		})
	}

	// A syntactically plausible but unknown token is anonymous, not a 500.
	garbage := doAs(t, s, "not-a-real-token", http.MethodGet, "/progress/sql-analytics", "")
	if garbage.Code != http.StatusUnauthorized {
		t.Fatalf("bogus token status = %d, want 401", garbage.Code)
	}
}

// Every route the table marks required refuses an anonymous caller before its
// handler runs, with the one code the client branches on. A route that skipped
// the table would still be closed, because the gate treats an undeclared
// pattern as required.
func TestRequiredRoutesRefuseAnonymousCallers(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	for _, rt := range s.routes() {
		if rt.auth != authRequired {
			continue
		}
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			w := do(t, s, rt.method, rt.path, "")
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %s)", w.Code, w.Body.String())
			}
			if !containsAny(w.Body.String(), "AUTH_REQUIRED") {
				t.Fatalf("missing AUTH_REQUIRED: %s", w.Body.String())
			}
		})
	}
}

// Progress must be invisible across accounts even when both name the same
// course, and the owner always comes from the session, never the body.
func TestProgressIsOwnerScoped(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	alice := signupUser(t, s, "alice@example.test")
	bob := signupUser(t, s, "bob@example.test")

	if w := doAs(t, s, alice, http.MethodPut, "/progress/sql-analytics", `{"version":5,"passed":{"a":true}}`); w.Code != http.StatusOK {
		t.Fatalf("alice put status = %d body = %s", w.Code, w.Body.String())
	}
	if w := doAs(t, s, bob, http.MethodPut, "/progress/sql-analytics", `{"version":5,"passed":{"b":true}}`); w.Code != http.StatusOK {
		t.Fatalf("bob put status = %d body = %s", w.Code, w.Body.String())
	}

	read := func(token string) string {
		w := doAs(t, s, token, http.MethodGet, "/progress/sql-analytics", "")
		if w.Code != http.StatusOK {
			t.Fatalf("get status = %d body = %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	aliceBody, bobBody := read(alice), read(bob)
	if !containsAny(aliceBody, `"a":true`) || containsAny(aliceBody, `"b":true`) {
		t.Fatalf("alice saw the wrong snapshot: %s", aliceBody)
	}
	if !containsAny(bobBody, `"b":true`) || containsAny(bobBody, `"a":true`) {
		t.Fatalf("bob saw the wrong snapshot: %s", bobBody)
	}

	// A body that names another account cannot redirect the write.
	spoof := `{"version":5,"user_id":"` + bob + `","passed":{"stolen":true}}`
	if w := doAs(t, s, alice, http.MethodPut, "/progress/spoofed", spoof); w.Code != http.StatusOK {
		t.Fatalf("spoofed put status = %d", w.Code)
	}
	if body := read(alice); containsAny(body, "stolen") {
		t.Fatalf("unexpected: spoofed body shape read back as %s", body)
	}
	spoofedForBob := doAs(t, s, bob, http.MethodGet, "/progress/spoofed", "")
	if containsAny(spoofedForBob.Body.String(), "stolen") {
		t.Fatalf("a body field redirected a write to another owner: %s", spoofedForBob.Body.String())
	}
}

// The install seeds an administrator whose password may be empty, and that
// account must be able to sign in without any environment configuration.
func TestDefaultAdminCanSignIn(t *testing.T) {
	st := store.NewMemory()
	log := slog.New(slog.NewTextHandler(nopWriter{}, nil))
	if _, err := auth.EnsureDefaultAdmin(context.Background(), st, auth.DefaultAdmin{
		Email: "admin@querycraft.local", Password: "", Name: "Administrator",
	}, log); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	// Idempotent: a second boot must not fail or duplicate.
	if _, err := auth.EnsureDefaultAdmin(context.Background(), st, auth.DefaultAdmin{
		Email: "Admin@Querycraft.Local", Password: "", Name: "Administrator",
	}, log); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if n, err := st.CountUsers(context.Background()); err != nil || n != 1 {
		t.Fatalf("count = %d err = %v, want exactly one admin", n, err)
	}

	s := New(&fakeExecutor{}, st, Config{MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2}, log)
	w := do(t, s, http.MethodPost, "/auth/login", `{"email":"admin@querycraft.local","password":""}`)
	if w.Code != http.StatusOK {
		t.Fatalf("admin login status = %d body = %s", w.Code, w.Body.String())
	}
	out := decodeAuthResponse(t, w.Body.Bytes())
	if out.User.Role != store.RoleAdmin {
		t.Fatalf("role = %q, want admin", out.User.Role)
	}
	// A non-empty password must not match an empty stored hash.
	bad := do(t, s, http.MethodPost, "/auth/login", `{"email":"admin@querycraft.local","password":"guess"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("guess against an empty password returned %d", bad.Code)
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if len(n) > 0 && indexOf(haystack, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
