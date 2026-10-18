// Package envdb owns the learner environment: the writable workspace a
// signed-in learner runs SQL in. It serves two backends of that one job. The
// PostgreSQL backend gives each account a database copied from one sealed
// template. The SQLite backend gives each account a database file seeded from
// the committed schema and seed.
//
// Both backends keep the same rule. The shared dataset the gateway serves is
// read-only on purpose: one learner typing DELETE FROM orders must not change
// what another learner sees, and a course that only runs SELECT cannot teach
// INSERT, UPDATE, DELETE, DDL or transactions. The read-only dataset stays the
// anonymous path. A learner's environment is separate and writable, so wrong
// DML there breaks their copy and nobody else's, and Reset restores it.
//
// This package owns each backend's lifecycle: the sealed templates and the
// environment databases, the per-account SQLite files, one small connection
// pool per active environment, the capacity cap and the idle reaper. The HTTP
// surface that calls it lives in the gateway; nothing here imports net/http.
//
// Design and the measurements behind it: docs/adr/0002-challenge-environments.md.
package envdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
)

// Metric names recorded when a Registry is supplied. They are exported so the
// gateway's /metrics and /diagnostics can declare the same strings instead of
// restating them.
const (
	MetricCreateSeconds = "querycraft_environment_create_seconds"
	MetricResetSeconds  = "querycraft_environment_reset_seconds"
	MetricActive        = "querycraft_environments_active"
)

const (
	helpCreate = "Time to make a challenge environment available (create from the sealed template)."
	helpReset  = "Time to reset a challenge environment (drop it and re-copy it from the template)."
	helpActive = "Challenge environments the gateway currently holds open (created, not yet reaped)."
)

// Defaults applied by New for any Options field left zero.
const (
	defaultMaxEnvironments = 8
	defaultMaxConnsPerEnv  = 2
	defaultIdleTTL         = 15 * time.Minute
)

// dropTimeout bounds a single DROP/CREATE during Close or a reap. It is longer
// than a reset on purpose: the first DROP in a cluster pays a forced checkpoint,
// measured at ~2.9 s in the ADR, and a reap that gave up early would leak the
// database it was trying to remove.
const dropTimeout = 60 * time.Second

// ErrTooManyEnvironments marks a request refused because every environment slot
// is already in use. It is a typed error rather than a raw PostgreSQL error so
// the caller can distinguish "the server said no" from "the gateway is full".
//
// Errors that mean the environment cannot be used also wrap
// engine.ErrEnvironmentUnavailable, so the gateway maps them to the contract's
// ENVIRONMENT_UNAVAILABLE without special-casing this package.
var ErrTooManyEnvironments = errors.New("too many active challenge environments")

// Options configures a Manager.
type Options struct {
	AdminDSN         string            // owner/superuser DSN: CREATE/DROP DATABASE and the template build
	LearnerPassword  string            // the password the existing querycraft_learner role has
	TemplateName     string            // default dataset.TemplateDatabaseName
	PerfTemplateName string            // default dataset.PerfTemplateDatabaseName (plan environments)
	MaxEnvironments  int               // default 8
	MaxConnsPerEnv   int32             // default 2
	IdleTTL          time.Duration     // default 15 * time.Minute
	Log              *slog.Logger      // nil => discard
	Metrics          *metrics.Registry // nil => no metrics
}

func (o Options) withDefaults() Options {
	if o.TemplateName == "" {
		o.TemplateName = dataset.TemplateDatabaseName
	}
	if o.PerfTemplateName == "" {
		o.PerfTemplateName = dataset.PerfTemplateDatabaseName
	}
	if o.MaxEnvironments <= 0 {
		o.MaxEnvironments = defaultMaxEnvironments
	}
	if o.MaxConnsPerEnv <= 0 {
		o.MaxConnsPerEnv = defaultMaxConnsPerEnv
	}
	if o.IdleTTL <= 0 {
		o.IdleTTL = defaultIdleTTL
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return o
}

// Facts is the honest answer to "what is this account's environment?" without
// creating one. It is what the diagnostics view and the API surface report.
type Facts struct {
	Account   string    `json:"account"`
	Database  string    `json:"database"`
	Exists    bool      `json:"exists"`
	Writable  bool      `json:"writable"`
	Tables    int       `json:"tables"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	ResetAt   time.Time `json:"reset_at,omitempty"`
	IdleUntil time.Time `json:"idle_until,omitempty"`
}

// environment is the manager's record of one account's database and pool.
type environment struct {
	account   string
	database  string
	exec      *engine.PgxExecutor
	createdAt time.Time
	lastUsed  time.Time
}

// Manager owns the template and every environment database it has created.
//
// It is safe for concurrent use. The admin connection is a single pgx.Conn, so
// every statement on it is serialised by the mutex; environment pools are
// independent and are not held under the lock.
type Manager struct {
	opts     Options
	adminCfg *pgx.ConnConfig
	admin    *pgx.Conn

	mu   sync.Mutex
	envs map[string]*environment
	// resetAt records the last reset per account. It cannot live on the
	// `environment` struct: reset drops that record and the next Ensure creates a
	// fresh one, so a reset time stored there would be lost before Facts could
	// read it. The map is keyed by the environment kind, so a plan reset time and
	// a challenge reset time never overwrite each other.
	resetAt map[string]time.Time
	// building marks an account whose environment is being created by one
	// goroutine, so a concurrent Ensure for the same account waits for that
	// result instead of opening a second pool and racing the envs write. The
	// per-account build (pool connect + probe) runs outside the lock, so this
	// map is what keeps that safe.
	building map[string]chan struct{}
	closed   bool

	stop chan struct{}
	wg   sync.WaitGroup
}

// New connects to the admin DSN and starts the idle reaper. It does not build
// the template or create any environment; EnsureTemplate and Ensure do that.
func New(ctx context.Context, opts Options) (*Manager, error) {
	opts = opts.withDefaults()
	if strings.TrimSpace(opts.AdminDSN) == "" {
		return nil, fmt.Errorf("%w: envdb: no admin DSN configured", engine.ErrEnvironmentUnavailable)
	}
	adminCfg, err := pgx.ParseConfig(opts.AdminDSN)
	if err != nil {
		return nil, fmt.Errorf("%w: envdb: parse admin DSN: %v", engine.ErrEnvironmentUnavailable, err)
	}
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return nil, fmt.Errorf("%w: envdb: connect admin: %v", engine.ErrEnvironmentUnavailable, err)
	}
	m := &Manager{
		opts:     opts,
		adminCfg: adminCfg,
		admin:    admin,
		envs:     map[string]*environment{},
		resetAt:  map[string]time.Time{},
		building: map[string]chan struct{}{},
		stop:     make(chan struct{}),
	}
	m.wg.Add(1)
	go m.reapLoop()
	return m, nil
}

// EnsureTemplate builds and seals the template database if it is missing, and
// re-seals it otherwise. Sealing is idempotent and is what makes the template
// copyable at any instant: PostgreSQL refuses to copy a database another
// session is attached to, and ALLOW_CONNECTIONS false guarantees none ever is.
func (m *Manager) EnsureTemplate(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errClosed()
	}
	return m.ensureTemplateLocked(ctx)
}

func (m *Manager) ensureTemplateLocked(ctx context.Context) error {
	// Writable, not read-only: this template is what every challenge
	// environment is copied from, and the copy carries its grant set.
	return m.ensureTemplateFromLocked(ctx, m.opts.TemplateName, "challenge", challengeEnv.load(m))
}

// EnsurePerfTemplate builds and seals the perf template if it is missing, and
// re-seals it otherwise. It is exported so the gateway can pay for the larger
// fixture at boot rather than on a learner's first plan request.
func (m *Manager) EnsurePerfTemplate(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errClosed()
	}
	return m.ensurePerfTemplateLocked(ctx)
}

func (m *Manager) ensurePerfTemplateLocked(ctx context.Context) error {
	// Writable: the plan lessons ask the learner to add the missing index
	// and re-run the statement, which only an environment they own can do.
	return m.ensureTemplateFromLocked(ctx, m.opts.PerfTemplateName, "perf", perfEnv.load(m))
}

// ensureTemplateFromLocked builds and seals one template. The challenge and perf
// templates differ only in their name, their log label and the fixture the kind
// loads into them, so the create/seal body is shared rather than copied. Two
// near-copies drift; one body cannot.
func (m *Manager) ensureTemplateFromLocked(ctx context.Context, name, label string, load func(context.Context, string) error) error {
	var exists bool
	if err := m.admin.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name,
	).Scan(&exists); err != nil {
		return envErr(fmt.Errorf("look up %s template %q: %w", label, name, err))
	}
	if !exists {
		m.opts.Log.Info("building "+label+" template", "template", name)
		if err := CreateDatabase(ctx, m.admin, name); err != nil {
			return envErr(err)
		}
		if err := load(ctx, name); err != nil {
			return envErr(err)
		}
	}
	if err := SealTemplate(ctx, m.admin, name); err != nil {
		return envErr(err)
	}
	return nil
}

// EnsurePerf returns a writable executor over the account's plan environment:
// the perf dataset, copied from its own sealed template on first use.
//
// It is deliberately a second environment rather than a second dataset inside
// the challenge environment. A plan lesson asks the learner to CREATE INDEX, and
// a challenge lesson asks them to INSERT and DELETE; sharing one database would
// let an experiment in one lesson decide the plan the other one is graded on.
// The entry lives in the same environment map, so the capacity cap, the idle
// reaper, the metrics and Close see it exactly like any other environment.
func (m *Manager) EnsurePerf(ctx context.Context, accountID string) (*engine.PgxExecutor, error) {
	return m.ensure(ctx, accountID, perfEnv)
}

// ResetPerf restores the account's plan environment from the sealed perf
// template, reporting how long the copy took.
//
// It is the perf counterpart of Reset and deliberately does not touch the
// challenge environment. The two are separate databases on purpose — a
// re-indexed perf table must not be able to decide the plan a challenge lesson
// is graded on — so a reset that restored both would throw away the learner's
// commerce work as a side effect of asking for a clean lab.
func (m *Manager) ResetPerf(ctx context.Context, accountID string) (time.Duration, error) {
	return m.reset(ctx, accountID, perfEnv)
}

// Ensure returns a writable executor for the account's environment, creating
// the database from the template on first use. The executor (and therefore the
// database) is cached per account, so a second call returns the same pool and
// the learner's writes are still there.
//
// The account id is hashed, never interpolated: see databaseName.
func (m *Manager) Ensure(ctx context.Context, accountID string) (*engine.PgxExecutor, error) {
	return m.ensure(ctx, accountID, challengeEnv)
}

// Reset drops the account's environment and rebuilds it from the template,
// returning how long that took. The cached executor is closed and dropped so
// the next Ensure binds to the fresh copy: a reset that left the old pool
// pointing at a dropped database would fail on the very next statement.
func (m *Manager) Reset(ctx context.Context, accountID string) (time.Duration, error) {
	return m.reset(ctx, accountID, challengeEnv)
}

// envKind names the two kinds of per-account environment. The challenge
// environment is the learner's own commerce workspace; the perf environment is
// the plan lab, a second database copied from its own sealed template so a
// re-indexed plan lesson cannot decide the plan a challenge lesson is graded on.
//
// Every per-kind difference is a field here: the cache-key prefix, the template
// to copy from, the fixture that loads into it, the environment database name and
// the log/error labels. Ensure and Reset each have exactly one body instead of
// two near-copies that drift.
type envKind struct {
	keyPrefix string
	template  func(m *Manager) string
	database  func(m *Manager, accountID string) string
	load      func(m *Manager) func(context.Context, string) error
	label     string // "challenge" or "plan", for logs and error messages
}

var challengeEnv = envKind{
	keyPrefix: "",
	template:  func(m *Manager) string { return m.opts.TemplateName },
	database:  func(m *Manager, accountID string) string { return m.databaseName(accountID) },
	load: func(m *Manager) func(context.Context, string) error {
		return func(ctx context.Context, name string) error {
			return dataset.LoadWritableIntoTemplate(ctx, m.admin, name, m.opts.LearnerPassword)
		}
	},
	label: "challenge",
}

var perfEnv = envKind{
	keyPrefix: "perf:",
	template:  func(m *Manager) string { return m.opts.PerfTemplateName },
	database: func(_ *Manager, accountID string) string {
		return dataset.EnvironmentDatabaseName("perf_" + digest(accountID)[:32])
	},
	load: func(m *Manager) func(context.Context, string) error {
		return func(ctx context.Context, name string) error {
			return dataset.LoadPerfIntoTemplate(ctx, m.admin, name, m.opts.LearnerPassword)
		}
	},
	label: "plan",
}

func (m *Manager) ensure(ctx context.Context, accountID string, kind envKind) (*engine.PgxExecutor, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, errors.New("envdb: empty account id")
	}
	key := kind.keyPrefix + digest(accountID)

	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errClosed()
		}
		if e, ok := m.envs[key]; ok {
			e.lastUsed = time.Now()
			m.mu.Unlock()
			return e.exec, nil
		}
		// Another goroutine is already building THIS account's environment.
		// Wait for it rather than building a second one, which would leak a
		// pool and race the map write. The lock is released while waiting.
		if wait, inflight := m.building[key]; inflight {
			m.mu.Unlock()
			select {
			case <-wait:
			case <-ctx.Done():
				return nil, envErr(ctx.Err())
			}
			continue
		}
		if len(m.envs) >= m.opts.MaxEnvironments {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: %w (limit %d active)", engine.ErrEnvironmentUnavailable, ErrTooManyEnvironments, m.opts.MaxEnvironments)
		}
		// The admin-connection statements (template lookup/build, create-if-absent)
		// need the lock; the pool connect and the writability probe below do NOT
		// — they are per-account and touch no shared state. Holding the single
		// mutex across them blocked every other account for the whole first
		// create (ADR 0002 measured a first drop at ~2.9 s), so they run outside
		// it, guarded only by this in-flight marker.
		template := kind.template(m)
		if err := m.ensureTemplateFromLocked(ctx, template, kind.label, kind.load(m)); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		database := kind.database(m, accountID)
		dsn, err := m.learnerDSN(database)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		if err := m.createIfAbsentFromLocked(ctx, database, template); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		done := make(chan struct{})
		m.building[key] = done
		m.mu.Unlock()

		exec, created, buildErr := m.buildEnvironment(ctx, kind, database, dsn)

		m.mu.Lock()
		delete(m.building, key)
		if m.closed {
			// Close ran while this build was outside the lock. Installing the
			// environment now would leave a pool and a database alive after
			// Close promised to drop every environment it owns, and the next
			// Ensure would be refused anyway. Close the pool the build just
			// opened and return the same refusal the caller would get at the
			// top of this loop.
			if exec != nil {
				exec.Close()
			}
			close(done)
			m.mu.Unlock()
			return nil, errClosed()
		}
		if buildErr == nil {
			now := time.Now()
			m.envs[key] = &environment{
				account: accountID, database: database, exec: exec,
				createdAt: now, lastUsed: now,
			}
			m.observeCreate(created)
			m.publishActiveLocked()
			m.opts.Log.Info(kind.label+" environment ready",
				"account", accountID, "database", database, "created_in", created.String())
		}
		close(done)
		m.mu.Unlock()
		return exec, buildErr
	}
}

// buildEnvironment connects the per-account pool and probes it writable, outside
// the manager lock (see ensure). A failure closes the pool it opened so no
// connection leaks.
func (m *Manager) buildEnvironment(ctx context.Context, kind envKind, database, dsn string) (*engine.PgxExecutor, time.Duration, error) {
	start := time.Now()
	exec, err := engine.NewPgxExecutorWithConfig(ctx, dsn, engine.PgxConfig{
		MaxConns: m.opts.MaxConnsPerEnv,
		MinConns: 1,
		Writable: true,
	})
	if err != nil {
		return nil, 0, envErr(err)
	}
	// A template built without the writable grants copies into environments
	// where every learner write fails. Verify once here, where the cause is a
	// mis-built template, rather than at the learner's first INSERT.
	if err := probeWritable(ctx, exec); err != nil {
		exec.Close()
		return nil, 0, envErr(fmt.Errorf("%s environment %q is not writable: %w", kind.label, database, err))
	}
	return exec, time.Since(start), nil
}

func (m *Manager) reset(ctx context.Context, accountID string, kind envKind) (time.Duration, error) {
	if strings.TrimSpace(accountID) == "" {
		return 0, errors.New("envdb: empty account id")
	}
	key := kind.keyPrefix + digest(accountID)

	var database, template string
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return 0, errClosed()
		}
		// Another goroutine is already building or resetting THIS account's
		// environment. Wait for it, exactly like ensure, so two DDL statements
		// cannot race over one database. The lock is released while waiting.
		if wait, inflight := m.building[key]; inflight {
			m.mu.Unlock()
			select {
			case <-wait:
			case <-ctx.Done():
				return 0, envErr(ctx.Err())
			}
			continue
		}
		if e, ok := m.envs[key]; ok {
			e.exec.Close()
			delete(m.envs, key)
		}
		template = kind.template(m)
		if err := m.ensureTemplateFromLocked(ctx, template, kind.label, kind.load(m)); err != nil {
			m.mu.Unlock()
			return 0, err
		}

		// The name must be the one ensure creates, so a reset restores the
		// environment the lesson is actually running against rather than a
		// second, empty one.
		database = kind.database(m, accountID)
		// The copy is seconds of DDL (ADR 0002 measured the first drop at
		// ~2.9 s), so it must run outside the state lock, guarded by the same
		// in-flight marker a build uses. Holding the lock across it froze every
		// other account for the whole reset.
		done := make(chan struct{})
		m.building[key] = done
		m.mu.Unlock()

		start := time.Now()
		err := m.resetDatabase(ctx, database, template)

		m.mu.Lock()
		delete(m.building, key)
		close(done)
		if err != nil {
			m.mu.Unlock()
			return 0, err
		}
		took := time.Since(start)
		// The key carries the environment kind, so a plan reset publishes its
		// own time and leaves the challenge reset time Facts reports alone.
		m.resetAt[key] = start
		m.observeReset(took)
		m.publishActiveLocked()
		m.opts.Log.Info(kind.label+" environment reset",
			"account", accountID, "database", database, "took", took.String())
		m.mu.Unlock()
		return took, nil
	}
}

// resetDatabase drops and rebuilds one environment database on a fresh
// administrative connection. The manager's admin connection is a single
// pgx.Conn that every catalog statement under the state lock shares, and pgx
// does not allow concurrent use of one connection. The copy runs outside the
// lock, so it gets a connection of its own.
func (m *Manager) resetDatabase(ctx context.Context, database, template string) error {
	conn, err := m.newAdminConn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err := ResetEnvironment(ctx, conn, database, template); err != nil {
		return envErr(err)
	}
	return nil
}

// newAdminConn opens a second administrative connection for slow DDL that runs
// outside the manager's state lock. It copies the connection config, so the
// caller can close it without touching the shared admin connection.
func (m *Manager) newAdminConn(ctx context.Context) (*pgx.Conn, error) {
	cfg := m.adminCfg.Copy()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, envErr(fmt.Errorf("open a DDL connection: %w", err))
	}
	return conn, nil
}

// Facts reports the account's environment without creating it. Exists is read
// from the catalog, so it is true for an environment this process did not
// create; Writable and Tables are read over the learner's own connection, so
// they describe what a learner statement actually sees.
func (m *Manager) Facts(ctx context.Context, accountID string) (Facts, error) {
	if strings.TrimSpace(accountID) == "" {
		return Facts{}, errors.New("envdb: empty account id")
	}
	database := m.databaseName(accountID)
	facts := Facts{Account: accountID, Database: database}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Facts{}, errClosed()
	}
	if e, ok := m.envs[digest(accountID)]; ok {
		facts.CreatedAt = e.createdAt
		facts.IdleUntil = e.lastUsed.Add(m.opts.IdleTTL)
	}
	// Facts describes the challenge environment, so it reads the challenge
	// key. A plan reset writes the perf key and must not change this time.
	facts.ResetAt = m.resetAt[challengeEnv.keyPrefix+digest(accountID)]
	err := m.admin.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", database,
	).Scan(&facts.Exists)
	m.mu.Unlock()
	if err != nil {
		return Facts{}, envErr(fmt.Errorf("look up environment %q: %w", database, err))
	}
	if !facts.Exists {
		return facts, nil
	}

	writable, tables, err := m.inspect(ctx, database)
	if err != nil {
		// The idle reaper drops databases outside the state lock, so the
		// database can disappear between the catalog check above and this
		// connection. That is the same answer as Exists=false, not a failed
		// environment.
		if isDatabaseGone(err) {
			facts.Exists = false
			return facts, nil
		}
		return facts, envErr(err)
	}
	facts.Writable = writable
	facts.Tables = tables
	return facts, nil
}

// isDatabaseGone reports whether PostgreSQL refused the work because the
// database no longer exists (SQLSTATE 3D000). Facts races its own idle reaper,
// and this is how it tells "that environment was just reaped" from "the server
// is unreachable".
func isDatabaseGone(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "3D000"
}

// Close stops the reaper, drops every environment this manager owns, and closes
// the pools and the admin connection. It is safe to call more than once.
//
// Dropping on close is deliberate: an environment is a scratch copy of the
// template, and leaving hundreds of them behind to be discovered by the next
// boot is how a cluster fills its disk with databases nobody remembers.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.stop)
	m.mu.Unlock()

	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()
	for key, e := range m.envs {
		// Close the pool first: DROP ... WITH (FORCE) would terminate its
		// connections anyway, but closing it makes "no pool outlives Close"
		// true by construction.
		e.exec.Close()
		if err := DropDatabase(ctx, m.admin, e.database); err != nil {
			m.opts.Log.Warn("drop environment on close", "database", e.database, "error", err)
		}
		delete(m.envs, key)
	}
	m.publishActiveLocked()
	if m.admin != nil {
		_ = m.admin.Close(context.WithoutCancel(ctx))
		m.admin = nil
	}
}

/* ---------- internals ---------- */

// createIfAbsentFromLocked copies one sealed template into an environment
// database. It takes the template as an argument because the plan environments
// are copied from the perf template while every other environment is copied from
// the challenge template.
func (m *Manager) createIfAbsentFromLocked(ctx context.Context, database, template string) error {
	var exists bool
	if err := m.admin.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", database,
	).Scan(&exists); err != nil {
		return envErr(fmt.Errorf("look up environment %q: %w", database, err))
	}
	if exists {
		return nil
	}
	if err := CreateDatabaseFromTemplate(ctx, m.admin, database, template); err != nil {
		return envErr(err)
	}
	return nil
}

// inspect reads table count and write privilege over a short-lived learner
// connection, so the answer is what the restricted role sees rather than what
// the admin assumes.
func (m *Manager) inspect(ctx context.Context, database string) (writable bool, tables int, err error) {
	cfg := m.adminCfg.Copy()
	cfg.Database = database
	cfg.User = dataset.LearnerRole
	cfg.Password = m.opts.LearnerPassword
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return false, 0, fmt.Errorf("connect to environment %q: %w", database, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_catalog.pg_class c
		   JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = $1 AND c.relkind = 'r'`,
		dataset.SchemaName,
	).Scan(&tables); err != nil {
		return false, 0, fmt.Errorf("count tables in %q: %w", database, err)
	}
	if err := conn.QueryRow(ctx,
		"SELECT has_table_privilege(current_user, $1, 'INSERT')",
		dataset.SchemaName+".customers",
	).Scan(&writable); err != nil {
		return false, 0, fmt.Errorf("check write access in %q: %w", database, err)
	}
	return writable, tables, nil
}

// probeWritable asserts that a freshly copied environment really accepts
// writes. It runs over the executor the caller will use, so it fails exactly
// when the first learner INSERT would.
func probeWritable(ctx context.Context, ex *engine.PgxExecutor) error {
	rows, err := ex.Query(ctx, "SELECT has_table_privilege(current_user, $1, 'INSERT')", dataset.SchemaName+".customers")
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return errors.New("privilege probe returned no row")
	}
	values, err := rows.Values()
	if err != nil {
		return err
	}
	if len(values) != 1 {
		return fmt.Errorf("privilege probe returned %d columns", len(values))
	}
	allowed, _ := values[0].(bool)
	if !allowed {
		return errors.New("the learner role cannot INSERT in the copied environment; the template was built read-only")
	}
	return nil
}

// learnerDSN derives the restricted-role connection string for an environment
// from the admin DSN. It swaps the user and password and points at the
// environment database; it never changes the host, port or SSL settings.
func (m *Manager) learnerDSN(database string) (string, error) {
	u, err := url.Parse(m.opts.AdminDSN)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", envErr(fmt.Errorf("envdb: admin DSN must be a postgres:// URL to derive an environment DSN"))
	}
	u.User = url.UserPassword(dataset.LearnerRole, m.opts.LearnerPassword)
	u.Path = "/" + database
	return u.String(), nil
}

// databaseName maps an account id to a database name. The id is hashed rather
// than sanitised: sanitising an email or a uuid and then truncating to
// PostgreSQL's 63-byte identifier limit can silently map two accounts onto one
// database, and a truncated email still leaks the account id into every catalog
// listing. A digest is fixed-length, [0-9a-f] only, and distinct per account.
func (m *Manager) databaseName(accountID string) string {
	return dataset.EnvironmentDatabaseName("acct_" + digest(accountID)[:32])
}

// AccountLister is the slice of store.Store the reaper needs: the set of account
// ids the app still knows about. It is an interface here so envdb does not import
// store (store already imports ai; this keeps the dependency one-way).
type AccountLister interface {
	UserIDs(ctx context.Context) ([]string, error)
}

// ReapUnreferenced drops every environment database the store no longer
// references. It is ADR 0002's orphan mitigation. An orphan is a database
// created for an account that was never recorded (a crash between CREATE and the
// store write), so the recorded account ids are exactly the keep-set. It is
// best-effort: a failure to list or drop is reported, never fatal, because a
// reaper that cannot run must not stop the gateway serving.
func (m *Manager) ReapUnreferenced(ctx context.Context, store AccountLister) {
	if store == nil {
		return
	}
	ids, err := store.UserIDs(ctx)
	if err != nil {
		m.opts.Log.Warn("environment reaper: could not list accounts; skipping", "err", err)
		return
	}
	keep := make([]string, 0, len(ids)*2)
	for _, id := range ids {
		keep = append(keep, m.databaseName(id))
		keep = append(keep, perfEnv.database(m, id))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	// The sealed templates must never be dropped: they are not account
	// environments, but ListEnvironmentDatabases matches the qc prefix, so they
	// are added to the keep-set explicitly.
	keep = append(keep, m.opts.TemplateName, m.opts.PerfTemplateName)
	dropped, derr := ReapEnvironmentDatabases(ctx, m.admin, keep)
	if len(dropped) > 0 {
		m.opts.Log.Info("environment reaper dropped orphaned databases", "count", len(dropped), "names", dropped)
	}
	if derr != nil {
		m.opts.Log.Warn("environment reaper: some databases could not be dropped", "err", derr)
	}
}

// digest is a stable, filesystem-safe hash of an account id. A hash rather than
// the id keeps a learner's email address out of database and file names, while
// still giving two accounts two environments. The first 32 hex characters (16
// bytes) of the 64-character digest are far more than enough to keep a single
// deployment's accounts apart, and both the database names and the SQLite file
// names truncate to that length.
func digest(accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) reapLoop() {
	defer m.wg.Done()
	interval := m.opts.IdleTTL / 4
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.reapOnce()
		}
	}
}

// reapOnce drops every environment that has been idle past IdleTTL. The learner
// pays ~35 ms to get it back; the cluster gets its connection slot and 7.6 MiB
// back instead of holding them for a learner who has gone.
//
// The DROP runs outside the state lock, one victim at a time, and uses a fresh
// admin connection for the same reason reset does: holding the lock across a
// sweep would freeze every other account's Ensure and Facts for the whole sweep.
// Each victim is marked in flight first, so an Ensure for the same account waits
// for the drop instead of binding a pool to a database about to disappear.
func (m *Manager) reapOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()

	type victim struct {
		key string
		env *environment
		// done is the in-flight marker: waiters on m.building[key] block on it
		// until the drop lands.
		done chan struct{}
	}
	var victims []victim

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	now := time.Now()
	for key, e := range m.envs {
		if now.Sub(e.lastUsed) < m.opts.IdleTTL {
			continue
		}
		if _, inflight := m.building[key]; inflight {
			continue
		}
		done := make(chan struct{})
		m.building[key] = done
		victims = append(victims, victim{key: key, env: e, done: done})
		delete(m.envs, key)
	}
	m.publishActiveLocked()
	m.mu.Unlock()

	if len(victims) == 0 {
		return
	}

	conn, err := m.newAdminConn(ctx)
	if err != nil {
		// Nothing was dropped, so every victim is restored and the next tick
		// retries. A reaper that cannot run is not fatal.
		m.mu.Lock()
		for _, v := range victims {
			delete(m.building, v.key)
			close(v.done)
			m.envs[v.key] = v.env
		}
		m.publishActiveLocked()
		m.mu.Unlock()
		m.opts.Log.Warn("reap idle environments: could not open a DDL connection; retrying on the next sweep", "err", err)
		return
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	for _, v := range victims {
		dropErr := DropDatabase(ctx, conn, v.env.database)

		m.mu.Lock()
		delete(m.building, v.key)
		close(v.done)
		if dropErr != nil {
			// A reap that failed is retried on the next tick, not fatal.
			m.opts.Log.Warn("reap idle environment", "database", v.env.database, "error", dropErr)
			m.envs[v.key] = v.env
			m.publishActiveLocked()
			m.mu.Unlock()
			continue
		}
		m.mu.Unlock()

		v.env.exec.Close()
		m.opts.Log.Info("reaped idle environment",
			"database", v.env.database, "idle_for", now.Sub(v.env.lastUsed).String())
	}
}

func (m *Manager) observeCreate(d time.Duration) {
	if m.opts.Metrics == nil {
		return
	}
	m.opts.Metrics.Observe(MetricCreateSeconds, nil, d.Seconds(), helpCreate, nil)
}

func (m *Manager) observeReset(d time.Duration) {
	if m.opts.Metrics == nil {
		return
	}
	m.opts.Metrics.Observe(MetricResetSeconds, nil, d.Seconds(), helpReset, nil)
}

// publishActiveLocked updates the active-environment gauge. Callers hold mu.
func (m *Manager) publishActiveLocked() {
	if m.opts.Metrics == nil {
		return
	}
	m.opts.Metrics.Set(MetricActive, nil, float64(len(m.envs)), helpActive)
}

// envErr marks a failure as "the environment could not be used" while keeping
// the underlying error reachable through errors.Is/As. The gateway maps the
// marker to the contract's ENVIRONMENT_UNAVAILABLE code.
func envErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, engine.ErrEnvironmentUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", engine.ErrEnvironmentUnavailable, err)
}

func errClosed() error {
	return envErr(errors.New("environment manager is closed"))
}
