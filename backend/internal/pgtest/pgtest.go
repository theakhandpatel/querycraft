// Package pgtest boots the embedded PostgreSQL the gateway ships, for tests
// that need a real server rather than a fake.
//
// It is a test helper, not production code: it picks a free port, puts the
// data directory under t.TempDir(), reuses the repository's cached PostgreSQL
// binaries, and registers its own cleanup. Nothing a test writes outlives the
// test (the golden cluster below is a cache, like the binaries).
//
// It deliberately starts the same `localdb` path the gateway uses, so a test
// gets the commerce dataset and the restricted learner role already applied to
// the default database, plus the DSNs to talk to both.
//
// The expensive half of a boot is initdb and the dataset seed, which no test
// varies. So the first test builds a golden cluster once under
// .data/pg/golden (next to the binaries cache), and every boot copies it: the
// copy is a full cluster with the dataset already applied, so each test still
// gets a virgin server, and nothing two tests do can meet. The golden cluster
// is rebuilt when the binaries or the dataset change.
package pgtest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
)

// Password is the admin and learner password inside a test cluster. The cluster
// lives under t.TempDir() and is never written to the tree.
const Password = "pgtest-learner-password"

// embeddedBinariesEnv overrides the search for an already-extracted
// PostgreSQL. Tests that need their own binary cache hardcode the same string
// rather than import this test helper's const.
const embeddedBinariesEnv = "QC_PG_BINARIES"

// goldenMarker is the file the golden cluster carries with the stamp of what it
// was built from, so a new PostgreSQL or a changed dataset invalidates it.
const goldenMarker = ".qc-golden-stamp"

// goldenFormat versions the golden cluster's construction itself, for changes
// no input hash would catch.
const goldenFormat = 1

// Server is a running embedded PostgreSQL plus the DSNs a test needs.
type Server struct {
	// DSN is the admin (superuser) connection string for the database this
	// test may write to.
	DSN string
	// Database is the database DSN points at.
	Database string
	// ClusterDSN is the admin connection string for the cluster's maintenance
	// database. A Shared server exposes it for tests that manage databases of
	// their own; a dedicated server sets it to DSN.
	ClusterDSN string
}

// Start boots a cluster and returns it. The server stops and its data directory
// is removed with the test.
//
// With the cached binaries present, the boot is a copy of the golden cluster
// plus a server start. Without them, it falls back to the embedded-postgres
// library's own cache and a fresh initdb.
func Start(t *testing.T) *Server {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a PostgreSQL server; skipped under -short")
	}

	port, err := FreePort()
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	root := t.TempDir()
	binaries := CachedBinaries()
	dataPath := filepath.Join(root, "pg", "data")
	if binaries != "" {
		if err := copyTree(goldenCluster(t, binaries), dataPath); err != nil {
			t.Fatalf("copy the golden cluster: %v", err)
		}
	}

	/* TempDir keeps the server's own postgres log under t.TempDir() too. Left
	   empty the library files it in the machine-wide temp directory and never
	   removes it, so every test run would leak a log file there. */
	cfg := localdb.Config{
		DataPath:     dataPath,
		RuntimePath:  filepath.Join(root, "pg", "runtime"),
		BinariesPath: binaries,
		TempDir:      filepath.Join(root, "tmp"),
		Port:         port,
		User:         "querycraft",
		Password:     Password,
		Database:     "querycraft",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	srv, err := localdb.Start(ctx, QuietLogger(), cfg)
	if err != nil {
		if binaries == "" {
			t.Skipf("no cached PostgreSQL binaries and no server could be started: %v", err)
		}
		t.Fatalf("boot embedded PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	return &Server{DSN: srv.DSN, Database: cfg.Database, ClusterDSN: srv.DSN}
}

// Main runs a package's tests around one shared cluster.
//
// A package that uses Shared must declare:
//
//	func TestMain(m *testing.M) { pgtest.Main(m) }
//
// Main stops the cluster after the tests, passing or failing, and exits with
// their code. A binary that is killed before Main runs is served by the
// watchdog startShared installs.
func Main(m *testing.M) {
	code := m.Run()
	StopShared()
	os.Exit(code)
}

// sharedServer is the package-wide cluster Shared hands databases from.
type sharedServer struct {
	server   *localdb.Server
	root     string
	dsn      string // admin DSN for the maintenance database
	template string // the database every test database is copied from
	counter  atomic.Uint64
}

var (
	sharedOnce sync.Once
	shared     *sharedServer
	sharedErr  error
)

// Shared returns a server for one test: the cluster is shared by the package
// (one boot per test binary) and the database is private to the test.
//
// The cluster is a copy of the golden cluster started once on first use; each
// call then creates a database from the dataset template with CREATE DATABASE
// ... TEMPLATE, which PostgreSQL performs as a file copy in milliseconds. A
// test that must stop and restart its own server keeps using Start.
func Shared(t *testing.T) *Server {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a PostgreSQL server; skipped under -short")
	}
	sharedOnce.Do(func() { shared, sharedErr = startShared() })
	if sharedErr != nil {
		if CachedBinaries() == "" {
			t.Skipf("no cached PostgreSQL binaries and no server could be started: %v", sharedErr)
		}
		t.Fatalf("start the shared PostgreSQL cluster: %v", sharedErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	name, err := shared.newDatabase(ctx)
	if err != nil {
		t.Fatalf("create this test's database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), time.Minute)
		defer dropCancel()
		_ = shared.dropDatabase(dropCtx, name)
	})

	dsn, err := databaseDSN(shared.dsn, name)
	if err != nil {
		t.Fatalf("build the test DSN: %v", err)
	}
	return &Server{DSN: dsn, Database: name, ClusterDSN: shared.dsn}
}

// StopShared stops the package-wide cluster and removes its data directory.
// Main calls it. A test binary that dies without running Main is handled by the
// watchdog startShared installs.
func StopShared() {
	if shared == nil || shared.server == nil {
		return
	}
	_ = shared.server.Stop()
	_ = os.RemoveAll(shared.root)
	shared.server = nil
}

// startShared boots the one cluster a package's tests share.
func startShared() (*sharedServer, error) {
	binaries := CachedBinaries()
	root, err := os.MkdirTemp("", "qc-pgtest-shared-")
	if err != nil {
		return nil, fmt.Errorf("make the shared cluster directory: %w", err)
	}
	dataPath := filepath.Join(root, "pg", "data")
	if binaries != "" {
		golden, err := goldenClusterPath(binaries)
		if err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		if err := copyTree(golden, dataPath); err != nil {
			_ = os.RemoveAll(root)
			return nil, fmt.Errorf("copy the golden cluster: %w", err)
		}
	}

	port, err := FreePort()
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("find a free port: %w", err)
	}
	cfg := localdb.Config{
		DataPath:     dataPath,
		RuntimePath:  filepath.Join(root, "pg", "runtime"),
		BinariesPath: binaries,
		TempDir:      filepath.Join(root, "tmp"),
		Port:         port,
		User:         "querycraft",
		Password:     Password,
		Database:     "querycraft",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	srv, err := localdb.Start(ctx, QuietLogger(), cfg)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("boot embedded PostgreSQL: %w", err)
	}
	maintenance, err := databaseDSN(srv.DSN, "postgres")
	if err != nil {
		_ = srv.Stop()
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("build the maintenance DSN: %w", err)
	}
	watchSharedCluster(root, dataPath)
	return &sharedServer{server: srv, root: root, dsn: maintenance, template: cfg.Database}, nil
}

// newDatabase copies the package template into a database private to one test.
//
// PostgreSQL refuses to copy a template with any session on it. The cluster's
// own boot connections (the readiness probe, the dataset check) normally close
// before this runs, but a probe whose 3-second context expired is handed to
// pgx's asynchronous closer, which can hold the socket for another 15 seconds.
// Under a loaded suite that window is reachable, so the fixture terminates any
// session still on the template (the cluster is ours and was booted seconds
// ago) and retries the copy while they exit.
func (c *sharedServer) newDatabase(ctx context.Context) (string, error) {
	_ = c.exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, c.template)

	name := fmt.Sprintf("qc_t_%d_%d", os.Getpid(), c.counter.Add(1))
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := c.exec(ctx, `CREATE DATABASE "`+name+`" TEMPLATE "`+c.template+`"`)
		if err == nil {
			return name, nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55006" || time.Now().After(deadline) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// dropDatabase removes one test's database, forcing its remaining sessions
// closed. Cleanups run in reverse order, so the pools a test opened are closed
// before this runs; FORCE covers the ones a test forgot.
func (c *sharedServer) dropDatabase(ctx context.Context, name string) error {
	return c.exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
}

// exec runs one maintenance statement over a fresh admin connection. The
// connection goes to `postgres`, never the template: PostgreSQL refuses to copy
// a database that has a connected session, so the fixture leaves the template
// untouched and connects somewhere else.
func (c *sharedServer) exec(ctx context.Context, stmt string, args ...any) error {
	conn, err := pgx.Connect(ctx, c.dsn)
	if err != nil {
		return fmt.Errorf("connect to the maintenance database: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	_, err = conn.Exec(ctx, stmt, args...)
	return err
}

// databaseDSN swaps the database in a DSN, keeping the address and the
// credentials.
func databaseDSN(clusterDSN, database string) (string, error) {
	u, err := url.Parse(clusterDSN)
	if err != nil {
		return "", err
	}
	u.Path = "/" + database
	return u.String(), nil
}

// watchSharedCluster leaves a small process behind that kills the cluster if
// the test binary dies without running TestMain.
//
// A SIGKILL is the one cleanup t.Cleanup and Main cannot serve: the postmaster
// is reparented and then holds its processes and memory for the life of the
// machine. The watchdog polls the test binary's pid, then the postmaster's from
// the data directory, and removes the cluster directory. It runs in its own
// process group so a signal aimed at the test run cannot take it down with the
// binary it is watching. Its stdio is the null device, so it never holds a test
// run's output pipe open.
func watchSharedCluster(root, dataPath string) {
	const script = `
parent="$1"; data="$2"; root="$3"
while kill -0 "$parent" 2>/dev/null; do sleep 1; done
pid=$(head -1 "$data/postmaster.pid" 2>/dev/null)
if [ -n "$pid" ]; then kill -9 "$pid" 2>/dev/null; fi
rm -rf "$root"
`
	cmd := exec.Command("sh", "-c", script, "pgtest-watchdog", strconv.Itoa(os.Getpid()), dataPath, root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

// goldenCluster returns the path of a pre-initialised cluster: initdb has run,
// the commerce dataset and the learner role are applied, and the server is
// cleanly stopped. It builds the cluster when it is missing, once per
// repository; concurrent test binaries race through an atomic rename, and the
// loser uses the winner's cluster.
func goldenCluster(t *testing.T, binaries string) string {
	t.Helper()
	root, err := goldenClusterPath(binaries)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// goldenClusterPath is goldenCluster without the test dependency: the shared
// fixture builds the same cache on a package-wide boot, where there is no
// testing.T to fail.
func goldenClusterPath(binaries string) (string, error) {
	root := filepath.Join(filepath.Dir(binaries), "golden")
	if goldenValid(root, binaries) {
		return root, nil
	}

	base := filepath.Dir(binaries)
	staging, err := os.MkdirTemp(base, "golden-staging-")
	if err != nil {
		return "", fmt.Errorf("stage the golden cluster: %w", err)
	}
	// No-op after the rename below; the staging data directory never leaks.
	defer func() { _ = os.RemoveAll(staging) }()

	port, err := FreePort()
	if err != nil {
		return "", fmt.Errorf("find a free port: %w", err)
	}
	cfg := localdb.Config{
		DataPath:     filepath.Join(staging, "data"),
		RuntimePath:  filepath.Join(staging, "runtime"),
		BinariesPath: binaries,
		TempDir:      filepath.Join(staging, "tmp"),
		Port:         port,
		User:         "querycraft",
		Password:     Password,
		Database:     "querycraft",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	srv, err := localdb.Start(ctx, QuietLogger(), cfg)
	if err != nil {
		return "", fmt.Errorf("build the golden cluster: %w", err)
	}
	if err := srv.Stop(); err != nil {
		return "", fmt.Errorf("stop the golden cluster: %w", err)
	}
	// Only the data directory is the cache: the runtime and log directories are
	// recreated per boot.
	_ = os.RemoveAll(cfg.RuntimePath)
	_ = os.RemoveAll(cfg.TempDir)

	stamp := goldenStamp(binaries)
	if err := os.WriteFile(filepath.Join(cfg.DataPath, goldenMarker), []byte(stamp), 0o600); err != nil {
		return "", fmt.Errorf("stamp the golden cluster: %w", err)
	}
	if err := os.Rename(cfg.DataPath, root); err != nil {
		// Another test binary installed its golden cluster between the check
		// above and this rename; use it when it is valid.
		if goldenValid(root, binaries) {
			return root, nil
		}
		return "", fmt.Errorf("install the golden cluster: %w (remove %s and retry)", err, root)
	}
	return root, nil
}

// goldenValid reports whether root is a golden cluster built from the current
// binaries and dataset.
func goldenValid(root, binaries string) bool {
	stamp, err := os.ReadFile(filepath.Join(root, goldenMarker))
	if err != nil {
		return false
	}
	if string(stamp) != goldenStamp(binaries) {
		return false
	}
	_, err = os.Stat(filepath.Join(root, "PG_VERSION"))
	return err == nil
}

// goldenStamp fingerprints what the golden cluster must match: the PostgreSQL
// binaries it was built with and the dataset it was seeded with.
func goldenStamp(binaries string) string {
	out, err := exec.Command(filepath.Join(binaries, "bin", "postgres"), "--version").Output()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(dataset.SchemaSQL() + dataset.SeedSQL()))
	return fmt.Sprintf("%d|%s|%x", goldenFormat, strings.TrimSpace(string(out)), sum)
}

// copyTree copies a directory recursively, preserving file modes. The source
// is only read, so concurrent copies of one golden cluster are safe.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

// copyFile copies one regular file, creating dst or truncating it, with the
// source's permission bits. The golden cluster contains no hard links, so a
// plain read/write copy preserves everything a PostgreSQL data directory needs.
func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Password returns the password the restricted learner role was given. Every
// cluster this package starts uses the exported const.
func (s *Server) Password() string { return Password }

// LearnerDSN builds a restricted-role DSN for a database: same host, port and
// password as the admin DSN, with the user swapped to the learner role.
func (s *Server) LearnerDSN(t *testing.T, database string) string {
	t.Helper()
	u, err := url.Parse(s.DSN)
	if err != nil {
		t.Fatalf("parse cluster DSN: %v", err)
	}
	u.User = url.UserPassword(dataset.LearnerRole, Password)
	u.Path = "/" + database
	return u.String()
}

// FreePort picks an unused TCP port so a test never collides with a gateway a
// developer may be running.
func FreePort() (uint32, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return uint32(port), nil
}

// QuietLogger discards the embedded server's log; a test reports its own
// assertions and a noisy server log would drown them.
func QuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// CachedBinaries finds an already-extracted PostgreSQL under the repository's
// .data directory so a test needs no network download. An empty result lets
// embedded-postgres fall back to its own cache.
func CachedBinaries() string {
	if v := os.Getenv(embeddedBinariesEnv); v != "" {
		return v
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, ".data", "pg", "binaries")
		if _, err := os.Stat(filepath.Join(candidate, "bin", "pg_ctl")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
