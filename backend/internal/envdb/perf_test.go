package envdb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

// TestEnsurePerfProvidesThePlanFixture is the ADR 0002 claim applied to the M5
// perf dataset: a second sealed template, copied per account on demand, at the
// scale and with the skew the plan lessons are written against — and separate
// from the challenge environment, so a CREATE INDEX in a plan lesson cannot
// decide the answer a challenge lesson is graded on.
func TestEnsurePerfProvidesThePlanFixture(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	m := newManager(t, ctx, srv, envdb.Options{MaxEnvironments: 4, IdleTTL: time.Hour})
	if err := m.EnsurePerfTemplate(ctx); err != nil {
		t.Fatalf("ensure perf template: %v", err)
	}

	plan, err := m.EnsurePerf(ctx, accountA)
	if err != nil {
		t.Fatalf("ensure perf environment: %v", err)
	}

	// The fixture is the documented scale, so a plan captured here is a plan
	// against 10 000 rows rather than against whatever the machine felt like.
	counts := map[string]int{
		"customers":   2000,
		"products":    500,
		"orders":      10000,
		"order_items": 20000,
	}
	for table, want := range counts {
		got := scalarInt(t, ctx, plan, "SELECT count(*) FROM dataset."+table)
		if got != want {
			t.Errorf("dataset.%s has %d rows, want %d", table, got, want)
		}
	}

	// Selectivity is a real lesson only if the distribution is real: 90% of
	// orders are 'shipped' (a scan is correct) and 0.5% are 'refunded' (an
	// index is). The whole vocabulary is pinned, not just the two ends: the
	// authored lessons state every one of these five counts, so a seed that
	// renames or reweights a value has to fail here rather than quietly make
	// the course wrong.
	for status, want := range map[string]int{
		"shipped": 9000, "completed": 400, "cancelled": 300, "pending": 250, "refunded": 50,
	} {
		q := fmt.Sprintf("SELECT count(*) FROM dataset.orders WHERE status = '%s'", status)
		if got := scalarInt(t, ctx, plan, q); got != want {
			t.Errorf("'%s' orders = %d, want %d", status, got, want)
		}
	}
	if got := scalarInt(t, ctx, plan, "SELECT count(DISTINCT status) FROM dataset.orders"); got != 5 {
		t.Errorf("distinct status values = %d, want 5 (so every value is in the MCV list)", got)
	}

	// The deliberate index set: the three indexes the lessons rely on exist and
	// the fourth — the one the learner adds — does not.
	const indexed = `SELECT count(*) FROM pg_indexes WHERE schemaname = 'dataset' AND indexname IN
		('orders_customer_id_idx','orders_status_idx','order_items_order_id_idx')`
	if got := scalarInt(t, ctx, plan, indexed); got != 3 {
		t.Errorf("deliberate indexes present = %d, want 3", got)
	}
	const missing = `SELECT count(*) FROM pg_indexes WHERE schemaname = 'dataset' AND indexname = 'orders_ordered_at_idx'`
	if got := scalarInt(t, ctx, plan, missing); got != 0 {
		t.Errorf("orders_ordered_at_idx exists, but it is the index the lesson adds")
	}

	// The experiment the lesson asks for works: the learner owns the table, so
	// CREATE INDEX is allowed, and it can be dropped again.
	exec(t, ctx, plan, "CREATE INDEX orders_ordered_at_idx ON dataset.orders (ordered_at DESC)")
	if got := scalarInt(t, ctx, plan, missing); got != 1 {
		t.Fatalf("after CREATE INDEX, orders_ordered_at_idx count = %d, want 1", got)
	}
	exec(t, ctx, plan, "DROP INDEX dataset.orders_ordered_at_idx")

	// A plan environment is its own database, and the challenge environment is
	// untouched by the experiment.
	challenge, err := m.Ensure(ctx, accountA)
	if err != nil {
		t.Fatalf("ensure challenge environment: %v", err)
	}
	if db, db2 := scalarString(t, ctx, plan, "SELECT current_database()"), scalarString(t, ctx, challenge, "SELECT current_database()"); db == db2 {
		t.Fatalf("plan and challenge environments share database %q", db)
	}
	if got := scalarInt(t, ctx, challenge, "SELECT count(*) FROM dataset.customers"); got != 30 {
		t.Errorf("the challenge environment has %d customers, want the 30-row commerce dataset", got)
	}
	if !plan.Writable() {
		t.Error("the plan environment reports itself read-only; the lesson adds an index")
	}
}

// TestPerfResetKeepsTheChallengeResetTime pins the reset bookkeeping to the
// environment kind. The plan environment and the challenge environment share
// one resetAt map, keyed by kind prefix, so a plan reset that wrote the bare
// account digest would overwrite the challenge time Facts reports.
func TestPerfResetKeepsTheChallengeResetTime(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	m := newManager(t, ctx, srv, envdb.Options{MaxEnvironments: 4, IdleTTL: time.Hour})
	if err := m.EnsureTemplate(ctx); err != nil {
		t.Fatalf("ensure challenge template: %v", err)
	}
	if err := m.EnsurePerfTemplate(ctx); err != nil {
		t.Fatalf("ensure plan template: %v", err)
	}

	if _, err := m.Reset(ctx, accountA); err != nil {
		t.Fatalf("reset challenge environment: %v", err)
	}
	challenge, err := m.Facts(ctx, accountA)
	if err != nil {
		t.Fatalf("facts after the challenge reset: %v", err)
	}
	if challenge.ResetAt.IsZero() {
		t.Fatal("the challenge reset time was not recorded")
	}

	if _, err := m.ResetPerf(ctx, accountA); err != nil {
		t.Fatalf("reset plan environment: %v", err)
	}
	after, err := m.Facts(ctx, accountA)
	if err != nil {
		t.Fatalf("facts after the plan reset: %v", err)
	}
	if !after.ResetAt.Equal(challenge.ResetAt) {
		t.Fatalf("the plan reset overwrote the challenge reset time: %v, want the first reset at %v", after.ResetAt, challenge.ResetAt)
	}
}
