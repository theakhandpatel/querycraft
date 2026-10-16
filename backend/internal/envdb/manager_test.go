package envdb_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

const (
	accountA = "ada@example.com"
	accountB = "bruno@example.com"
)

// TestManagerGivesEachAccountItsOwnWritableEnvironment is the whole claim of
// the challenge-environment model, on a real server: separate databases per
// account, writes that persist and stay private, an honest Facts view, a reset
// that restores the seed, and metadata still out of reach even where writes are
// allowed.
func TestManagerGivesEachAccountItsOwnWritableEnvironment(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	registry := metrics.New()
	m := newManager(t, ctx, srv, envdb.Options{
		MaxEnvironments: 4,
		IdleTTL:         time.Hour, // the reaper is a separate test
		Metrics:         registry,
	})
	if err := m.EnsureTemplate(ctx); err != nil {
		t.Fatalf("ensure template: %v", err)
	}

	envA, err := m.Ensure(ctx, accountA)
	if err != nil {
		t.Fatalf("ensure account A: %v", err)
	}
	envB, err := m.Ensure(ctx, accountB)
	if err != nil {
		t.Fatalf("ensure account B: %v", err)
	}

	// (a) Two accounts, two databases, one executor each.
	dbA := scalarString(t, ctx, envA, "SELECT current_database()")
	dbB := scalarString(t, ctx, envB, "SELECT current_database()")
	if dbA == dbB {
		t.Fatalf("both accounts resolved to database %q", dbA)
	}
	if envA == envB {
		t.Fatal("both accounts share one executor")
	}
	if !envA.Writable() || !envB.Writable() {
		t.Fatal("an environment reports itself read-only; challenge environments must accept writes")
	}

	// Each environment arrives seeded from the template.
	for name, ex := range map[string]*engine.PgxExecutor{accountA: envA, accountB: envB} {
		if got := scalarInt(t, ctx, ex, "SELECT count(*) FROM customers"); got != 30 {
			t.Fatalf("%s copied %d customers, want the seeded 30", name, got)
		}
	}

	// (b) A row written in A is invisible from B.
	exec(t, ctx, envA, "INSERT INTO customers (id, name, signup_date) VALUES (9001, 'Only In A', '2026-01-01')")
	if got := scalarInt(t, ctx, envA, "SELECT count(*) FROM customers WHERE id = 9001"); got != 1 {
		t.Fatalf("account A sees %d of its own rows, want 1", got)
	}
	if got := scalarInt(t, ctx, envB, "SELECT count(*) FROM customers WHERE id = 9001"); got != 0 {
		t.Fatalf("account B sees %d of account A's rows; environments are not isolated", got)
	}

	// (c) Ensure is idempotent for an existing account: same pool, writes stay.
	again, err := m.Ensure(ctx, accountA)
	if err != nil {
		t.Fatalf("second ensure for account A: %v", err)
	}
	if again != envA {
		t.Fatal("Ensure returned a new executor for an existing environment")
	}
	if got := scalarInt(t, ctx, again, "SELECT count(*) FROM customers WHERE id = 9001"); got != 1 {
		t.Fatalf("the write did not persist across Ensure calls: %d row(s)", got)
	}

	// (e) Facts describes the environment without assuming it exists.
	factsA, err := m.Facts(ctx, accountA)
	if err != nil {
		t.Fatalf("facts for account A: %v", err)
	}
	if !factsA.Exists || !factsA.Writable {
		t.Fatalf("Facts(A) = %+v, want exists and writable", factsA)
	}
	if factsA.Database != dbA {
		t.Fatalf("Facts(A).Database = %q, want the connected %q", factsA.Database, dbA)
	}
	if factsA.Tables < 4 {
		t.Fatalf("Facts(A).Tables = %d, want at least the four commerce tables", factsA.Tables)
	}
	if factsA.Account != accountA {
		t.Fatalf("Facts(A).Account = %q", factsA.Account)
	}
	missing, err := m.Facts(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("facts for an unknown account: %v", err)
	}
	if missing.Exists || missing.Writable || missing.Tables != 0 {
		t.Fatalf("Facts(nobody) = %+v, want absent", missing)
	}

	// (f) The gateway's metadata and the server's file functions stay out of
	// reach even in a writable environment. There is no `app` schema in the
	// copy, and the learner role was never a superuser.
	if res := engine.Execute(ctx, envA, "SELECT count(*) FROM app.progress", engine.DefaultLimits()); res.Status != "failed" {
		t.Fatalf("the writable environment can read app.progress: %+v", res)
	}
	if res := engine.Execute(ctx, envA, "SELECT pg_read_file('/etc/hostname')", engine.DefaultLimits()); res.Status != "failed" {
		t.Fatalf("the writable environment can call pg_read_file: %+v", res)
	}
	if res := engine.Execute(ctx, envA, "SELECT count(*) FROM pg_catalog.pg_authid", engine.DefaultLimits()); res.Status != "failed" {
		t.Fatalf("the writable environment can read pg_authid: %+v", res)
	}

	// (d) Reset replaces the environment with a fresh copy and invalidates the
	// executor that pointed at the dropped database.
	took, err := m.Reset(ctx, accountA)
	if err != nil {
		t.Fatalf("reset account A: %v", err)
	}
	if took <= 0 {
		t.Fatalf("Reset reported %v, want a positive duration", took)
	}
	fresh, err := m.Ensure(ctx, accountA)
	if err != nil {
		t.Fatalf("ensure account A after reset: %v", err)
	}
	if fresh == envA {
		t.Fatal("Reset did not invalidate the cached executor")
	}
	if got := scalarInt(t, ctx, fresh, "SELECT count(*) FROM customers"); got != 30 {
		t.Fatalf("after reset the environment has %d customers, want 30", got)
	}
	if got := scalarInt(t, ctx, fresh, "SELECT count(*) FROM customers WHERE id = 9001"); got != 0 {
		t.Fatalf("after reset the write is still present: %d row(s)", got)
	}

	// The metrics registry has the environment numbers the design asks for.
	if v, ok := registry.Value(envdb.MetricActive, nil); !ok || v != 2 {
		t.Fatalf("active-environment gauge = %v (present=%v), want 2", v, ok)
	}
	if count, _, ok := registry.Histogram(envdb.MetricCreateSeconds, nil); !ok || count != 3 {
		t.Fatalf("create histogram count = %d (present=%v), want 3", count, ok)
	}
	if count, _, ok := registry.Histogram(envdb.MetricResetSeconds, nil); !ok || count != 1 {
		t.Fatalf("reset histogram count = %d (present=%v), want 1", count, ok)
	}

	// Capacity is enforced before a create, by a typed error that the gateway
	// can map to ENVIRONMENT_UNAVAILABLE. A second manager on the same cluster
	// with room for one.
	capped := newManager(t, ctx, srv, envdb.Options{MaxEnvironments: 1, IdleTTL: time.Hour})
	if _, err := capped.Ensure(ctx, "capacity-one@example.com"); err != nil {
		t.Fatalf("first environment under a limit of one: %v", err)
	}
	_, err = capped.Ensure(ctx, "capacity-two@example.com")
	if err == nil {
		t.Fatal("a second environment was created past MaxEnvironments")
	}
	if !errors.Is(err, envdb.ErrTooManyEnvironments) {
		t.Fatalf("capacity error = %v, want ErrTooManyEnvironments", err)
	}
	if !errors.Is(err, engine.ErrEnvironmentUnavailable) {
		t.Fatalf("capacity error = %v, want it to wrap engine.ErrEnvironmentUnavailable", err)
	}
}

// TestManagerReapsIdleEnvironments proves the idle window is enforced and that
// the active gauge follows it down.
func TestManagerReapsIdleEnvironments(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	registry := metrics.New()
	m := newManager(t, ctx, srv, envdb.Options{
		IdleTTL: 1100 * time.Millisecond,
		Metrics: registry,
	})
	if _, err := m.Ensure(ctx, accountA); err != nil {
		t.Fatalf("ensure account A: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		facts, err := m.Facts(ctx, accountA)
		if err != nil {
			t.Fatalf("facts for account A: %v", err)
		}
		if !facts.Exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the idle environment was never reaped")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if v, ok := registry.Value(envdb.MetricActive, nil); !ok || v != 0 {
		t.Fatalf("active-environment gauge = %v (present=%v) after a reap, want 0", v, ok)
	}
}

/* ---------- helpers ---------- */

func newManager(t *testing.T, ctx context.Context, srv *pgtest.Server, opts envdb.Options) *envdb.Manager {
	t.Helper()
	opts.AdminDSN = srv.DSN
	opts.LearnerPassword = srv.Password()
	opts.Log = pgtest.QuietLogger()
	m, err := envdb.New(ctx, opts)
	if err != nil {
		t.Fatalf("new environment manager: %v", err)
	}
	// Cleanup runs before the server stops (LIFO), so Close drops its own
	// environments while the server is still there.
	t.Cleanup(m.Close)
	return m
}

func exec(t *testing.T, ctx context.Context, ex *engine.PgxExecutor, sql string) {
	t.Helper()
	if res := engine.Execute(ctx, ex, sql, engine.DefaultLimits()); res.Status != "succeeded" {
		t.Fatalf("exec %q: status=%s error=%+v", sql, res.Status, res.Error)
	}
}

func scalarInt(t *testing.T, ctx context.Context, ex *engine.PgxExecutor, sql string) int {
	t.Helper()
	res := engine.Execute(ctx, ex, sql, engine.DefaultLimits())
	if res.Status != "succeeded" {
		t.Fatalf("query %q failed: %+v", sql, res.Error)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("query %q returned %d row(s)", sql, len(res.Rows))
	}
	switch v := res.Rows[0][0].(type) {
	case int64:
		return int(v)
	case int32:
		return int(v)
	}
	t.Fatalf("query %q returned %T, want an integer", sql, res.Rows[0][0])
	return 0
}

func scalarString(t *testing.T, ctx context.Context, ex *engine.PgxExecutor, sql string) string {
	t.Helper()
	res := engine.Execute(ctx, ex, sql, engine.DefaultLimits())
	if res.Status != "succeeded" {
		t.Fatalf("query %q failed: %+v", sql, res.Error)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("query %q returned %d row(s)", sql, len(res.Rows))
	}
	s, ok := res.Rows[0][0].(string)
	if !ok {
		t.Fatalf("query %q returned %T, want string", sql, res.Rows[0][0])
	}
	return s
}

// TestConcurrentEnsureForOneAccountOpensOnePool pins the in-flight guard added
// when the pool connect + probe moved outside the manager lock: many callers
// racing for the SAME account's first environment must all receive the same
// executor, and only one pool must be built. Without the guard the losers would
// each open a pool and race the envs map write. Run with -race.
func TestConcurrentEnsureForOneAccountOpensOnePool(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	m := newManager(t, ctx, srv, envdb.Options{MaxEnvironments: 4, IdleTTL: time.Hour})
	if err := m.EnsureTemplate(ctx); err != nil {
		t.Fatalf("ensure template: %v", err)
	}

	const racers = 8
	execs := make([]*engine.PgxExecutor, racers)
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			execs[i], errs[i] = m.Ensure(ctx, accountA)
		}(i)
	}
	wg.Wait()

	first := execs[0]
	if errs[0] != nil || first == nil {
		t.Fatalf("first ensure failed: %v", errs[0])
	}
	for i := 1; i < racers; i++ {
		if errs[i] != nil {
			t.Fatalf("racer %d failed: %v", i, errs[i])
		}
		if execs[i] != first {
			t.Errorf("racer %d got a different *PgxExecutor; concurrent ensure opened more than one pool", i)
		}
	}
	// A second ensure after the race returns the same cached pool.
	again, err := m.Ensure(ctx, accountA)
	if err != nil || again != first {
		t.Errorf("second ensure = (%v, %v), want the cached pool", again, err)
	}
}
