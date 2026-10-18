package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMemoryRoundTrip(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	const alice, bob = "u_alice", "u_bob"

	// Progress and events carry a foreign key to the account that owns them on
	// every backend, including degraded mode, so the accounts exist first.
	for _, id := range []string{alice, bob} {
		if err := m.CreateUser(ctx, User{ID: id, Email: id + "@example.test", Name: id}, "hash"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	if _, ok, err := m.LoadProgress(ctx, alice, "missing"); err != nil || ok {
		t.Fatalf("missing course returned ok=%v err=%v", ok, err)
	}

	snap := json.RawMessage(`{"lesson":3,"done":[true,false]}`)
	if err := m.SaveProgress(ctx, alice, "sql-analytics", snap); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := m.LoadProgress(ctx, alice, "sql-analytics")
	if err != nil || !ok {
		t.Fatalf("load ok=%v err=%v", ok, err)
	}
	// Progress round-trips through the JSON column, which normalizes key order
	// and whitespace on every backend; the value is what must survive.
	if !sameJSON(got, snap) {
		t.Fatalf("got %s want %s", got, snap)
	}

	// Mutating the returned copy must not corrupt the store.
	got[0] = 'X'
	again, _, _ := m.LoadProgress(ctx, alice, "sql-analytics")
	if again[0] == 'X' {
		t.Fatalf("store returned an aliased slice")
	}

	if err := m.SaveEvent(ctx, alice, "evt_1", json.RawMessage(`{"kind":"passed"}`)); err != nil {
		t.Fatalf("save event: %v", err)
	}

	// Two accounts writing the same course key must not see each other's data.
	other := json.RawMessage(`{"lesson":9}`)
	if err := m.SaveProgress(ctx, bob, "sql-analytics", other); err != nil {
		t.Fatalf("save bob: %v", err)
	}
	aliceSnap, _, _ := m.LoadProgress(ctx, alice, "sql-analytics")
	bobSnap, _, _ := m.LoadProgress(ctx, bob, "sql-analytics")
	if !sameJSON(aliceSnap, snap) || !sameJSON(bobSnap, other) {
		t.Fatalf("progress leaked across owners: alice=%s bob=%s", aliceSnap, bobSnap)
	}
	if _, ok, _ := m.LoadProgress(ctx, "u_nobody", "sql-analytics"); ok {
		t.Fatalf("an unknown owner saw another account's course")
	}
}

// Progress and events must name an owner; an unscoped write would land in a
// shared row that any account could read back.
func TestMemoryRequiresOwner(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	if err := m.SaveProgress(ctx, "", "sql-analytics", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("progress write without an owner was accepted")
	}
	if err := m.SaveEvent(ctx, "", "evt_1", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("event write without an owner was accepted")
	}
}

func TestMemoryUsersAndSessions(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	if ids, err := m.UserIDs(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("fresh store ids=%v err=%v", ids, err)
	}

	u := User{ID: "u_1", Email: "a@example.test", Name: "A", Role: RoleAdmin}
	if err := m.CreateUser(ctx, u, "hash"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.CreateUser(ctx, User{ID: "u_2", Email: "A@Example.Test", Name: "dup"}, "hash"); err == nil {
		t.Fatalf("duplicate email (different case) was accepted")
	}

	got, hash, ok, err := m.UserByEmail(ctx, "A@EXAMPLE.TEST")
	if err != nil || !ok || got.ID != "u_1" || hash != "hash" {
		t.Fatalf("by email: %+v hash=%q ok=%v err=%v", got, hash, ok, err)
	}
	if !got.IsAdmin() {
		t.Fatalf("role = %q, want admin", got.Role)
	}

	expires := time.Now().Add(time.Hour)
	if err := m.CreateSession(ctx, "tok", "u_1", expires); err != nil {
		t.Fatalf("create session: %v", err)
	}
	su, ok, err := m.SessionUser(ctx, "tok")
	if err != nil || !ok || su.ID != "u_1" {
		t.Fatalf("session user: %+v ok=%v err=%v", su, ok, err)
	}
	if _, ok, _ := m.SessionUser(ctx, "nope"); ok {
		t.Fatalf("unknown token resolved")
	}
	if err := m.DeleteSession(ctx, "tok"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, ok, _ := m.SessionUser(ctx, "tok"); ok {
		t.Fatalf("deleted token still resolves")
	}
}

// The in-memory store must reject an event without an id, exactly as the
// Postgres store does, so degraded mode cannot report a success the real store
// would have refused.
func TestMemorySaveEventRequiresID(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	if err := m.CreateUser(ctx, User{ID: "u_1", Email: "u1@example.test", Name: "One"}, "hash"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := m.SaveEvent(ctx, "u_1", "", json.RawMessage(`{"kind":"passed"}`)); err == nil {
		t.Fatalf("empty event id was accepted")
	}
	if err := m.SaveEvent(ctx, "u_1", "evt_2", json.RawMessage(`{"kind":"passed"}`)); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
}
