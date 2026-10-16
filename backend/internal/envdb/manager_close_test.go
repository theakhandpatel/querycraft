package envdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

/* Manager lifecycle, in-package.
 *
 * These tests assert internal invariants (the envs map and the in-flight
 * marker), which is why they live beside the implementation. The public
 * behaviour they protect is what the gateway relies on: Close must leave no
 * environment behind, and a reset must not hold the manager lock across its
 * DDL. The stall proxy parks a real connection, so "the reset is in its DDL"
 * is a fact rather than a sleep. */

// The internal tests use the same two account ids as the external suite.
const (
	accountA = "ada@example.com"
	accountB = "bruno@example.com"
)

// TestEnsureAfterCloseLeavesNoEnvironment: a build that started before Close
// finishes after it, and installing its result would leave a pool and a
// database alive after Close cleared everything. The second closed check must
// refuse the build and close the pool it opened.
func TestEnsureAfterCloseLeavesNoEnvironment(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	proxy := startStallProxy(t, dsnHost(t, srv.DSN))
	m, err := New(ctx, Options{
		AdminDSN:        proxyDSN(t, srv.DSN, proxy),
		LearnerPassword: srv.Password(),
		Log:             pgtest.QuietLogger(),
		IdleTTL:         time.Hour,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(m.Close)
	if err := m.EnsureTemplate(ctx); err != nil {
		t.Fatalf("ensure template: %v", err)
	}

	// Park the next connection. The build's pool connect is the one that
	// reaches the proxy, because the admin connection is already established.
	proxy.stall()

	ensureErr := make(chan error, 1)
	go func() {
		_, err := m.Ensure(ctx, accountA)
		ensureErr <- err
	}()
	proxy.waitArrival(t)

	// Close while the build is stuck in its pool connect.
	m.Close()

	proxy.resume()
	err = <-ensureErr
	if err == nil {
		t.Fatal("Ensure returned an environment after Close")
	}
	if !errors.Is(err, engine.ErrEnvironmentUnavailable) {
		t.Fatalf("Ensure after Close = %v, want it to wrap engine.ErrEnvironmentUnavailable", err)
	}

	m.mu.Lock()
	envs := len(m.envs)
	m.mu.Unlock()
	if envs != 0 {
		t.Fatalf("Close left %d environment(s) in the manager", envs)
	}
}

// TestResetDoesNotBlockEnsureForAnotherAccount: a reset's DROP+CREATE takes
// seconds, and the manager lock must not be held across it. A different
// account's Ensure is the proof: it must return while the reset is parked in
// its DDL.
func TestResetDoesNotBlockEnsureForAnotherAccount(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	proxy := startStallProxy(t, dsnHost(t, srv.DSN))
	m, err := New(ctx, Options{
		AdminDSN:        proxyDSN(t, srv.DSN, proxy),
		LearnerPassword: srv.Password(),
		Log:             pgtest.QuietLogger(),
		IdleTTL:         time.Hour,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(m.Close)

	if err := m.EnsureTemplate(ctx); err != nil {
		t.Fatalf("ensure template: %v", err)
	}
	if _, err := m.Ensure(ctx, accountA); err != nil {
		t.Fatalf("ensure account A: %v", err)
	}
	if _, err := m.Ensure(ctx, accountB); err != nil {
		t.Fatalf("ensure account B: %v", err)
	}

	proxy.stall()

	resetErr := make(chan error, 1)
	go func() {
		_, err := m.Reset(ctx, accountB)
		resetErr <- err
	}()

	// The reset sets its in-flight marker while it holds the state lock and
	// releases the lock before the DDL, so the marker is the signal that the
	// locked section is over.
	key := challengeEnv.keyPrefix + digest(accountB)
	deadline := time.Now().Add(30 * time.Second)
	for {
		m.mu.Lock()
		inflight := m.building[key] != nil
		m.mu.Unlock()
		if inflight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Reset never entered its DDL phase")
		}
		time.Sleep(5 * time.Millisecond)
	}
	proxy.waitArrival(t)

	done := make(chan error, 1)
	go func() {
		_, err := m.Ensure(ctx, accountA)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Ensure during another account's reset: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ensure for another account waited for the reset's DDL")
	}

	proxy.resume()
	select {
	case err := <-resetErr:
		if err != nil {
			t.Fatalf("reset account B: %v", err)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("reset did not finish after the proxy resumed")
	}
}
