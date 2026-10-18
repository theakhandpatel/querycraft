// Package localdb boots a real PostgreSQL server for the gateway when no
// external database is configured.
//
// The point is that the gateway owns the database without Docker: it downloads
// prebuilt PostgreSQL binaries once, initialises a data directory inside the
// repo, starts the server, and applies the shared commerce dataset. Everything
// above this package talks the ordinary PostgreSQL wire protocol, so pointing
// database_url at a container or a managed server replaces it with no code
// change at all.
package localdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
)

// Config describes the local server to start.
type Config struct {
	DataPath     string // cluster data directory, e.g. .data/pg/data
	RuntimePath  string // where extracted binaries live, e.g. .data/pg/runtime
	BinariesPath string // download cache for the PostgreSQL binaries
	TempDir      string // where the embedded server writes its postgres log; empty = os.TempDir(), which the library never cleans up. See startWithTempDir.
	Port         uint32
	User         string
	Password     string
	Database     string
}

// Server is a running embedded PostgreSQL instance.
type Server struct {
	pg  *embeddedpostgres.EmbeddedPostgres
	DSN string

	// learnerPassword is the password the restricted execution role is given,
	// so the learner DSN differs from the admin DSN only by username.
	learnerPassword string

	log *slog.Logger
}

// DSNFor builds the connection string for a local server.
func (c Config) DSNFor() string {
	return fmt.Sprintf(
		"postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
		url.QueryEscape(c.User), url.QueryEscape(c.Password), c.Port, url.PathEscape(c.Database),
	)
}

// Start boots the server, waits for it to accept connections, and applies the
// commerce schema and seed if this data directory has never been initialised.
//
// The dataset is applied once: re-applying it on every boot would drop and
// recreate the tables, discarding anything the learner has stored alongside it.
func Start(ctx context.Context, log *slog.Logger, cfg Config) (*Server, error) {
	// A previous gateway may have died without a clean shutdown, a SIGHUP when
	// its parent shell exits or a SIGKILL, and left the postmaster running.
	// Adopting it is right for a restart: the cluster, the dataset and
	// everything saved to it are still there. Adoption requires proof that the
	// data directory is ours, so an unrelated PostgreSQL that happens to hold
	// the port is never taken over.
	if srv, ok, err := adopt(ctx, log, cfg); err != nil {
		return nil, err
	} else if ok {
		return srv, nil
	}

	ep := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Username(cfg.User).
			Password(cfg.Password).
			Database(cfg.Database).
			Port(cfg.Port).
			DataPath(cfg.DataPath).
			RuntimePath(cfg.RuntimePath).
			BinariesPath(cfg.BinariesPath).
			StartTimeout(90 * time.Second),
	)

	log.Info("starting embedded PostgreSQL", "port", cfg.Port, "data", cfg.DataPath)
	if cfg.TempDir != "" {
		if err := os.MkdirAll(cfg.TempDir, 0o700); err != nil {
			return nil, fmt.Errorf("create the directory for the postgres log: %w", err)
		}
	}
	if err := startWithTempDir(cfg.TempDir, ep.Start); err != nil {
		return nil, fmt.Errorf("start embedded postgres: %w", err)
	}

	srv := &Server{pg: ep, DSN: cfg.DSNFor(), log: log, learnerPassword: cfg.Password}

	if err := srv.waitReady(ctx, 30*time.Second); err != nil {
		_ = ep.Stop()
		return nil, err
	}
	if err := srv.applyDataset(ctx); err != nil {
		_ = ep.Stop()
		return nil, err
	}
	log.Info("embedded PostgreSQL ready", "port", cfg.Port)
	return srv, nil
}

// tempDirMu serializes the process-wide $TMPDIR swap in startWithTempDir.
//
// The embedded-postgres library creates its postgres log with
// os.CreateTemp("", "embedded_postgres_log"), which resolves os.TempDir(),
// i.e. $TMPDIR at the moment of the call, and never removes the file. Every
// boot that lets it use the OS temp directory therefore leaves a log behind:
// one `go test ./...` of this repository accumulates one per cluster, and a
// few hundred runs leave thousands of them in /tmp. Pointing $TMPDIR at a
// directory the caller owns makes the log per-run and removes it with that
// directory.
var tempDirMu sync.Mutex

// startWithTempDir runs start with $TMPDIR pointed at dir, restoring the
// previous value afterwards. dir is left to the caller to create and to remove.
//
// The library's log file is already open and addressed by its absolute name by
// the time start returns, so restoring $TMPDIR does not disturb the flush that
// Stop performs.
func startWithTempDir(dir string, start func() error) error {
	if dir == "" {
		return start()
	}
	tempDirMu.Lock()
	defer tempDirMu.Unlock()

	previous, had := os.LookupEnv("TMPDIR")
	if err := os.Setenv("TMPDIR", dir); err != nil {
		return fmt.Errorf("point TMPDIR at the postgres log directory: %w", err)
	}
	defer func() {
		if had {
			_ = os.Setenv("TMPDIR", previous)
			return
		}
		_ = os.Unsetenv("TMPDIR")
	}()

	return start()
}

// adopt attaches to a postmaster already serving cfg's data directory.
//
// The second return value says whether the directory is ours; the third carries
// a real setup failure. Swallowing that failure made a broken migration look
// like "no database here", and the caller then tried to start a second server
// on a busy port, hiding the actual cause behind a port error.
func adopt(ctx context.Context, log *slog.Logger, cfg Config) (*Server, bool, error) {
	if _, err := os.Stat(filepath.Join(cfg.DataPath, "postmaster.pid")); err != nil {
		return nil, false, nil
	}
	srv := &Server{DSN: cfg.DSNFor(), log: log, learnerPassword: cfg.Password}
	if err := srv.waitReady(ctx, 3*time.Second); err != nil {
		// A pid file with nothing behind it is stale, not ours.
		return nil, false, nil
	}
	if err := srv.applyDataset(ctx); err != nil {
		return nil, true, fmt.Errorf("prepare existing cluster: %w", err)
	}
	log.Info("adopted the PostgreSQL server already running for this cluster",
		"port", cfg.Port, "data", cfg.DataPath)
	return srv, true, nil
}

// waitReady polls until the server accepts a connection or the deadline passes.
func (s *Server) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		conn, err := pgx.Connect(attempt, s.DSN)
		if err == nil {
			err = conn.Ping(attempt)
			_ = conn.Close(attempt)
		}
		cancel()
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	return fmt.Errorf("embedded postgres did not become ready: %w", last)
}

// A database created before the dataset and the gateway metadata moved into
// their own schemas holds duplicates of those tables in `public`.
// legacyPublicTable names one such table and the authoritative schema its
// replacement lives in.
type legacyPublicTable struct {
	name      string
	authority string // schema holding the real table
}

// legacyPublicTables is the list dropLegacyPublicTables removes, in drop order.
// Order matters: children before the rows they reference, because order_items
// holds foreign keys to orders and products, so dropping a parent first fails
// with a dependency error. CASCADE covers anything else attached to them.
var legacyPublicTables = []legacyPublicTable{
	{"order_items", "dataset"},
	{"orders", "dataset"},
	{"products", "dataset"},
	{"customers", "dataset"},
	{"dataset_meta", "dataset"},
	{"progress", "app"},
	{"events", "app"},
}

// dropLegacyPublicTables removes those stale duplicates. Idempotent, and a
// no-op on any database that never had them.
func dropLegacyPublicTables(ctx context.Context, conn *pgx.Conn, log *slog.Logger) error {
	for _, table := range legacyPublicTables {
		// Only drop a duplicate: the authoritative table must already exist,
		// otherwise the public copy may still be the only one.
		var duplicate bool
		q := fmt.Sprintf(
			"SELECT to_regclass('public.%s') IS NOT NULL AND to_regclass('%s.%s') IS NOT NULL",
			table.name, table.authority, table.name,
		)
		if err := conn.QueryRow(ctx, q).Scan(&duplicate); err != nil {
			return fmt.Errorf("check legacy table public.%s: %w", table.name, err)
		}
		if !duplicate {
			continue
		}
		if _, err := conn.Exec(ctx, `DROP TABLE IF EXISTS "public"."`+table.name+`" CASCADE`); err != nil {
			return fmt.Errorf("drop legacy table public.%s: %w", table.name, err)
		}
		log.Info("dropped legacy duplicate table left over from the previous schema layout",
			"table", "public."+table.name, "replaced_by", table.authority+"."+table.name)
	}
	return nil
}

// applyDataset loads schema.sql + seed.sql when the dataset is absent.
func (s *Server) applyDataset(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, s.DSN)
	if err != nil {
		return fmt.Errorf("connect for dataset check: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	// The restricted execution role is (re)asserted on every boot, including
	// clusters that already hold the dataset: without it the gateway would run
	// learner SQL as the superuser that owns this database.
	if err := dataset.EnsureLearnerRole(ctx, conn, s.learnerPassword); err != nil {
		return fmt.Errorf("ensure learner role: %w", err)
	}

	var present bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('dataset.customers') IS NOT NULL").Scan(&present); err != nil {
		return fmt.Errorf("check dataset presence: %w", err)
	}
	if present {
		// A cluster created before the dataset moved into its own schema still
		// holds the old copies in `public`. They are on the execution search path
		// (`dataset, public`), so any name missing from `dataset` would silently
		// resolve to stale rows. Remove them, but only now that the authoritative
		// copy exists, so this can never destroy the only dataset.
		if err := dropLegacyPublicTables(ctx, conn, s.log); err != nil {
			return err
		}
		return nil
	}

	s.log.Info("applying commerce dataset")
	// Schema and seed are multi-statement scripts with their own BEGIN/COMMIT,
	// so they must run over the simple query protocol. The order is load-bearing
	// and iteration over a map is randomized, so a slice holds the steps: with a
	// map the seed fails with "relation dataset.customers does not exist"
	// roughly half the time and the gateway boots into ENVIRONMENT_UNAVAILABLE.
	for _, step := range []struct{ name, script string }{
		{"schema", dataset.SchemaSQL()},
		{"seed", dataset.SeedSQL()},
	} {
		if _, err := conn.PgConn().Exec(ctx, step.script).ReadAll(); err != nil {
			return fmt.Errorf("apply dataset %s: %w", step.name, err)
		}
	}
	return nil
}

// Stop shuts the server down. Safe to call on a nil receiver. A server this
// process adopted rather than started carries no handle, so Stop leaves it
// alone.
func (s *Server) Stop() error {
	if s == nil || s.pg == nil {
		return nil
	}
	if err := s.pg.Stop(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop embedded postgres: %w", err)
	}
	return nil
}

// DefaultDataDir returns the cluster directory used when none is configured.
func DefaultDataDir() string {
	return ".data/pg"
}

// BinariesDir is where the PostgreSQL binaries are cached between runs.
func BinariesDir() string {
	return filepath.Join(DefaultDataDir(), "binaries")
}
