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
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/auth"
	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/httpapi"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := httpapi.Config{
		MaxRows:       envInt("QC_MAX_ROWS", engine.DefaultMaxRows),
		MaxBytes:      envInt("QC_MAX_BYTES", engine.DefaultMaxBytes),
		TimeoutMS:     envInt("QC_TIMEOUT_MS", engine.DefaultTimeoutMS),
		MaxConcurrent: envInt("QC_MAX_CONCURRENT", 4),
	}
	addr := env("QC_ADDR", ":8080")

	// SIGHUP matters here: a terminal closing (or a parent shell exiting) sends
	// it, and without a handler the process dies instantly, orphaning the
	// embedded PostgreSQL it started. Treating it as a shutdown signal keeps the
	// cluster's lifecycle tied to the gateway's.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// The gateway owns the database. An external PostgreSQL (container, managed
	// service, or a local server) is used when QC_DATABASE_URL is set; otherwise
	// a real PostgreSQL is started from a repo-local data directory. Both paths
	// speak the same wire protocol, so the swap is a configuration change.
	dsn := os.Getenv("QC_DATABASE_URL")
	adminPassword := ""
	var local *localdb.Server
	if dsn == "" && !envBool("QC_NO_LOCAL_DB", false) {
		cfg := localdb.Config{
			DataPath:     env("QC_PG_DATA", localdb.DefaultDataDir("")+"/data"),
			RuntimePath:  env("QC_PG_RUNTIME", localdb.DefaultDataDir("")+"/runtime"),
			BinariesPath: env("QC_PG_BINARIES", localdb.BinariesDir("")),
			Port:         uint32(envInt("QC_PG_PORT", 54329)),
			User:         env("QC_PG_USER", "querycraft"),
			Password:     env("QC_PG_PASSWORD", "querycraft"),
			Database:     env("QC_PG_DATABASE", "querycraft"),
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
	learner := env("QC_LEARNER_DATABASE_URL", "")
	if learner == "" && dsn != "" {
		learner = swapDSNUser(dsn, dataset.LearnerRole, adminPassword)
	}
	if dsn != "" {
		ensureLearnerRole(ctx, log, dsn, adminPassword)
	}

	exec := newExecutor(ctx, log, learner)
	defer exec.Close()

	st := newStore(ctx, log, dsn)
	defer st.Close()

	// Seed the local administrator before any request can be served. Accounts
	// own rows in app.progress/app.events through foreign keys, so the users
	// table has to exist with at least one row before the first write; without
	// this a fresh install would reject every progress save.
	admin, err := auth.EnsureDefaultAdmin(ctx, st, auth.DefaultAdmin{
		Email:    env("QC_ADMIN_EMAIL", "admin@querycraft.local"),
		Password: os.Getenv("QC_ADMIN_PASSWORD"),
		Name:     env("QC_ADMIN_NAME", "Administrator"),
	}, log)
	if err != nil {
		log.Error("could not seed the default administrator; sign-in will be unavailable until the database accepts writes", "err", err)
	} else {
		log.Info("default administrator ready", "email", admin.Email, "passwordSet", os.Getenv("QC_ADMIN_PASSWORD") != "")
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.New(exec, st, cfg, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
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

func newStore(ctx context.Context, log *slog.Logger, dsn string) store.Store {
	dsn = env("QC_ADMIN_DATABASE_URL", dsn)
	if dsn == "" {
		log.Warn("no metadata database configured; using in-memory progress store")
		return store.NewMemory()
	}
	pg, err := store.NewPostgres(ctx, dsn)
	if err != nil {
		if errors.Is(err, store.ErrStaleSchema) {
			// Degrading to memory here would look like it worked and then lose
			// every account and snapshot on restart. Say what to do instead.
			log.Error("the metadata database was created before accounts existed and has no upgrade path; run `make db-reset` (or DROP SCHEMA app CASCADE) and restart; falling back to the in-memory store until then", "err", err)
			return store.NewMemory()
		}
		log.Warn("metadata store unavailable; using in-memory store", "err", err)
		return store.NewMemory()
	}
	return pg
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
// or a hosted server configured through QC_DATABASE_URL. Failure is logged and
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}
