package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

/* The throttle's own tests: the window, the bound and the fallback, driven
 * directly because there is no HTTP-visible difference between "the entry is
 * still there" and "the entry is gone" at the moment it goes. The endpoint
 * behaviour built on top of the counter is exercised through the real handler
 * in internal/httpapi (login_throttle_test.go). */

// TestLoginThrottleDropsExpiredEntries pins the window directly: a blocked pair
// is allowed again once its window has passed, and an expired record is removed
// rather than lingering in the table.
func TestLoginThrottleDropsExpiredEntries(t *testing.T) {
	th := NewThrottle(2, 1000, 100)
	now := time.Unix(1_700_000_000, 0)
	th.now = func() time.Time { return now }

	th.Fail("pair")
	th.Fail("pair")
	if ok, _ := th.Allow("pair"); ok {
		t.Fatal("a pair over its limit was allowed")
	}
	if th.Size() != 1 {
		t.Fatalf("size = %d, want 1", th.Size())
	}

	now = now.Add(2 * time.Second) // past the 1s window
	if ok, retry := th.Allow("pair"); !ok || retry != 0 {
		t.Fatalf("allow after the window = (%v, %d), want (true, 0)", ok, retry)
	}
	if th.Size() != 0 {
		t.Fatalf("the expired record was not dropped: size = %d", th.Size())
	}

	// Expired records are also purged when the table is at its cap, so an
	// attacker cannot pin old keys by filling it.
	th = NewThrottle(2, 1000, 2)
	th.now = func() time.Time { return now }
	th.Fail("old-1")
	th.Fail("old-2")
	now = now.Add(2 * time.Second)
	th.Fail("fresh")
	if th.Size() != 1 {
		t.Fatalf("after expiry-at-cap size = %d, want only the fresh pair", th.Size())
	}
}

// TestLoginThrottleDefaultsAreSane guards the fallback: a caller that leaves the
// numbers unset must still throttle with the shipped ones, so it cannot
// accidentally disable the limit or leave the table unbounded by passing zero.
func TestLoginThrottleDefaultsAreSane(t *testing.T) {
	th := NewThrottle(0, 0, 0)
	if th.maxFailures != DefaultLoginMaxFailures {
		t.Fatalf("maxFailures = %d, want %d", th.maxFailures, DefaultLoginMaxFailures)
	}
	if th.maxKeys != DefaultLoginMaxTracked {
		t.Fatalf("maxKeys = %d, want %d", th.maxKeys, DefaultLoginMaxTracked)
	}
	if th.window != time.Duration(DefaultLoginWindowMS)*time.Millisecond {
		t.Fatalf("window = %s, want %dms", th.window, DefaultLoginWindowMS)
	}
	// A non-positive value must not remove the limit either.
	th.Fail("k")
	if ok, _ := th.Allow("k"); !ok {
		t.Fatal("one failure should not block")
	}
	for i := 1; i < DefaultLoginMaxFailures; i++ {
		th.Fail("k")
	}
	if ok, retry := th.Allow("k"); ok || retry < 1 {
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
	th := NewThrottle(maxFailures, 60_000, maxTracked)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("203.0.113.%d\x00u%d@example.test", w, w)
			for i := 0; i < 50; i++ {
				th.Allow(key)
				th.Fail(key)
				// Reset periodically to exercise that path under contention, but
				// only in the first half: the last 25 iterations are a clean run
				// of failures the final assertion can rely on.
				if i < 25 && i%7 == 5 {
					th.Reset(key)
				}
				if th.Size() > maxTracked {
					t.Errorf("table grew past its cap: %d > %d", th.Size(), maxTracked)
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
		if ok, _ := th.Allow(key); ok {
			t.Fatalf("worker %d's pair was allowed after %d failures", w, 50)
		}
	}
}
