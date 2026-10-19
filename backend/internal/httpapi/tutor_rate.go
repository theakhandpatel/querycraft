package httpapi

import (
	"sync"
	"time"
)

/* The tutor rate limit.
 *
 * A tutor turn is a provider call the learner can start at will, so it is
 * bounded per account. The bucket is keyed by account id: a shared bucket would
 * let one learner spend another's budget, and an address-keyed bucket would
 * punish a whole classroom behind one NAT. The refusal is AI_RATE_LIMITED, a
 * 429, which is a different fact from AI_UNAVAILABLE: the provider is up.
 */

// tutorRate is a token bucket per account. The zero value is disabled.
type tutorRate struct {
	capacity float64 // tokens a full bucket holds
	refill   float64 // tokens added per second
	tracked  int     // most accounts held at once
	now      func() time.Time

	mu      sync.Mutex
	buckets map[string]*tutorBucket
}

type tutorBucket struct {
	tokens float64
	last   time.Time
}

// newTutorRate builds the limiter. perHour is the budget; a non-positive value
// falls back to the shipped default, so leaving it unset cannot disable the
// limit.
func newTutorRate(perHour, tracked int) *tutorRate {
	if perHour <= 0 {
		perHour = defaultTutorTurnsPerHour
	}
	if tracked <= 0 {
		tracked = defaultTutorTrackedAccounts
	}
	return &tutorRate{
		capacity: float64(perHour),
		refill:   float64(perHour) / 3600,
		tracked:  tracked,
		now:      time.Now,
		buckets:  make(map[string]*tutorBucket),
	}
}

const (
	defaultTutorTurnsPerHour    = 30
	defaultTutorTrackedAccounts = 4096
)

// allow spends one token for an account, reporting whether one was available. A
// new account starts with a full bucket; a bucket is never evicted while held,
// so a burst cannot reset the budget.
func (r *tutorRate) allow(accountID string) bool {
	if r == nil || r.capacity <= 0 {
		return true
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[accountID]
	if !ok {
		// A full table is not a reason to lock a new learner out: the map is
		// just an eviction bound, and the honest answer to "too many tracked
		// accounts" is to serve the request, not to invent a refusal.
		if len(r.buckets) >= r.tracked {
			return true
		}
		b = &tutorBucket{tokens: r.capacity, last: now}
		r.buckets[accountID] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * r.refill
		if b.tokens > r.capacity {
			b.tokens = r.capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
