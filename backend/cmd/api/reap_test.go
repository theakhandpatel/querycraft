package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

// TestBootReaperSkipsTheMemoryStore: newStore falls back to the in-memory
// store when the metadata database is stale or unreachable, and that store
// knows only the seeded administrator. Reaping on it would drop every other
// learner's environment database, so the boot path must skip the sweep.
func TestBootReaperSkipsTheMemoryStore(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	m, err := envdb.New(ctx, envdb.Options{
		AdminDSN:        srv.DSN,
		LearnerPassword: srv.Password(),
		Log:             pgtest.QuietLogger(),
		IdleTTL:         time.Hour, // the idle reaper has its own test
	})
	if err != nil {
		t.Fatalf("new environment manager: %v", err)
	}
	t.Cleanup(m.Close)

	st := store.NewMemory()
	if err := st.CreateUser(ctx, store.User{ID: "admin-1", Email: "admin@querycraft.local", Role: store.RoleAdmin}, "hash"); err != nil {
		t.Fatalf("seed the administrator: %v", err)
	}
	ids, err := st.UserIDs(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("the in-memory store lists %d accounts, want only the seeded administrator", len(ids))
	}

	// A second learner's database exists on the cluster but has no row in the
	// memory store, which is exactly the state a degraded boot creates.
	if _, err := m.Ensure(ctx, "learner-2@example.com"); err != nil {
		t.Fatalf("ensure the second account's environment: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reapOrphanedEnvironments(ctx, log, m, st)

	facts, err := m.Facts(ctx, "learner-2@example.com")
	if err != nil {
		t.Fatalf("facts for the second account: %v", err)
	}
	if !facts.Exists {
		t.Fatal("the boot reaper dropped a learner database because the in-memory store does not know the account")
	}
}
