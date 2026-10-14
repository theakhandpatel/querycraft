// Package envdb gives each signed-in learner their own writable PostgreSQL
// database, created as a copy of one sealed template.
//
// The shared dataset the gateway serves is read-only on purpose: one learner
// typing DELETE FROM orders must not change what another learner sees, and a
// course that only runs SELECT cannot teach INSERT, UPDATE, DELETE, DDL or
// transactions. So the read-only dataset stays the anonymous path, and a
// challenge environment is a separate database the learner owns — wrong DML
// there breaks their copy and nobody else's, and Reset restores it from the
// template in one operation.
//
// This package is the library layer for that: it owns the template's lifecycle,
// the environment databases, one small connection pool per active environment,
// the capacity cap and the idle reaper. The HTTP surface that calls it lives in
// the gateway; nothing here imports net/http.
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
	AdminDSN        string            // owner/superuser DSN: CREATE/DROP DATABASE and the template build
	LearnerPassword string            // the password the existing querycraft_learner role has
	TemplateName    string            // default dataset.TemplateDatabaseName
	MaxEnvironments int               // default 8
	MaxConnsPerEnv  int32             // default 2
	IdleTTL         time.Duration     // default 15 * time.Minute
	Log             *slog.Logger      // nil => discard
	Metrics         *metrics.Registry // nil => no metrics
}

func (o Options) withDefaults() Options {
	if o.TemplateName == "" {
		o.TemplateName = dataset.TemplateDatabaseName
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
	resetAt   time.Time
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

	mu     sync.Mutex
	envs   map[string]*environment
	closed bool

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
	var exists bool
	if err := m.admin.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", m.opts.TemplateName,
	).Scan(&exists); err != nil {
		return envErr(fmt.Errorf("look up template %q: %w", m.opts.TemplateName, err))
	}
	if !exists {
		m.opts.Log.Info("building challenge template", "template", m.opts.TemplateName)
		if err := dataset.CreateDatabase(ctx, m.admin, m.opts.TemplateName); err != nil {
			return envErr(err)
		}
		// Writable, not read-only: this template is what every challenge
		// environment is copied from, and the copy carries its grant set.
		if err := dataset.LoadWritableIntoTemplate(ctx, m.admin, m.opts.TemplateName, m.opts.LearnerPassword); err != nil {
			return envErr(err)
		}
	}
	if err := dataset.SealTemplate(ctx, m.admin, m.opts.TemplateName); err != nil {
		return envErr(err)
	}
	return nil
}

// Ensure returns a writable executor for the account's environment, creating
// the database from the template on first use. The executor (and therefore the
// database) is cached per account, so a second call returns the same pool and
// the learner's writes are still there.
//
// The account id is hashed, never interpolated: see databaseName.
func (m *Manager) Ensure(ctx context.Context, accountID string) (*engine.PgxExecutor, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, errors.New("envdb: empty account id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errClosed()
	}

	key := digest(accountID)
	if e, ok := m.envs[key]; ok {
		e.lastUsed = time.Now()
		return e.exec, nil
	}
	if len(m.envs) >= m.opts.MaxEnvironments {
		return nil, fmt.Errorf("%w: %w (limit %d active)", engine.ErrEnvironmentUnavailable, ErrTooManyEnvironments, m.opts.MaxEnvironments)
	}
	if err := m.ensureTemplateLocked(ctx); err != nil {
		return nil, err
	}

	database := m.databaseName(accountID)
	dsn, err := m.learnerDSN(database)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	if err := m.createIfAbsentLocked(ctx, database); err != nil {
		return nil, err
	}
	exec, err := engine.NewPgxExecutorWithConfig(ctx, dsn, engine.PgxConfig{
		MaxConns: m.opts.MaxConnsPerEnv,
		MinConns: 1,
		Writable: true,
	})
	if err != nil {
		return nil, envErr(err)
	}
	// A template built without the writable grants copies into environments
	// where every learner write fails. Verify once here, where the cause is a
	// mis-built template, rather than at the learner's first INSERT.
	if err := probeWritable(ctx, exec); err != nil {
		exec.Close()
		return nil, envErr(fmt.Errorf("environment %q is not writable: %w", database, err))
	}

	created := time.Since(start)
	now := time.Now()
	m.envs[key] = &environment{
		account:   accountID,
		database:  database,
		exec:      exec,
		createdAt: now,
		lastUsed:  now,
	}
	m.observeCreate(created)
	m.publishActiveLocked()
	m.opts.Log.Info("challenge environment ready",
		"account", accountID, "database", database, "created_in", created.String())
	return exec, nil
}

// Reset drops the account's environment and rebuilds it from the template,
// returning how long that took. The cached executor is closed and dropped so
// the next Ensure binds to the fresh copy: a reset that left the old pool
// pointing at a dropped database would fail on the very next statement.
func (m *Manager) Reset(ctx context.Context, accountID string) (time.Duration, error) {
	if strings.TrimSpace(accountID) == "" {
		return 0, errors.New("envdb: empty account id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, errClosed()
	}

	key := digest(accountID)
	if e, ok := m.envs[key]; ok {
		e.exec.Close()
		delete(m.envs, key)
	}
	if err := m.ensureTemplateLocked(ctx); err != nil {
		return 0, err
	}

	database := m.databaseName(accountID)
	start := time.Now()
	if err := dataset.ResetEnvironment(ctx, m.admin, database, m.opts.TemplateName); err != nil {
		return 0, envErr(err)
	}
	took := time.Since(start)
	m.observeReset(took)
	m.publishActiveLocked()
	m.opts.Log.Info("challenge environment reset",
		"account", accountID, "database", database, "took", took.String())
	return took, nil
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
		facts.ResetAt = e.resetAt
		facts.IdleUntil = e.lastUsed.Add(m.opts.IdleTTL)
	}
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
		return facts, envErr(err)
	}
	facts.Writable = writable
	facts.Tables = tables
	return facts, nil
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
		if err := dataset.DropDatabase(ctx, m.admin, e.database); err != nil {
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

func (m *Manager) createIfAbsentLocked(ctx context.Context, database string) error {
	var exists bool
	if err := m.admin.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", database,
	).Scan(&exists); err != nil {
		return envErr(fmt.Errorf("look up environment %q: %w", database, err))
	}
	if exists {
		return nil
	}
	if err := dataset.CreateDatabaseFromTemplate(ctx, m.admin, database, m.opts.TemplateName); err != nil {
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
func (m *Manager) reapOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	now := time.Now()
	for key, e := range m.envs {
		if now.Sub(e.lastUsed) < m.opts.IdleTTL {
			continue
		}
		if err := dataset.DropDatabase(ctx, m.admin, e.database); err != nil {
			// A reap that failed is retried on the next tick, not fatal.
			m.opts.Log.Warn("reap idle environment", "database", e.database, "error", err)
			continue
		}
		e.exec.Close()
		delete(m.envs, key)
		m.opts.Log.Info("reaped idle environment",
			"database", e.database, "idle_for", now.Sub(e.lastUsed).String())
	}
	m.publishActiveLocked()
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
