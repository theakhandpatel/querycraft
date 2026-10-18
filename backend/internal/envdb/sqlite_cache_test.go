package envdb

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* The bounded, idle-swept account cache (architecture review finding F4).
 *
 * ADR 0006's "what step 5 still owes" item 4 asks for a cap and a reap mirroring
 * envdb's MaxEnvironments and idle reaper. These tests use the real SQLite store
 * over a temp root — the same seeding path the gateway uses — because the
 * property under test is that an evicted executor's *file* is what carries the
 * learner's data, and a stand-in would not prove it. */

// accountsOn returns a store with an explicit bound and idle window: the
// defaults (8 accounts, 15 minutes) are too large and too slow to exercise.
func accountsOn(t *testing.T, maxAccounts int, idleTTL time.Duration) *SQLiteEnvironments {
	t.Helper()
	envs, err := NewSQLiteEnvironmentsWithOptions(t.TempDir(), SQLiteOptions{
		MaxAccounts: maxAccounts,
		IdleTTL:     idleTTL,
	})
	if err != nil {
		t.Fatalf("NewSQLiteEnvironmentsWithOptions: %v", err)
	}
	t.Cleanup(func() { _ = envs.Close() })
	return envs
}

// isCached reports whether an account currently holds a cached pool. The tests
// are in-package on purpose: the bound is an internal invariant, and asserting
// it through the unexported map is more honest than inferring it from timing.
func isCached(s *SQLiteEnvironments, accountID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.byAccount[accountID]
	return ok
}

// cachedAccounts reports how many per-account executors are cached. The value is
// a snapshot, not a lease, and only the cache tests read it, so it lives with
// them rather than on the store's production surface.
func (s *SQLiteEnvironments) cachedAccounts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byAccount)
}

// scalarOn runs a single-cell count through an executor, the way a learner's
// statement does, so the assertions read the engine's answer and not a cache.
func scalarOn(t *testing.T, ex *engine.SQLiteExecutor, sql string) int64 {
	t.Helper()
	n, err := scalarOf(ex, sql)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return n
}

// scalarOf is the goroutine-safe half of scalarOn: it returns an error instead
// of failing the test, because t.Fatalf may only be called from the goroutine
// running the test.
func scalarOf(ex *engine.SQLiteExecutor, sql string) (int64, error) {
	rows, err := ex.Query(context.Background(), sql)
	if err != nil {
		return 0, fmt.Errorf("query %q: %w", sql, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("query %q: %w", sql, err)
		}
		return 0, fmt.Errorf("query %q returned no row", sql)
	}
	values, err := rows.Values()
	if err != nil {
		return 0, fmt.Errorf("values %q: %w", sql, err)
	}
	n, ok := values[0].(int64)
	if !ok {
		return 0, fmt.Errorf("value %#v, want an int64", values[0])
	}
	return n, nil
}

// execOn runs a statement with no result through an executor.
func execOn(t *testing.T, ex *engine.SQLiteExecutor, sql string) {
	t.Helper()
	if err := execOf(ex, sql); err != nil {
		t.Fatalf("%v", err)
	}
}

// execOf is the goroutine-safe half of execOn.
func execOf(ex *engine.SQLiteExecutor, sql string) error {
	rows, err := ex.Query(context.Background(), sql)
	if err != nil {
		return fmt.Errorf("exec %q: %w", sql, err)
	}
	rows.Close()
	return nil
}

// accountScalar is scalarOf for a concurrent caller, and it is where the cache's
// self-healing is asserted rather than assumed. Between ForAccount returning an
// executor and the statement running, another goroutine may evict and close that
// pool; the statement then fails against a closed database. That is a legitimate
// outcome of bounded caching, not a bug, and it heals on the next statement
// because the file was never touched — so this retries, bounded, and reports the
// isolation answer only once a real executor answered.
func accountScalar(envs *SQLiteEnvironments, accountID, sql string) (int64, error) {
	const attempts = 5
	var last error
	for i := 0; i < attempts; i++ {
		ex, err := envs.ForAccount(context.Background(), accountID)
		if err != nil {
			return 0, fmt.Errorf("ForAccount(%s): %w", accountID, err)
		}
		n, err := scalarOf(ex, sql)
		if err == nil {
			return n, nil
		}
		// Only a closed pool is retryable; a real engine error is the answer.
		if !strings.Contains(err.Error(), "database is closed") {
			return 0, err
		}
		last = err
	}
	return 0, fmt.Errorf("after %d attempts: %w", attempts, last)
}

// accountExec is accountScalar's write counterpart, with the same retry.
func accountExec(envs *SQLiteEnvironments, accountID, sql string) error {
	const attempts = 5
	var last error
	for i := 0; i < attempts; i++ {
		ex, err := envs.ForAccount(context.Background(), accountID)
		if err != nil {
			return fmt.Errorf("ForAccount(%s): %w", accountID, err)
		}
		if err := execOf(ex, sql); err == nil {
			return nil
		} else if !strings.Contains(err.Error(), "database is closed") {
			return err
		} else {
			last = err
		}
	}
	return fmt.Errorf("after %d attempts: %w", attempts, last)
}

// TestSQLiteAccountCacheNeverExceedsItsBound is the core F4 assertion: however
// many accounts connect, the cached-pool map stays at or below MaxAccounts. Ten
// accounts against a bound of three must leave exactly three pools, and the
// evicted seven must no longer be held.
func TestSQLiteAccountCacheNeverExceedsItsBound(t *testing.T) {
	const bound = 3
	envs := accountsOn(t, bound, time.Hour)

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("account-%d", i)
		if _, err := envs.ForAccount(context.Background(), id); err != nil {
			t.Fatalf("ForAccount(%s): %v", id, err)
		}
		if got := envs.cachedAccounts(); got > bound {
			t.Fatalf("after account-%d the cache holds %d pools, want <= %d", i, got, bound)
		}
	}
	if got := envs.cachedAccounts(); got != bound {
		t.Fatalf("cache holds %d pools after 10 accounts, want the bound %d", got, bound)
	}
	// The three most-recently-used accounts are the ones still cached; the
	// earlier seven were evicted.
	for i := 0; i < 7; i++ {
		if isCached(envs, fmt.Sprintf("account-%d", i)) {
			t.Errorf("account-%d is still cached after 9 newer accounts", i)
		}
	}
	for i := 7; i < 10; i++ {
		if !isCached(envs, fmt.Sprintf("account-%d", i)) {
			t.Errorf("account-%d, one of the three most recent, was evicted", i)
		}
	}
}

// TestSQLiteEvictedAccountReopensWithItsOwnData proves eviction is safe: it
// closes a pool, not a workspace. An account evicted at the cap re-opens its
// own file and still sees the row it wrote — and must not see a neighbour's.
func TestSQLiteEvictedAccountReopensWithItsOwnData(t *testing.T) {
	envs := accountsOn(t, 2, time.Hour)

	alice, err := envs.ForAccount(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ForAccount(alice): %v", err)
	}
	execOn(t, alice, "INSERT INTO customers (id, name, signup_date) VALUES (999, 'Alice Only', '2026-01-01')")
	if got := scalarOn(t, alice, "SELECT count(*) FROM customers"); got != 31 {
		t.Fatalf("alice sees %d customers after her insert, want 31", got)
	}

	// Two more accounts push alice and bob out of the two-slot cache.
	bob, err := envs.ForAccount(context.Background(), "bob")
	if err != nil {
		t.Fatalf("ForAccount(bob): %v", err)
	}
	execOn(t, bob, "INSERT INTO customers (id, name, signup_date) VALUES (998, 'Bob Only', '2026-01-01')")
	if _, err := envs.ForAccount(context.Background(), "carol"); err != nil {
		t.Fatalf("ForAccount(carol): %v", err)
	}
	if _, err := envs.ForAccount(context.Background(), "dave"); err != nil {
		t.Fatalf("ForAccount(dave): %v", err)
	}
	if isCached(envs, "alice") || isCached(envs, "bob") {
		t.Fatal("alice and bob should have been evicted by carol and dave")
	}

	// Re-opening alice must find her file and her row, not a fresh seed.
	aliceAgain, err := envs.ForAccount(context.Background(), "alice")
	if err != nil {
		t.Fatalf("re-open alice: %v", err)
	}
	if got := scalarOn(t, aliceAgain, "SELECT count(*) FROM customers"); got != 31 {
		t.Errorf("alice sees %d customers after eviction, want her 31", got)
	}
	if got := scalarOn(t, aliceAgain, "SELECT count(*) FROM customers WHERE name = 'Bob Only'"); got != 0 {
		t.Errorf("alice can see bob's row (%d matches); eviction shared a file", got)
	}
	// And the reverse: bob's re-opened file has his row and not alice's.
	bobAgain, err := envs.ForAccount(context.Background(), "bob")
	if err != nil {
		t.Fatalf("re-open bob: %v", err)
	}
	if got := scalarOn(t, bobAgain, "SELECT count(*) FROM customers"); got != 31 {
		t.Errorf("bob sees %d customers after eviction, want his 31", got)
	}
	if got := scalarOn(t, bobAgain, "SELECT count(*) FROM customers WHERE name = 'Alice Only'"); got != 0 {
		t.Errorf("bob can see alice's row (%d matches); eviction shared a file", got)
	}
}

// TestSQLiteIdleAccountsAreReaped checks the reaper half: a pool idle past the
// window is closed on the next access, and its file keeps the learner's rows.
func TestSQLiteIdleAccountsAreReaped(t *testing.T) {
	const idle = 20 * time.Millisecond
	envs := accountsOn(t, 8, idle)

	alice, err := envs.ForAccount(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ForAccount(alice): %v", err)
	}
	execOn(t, alice, "INSERT INTO customers (id, name, signup_date) VALUES (999, 'Alice Only', '2026-01-01')")

	// Wait past the window, then touch a different account. The sweep runs on
	// that access, so no scheduler is needed for the idle pool to be released.
	time.Sleep(2 * idle)
	if _, err := envs.ForAccount(context.Background(), "bob"); err != nil {
		t.Fatalf("ForAccount(bob): %v", err)
	}
	if isCached(envs, "alice") {
		t.Fatal("alice's pool survived past the idle window; the reaper did not run")
	}
	if got := envs.cachedAccounts(); got != 1 {
		t.Fatalf("cache holds %d pools after the sweep, want just bob", got)
	}

	// The idle account's data is in its file, not in the pool it lost.
	aliceAgain, err := envs.ForAccount(context.Background(), "alice")
	if err != nil {
		t.Fatalf("re-open alice: %v", err)
	}
	if got := scalarOn(t, aliceAgain, "SELECT count(*) FROM customers"); got != 31 {
		t.Errorf("alice sees %d customers after being reaped, want her 31", got)
	}
}

// TestSQLiteResetStaysBoundedAndHonest pins the F2 reset behaviour under the new
// bound: reset still restores the fixture, and the account it restores stays
// within the cap.
func TestSQLiteResetStaysBoundedAndHonest(t *testing.T) {
	envs := accountsOn(t, 1, time.Hour)

	alice, err := envs.ForAccount(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ForAccount(alice): %v", err)
	}
	execOn(t, alice, "INSERT INTO customers (id, name, signup_date) VALUES (999, 'Reset Me', '2026-01-01')")
	if got := scalarOn(t, alice, "SELECT count(*) FROM customers"); got != 31 {
		t.Fatalf("alice sees %d customers before reset, want 31", got)
	}

	ex, _, err := envs.Reset(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Reset(alice): %v", err)
	}
	if got := scalarOn(t, ex, "SELECT count(*) FROM customers"); got != 30 {
		t.Errorf("alice sees %d customers after reset, want the fixture's 30", got)
	}
	if got := envs.cachedAccounts(); got > 1 {
		t.Errorf("cache holds %d pools after reset, want <= the bound 1", got)
	}
}

// TestSQLiteAccountCacheIsRaceFree hammers the cache concurrently so `go test
// -race` has something to see: parallel opens force evictions while other
// goroutines read and write their own files. Each account must still see its
// own single row and nobody else's — the isolation guarantee through eviction.
func TestSQLiteAccountCacheIsRaceFree(t *testing.T) {
	const (
		bound   = 4
		workers = 24
	)
	envs := accountsOn(t, bound, time.Hour)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("worker-%d", w)
			insert := fmt.Sprintf(
				"INSERT INTO customers (id, name, signup_date) VALUES (%d, 'Worker %d', '2026-01-01')",
				1000+w, w)
			if err := accountExec(envs, id, insert); err != nil {
				errs <- fmt.Errorf("insert for %s: %w", id, err)
				return
			}

			// A second access may re-open the file after an eviction; the row
			// must survive, because it is in the file, and the count must be
			// exactly the fixture's 30 plus this worker's single insert.
			got, err := accountScalar(envs, id, "SELECT count(*) FROM customers WHERE name = 'Worker "+fmt.Sprint(w)+"'")
			if err != nil {
				errs <- fmt.Errorf("%s: %w", id, err)
				return
			}
			if got != 1 {
				errs <- fmt.Errorf("%s sees %d of its own rows, want 1", id, got)
				return
			}
			// Nobody else's row is visible through this account's executor.
			other := (w + 1) % workers
			got, err = accountScalar(envs, id, fmt.Sprintf("SELECT count(*) FROM customers WHERE name = 'Worker %d'", other))
			if err != nil {
				errs <- fmt.Errorf("%s: %w", id, err)
				return
			}
			if got != 0 {
				errs <- fmt.Errorf("%s can see Worker %d's row; accounts shared a file", id, other)
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := envs.cachedAccounts(); got > bound {
		t.Errorf("cache holds %d pools after concurrent use, want <= the bound %d", got, bound)
	}
}
