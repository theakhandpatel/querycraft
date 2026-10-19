package envdb

import (
	"context"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

/* The parallel-gate flake, repaired at the fixture.
 *
 * The first copy of the perf template is seconds of DDL. It used to run under
 * the caller's statement deadline, so a loaded machine cancelled it mid-flight.
 * pgx closes the socket when a query is cancelled, and every later statement on
 * the manager's one admin connection then failed with "conn closed" before it
 * was sent. The retry the Go test carried papered over a copy that outlived the
 * request. These two tests pin the two halves of the fix: provisioning gets its
 * own bounded deadline, and a connection the driver has already closed is
 * replaced and retried rather than surfaced.
 */

// TestProvisioningRecoversFromAClosedAdminConnection simulates a query
// cancelled mid-flight closing the admin socket, then proves the next
// provisioning statement replaces the connection and succeeds.
func TestProvisioningRecoversFromAClosedAdminConnection(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	m, err := New(ctx, Options{
		AdminDSN:        srv.DSN,
		LearnerPassword: srv.Password(),
		Log:             pgtest.QuietLogger(),
		IdleTTL:         time.Hour,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(m.Close)

	// Pay for the template once, so the recovery below is exercised on the
	// per-account lookup, copy and seal rather than on the fixture build.
	if err := m.EnsureTemplate(ctx); err != nil {
		t.Fatalf("ensure template: %v", err)
	}

	m.mu.Lock()
	closed := m.admin
	_ = closed.Close(context.WithoutCancel(ctx))
	m.mu.Unlock()
	if !closed.IsClosed() {
		t.Fatal("the injected admin connection did not close")
	}

	// Before the fix every statement here failed with "conn closed".
	exec, err := m.Ensure(ctx, accountA)
	if err != nil {
		t.Fatalf("Ensure surfaced a closed admin connection instead of recovering: %v", err)
	}
	m.mu.Lock()
	replaced := m.admin != closed
	m.mu.Unlock()
	if !replaced {
		t.Fatal("the closed admin connection was reused")
	}

	if res := engine.Execute(ctx, exec, "SELECT count(*) FROM dataset.customers", engine.DefaultLimits()); res.Status != "succeeded" {
		t.Fatalf("the recovered environment could not be queried: %+v", res.Error)
	}
}

// TestProvisioningSurvivesACancelledStatementContext proves the copy is not
// bounded by the caller's statement deadline: a cancelled request context
// leaves a usable environment behind for the next request instead of failing
// the copy halfway.
func TestProvisioningSurvivesACancelledStatementContext(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	m, err := New(ctx, Options{
		AdminDSN:        srv.DSN,
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

	// A statement deadline that has already expired stands in for the loaded
	// machine where the copy outlives the request budget.
	expired, cancelExpired := context.WithCancel(ctx)
	cancelExpired()

	exec, err := m.Ensure(expired, accountB)
	if err != nil {
		t.Fatalf("a cancelled statement deadline refused provisioning: %v", err)
	}
	if res := engine.Execute(ctx, exec, "SELECT count(*) FROM dataset.customers", engine.DefaultLimits()); res.Status != "succeeded" {
		t.Fatalf("the provisioned environment could not be queried: %+v", res.Error)
	}
}
