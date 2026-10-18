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
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// DSN is the admin (superuser) connection string.
	DSN string
	// Database is the default database the dataset was applied to.
	Database string
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

	return &Server{DSN: srv.DSN, Database: cfg.Database}
}

// goldenCluster returns the path of a pre-initialised cluster: initdb has run,
// the commerce dataset and the learner role are applied, and the server is
// cleanly stopped. It builds the cluster when it is missing, once per
// repository; concurrent test binaries race through an atomic rename, and the
// loser uses the winner's cluster.
func goldenCluster(t *testing.T, binaries string) string {
	t.Helper()
	root := filepath.Join(filepath.Dir(binaries), "golden")
	if goldenValid(root, binaries) {
		return root
	}

	base := filepath.Dir(binaries)
	staging, err := os.MkdirTemp(base, "golden-staging-")
	if err != nil {
		t.Fatalf("stage the golden cluster: %v", err)
	}
	// No-op after the rename below; the staging data directory never leaks.
	defer func() { _ = os.RemoveAll(staging) }()

	port, err := FreePort()
	if err != nil {
		t.Fatalf("find a free port: %v", err)
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
		t.Fatalf("build the golden cluster: %v", err)
	}
	if err := srv.Stop(); err != nil {
		t.Fatalf("stop the golden cluster: %v", err)
	}
	// Only the data directory is the cache: the runtime and log directories are
	// recreated per boot.
	_ = os.RemoveAll(cfg.RuntimePath)
	_ = os.RemoveAll(cfg.TempDir)

	stamp := goldenStamp(binaries)
	if err := os.WriteFile(filepath.Join(cfg.DataPath, goldenMarker), []byte(stamp), 0o600); err != nil {
		t.Fatalf("stamp the golden cluster: %v", err)
	}
	if err := os.Rename(cfg.DataPath, root); err != nil {
		// Another test binary installed its golden cluster between the check
		// above and this rename; use it when it is valid.
		if goldenValid(root, binaries) {
			return root
		}
		t.Fatalf("install the golden cluster: %v (remove %s and retry)", err, root)
	}
	return root
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
