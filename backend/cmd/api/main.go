// Command api is the Querycraft execution gateway (Adapter B): a JSON HTTP
// service that runs learner SQL against native PostgreSQL and grades it against
// result contracts. It runs usefully with no database configured: SQL execution
// reports ENVIRONMENT_UNAVAILABLE and progress falls back to memory, so the
// React client always has something to talk to.
//
// The command splits across three files. cli.go builds the cobra tree and
// resolves the settings. ai.go turns the AI settings into a provider and the
// pipeline's bounds. This file owns the process: Execute runs the tree, serve
// resolves the last settings layer, and run walks the boot.
//
// run takes five phases, in this order:
//
//  1. Config. cli.go has already resolved the defaults, the config file and the
//     flags into config.Settings, and run receives the result.
//  2. Databases. openDatabases starts or adopts the embedded PostgreSQL
//     (localdb), opens the learner execution pool (engine) and the metadata
//     store (store), and asserts the restricted learner role (dataset).
//  3. Gateway. buildGateway seeds the administrator (auth), refuses a blank
//     password on a non-loopback bind, and wires the two learner dialects
//     (envdb: PostgreSQL environment databases and SQLite files).
//  4. AI wiring. wireAI attaches the challenge-lab provider, applies any
//     persisted AI setting, and sweeps the stale course builds.
//  5. Serve and drain. serveAndDrain serves httpapi until a signal, shuts the
//     server down, and drains the detached generation jobs.
//
// Deferred closers release the resources in the reverse of the order they were
// acquired, so the PostgreSQL cluster outlives the pools and stores that use it.
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
// resolved: cli.go has already applied defaults, the config file and the
// command-line flags, in that order.
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
	// only on a loopback bind (see the refusal in buildGateway), which is the
	// zero-setup local flow. Reaching the gateway from the network is an explicit
	// opt-in through the addr setting (querycraft.json or --addr), and that
	// opt-in requires a real admin password.
	addr := settings.Addr

	// SIGHUP matters here: a terminal closing (or a parent shell exiting) sends
	// it, and without a handler the process dies instantly, orphaning the
	// embedded PostgreSQL it started. Treating it as a shutdown signal keeps the
	// cluster's lifecycle tied to the gateway's.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// The resources are released by deferred closers, in the reverse of the
	// order they were registered: the SQLite environments, then the environment
	// manager, then the store, the execution pool and the embedded server. The
	// order is load-bearing: a learner's database is dropped while its cluster
	// is still running.
	dbs, err := openDatabases(ctx, log, settings)
	if dbs != nil {
		defer dbs.Close(log)
	}
	if err != nil {
		log.Error("could not open explicitly selected metadata store", "err", err)
		return 1
	}

	gw, ok := buildGateway(ctx, log, settings, dbs, cfg, addr)
	if !ok {
		return 1
	}
	defer gw.Close(log)

	wireAI(ctx, log, settings, dbs, gw)
	return serveAndDrain(ctx, stop, log, gw.api, addr)
}

// databases is the set of resources the gateway owns for its lifetime: the
// embedded PostgreSQL when this process started one, the learner execution pool
// and the metadata store. Close releases them in dependency order, so a store
// or pool that still needs the cluster is closed before it.
type databases struct {
	local         *localdb.Server
	exec          engine.Executor
	store         store.Store
	dsn           string
	adminPassword string
	logDir        string
}

// Close releases the store, the execution pool, the embedded server and the
// server's per-run log directory, in that order.
func (d *databases) Close(log *slog.Logger) {
	if d.store != nil {
		_ = d.store.Close()
	}
	if d.exec != nil {
		_ = d.exec.Close()
	}
	if d.local != nil {
		if err := d.local.Stop(); err != nil {
			log.Warn("stopping local database failed", "err", err)
		}
	}
	if d.logDir != "" {
		_ = os.RemoveAll(d.logDir)
	}
}

// openDatabases starts the database the gateway owns and opens the learner
// execution pool and metadata store over it. The only error it returns is an
// explicitly selected metadata store that cannot be opened; every other failure
// degrades the gateway and is logged here. It returns a non-nil databases even
// on that error, so the caller can release what it opened.
func openDatabases(ctx context.Context, log *slog.Logger, settings config.Settings) (*databases, error) {
	// The gateway owns the database. An external PostgreSQL (container, managed
	// service, or a local server) is used when database_url is set; otherwise
	// a real PostgreSQL is started from a repo-local data directory. Both paths
	// speak the same wire protocol, so the swap is a configuration change.
	dbs := &databases{dsn: settings.DatabaseURL}
	if dbs.dsn == "" && !settings.NoLocalDB {
		runtimeDir := settings.PGRuntimeDir
		/* The embedded server writes its postgres log into os.TempDir() and
		   never removes it. Give it a per-run directory beside the cluster
		   instead, so the log leaves with the process rather than accumulating
		   in the machine-wide temp directory across every restart. */
		logParent := filepath.Join(filepath.Dir(runtimeDir), "tmp")
		if err := os.MkdirAll(logParent, 0o700); err != nil {
			log.Warn("could not create the embedded server's log directory; it will use the system temp directory", "err", err)
		} else if dir, err := os.MkdirTemp(logParent, "run-"); err != nil {
			log.Warn("could not create the embedded server's log directory; it will use the system temp directory", "err", err)
		} else {
			dbs.logDir = dir
		}
		cfg := localdb.Config{
			DataPath:     settings.PGDataDir,
			RuntimePath:  runtimeDir,
			BinariesPath: settings.PGBinariesDir,
			TempDir:      dbs.logDir,
			Port:         uint32(settings.PGPort),
			User:         settings.PGUser,
			Password:     settings.PGPassword,
			Database:     settings.PGDatabase,
		}
		dbs.adminPassword = cfg.Password
		srv, err := localdb.Start(ctx, log, cfg)
		if err != nil {
			log.Warn("could not start the local database; SQL execution will report ENVIRONMENT_UNAVAILABLE", "err", err)
		} else {
			dbs.local = srv
			dbs.dsn = srv.DSN
		}
	}
	if dbs.adminPassword == "" {
		dbs.adminPassword = passwordFromDSN(dbs.dsn)
	}

	// Learner SQL must not run as the owner. The admin connection creates the
	// schema and owns the progress store; a restricted role executes learner
	// statements, so a fully qualified name cannot reach `app`, and the
	// superuser-only catalog and file functions are refused by PostgreSQL.
	learner := settings.LearnerDatabaseURL
	if learner == "" && dbs.dsn != "" {
		learner = swapDSNUser(dbs.dsn, dataset.LearnerRole, dbs.adminPassword)
	}
	if dbs.dsn != "" {
		ensureLearnerRole(ctx, log, dbs.dsn, dbs.adminPassword)
	}

	dbs.exec = newExecutor(ctx, log, learner)
	st, err := newStore(ctx, log, dbs.dsn, settings)
	if err != nil {
		return dbs, err
	}
	dbs.store = st
	return dbs, nil
}

// gateway is the assembled API surface and the per-dialect learner
// environments behind it.
type gateway struct {
	api    *httpapi.Server
	envs   *envdb.Manager
	sqlite *envdb.SQLiteEnvironments
}

// Close releases the SQLite environments, then the PostgreSQL environment
// manager. That is the reverse of the order they were registered, so an
// in-flight PostgreSQL statement keeps its cluster running until it ends; the
// cluster itself stops later still, in databases.Close.
func (g *gateway) Close(log *slog.Logger) {
	if g.sqlite != nil {
		if err := g.sqlite.Close(); err != nil {
			log.Warn("closing the SQLite environment store failed", "err", err)
		}
	}
	if g.envs != nil {
		g.envs.Close()
	}
}

// buildGateway seeds the administrator, refuses a blank-password admin on a
// non-loopback bind, and wires the two learner-environment dialects. It returns
// false only for that refusal, which the caller turns into exit code 1.
func buildGateway(ctx context.Context, log *slog.Logger, settings config.Settings, dbs *databases, cfg httpapi.Config, addr string) (*gateway, bool) {
	// Seed the local administrator before any request can be served. Accounts
	// own rows in app.progress/app.events through foreign keys, so the users
	// table has to exist with at least one row before the first write; without
	// this a fresh install would reject every progress save.
	admin, err := auth.EnsureDefaultAdmin(ctx, dbs.store, auth.DefaultAdmin{
		Email:    settings.AdminEmail,
		Password: settings.AdminPassword,
		Name:     settings.AdminName,
	}, log)
	if err != nil {
		log.Error("could not seed the default administrator; sign-in will be unavailable until the database accepts writes", "err", err)
	} else {
		log.Info("default administrator ready", "email", admin.Email, "passwordSet", settings.AdminPassword != "")
	}
	// Read the stored state rather than the setting: an operator may have set
	// admin_password on an earlier boot and removed it since, and the seed
	// never rewrites an existing account. The question that decides the
	// refusal is whether an empty password signs this account in, which is
	// exactly auth.Verify(hash, ""). A seeded admin's blank password is stored
	// as a real bcrypt hash, not as an empty string, so testing the hash for
	// emptiness would miss it.
	adminPasswordEmpty := false
	if err == nil {
		if _, hash, found, herr := dbs.store.UserByEmail(ctx, admin.Email); herr == nil && found {
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
		return nil, false
	}

	api := httpapi.New(dbs.exec, dbs.store, cfg, log)
	// Advertised on /health so the sign-in screen shows the local-admin hint
	// only when it is actually true. A boolean, never the password or its hash.
	api.UseAdminPasswordEmpty(adminPasswordEmpty)
	gw := &gateway{api: api}

	// ADR 0003: a signed-in learner gets their own writable database, copied
	// from the sealed template and created on first use. The manager needs the
	// administrative connection to CREATE/DROP the copies. With no database, or
	// a connection it cannot make, it stays nil and the gateway answers
	// ENVIRONMENT_UNAVAILABLE rather than inventing a workspace. Anonymous
	// callers are unaffected: they keep the shared, read-only dataset.
	if dbs.dsn != "" {
		m, err := envdb.New(ctx, envdb.Options{
			AdminDSN:        dbs.dsn,
			LearnerPassword: dbs.adminPassword,
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
			api.UseEnvironments(m)
			gw.envs = m

			// Build both sealed templates at boot rather than on a learner's
			// first request. ensureTemplate and ensurePerfTemplate both run
			// lazily under the manager's mutex, so without this the first
			// challenge request and the first plan request each built their
			// template while every other account waited. A failure is logged,
			// not fatal: the lazy path still builds it on demand, so the
			// gateway stays usable.
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
			reapOrphanedEnvironments(ctx, log, m, dbs.store)
		}
	}

	// M6 step 4: SQLite is the second dialect, and unlike PostgreSQL it needs no
	// server. The executor is cgo-free SQLite over a file, so the gateway can
	// serve it even in degraded mode. A signed-in learner gets their own
	// writable file (ADR 0003's split, with a file in place of a copied
	// database); an anonymous caller reads the shared fixture. If the root
	// cannot be prepared the dialect is not registered at all, so a sqlite
	// request is refused with DIALECT_MISMATCH instead of being answered by the
	// wrong engine.
	// The SQLite dialect's files sit beside the embedded PostgreSQL's, under the
	// same .data root, and can be moved independently with the sqlite_data key.
	// The default derives from the pg_data setting, so relocating the data
	// directory relocates both dialects' state together.
	if sqliteEnvs, err := envdb.NewSQLiteEnvironmentsWithOptions(settings.DeriveSQLiteRoot(), sqliteOptions(settings)); err != nil {
		log.Warn("SQLite dialect unavailable; a request for it will be refused with DIALECT_MISMATCH", "err", err)
	} else {
		api.UseSQLite(sqliteEnvs)
		gw.sqlite = sqliteEnvs
		log.Info("SQLite dialect ready",
			"root", sqliteEnvs.Root(),
			"max_accounts", sqliteEnvs.MaxAccounts(),
			"idle_ttl", sqliteEnvs.IdleTTL().String())
	}
	return gw, true
}

// wireAI attaches the challenge-lab provider to the API. The provider grades
// against the metadata store, and a runner verifies a generated artifact when a
// PostgreSQL environment manager exists.
func wireAI(ctx context.Context, log *slog.Logger, settings config.Settings, dbs *databases, gw *gateway) {
	// M4 (ADR 0005): the challenge lab is a capability, not an assumption. It is
	// available only when a provider is named AND there is a database to verify
	// a generated artifact against; /capabilities reports which of the two is
	// missing instead of offering a lab that cannot work.
	//
	// The runner is assigned explicitly rather than handed over as a typed nil:
	// a nil *envdb.Manager stored in an interface is not nil, and would make an
	// unverifiable setup look available.
	var runner ai.Runner
	if gw.envs != nil {
		runner = gw.envs
	}
	aiCfg := ai.Config{
		Provider:       aiProviderFrom(settings, log),
		Depot:          dbs.store,
		Runner:         runner,
		Logger:         log,
		Limits:         aiLimitsFrom(settings),
		DatasetVersion: dataset.DatasetVersion,
	}
	gw.api.UseAI(aiCfg)

	// A configuration the manage surface saved outranks the config file's
	// provider: it is the runtime truth, and the restart applies it before the
	// first request. An unreadable setting keeps the file's provider and says
	// why in the boot log, so a bad row cannot silently turn AI off.
	if raw, found, err := dbs.store.Setting(ctx, "ai"); err != nil {
		log.Warn("could not read the persisted AI setting; the config file's provider stays live", "err", err)
	} else if found {
		cfg, err := httpapi.AIConfigFromSetting(raw, aiCfg, log)
		if err != nil {
			log.Warn("the persisted AI setting is not readable; the config file's provider stays live", "err", err)
		} else {
			gw.api.SetAIConfig(cfg)
			if cfg.Provider != nil {
				d := cfg.Provider.Descriptor()
				log.Info("AI configuration applied from the persisted setting", "provider", d.Name, "model", d.Model)
			} else {
				log.Info("AI configuration applied from the persisted setting", "provider", "off")
			}
		}
	}

	// A `building` row at boot has no live writer: the process that was
	// writing it is this one's predecessor. The sweep keeps the rows that
	// verified a chapter (as `interrupted`, which POST /courses/{id}/build
	// resumes) and removes the rows that never got one.
	if kept, removed, err := ai.NewService(aiCfg).RecoverInterruptedBuilds(ctx); err != nil {
		log.Warn("the stale course builds could not be swept", "err", err)
	} else if kept > 0 || removed > 0 {
		log.Info("stale course builds swept", "kept_as_interrupted", kept, "removed_empty", removed)
	}
}

// serveAndDrain listens until ctx is cancelled, shuts the server down, and
// drains the detached generation jobs before returning. The drain runs before
// run's deferred closers, so a run that stored its result through the store or
// the cluster can finish. A run that outlasts the drain window is reported
// rather than waited for forever; its own context remains bounded by the
// pipeline's budget.
func serveAndDrain(ctx context.Context, stop context.CancelFunc, log *slog.Logger, api *httpapi.Server, addr string) int {
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
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelDrain()
	if err := api.DrainJobs(drainCtx); err != nil {
		log.Warn("generation jobs did not finish before shutdown", "err", err)
	}
	return 0
}

// isLoopbackAddr reports whether the listen address binds only the loopback
// interface. A wildcard host like ":8080" or "0.0.0.0:8080", or a concrete
// non-loopback address, could accept a connection from another host, so it is
// not loopback and a blank-password administrator is refused there. An
// unresolvable host is treated as non-loopback: failing closed is the
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

// newExecutor opens the pgx pool learner SQL runs on. Every failure degrades to
// engine.UnavailableExecutor rather than an error: the gateway must still serve
// sign-in and the docs with no database, and /query then reports
// ENVIRONMENT_UNAVAILABLE with the reason. A pool that opened but failed its
// ping is closed before it degrades, so a bad probe does not leak the pool.
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

// metadataStoreChoice is the metadata store the resolved settings describe.
// kind is one of "admin", "sqlite", "dsn" or "memory"; dsn and path carry what
// the chosen store needs to open.
type metadataStoreChoice struct {
	kind string
	dsn  string
	path string
}

// selectMetadataStore is the one place that resolves which metadata store the
// settings ask for. The precedence is admin_database_url, then metadata=sqlite,
// then a DSN, then memory. newStore passes the DSN the execution layer resolved,
// which is the local server's when database_url is empty; dbStatus passes the
// configured database_url, because it starts nothing and has no local server.
// Keeping the two consumers on one function means the status report cannot
// claim a store the gateway would not open.
func selectMetadataStore(settings config.Settings, resolvedDSN string) metadataStoreChoice {
	switch {
	case settings.AdminDatabaseURL != "":
		return metadataStoreChoice{kind: "admin", dsn: settings.AdminDatabaseURL}
	case strings.EqualFold(settings.Metadata, "sqlite"):
		return metadataStoreChoice{kind: "sqlite", path: settings.DeriveMetadataPath()}
	case resolvedDSN != "":
		return metadataStoreChoice{kind: "dsn", dsn: resolvedDSN}
	default:
		return metadataStoreChoice{kind: "memory"}
	}
}

func newStore(ctx context.Context, log *slog.Logger, dsn string, settings config.Settings) (store.Store, error) {
	switch choice := selectMetadataStore(settings, dsn); choice.kind {
	case "sqlite":
		return store.NewSQLite(ctx, choice.path)
	case "memory":
		log.Warn("no metadata database configured; using in-memory progress store")
		return store.NewMemory(), nil
	default:
		pg, err := store.NewPostgres(ctx, choice.dsn)
		if err != nil {
			log.Warn("metadata store unavailable; using in-memory store", "err", err)
			return store.NewMemory(), nil
		}
		return pg, nil
	}
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
