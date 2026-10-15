package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* Failed sign-in throttling, exercised through the real handler exactly as
 * auth_test.go does. The throttle is part of Server, so these tests speak HTTP
 * rather than calling the counter directly: what has to hold is the endpoint's
 * behaviour — a 429 after N failures, a 401 below the limit, a success that
 * clears the count, and a source or account that is unaffected — not the map's
 * internal shape. The two tests that pin the bound and the window drive the
 * throttle directly, because there is no HTTP-visible difference between "the
 * entry is still there" and "the entry is gone" at the moment it goes. */

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
	for i := 0; i < DefaultLoginMaxFailures; i++ {
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
		t.Fatalf("status after %d failures = %d, want 429 (body %s)", DefaultLoginMaxFailures, blocked.Code, blocked.Body.String())
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
	for i := 0; i <= DefaultLoginMaxFailures; i++ {
		unknown = doFromIP(t, s, unknownIP, http.MethodPost, "/auth/login", loginBody("nobody@example.test", "wrong-password"))
		if i < DefaultLoginMaxFailures && unknown.Code != http.StatusUnauthorized {
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
	for i := 0; i < DefaultLoginMaxFailures-1; i++ {
		if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, "wrong-password")); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d, want 401", i+1, w.Code)
		}
	}
	if w := doFromIP(t, s, ip, http.MethodPost, "/auth/login", loginBody(email, password)); w.Code != http.StatusOK {
		t.Fatalf("login after %d failures status = %d body = %s", DefaultLoginMaxFailures-1, w.Code, w.Body.String())
	}

	// The success cleared the count. Were that not so, the pair would now stand
	// one failure from the limit and the next failure would answer 429; instead
	// a whole fresh budget of failures is available before the limit is felt.
	for i := 0; i < DefaultLoginMaxFailures; i++ {
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

	for i := 0; i < DefaultLoginMaxFailures; i++ {
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
	if s.login.maxKeys != maxTracked {
		t.Fatalf("maxKeys = %d, want %d", s.login.maxKeys, maxTracked)
	}

	for i := 0; i < 100; i++ {
		email := fmt.Sprintf("nobody%d@example.test", i)
		doFromIP(t, s, "203.0.113.40", http.MethodPost, "/auth/login", loginBody(email, "guess"))
		if got := s.login.size(); got > maxTracked {
			t.Fatalf("after %d pairs the table holds %d, cap is %d", i+1, got, maxTracked)
		}
	}
	if got := s.login.size(); got != maxTracked {
		t.Fatalf("table settled at %d, want it filled to the cap %d", got, maxTracked)
	}
}

// TestLoginThrottleDropsExpiredEntries pins the window directly: a blocked pair
// is allowed again once its window has passed, and an expired record is removed
// rather than lingering in the table.
func TestLoginThrottleDropsExpiredEntries(t *testing.T) {
	th := newLoginThrottle(2, 1000, 100)
	now := time.Unix(1_700_000_000, 0)
	th.now = func() time.Time { return now }

	th.fail("pair")
	th.fail("pair")
	if ok, _ := th.allow("pair"); ok {
		t.Fatal("a pair over its limit was allowed")
	}
	if th.size() != 1 {
		t.Fatalf("size = %d, want 1", th.size())
	}

	now = now.Add(2 * time.Second) // past the 1s window
	if ok, retry := th.allow("pair"); !ok || retry != 0 {
		t.Fatalf("allow after the window = (%v, %d), want (true, 0)", ok, retry)
	}
	if th.size() != 0 {
		t.Fatalf("the expired record was not dropped: size = %d", th.size())
	}

	// Expired records are also purged when the table is at its cap, so an
	// attacker cannot pin old keys by filling it.
	th = newLoginThrottle(2, 1000, 2)
	th.now = func() time.Time { return now }
	th.fail("old-1")
	th.fail("old-2")
	now = now.Add(2 * time.Second)
	th.fail("fresh")
	if th.size() != 1 {
		t.Fatalf("after expiry-at-cap size = %d, want only the fresh pair", th.size())
	}
}

// TestLoginThrottleDefaultsAreSane guards the fallback: a Config that leaves the
// login fields unset must still throttle with the shipped numbers, so a caller
// cannot accidentally disable the limit by passing zero.
func TestLoginThrottleDefaultsAreSane(t *testing.T) {
	s := New(&fakeExecutor{}, store.NewMemory(), Config{}, nil)
	if s.login.maxFailures != DefaultLoginMaxFailures {
		t.Fatalf("maxFailures = %d, want %d", s.login.maxFailures, DefaultLoginMaxFailures)
	}
	if s.login.maxKeys != DefaultLoginMaxTracked {
		t.Fatalf("maxKeys = %d, want %d", s.login.maxKeys, DefaultLoginMaxTracked)
	}
	if s.login.window != time.Duration(DefaultLoginWindowMS)*time.Millisecond {
		t.Fatalf("window = %s, want %dms", s.login.window, DefaultLoginWindowMS)
	}
	// A non-positive value must not remove the limit either.
	th := newLoginThrottle(0, 0, 0)
	th.fail("k")
	if ok, _ := th.allow("k"); !ok {
		t.Fatal("one failure should not block")
	}
	for i := 1; i < DefaultLoginMaxFailures; i++ {
		th.fail("k")
	}
	if ok, retry := th.allow("k"); ok || retry < 1 {
		t.Fatalf("allow after the default budget = (%v, %d), want (false, >0)", ok, retry)
	}
}

// TestLoginThrottleIsConcurrencySafe runs many goroutines through the same pairs
// at once. Any unsynchronised read or write of the map would be a data race
// (caught by -race) or a lost update, and a nil-map write would panic. The
// assertion is the invariant: the table never exceeds its cap, and a pair with
// more than maxFailures recorded failures is refused.
func TestLoginThrottleIsConcurrencySafe(t *testing.T) {
	const (
		maxFailures = 5
		maxTracked  = 16
		workers     = 8
	)
	th := newLoginThrottle(maxFailures, 60_000, maxTracked)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("203.0.113.%d\x00u%d@example.test", w, w)
			for i := 0; i < 50; i++ {
				th.allow(key)
				th.fail(key)
				// Reset periodically to exercise that path under contention, but
				// only in the first half: the last 25 iterations are a clean run
				// of failures the final assertion can rely on.
				if i < 25 && i%7 == 5 {
					th.reset(key)
				}
				if th.size() > maxTracked {
					t.Errorf("table grew past its cap: %d > %d", th.size(), maxTracked)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Every worker's last write was a failure, so the final allow must be a
	// refusal: at least maxFailures were recorded since the last reset.
	for w := 0; w < workers; w++ {
		key := fmt.Sprintf("203.0.113.%d\x00u%d@example.test", w, w)
		if ok, _ := th.allow(key); ok {
			t.Fatalf("worker %d's pair was allowed after %d failures", w, 50)
		}
	}
}
