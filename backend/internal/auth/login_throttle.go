package auth

import (
	"net"
	"strings"
	"sync"
	"time"
)

/* Failed sign-in throttling.
 *
 * POST /auth/login is the one endpoint where an anonymous caller can spend the
 * server's time guessing a secret, and the seeded administrator's address is
 * public knowledge (admin@querycraft.local), so a known address plus no limit
 * is a brute-forceable pair. This is the whole defence: an in-process, bounded
 * table of failures per (client address, account), counted over a fixed window
 * and forgotten when the window rolls over or the sign-in succeeds.
 *
 * It is deliberately not a rate-limiting framework. There is no dependency to
 * add, no shared store to run, and nothing to configure beyond three numbers.
 * What it cannot do is stated rather than implied:
 *
 *   - It does not survive a restart and is not shared between processes, so two
 *     gateways behind one address keep a budget each.
 *   - It keys on the socket address, never on X-Forwarded-For: a caller-supplied
 *     header is a budget an attacker can mint at will, so trusting it would make
 *     the limit decorative.
 *   - It is a speed bump on one source guessing one account, not a distributed
 *     defence. A caller who can force many distinct keys can evict an entry;
 *     bounding the table is the trade that keeps the map from itself becoming
 *     the denial of service.
 *
 * It never stores the attempted password. The key is the address and the
 * normalized email; the value is a count and a window start.
 */

// The shipped defaults. They are what NewThrottle applies when a Config leaves
// the login fields unset, and the values cmd/api reads for QC_LOGIN_*.
const (
	// DefaultLoginMaxFailures is how many failed sign-ins one (address, email)
	// pair may accumulate inside the window before the next attempt is refused.
	DefaultLoginMaxFailures = 5
	// DefaultLoginWindowMS is the fixed window, in milliseconds, that a pair's
	// failure count is measured over.
	DefaultLoginWindowMS = 15 * 60 * 1000
	// DefaultLoginMaxTracked bounds the throttle's memory: the number of
	// distinct (address, email) pairs it will hold at once.
	DefaultLoginMaxTracked = 4096
)

// loginFailure is one pair's record: how many failures inside the window, and
// when that window opened. There is deliberately no field for the password.
type loginFailure struct {
	count       int
	windowStart time.Time
}

// Throttle is a fixed-window failure counter keyed by client address and
// normalized email. The zero value is unusable; call NewThrottle.
//
// It is safe for concurrent use: every read and write of the map happens under
// the mutex, so two parallel sign-ins cannot race the same counter.
type Throttle struct {
	maxFailures int
	window      time.Duration
	maxKeys     int

	// now is time.Now in production and an injected clock in tests, so a test
	// can roll the window over without sleeping for it.
	now func() time.Time

	mu      sync.Mutex
	entries map[string]*loginFailure
}

// NewThrottle builds a throttle. Non-positive arguments fall back to the
// shipped defaults, so a caller that passes nothing gets the documented
// behaviour rather than an unlimited or unbounded one.
func NewThrottle(maxFailures, windowMS, maxKeys int) *Throttle {
	if maxFailures <= 0 {
		maxFailures = DefaultLoginMaxFailures
	}
	if windowMS <= 0 {
		windowMS = DefaultLoginWindowMS
	}
	if maxKeys <= 0 {
		maxKeys = DefaultLoginMaxTracked
	}
	return &Throttle{
		maxFailures: maxFailures,
		window:      time.Duration(windowMS) * time.Millisecond,
		maxKeys:     maxKeys,
		now:         time.Now,
		entries:     make(map[string]*loginFailure),
	}
}

// LoginKey names one budget: the caller's address and the account it named,
// lowercased and trimmed so casing cannot mint a second budget. NUL is the
// separator because an email cannot contain it.
func LoginKey(ip, email string) string {
	return ip + "\x00" + email
}

// ClientIP is the caller's address as the kernel reports it, without the port.
// It reads the socket address only: X-Forwarded-For is caller-supplied and
// would let an attacker choose a fresh budget per request.
func ClientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return strings.TrimSpace(remoteAddr)
	}
	return host
}

// Allow reports whether an attempt may proceed, and when it may not, how many
// whole seconds until the window ends. It does not count the attempt: a caller
// that hammers the endpoint while blocked does not extend its own window, and
// neither does it delay the unlucky learner's recovery.
func (t *Throttle) Allow(key string) (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.entries) >= t.maxKeys {
		// A table at its cap may be full of windows that have already ended.
		// Sweep those before turning anyone away, so a burst of ancient
		// failures cannot deny a fresh caller a budget it should have.
		t.dropExpiredLocked(now)
	}
	e := t.entries[key]
	if e == nil {
		return true, 0
	}
	elapsed := now.Sub(e.windowStart)
	if elapsed >= t.window {
		// The window has rolled over; the record is stale, so the attempt
		// counts afresh. Dropping it here keeps the table from accumulating
		// expired rows between eviction passes.
		delete(t.entries, key)
		return true, 0
	}
	if e.count < t.maxFailures {
		return true, 0
	}
	return false, retryAfterSeconds(t.window - elapsed)
}

// Fail records one failed attempt for the pair. It bounds the table before it
// grows: a new key makes room first.
func (t *Throttle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	e := t.entries[key]
	if e == nil || now.Sub(e.windowStart) >= t.window {
		t.makeRoomLocked(now)
		t.entries[key] = &loginFailure{count: 1, windowStart: now}
		return
	}
	e.count++
}

// Reset forgets a pair's failures. A sign-in that worked is the proof the
// caller is not an attacker, so the record goes.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

// Size reports how many pairs are tracked, for tests and diagnostics.
func (t *Throttle) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// dropExpiredLocked removes every record whose window has ended.
func (t *Throttle) dropExpiredLocked(now time.Time) {
	for key, e := range t.entries {
		if now.Sub(e.windowStart) >= t.window {
			delete(t.entries, key)
		}
	}
}

// makeRoomLocked keeps the table bounded before a new key is inserted. Expired
// records go first; if it is still at the cap, as when an attacker invents key
// after key, the record closest to expiry is dropped so the table can never
// exceed maxKeys. That is last-write wins: the newest source is tracked, and an old
// counter may be lost. It is the honest bound, because the alternative to a
// bound is a map an attacker grows until the process dies.
func (t *Throttle) makeRoomLocked(now time.Time) {
	if len(t.entries) < t.maxKeys {
		return
	}
	t.dropExpiredLocked(now)
	for len(t.entries) >= t.maxKeys {
		oldestKey := ""
		var oldest time.Time
		for key, e := range t.entries {
			if oldestKey == "" || e.windowStart.Before(oldest) {
				oldestKey, oldest = key, e.windowStart
			}
		}
		if oldestKey == "" {
			return
		}
		delete(t.entries, oldestKey)
	}
}

// retryAfterSeconds rounds up to whole seconds and never reports zero, so a
// Retry-After header never advertises "retry immediately".
func retryAfterSeconds(d time.Duration) int {
	s := int(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	if s < 1 {
		s = 1
	}
	return s
}
