package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/auth"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* Failed sign-in throttling, exercised through the real handler exactly as
 * auth_test.go does. The throttle is part of Server, so these tests speak HTTP
 * rather than calling the counter directly: what has to hold is the endpoint's
 * behaviour — a 429 after N failures, a 401 below the limit, a success that
 * clears the count, and a source or account that is unaffected. The bound test
 * reads the Server's table through auth.Throttle.Size, because there is no
 * HTTP-visible difference between "the entry is still there" and "the entry is
 * gone" at the moment it goes. The window and concurrency tests drive the
 * counter directly and live beside it in internal/auth. */

func loginBody(email, password string) string {
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	return string(b)
}

// doFromIP is do with an explicit client address. httptest.NewRequest fills
// RemoteAddr itself, and the throttle keys on exactly that, so a test that needs
// two sources must set it.
func doFromIP(t *testing.T, s *Server, ip, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.RemoteAddr = ip + ":54321"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestLoginThrottleRefusesAfterFailures runs the shipped default budget: N
// failures answer the ordinary 401, the next attempt is refused with 429
// AUTH_RATE_LIMITED and a Retry-After, and the refusal cannot be used to tell a
// real address from an invented one.
func TestLoginThrottleRefusesAfterFailures(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	email, password := "throttle@example.test", "a-good-passphrase"
	if w := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "Throttle", password)); w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d body = %s", w.Code, w.Body.String())
	}
	const ip = "203.0.113.7"

	// Below the limit the answer is the ordinary 401, unchanged, and the body
	// never echoes the attempted password.
	for i := 0; i < auth.DefaultLoginMaxFailures; i++ {
		w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, "wrong-password"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d, want 401 (body %s)", i+1, w.Code, w.Body.String())
		}
		if containsAny(w.Body.String(), "wrong-password") {
			t.Fatalf("failure %d echoed the attempted password: %s", i+1, w.Body.String())
		}
	}

	blocked := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, "wrong-password"))
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("status after %d failures = %d, want 429 (body %s)", auth.DefaultLoginMaxFailures, blocked.Code, blocked.Body.String())
	}
	if !containsAny(blocked.Body.String(), "AUTH_RATE_LIMITED") {
		t.Fatalf("429 body missing AUTH_RATE_LIMITED: %s", blocked.Body.String())
	}
	ra := blocked.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("429 without a Retry-After header")
	}
	if n, err := strconv.Atoi(ra); err != nil || n < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer", ra)
	}

	// The refusal must not leak whether the account exists: exhausting the
	// budget for an address that was never registered produces a byte-identical
	// 429, because the throttle never consults the store.
	const unknownIP = "198.51.100.9"
	var unknown *httptest.ResponseRecorder
	for i := 0; i <= auth.DefaultLoginMaxFailures; i++ {
		unknown = doFromIP(t, s, unknownIP, http.MethodPost, "/auth/login", loginBody("nobody@example.test", "wrong-password"))
		if i < auth.DefaultLoginMaxFailures && unknown.Code != http.StatusUnauthorized {
			t.Fatalf("unknown-account failure %d status = %d, want 401", i+1, unknown.Code)
		}
	}
	if unknown.Code != http.StatusTooManyRequests {
		t.Fatalf("unknown-account status = %d, want 429 (body %s)", unknown.Code, unknown.Body.String())
	}
	if unknown.Body.String() != blocked.Body.String() {
		t.Fatalf("429 leaks whether the address exists:\n known: %s\nunknown: %s", blocked.Body.String(), unknown.Body.String())
	}
}

// TestLoginThrottleSuccessResets proves the counter is cleared by a sign-in that
// works: the budget that was almost spent is available again afterwards.
func TestLoginThrottleSuccessResets(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	email, password := "reset@example.test", "a-good-passphrase"
	if w := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "Reset", password)); w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d", w.Code)
	}
	const ip = "203.0.113.20"

	// Spend every failure the budget allows *except one* — the reserve is what
	// lets the real password through — and then sign in for real. The sign-in
	// must succeed, which it can only do while the pair is not over the limit.
	for i := 0; i < auth.DefaultLoginMaxFailures-1; i++ {
		if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, "wrong-password")); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d, want 401", i+1, w.Code)
		}
	}
	if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, password)); w.Code != http.StatusOK {
		t.Fatalf("login after %d failures status = %d body = %s", auth.DefaultLoginMaxFailures-1, w.Code, w.Body.String())
	}

	// The success cleared the count. Were that not so, the pair would now stand
	// one failure from the limit and the next failure would answer 429; instead
	// a whole fresh budget of failures is available before the limit is felt.
	for i := 0; i < auth.DefaultLoginMaxFailures; i++ {
		if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, "wrong-password")); w.Code != http.StatusUnauthorized {
			t.Fatalf("post-reset failure %d status = %d, want 401 — the success did not clear the count", i+1, w.Code)
		}
	}
	if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, "wrong-password")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("status after a fresh budget = %d, want 429", w.Code)
	}

	// A success at the limit itself is still refused: the point of the throttle
	// is that a caller who has spent the budget waits, even holding the password.
	if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, password)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a correct password over the limit = %d, want 429", w.Code)
	}
}

// TestLoginThrottleIsKeyedByAddressAndAccount shows the budget is per pair: a
// different source, or the same source naming a different account, is unaffected.
func TestLoginThrottleIsKeyedByAddressAndAccount(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	alice, bob := "alice@example.test", "bob@example.test"
	for _, email := range []string{alice, bob} {
		if w := do(t, s, http.MethodPost, "/auth/signup", signupBody(email, "Person", "a-good-passphrase")); w.Code != http.StatusCreated {
			t.Fatalf("signup %s status = %d", email, w.Code)
		}
	}
	const ip = "203.0.113.30"

	for i := 0; i < auth.DefaultLoginMaxFailures; i++ {
		if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(alice, "wrong-password")); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d, want 401", i+1, w.Code)
		}
	}
	if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(alice, "wrong-password")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("alice at the exhausted source = %d, want 429", w.Code)
	}

	// A different source guessing alice's password is a different budget.
	if w := doFromIP(t, s, "203.0.113.31", http.MethodPost, "/auth/login", loginBody(alice, "wrong-password")); w.Code != http.StatusUnauthorized {
		t.Fatalf("another source = %d, want 401 (body %s)", w.Code, w.Body.String())
	}
	// The same source naming another account is a different budget.
	if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(bob, "wrong-password")); w.Code != http.StatusUnauthorized {
		t.Fatalf("another account = %d, want 401 (body %s)", w.Code, w.Body.String())
	}
	// Casing must not mint a second budget for the same account.
	if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody("ALICE@EXAMPLE.TEST", "wrong-password")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("cased address at the exhausted source = %d, want 429", w.Code)
	}
}

// TestLoginThrottleStaysBounded drives many distinct pairs through the endpoint
// and asserts the table never grows past its cap. An unbounded map is the
// denial-of-service this bound exists to prevent, so the assertion is on the
// Server's own table after every request.
func TestLoginThrottleStaysBounded(t *testing.T) {
	const maxTracked = 8
	s := New(&fakeExecutor{}, store.NewMemory(), Config{
		MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2,
		LoginMaxFailures: 3, LoginWindowMS: 60_000, LoginMaxTracked: maxTracked,
	}, nil)

	for i := 0; i < 100; i++ {
		email := fmt.Sprintf("nobody%d@example.test", i)
		doFromIP(t, s, "203.0.113.40", http.MethodPost, "/auth/login", loginBody(email, "guess"))
		if got := s.login.Size(); got > maxTracked {
			t.Fatalf("after %d pairs the table holds %d, cap is %d", i+1, got, maxTracked)
		}
	}
	if got := s.login.Size(); got != maxTracked {
		t.Fatalf("table settled at %d, want it filled to the cap %d", got, maxTracked)
	}
}
