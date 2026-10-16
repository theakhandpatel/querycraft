package ai

import (
	"testing"
	"time"
)

// TestAvailabilityAdvertisesTheRunBudget pins the number a client waits on.
//
// A provider-backed call runs for minutes, so a browser cannot use the same
// deadline it uses for a query: /capabilities advertises the pipeline's budget
// and the transport outlasts it (web/src/api/execution/). Advertising zero
// would send a client back to its own guess, which is how a 72-second course
// generation was reported as an unreachable gateway.
func TestAvailabilityAdvertisesTheRunBudget(t *testing.T) {
	// No provider: generation cannot run, and the budget is still described.
	off := NewService(Config{}).Available()
	if want := DefaultLimits().Timeout.Milliseconds(); off.TimeoutMS != want {
		t.Fatalf("TimeoutMS = %d with no provider, want the default %d", off.TimeoutMS, want)
	}
	if off.Available {
		t.Fatalf("Available() = %+v, want unavailable", off)
	}

	// Configured limits are what is advertised: a client must wait for the
	// budget this gateway enforces, not the package default.
	budget := 7 * time.Minute
	on := NewService(Config{Provider: NewFakeProvider(), Depot: newTestDepot(), Limits: Limits{Timeout: budget}}).Available()
	if on.TimeoutMS != budget.Milliseconds() {
		t.Fatalf("TimeoutMS = %d, want the configured %d", on.TimeoutMS, budget.Milliseconds())
	}
	if on.TimeoutMS <= 0 {
		t.Fatal("a client cannot outlast a budget of zero")
	}
}
