package ai

import (
	"testing"
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
	// (The configured-budget advertisement is the capability surface's own
	// test: internal/httpapi pins the number the client waits on.)
	off := NewService(Config{}).Available()
	if want := DefaultLimits().Timeout.Milliseconds(); off.TimeoutMS != want {
		t.Fatalf("TimeoutMS = %d with no provider, want the default %d", off.TimeoutMS, want)
	}
	if off.Available {
		t.Fatalf("Available() = %+v, want unavailable", off)
	}
}
