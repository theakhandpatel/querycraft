// Command api is the Querycraft execution gateway (Adapter B): a JSON HTTP
// service that runs learner SQL against native PostgreSQL and grades it against
// result contracts. It runs usefully with no database configured: SQL execution
// reports ENVIRONMENT_UNAVAILABLE and progress falls back to memory, so the
// React client always has something to talk to.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/auth"
	"github.com/querycraft/sqlquery/backend/internal/config"
	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/httpapi"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

func main() {
	// run owns the lifecycle, so its deferred cleanup (the embedded PostgreSQL,
	// the pools, the store) executes even on the misconfiguration refusal below;
	// os.Exit here would skip every defer and orphan the cluster.
	os.Exit(Execute())
}

// run starts the gateway and returns the process exit code: 0 for a clean
// signal-driven shutdown, 1 for a refusal to start. The settings arrive fully
// resolved: cli.go has already applied defaults, the config file, the
// environment and the command-line flags, in that order.
func run(settings config.Settings) int {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := httpapi.Config{
		MaxRows:       settings.MaxRows,
		MaxBytes:      settings.MaxBytes,
		TimeoutMS:     settings.TimeoutMS,
		MaxConcurrent: settings.MaxConcurrent,
		// Failed sign-in throttling (see login_throttle.go). The defaults are the
		// shipped ones, so an install that sets nothing is still limited.
		LoginMaxFailures: settings.LoginMaxFailures,
		LoginWindowMS:    settings.LoginWindowMS,
		LoginMaxTracked:  settings.LoginMaxTracked,
	}
	// Loopback by default: the seeded administrator may keep an empty password
	// only on a loopback bind (see the refusal below), which is the zero-setup
	// local flow. Reaching the gateway from the network is an explicit opt-in
	// through the addr setting (querycraft.json or --addr), and that
	// opt-in requires a real admin password.
	addr := settings.Addr

	// SIGHUP matters here: a terminal closing (or a parent shell exiting) sends
	// it, and without a handler the process dies instantly, orphaning the
	// embedded PostgreSQL it started. Treating it as a shutdown signal keeps the
	// cluster's lifecycle tied to the gateway's.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// The gateway owns the database. An external PostgreSQL (container, managed
	// service, or a local server) is used when database_url is set; otherwise
	// a real PostgreSQL is started from a repo-local data directory. Both paths
	// speak the same wire protocol, so the swap is a configuration change.
	// The SQLite dialect's files sit beside the embedded PostgreSQL's, under the
	// same .data root, and can be moved independently with the sqlite_data key (or
	// the sqlite_data key). The default derives from the pg_data setting, so
	// relocating the data directory relocates both dialects' state together.
	pgDataDir := settings.PGDataDir
	sqliteDataDir := settings.DeriveSQLiteRoot()

	dsn := settings.DatabaseURL
	adminPassword := ""
	var local *localdb.Server
	if dsn == "" && !settings.NoLocalDB {
		runtimeDir := settings.PGRuntimeDir
		/* The embedded server writes its postgres log into os.TempDir() and
		   never removes it. Give it a per-run directory beside the cluster
		   instead, so the log leaves with the process rather than accumulating
		   in the machine-wide temp directory across every restart. */
		logDir := ""
		logParent := filepath.Join(filepath.Dir(runtimeDir), "tmp")
		if err := os.MkdirAll(logParent, 0o700); err != nil {
			log.Warn("could not create the embedded server's log directory; it will use the system temp directory", "err", err)
		} else if dir, err := os.MkdirTemp(logParent, "run-"); err != nil {
			log.Warn("could not create the embedded server's log directory; it will use the system temp directory", "err", err)
		} else {
			logDir = dir
			defer func() { _ = os.RemoveAll(logDir) }()
		}
		cfg := localdb.Config{
			DataPath:     pgDataDir,
			RuntimePath:  runtimeDir,
			BinariesPath: settings.PGBinariesDir,
			TempDir:      logDir,
			Port:         uint32(settings.PGPort),
			User:         settings.PGUser,
			Password:     settings.PGPassword,
			Database:     settings.PGDatabase,
		}
		adminPassword = cfg.Password
		srv, err := localdb.Start(ctx, log, cfg)
		if err != nil {
			log.Warn("could not start the local database; SQL execution will report ENVIRONMENT_UNAVAILABLE", "err", err)
		} else {
			local = srv
			dsn = srv.DSN
			defer func() {
				if err := local.Stop(); err != nil {
					log.Warn("stopping local database failed", "err", err)
				}
			}()
		}
	}
	if adminPassword == "" {
		adminPassword = passwordFromDSN(dsn)
	}

	// Learner SQL must not run as the owner. The admin connection creates the
	// schema and owns the progress store; a restricted role executes learner
	// statements, so a fully qualified name cannot reach `app`, and the
	// superuser-only catalog and file functions are refused by PostgreSQL.
	learner := settings.LearnerDatabaseURL
	if learner == "" && dsn != "" {
		learner = swapDSNUser(dsn, dataset.LearnerRole, adminPassword)
	}
	if dsn != "" {
		ensureLearnerRole(ctx, log, dsn, adminPassword)
	}

	exec := newExecutor(ctx, log, learner)
	defer exec.Close()

	st, err := newStore(ctx, log, dsn, settings)
	if err != nil {
		log.Error("could not open explicitly selected metadata store", "err", err)
		return 1
	}
	defer st.Close()

	// Seed the local administrator before any request can be served. Accounts
	// own rows in app.progress/app.events through foreign keys, so the users
	// table has to exist with at least one row before the first write; without
	// this a fresh install would reject every progress save.
	adminEmail := settings.AdminEmail
	admin, err := auth.EnsureDefaultAdmin(ctx, st, auth.DefaultAdmin{
		Email:    adminEmail,
		Password: settings.AdminPassword,
		Name:     settings.AdminName,
	}, log)
	if err != nil {
		log.Error("could not seed the default administrator; sign-in will be unavailable until the database accepts writes", "err", err)
	} else {
		log.Info("default administrator ready", "email", admin.Email, "passwordSet", settings.AdminPassword != "")
	}
	// Read the *stored* state rather than the environment: an operator may have
	// set admin_password on a previous boot and removed it since, and the seed
	// deliberately never rewrites an existing account. The honest question is
	// "does an empty password sign this account in", which is exactly
	// auth.Verify-hash-against-"": the seeded admin's blank password is stored
	// as a real bcrypt hash, not as an empty string, so testing the hash for
	// emptiness would miss it.
	adminPasswordEmpty := false
	if err == nil {
		if _, hash, found, herr := st.UserByEmail(ctx, admin.Email); herr == nil && found {
			adminPasswordEmpty = auth.Verify(hash, "")
		}
	}
	// An empty-password administrator is a local-development convenience, so it
	// is allowed only on a loopback bind. Reaching the gateway over the network
	// is an explicit opt-in (a non-loopback addr), and that opt-in must not also publish a
	// blank-password admin. Never print the password or its hash: only that it
	// is unset.
	if adminPasswordEmpty && !isLoopbackAddr(addr) {
		log.Error("refusing to start: the default administrator has an empty password, which is allowed only on a loopback address; set admin_password in querycraft.json or bind the gateway to 127.0.0.1", "addr", addr, "admin", admin.Email)
		return 1
	}

	api := httpapi.New(exec, st, cfg, log)
	// Advertised on /health so the sign-in screen shows the local-admin hint
	// only when it is actually true. A boolean, never the password or its hash.
	api.UseAdminPasswordEmpty(adminPasswordEmpty)

	// ADR 0003: a signed-in learner gets their own writable database, copied
	// from the sealed template and created on first use. The manager needs the
	// administrative connection to CREATE/DROP the copies; with no database —
	// or if it cannot connect — it stays nil and the gateway answers
	// ENVIRONMENT_UNAVAILABLE rather than inventing a workspace. Anonymous
	// callers are unaffected: they keep the shared, read-only dataset.
	var envManager *envdb.Manager
	if dsn != "" {
		m, err := envdb.New(ctx, envdb.Options{
			AdminDSN:        dsn,
			LearnerPassword: adminPassword,
			Log:             log,
			Metrics:         api.Registry(),
			// The two environment knobs (ADR 0006). Zero keeps envdb's own
			// defaults (8 environments, a 15 minute idle window), which is what
			// every install that sets nothing gets.
			MaxEnvironments: settings.MaxEnvironments,
			IdleTTL:         settings.IdleTTL,
		})
		if err != nil {
			log.Warn("challenge environments unavailable; signed-in learners will be refused with ENVIRONMENT_UNAVAILABLE", "err", err)
		} else {
			// Registered after the local server's Stop defer, so it runs first
			// (LIFO): dropping a learner's database needs the cluster running.
			defer m.Close()
			api.UseEnvironments(m)
			envManager = m

			// Pay for both sealed templates at boot rather than on a learner's
			// first request. ensureTemplate/ensurePerfTemplate are both called
			// lazily under the manager's mutex, so the first challenge request
			// and the first plan request each built their template while every
			// other account waited. The perf template comment already claimed a
			// boot caller; this is it. A failure is logged, not fatal: the lazy
			// path still builds it on demand, so the gateway stays usable.
			if err := m.EnsureTemplate(ctx); err != nil {
				log.Warn("challenge template could not be pre-built; it will be built on first use", "err", err)
			}
			if err := m.EnsurePerfTemplate(ctx); err != nil {
				log.Warn("plan template could not be pre-built; it will be built on first use", "err", err)
			}

			// ADR 0002's orphan mitigation: a crash between creating an
			// environment database and recording it leaked the database forever.
			// Reap every qc environment the store no longer references at boot.
			// Best-effort and logged: a reaper that cannot run must not stop the
			// gateway from serving.
			reapOrphanedEnvironments(ctx, log, m, st)
		}
	}

	// M6 step 4: SQLite is the second dialect, and unlike PostgreSQL it needs no
	// server — the executor is cgo-free SQLite over a file — so the gateway can
	// serve it even in degraded mode. A signed-in learner gets their own
	// writable file (ADR 0003's split, with a file in place of a copied
	// database); an anonymous caller reads the shared fixture. If the root
	// cannot be prepared the dialect is not registered at all, so a sqlite
	// request is refused with DIALECT_MISMATCH instead of being answered by the
	// wrong engine.
	if sqliteEnvs, err := envdb.NewSQLiteEnvironmentsWithOptions(sqliteDataDir, sqliteOptions(settings)); err != nil {
		log.Warn("SQLite dialect unavailable; a request for it will be refused with DIALECT_MISMATCH", "err", err)
	} else {
		defer func() {
			if err := sqliteEnvs.Close(); err != nil {
				log.Warn("closing the SQLite environment store failed", "err", err)
			}
		}()
		api.UseSQLite(sqliteEnvs)
		log.Info("SQLite dialect ready",
			"root", sqliteEnvs.Root(),
			"max_accounts", sqliteEnvs.MaxAccounts(),
			"idle_ttl", sqliteEnvs.IdleTTL().String())
	}

	// M4 (ADR 0005): the challenge lab is a capability, not an assumption. It is
	// available only when a provider is named AND there is a database to verify
	// a generated artifact against; /capabilities reports which of the two is
	// missing instead of offering a lab that cannot work.
	//
	// The runner is assigned explicitly rather than handed over as a typed nil:
	// a nil *envdb.Manager stored in an interface is not nil, and would make an
	// unverifiable setup look available.
	var runner ai.Runner
	if envManager != nil {
		runner = envManager
	}
	api.UseAI(ai.Config{
		Provider:       aiProviderFrom(settings, log),
		Depot:          st,
		Runner:         runner,
		Logger:         log,
		Limits:         aiLimitsFrom(settings),
		DatasetVersion: dataset.DatasetVersion,
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// A generation run is bounded by ai.Limits.Timeout, which is minutes,
		// not by the server's write timeout: at 30 s the write timeout ended
		// the connection before the pipeline could answer, so the learner saw
		// a transport error instead of the honest stage-by-stage failure the
		// pipeline had already produced. ReadHeaderTimeout and ReadTimeout are
		// unchanged, so a slow client cannot hold a connection open with a
		// body it never sends.
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		log.Info("querycraft gateway listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	// A generation run is detached from its request (see ai/jobs.go), so the
	// server's Shutdown does not wait for it. Drain before run returns: the
	// deferred closes for the environment manager, the stores, the execution
	// pool and the embedded PostgreSQL all run after this function returns, and
	// a run that stored its result through one of them would lose the work.
	// A run that outlasts the drain window is reported rather than waited for
	// forever; its own context remains bounded by the pipeline's budget.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelDrain()
	if err := api.DrainJobs(drainCtx); err != nil {
		log.Warn("generation jobs did not finish before shutdown", "err", err)
	}
	return 0
}

// isLoopbackAddr reports whether the listen address binds only the loopback
// interface. Anything that could accept a connection from another host — a
// wildcard host like ":8080" or "0.0.0.0:8080", or a concrete non-loopback
// address — is not loopback, and a blank-password administrator is refused
// there. An unresolvable host is treated as non-loopback: failing closed is the
// safe answer when the interface cannot be proven local.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// A bare host with no port ("localhost") still names an interface.
		host = addr
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		// No host part at all: the wildcard bind.
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// A hostname: "localhost" and nothing else is loopback. Any other name may
	// resolve to a routable address, so it is not treated as local.
	return strings.EqualFold(host, "localhost")
}

func newExecutor(ctx context.Context, log *slog.Logger, dsn string) engine.Executor {
	if dsn == "" {
		log.Warn("no database configured; SQL execution will report ENVIRONMENT_UNAVAILABLE")
		return engine.UnavailableExecutor{}
	}
	ex, err := engine.NewPgxExecutor(ctx, dsn)
	if err != nil {
		log.Warn("could not open execution pool; SQL execution unavailable", "err", err)
		return engine.UnavailableExecutor{Reason: err.Error()}
	}
	if err := ex.Ping(ctx); err != nil {
		log.Warn("execution database unreachable; SQL execution unavailable", "err", err)
		// The pool has already spawned its background goroutines; close it so a
		// failed probe does not leak the pool for the process lifetime.
		_ = ex.Close()
		return engine.UnavailableExecutor{Reason: err.Error()}
	}
	return ex
}

func newStore(ctx context.Context, log *slog.Logger, dsn string, settings config.Settings) (store.Store, error) {
	adminDSN := settings.AdminDatabaseURL
	// admin_database_url is an explicit request for PostgreSQL metadata.
	// Otherwise the opt-in selects SQLite independently of the learner's
	// execution DSN (which is commonly the gateway's embedded PostgreSQL).
	if adminDSN == "" && strings.EqualFold(settings.Metadata, "sqlite") {
		path := settings.DeriveMetadataPath()
		sqliteStore, err := store.NewSQLite(ctx, path)
		if err != nil {
			return nil, err
		}
		return sqliteStore, nil
	}
	if adminDSN != "" {
		dsn = adminDSN
	}
	if dsn == "" {
		log.Warn("no metadata database configured; using in-memory progress store")
		return store.NewMemory(), nil
	}
	pg, err := store.NewPostgres(ctx, dsn)
	if err != nil {
		if errors.Is(err, store.ErrStaleSchema) {
			// Degrading to memory here would look like it worked and then lose
			// every account and snapshot on restart. Say what to do instead.
			log.Error("the metadata database was created before accounts existed and has no upgrade path; run `make db-reset` (or DROP SCHEMA app CASCADE) and restart; falling back to the in-memory store until then", "err", err)
			return store.NewMemory(), nil
		}
		log.Warn("metadata store unavailable; using in-memory store", "err", err)
		return store.NewMemory(), nil
	}
	return pg, nil
}

// reapOrphanedEnvironments runs the boot reaper, unless the account list is not
// durable enough to answer for the cluster.
//
// The keep-set is the account list, so the reaper is only as honest as that
// list. newStore falls back to the in-memory store when the metadata database
// is stale or unreachable, and that store knows only the seeded administrator.
// Reaping against it would treat every other registered learner's database as
// an orphan and drop it. A memory store skips the sweep instead, and the next
// boot with the real accounts cleans up what is genuinely orphaned.
func reapOrphanedEnvironments(ctx context.Context, log *slog.Logger, m *envdb.Manager, st store.Store) {
	if st.Kind() == "memory" {
		log.Warn("skipping the environment reaper: an in-memory account list knows only the seeded administrator, so every other database would look orphaned")
		return
	}
	m.ReapUnreferenced(ctx, st)
}

// passwordFromDSN extracts the password a managed DSN already carries, so the
// restricted role can be given the same one and the learner DSN differs only by
// username.
func passwordFromDSN(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}

// swapDSNUser rewrites a DSN's username, keeping the host, database and options.
func swapDSNUser(raw, user, password string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if password == "" {
		u.User = url.User(user)
	} else {
		u.User = url.UserPassword(user, password)
	}
	return u.String()
}

// ensureLearnerRole makes the restricted role exist and hold the right grants
// even when the database is managed outside this process, such as a container
// or a hosted server configured through the database_url key. Failure is logged and
// left to surface as an unavailable engine rather than silently falling back to
// the privileged connection.
func ensureLearnerRole(ctx context.Context, log *slog.Logger, dsn, password string) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Error("cannot reach the database to configure the restricted learner role; SQL execution will be unavailable", "err", err)
		return
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err := dataset.EnsureLearnerRole(ctx, conn, password); err != nil {
		log.Error("could not configure the restricted learner role; SQL execution will be unavailable", "err", err)
	}
}

// sqliteOptions reads the SQLite store's bound and idle window from the
// resolved settings. The cap is a budget on cached pools, not on accounts: an
// evicted account keeps its file, and its next statement re-opens it, so a
// deployment with more accounts than sqlite_max_accounts is still correct; it
// just re-opens more often. Zero keeps the store's own default.
func sqliteOptions(settings config.Settings) envdb.SQLiteOptions {
	return envdb.SQLiteOptions{
		MaxAccounts: settings.SQLiteMaxAccounts,
		IdleTTL:     settings.SQLiteIdle,
	}
}
