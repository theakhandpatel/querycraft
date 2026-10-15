package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"

	// database/sql is the one-time seed path below; the driver registers itself
	// as "sqlite" in init(). The blank import is repeated rather than inherited
	// from engine so this file keeps working if that package ever stops
	// importing the driver.
	_ "modernc.org/sqlite"
)

/* ADR 0003 in SQLite: a learner's writes land in their own file.
 *
 * The PostgreSQL track gives a signed-in learner a database copied from a
 * sealed template; SQLite has no server, no template and no role, so the
 * equivalent isolation is one database *file* per account, seeded once from the
 * committed schema+seed. An anonymous visitor stays on a single shared,
 * read-only file, which is ADR 0003's "anonymous visitors keep the shared,
 * read-only dataset" with a file in place of a database.
 *
 * The store never invents an environment: a root that cannot be created, a seed
 * that fails and a file that will not open all surface as errors the handler
 * turns into ENVIRONMENT_UNAVAILABLE, exactly as the PostgreSQL manager does.
 */

// sharedSQLiteFile is the anonymous dataset's file name. Its rows are the
// committed commerce fixture, not a learner's, so every anonymous visitor sees
// the same thirty customers.
const sharedSQLiteFile = "commerce.db"

// sqlitePoolConns bounds connections to one database file. It is deliberately
// small: SQLite has no server to multiplex and a pool larger than the gateway's
// statement concurrency would only add lock contention between its own
// connections.
const sqlitePoolConns = 4

// defaultMaxAccounts and defaultSQLiteIdleTTL mirror envdb's
// defaultMaxEnvironments and defaultIdleTTL (ADR 0006's "what step 5 still
// owes" item 4): a file per account is a bounded promise, not an unbounded map.
// The cap is a budget on cached pools, not on files — an evicted account's file
// stays on disk and re-opens cheaply — so it can be smaller than the number of
// accounts the deployment serves.
const (
	defaultMaxAccounts   = 8
	defaultSQLiteIdleTTL = 15 * time.Minute
)

// SQLiteOptions configures a SQLiteEnvironments store. The zero value keeps the
// shipped defaults, so NewSQLiteEnvironments and the existing tests need no
// change; the gateway fills MaxAccounts and IdleTTL from the environment.
type SQLiteOptions struct {
	// MaxAccounts bounds the number of cached per-account executors. When the
	// bound is reached the least-recently-used executor is closed and evicted;
	// its file is left in place and re-opens on the account's next use, so
	// eviction is invisible to the learner. Zero uses defaultMaxAccounts.
	MaxAccounts int

	// IdleTTL is how long a cached executor may go unused before a lazy sweep
	// on the next access closes and evicts it. Like eviction at the cap, it
	// releases the connection pool only; the account's file is its data and
	// stays. Zero uses defaultSQLiteIdleTTL.
	IdleTTL time.Duration

	// Log records evictions. Nil discards them, which is what the tests want.
	Log *slog.Logger
}

func (o SQLiteOptions) withDefaults() SQLiteOptions {
	if o.MaxAccounts <= 0 {
		o.MaxAccounts = defaultMaxAccounts
	}
	if o.IdleTTL <= 0 {
		o.IdleTTL = defaultSQLiteIdleTTL
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return o
}

// accountEnvironment is one cached per-account executor plus the bookkeeping the
// bound and the reaper need. lastUsed is updated on every access, under the same
// mutex that guards the map, so the LRU order and the map can never disagree.
type accountEnvironment struct {
	exec     *engine.SQLiteExecutor
	lastUsed time.Time
}

// SQLiteEnvironments hands out executors over SQLite database files.
//
// Executors are cached, not re-opened per request: a SQLiteExecutor owns a
// connection pool, and re-opening the file on every statement would pay the
// pragma and ping cost each time. Close releases every handle, so the gateway's
// shutdown does not leave a pool behind.
//
// The cache is bounded (MaxAccounts) and idle-swept (IdleTTL), mirroring
// envdb's capacity cap and reaper. Evicting a cached executor closes its pool
// only: the account's file is left in place, and the isolation guarantee — two
// accounts never share a file — is unaffected because a file is never reused
// for a different account, evicted or not.
type SQLiteEnvironments struct {
	root     string
	accounts string
	opts     SQLiteOptions

	mu        sync.Mutex
	shared    *engine.SQLiteExecutor
	byAccount map[string]*accountEnvironment
	closed    bool
}

// NewSQLiteEnvironments prepares the data root and returns the store with the
// default options. It fails rather than deferring the problem to the first
// request, because a root the process cannot write is a configuration fault the
// operator should read in the log at boot.
func NewSQLiteEnvironments(root string) (*SQLiteEnvironments, error) {
	return NewSQLiteEnvironmentsWithOptions(root, SQLiteOptions{})
}

// NewSQLiteEnvironmentsWithOptions is NewSQLiteEnvironments with an explicit
// bound and idle timeout. The gateway uses it to read the cap and the reaper
// window from the environment; tests use it to exercise the bound without
// creating hundreds of files.
func NewSQLiteEnvironmentsWithOptions(root string, opts SQLiteOptions) (*SQLiteEnvironments, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("no SQLite data directory configured")
	}
	accounts := filepath.Join(root, "accounts")
	if err := os.MkdirAll(accounts, 0o755); err != nil {
		return nil, fmt.Errorf("create SQLite data directory: %w", err)
	}
	return &SQLiteEnvironments{
		root:      root,
		accounts:  accounts,
		opts:      opts.withDefaults(),
		byAccount: map[string]*accountEnvironment{},
	}, nil
}

// Root reports the directory the files live in, for diagnostics and tests.
func (s *SQLiteEnvironments) Root() string { return s.root }

// MaxAccounts reports the cached-executor bound, so the gateway can log the
// effective value after defaults have been applied rather than the configured
// zero.
func (s *SQLiteEnvironments) MaxAccounts() int { return s.opts.MaxAccounts }

// IdleTTL reports the idle-eviction window (see SQLiteOptions.IdleTTL).
func (s *SQLiteEnvironments) IdleTTL() time.Duration { return s.opts.IdleTTL }

// cachedAccounts reports how many per-account executors are cached. It exists
// for tests and diagnostics; the value is a snapshot, not a lease.
func (s *SQLiteEnvironments) cachedAccounts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byAccount)
}

// Shared returns the anonymous learner's executor: the committed fixture, read
// only. ReadOnly adds SQLite's own mode=ro and query_only(1) together, so a
// write is refused by the engine rather than by convention.
func (s *SQLiteEnvironments) Shared(ctx context.Context) (*engine.SQLiteExecutor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("%w: the SQLite store is closed", engine.ErrEnvironmentUnavailable)
	}
	if s.shared != nil {
		return s.shared, nil
	}
	path := filepath.Join(s.root, sharedSQLiteFile)
	if err := s.seed(ctx, path); err != nil {
		return nil, err
	}
	ex, err := engine.NewSQLiteExecutor(engine.SQLiteConfig{
		Path:         path,
		ReadOnly:     true,
		Pragmas:      engine.SQLiteReadOnlyPragmas(),
		MaxOpenConns: sqlitePoolConns,
		MaxIdleConns: 1,
	})
	if err != nil {
		return nil, err
	}
	s.shared = ex
	return ex, nil
}

// ForAccount returns a signed-in learner's writable executor, seeding their file
// from the committed schema+seed on first use. Two accounts get two files, so
// one learner's INSERT cannot be seen by another.
//
// The cache this serves from is bounded and idle-swept (SQLiteOptions): a hit
// refreshes the account's LRU position, a sweep on every miss drops pools idle
// past IdleTTL, and a miss at the cap evicts the least-recently-used account.
// Eviction closes a pool only — the account's file is left in place, so the
// learner's next statement re-seeds nothing and opens it again intact.
func (s *SQLiteEnvironments) ForAccount(ctx context.Context, accountID string) (*engine.SQLiteExecutor, error) {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return nil, fmt.Errorf("%w: no account id for a SQLite environment", engine.ErrEnvironmentUnavailable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("%w: the SQLite store is closed", engine.ErrEnvironmentUnavailable)
	}
	// Reap before looking up, so an idle pool is released even when the account
	// returning is not the one that went quiet. This is the whole reaper: a pass
	// on access, not a scheduler, which is enough to keep a long-running gateway
	// from holding pools for accounts that stopped working.
	s.reapIdleLocked(time.Now())
	if a := s.byAccount[id]; a != nil {
		a.lastUsed = time.Now()
		return a.exec, nil
	}
	// Make room before the new pool is opened, so the map is never larger than
	// the cap even for the instant between open and insert.
	s.evictToMakeRoomLocked()
	path := filepath.Join(s.accounts, accountFileStem(id)+".db")
	// The writable path shares the read-only pragma set on purpose: the pragmas
	// that matter (foreign_keys, busy_timeout) are not about access, and only
	// SQLiteConfig.ReadOnly adds query_only. Keeping one set means the two paths
	// cannot drift in what they teach.
	return s.forAccountLocked(ctx, id, path)
}

// Reset restores a signed-in learner's SQLite file to the committed fixture and
// returns the fresh executor plus how long it took.
//
// ADR 0003's PostgreSQL reset copies the sealed template over the account's
// database; SQLite has no template to copy from and no role to copy as, so the
// equivalent is the same one the seed uses: drop the account's file and let the
// next open re-seed it from the committed schema+seed. That is the whole
// "a copy is the reseed" rule with a file in place of a database.
//
// The cached executor must be closed before the file is removed — a pool that
// still holds the inode would keep serving the old rows (or fail) — and the
// cache entry must be dropped so the next ForAccount re-seeds instead of handing
// back a handle to a deleted file. Reset is therefore a full teardown+reseed,
// exactly as the PostgreSQL reset is a full drop+copy.
//
// An account that has never opened a workspace has no file to reset. That is not
// an error: the learner's next use seeds a fresh one, and reporting an error
// here would refuse a "restore" that has nothing to restore. The elapsed time is
// still reported so the shape matches the PostgreSQL path.
func (s *SQLiteEnvironments) Reset(ctx context.Context, accountID string) (*engine.SQLiteExecutor, time.Duration, error) {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return nil, 0, fmt.Errorf("%w: no account id for a SQLite environment", engine.ErrEnvironmentUnavailable)
	}
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, 0, fmt.Errorf("%w: the SQLite store is closed", engine.ErrEnvironmentUnavailable)
	}
	path := filepath.Join(s.accounts, accountFileStem(id)+".db")
	if a := s.byAccount[id]; a != nil {
		if err := a.exec.Close(); err != nil {
			return nil, 0, fmt.Errorf("close the SQLite workspace before reset: %w", err)
		}
		delete(s.byAccount, id)
	}
	// A SQLite file may exist beside its rollback journal and its WAL/SHM
	// companions; a stale -journal left behind would be replayed over the fresh
	// seed and resurrect the learner's writes. Remove the whole family.
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, 0, fmt.Errorf("remove the SQLite workspace: %w", err)
		}
	}
	// A reset is a create: make room for the fresh entry so it cannot push the
	// cache past the cap when the account was not cached (the common case for a
	// grant that has already been fully evicted).
	s.evictToMakeRoomLocked()
	ex, err := s.forAccountLocked(ctx, id, path)
	if err != nil {
		return nil, 0, err
	}
	return ex, time.Since(start), nil
}

// forAccountLocked seeds path if needed and opens the learner's writable
// executor, caching it. It is the body of ForAccount, split out so Reset can
// re-seed after dropping the file under the same lock rather than releasing and
// re-acquiring it (which would let a concurrent ForAccount race the reseed).
func (s *SQLiteEnvironments) forAccountLocked(ctx context.Context, id, path string) (*engine.SQLiteExecutor, error) {
	if err := s.seed(ctx, path); err != nil {
		return nil, err
	}
	ex, err := engine.NewSQLiteExecutor(engine.SQLiteConfig{
		Path:         path,
		Pragmas:      engine.SQLiteReadOnlyPragmas(),
		MaxOpenConns: sqlitePoolConns,
		MaxIdleConns: 1,
	})
	if err != nil {
		return nil, err
	}
	s.byAccount[id] = &accountEnvironment{exec: ex, lastUsed: time.Now()}
	return ex, nil
}

// reapIdleLocked closes and evicts every executor idle for at least IdleTTL.
// Callers hold s.mu, so this runs on the request path: a pass per miss, not a
// background goroutine. The file is not touched — a reaped account keeps every
// row it wrote, and its next ForAccount re-opens the same file.
func (s *SQLiteEnvironments) reapIdleLocked(now time.Time) {
	for id, a := range s.byAccount {
		if now.Sub(a.lastUsed) < s.opts.IdleTTL {
			continue
		}
		if err := a.exec.Close(); err != nil {
			// A pool that will not close is closed again on the next pass; the
			// entry stays so the account is not silently re-opened on every
			// statement while the failure persists.
			s.opts.Log.Warn("reap idle SQLite workspace", "err", err)
			continue
		}
		delete(s.byAccount, id)
		s.opts.Log.Info("reaped idle SQLite workspace",
			"idle_for", now.Sub(a.lastUsed).String())
	}
}

// evictToMakeRoomLocked closes and evicts the least-recently-used executor when
// the cache is full, so the entry about to be inserted keeps the map within its
// bound. Callers hold s.mu.
//
// The evicted file is untouched: SQLite's data lives in the file, not in the
// pool, so closing the pool loses nothing and the account's next statement
// re-opens the same file with all of its rows.
//
// Closing a pool a request is still using is safe but not free: database/sql's
// Close waits for statements already in flight and refuses ones that start
// after, so a statement that loses this race fails against the closed pool —
// reported honestly as a failed result, never as invented rows, and healed by
// the next statement, which re-opens the same file. The alternative envdb takes
// for PostgreSQL is to refuse the *new* account at the cap; SQLite prefers
// evicting a cold pool, because its create cost is ~2 ms and its file is the
// workspace (ADR 0006), so no learner is ever told their own environment is full.
func (s *SQLiteEnvironments) evictToMakeRoomLocked() {
	if len(s.byAccount) < s.opts.MaxAccounts {
		return
	}
	victim := ""
	var oldest time.Time
	for id, a := range s.byAccount {
		if victim == "" || a.lastUsed.Before(oldest) {
			victim, oldest = id, a.lastUsed
		}
	}
	if victim == "" {
		return
	}
	a := s.byAccount[victim]
	if err := a.exec.Close(); err != nil {
		// Still drop the entry: keeping a closed pool would hand the account a
		// dead executor. The file survives, so the next open is honest.
		s.opts.Log.Warn("evict LRU SQLite workspace", "err", err)
	}
	delete(s.byAccount, victim)
	s.opts.Log.Info("evicted LRU SQLite workspace",
		"cached", len(s.byAccount), "limit", s.opts.MaxAccounts)
}

// Close releases every executor. A double Close is a no-op so the gateway's
// shutdown path can be unconditional.
func (s *SQLiteEnvironments) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
	if s.shared != nil {
		if err := s.shared.Close(); err != nil {
			firstErr = err
		}
	}
	for _, a := range s.byAccount {
		if err := a.exec.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.byAccount = map[string]*accountEnvironment{}
	return firstErr
}

// seed creates path from the embedded schema and seed if it is not there yet.
//
// It writes a private file and renames it into place, so a reader that arrives
// mid-seed sees either nothing or the whole fixture — never a half-applied
// script, which is the SQLite shape of ADR 0002's "a copy is the reseed".
func (s *SQLiteEnvironments) seed(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", filepath.Base(path), err)
	}

	tmp := path + ".seed"
	_ = os.Remove(tmp)
	defer func() { _ = os.Remove(tmp) }()

	db, err := sql.Open(engine.SQLiteDriverName, sqliteSeedDSN(tmp))
	if err != nil {
		return fmt.Errorf("open %s for seeding: %w", filepath.Base(path), err)
	}
	for _, step := range []struct{ name, script string }{
		{"schema", dataset.SQLiteSchemaSQL()},
		{"seed", dataset.SQLiteSeedSQL()},
	} {
		if _, err := db.ExecContext(ctx, step.script); err != nil {
			_ = db.Close()
			return fmt.Errorf("apply the SQLite %s: %w", step.name, err)
		}
	}
	// Close before the rename: the file must be flushed to be the fixture the
	// next open expects, and a rename of a file another handle still holds is
	// exactly the window this pattern is here to close.
	if err := db.Close(); err != nil {
		return fmt.Errorf("close the seeded SQLite database: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("move the seeded SQLite database into place: %w", err)
	}
	return nil
}

// sqliteSeedDSN is the one-time write DSN for seeding.
//
// It carries the same pragmas every runtime connection gets. journal_mode is
// deliberately not set: a WAL file cannot be opened read-only on every platform,
// and the shared fixture is opened read-only, so the seeded file stays in
// SQLite's default rollback-journal mode.
func sqliteSeedDSN(path string) string {
	q := url.Values{}
	for _, p := range engine.SQLiteReadOnlyPragmas() {
		if strings.TrimSpace(p) != "" {
			q.Add("_pragma", p)
		}
	}
	u := url.URL{Scheme: "file", Path: path, OmitHost: true, RawQuery: q.Encode()}
	return u.String()
}

// accountFileStem is a stable, filesystem-safe file name for an account.
//
// A hash rather than the account id keeps a learner's email address out of the
// data directory's file names, while still giving two accounts two files. Sixteen
// bytes of SHA-256 is far more than enough to keep a single deployment's
// accounts apart.
func accountFileStem(accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return hex.EncodeToString(sum[:16])
}
