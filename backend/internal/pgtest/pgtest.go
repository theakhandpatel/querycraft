// Package pgtest boots the embedded PostgreSQL the gateway ships, for tests
// that need a real server rather than a fake.
//
// It is a test helper, not production code: it picks a free port, puts the
// data directory under t.TempDir(), reuses the repository's cached PostgreSQL
// binaries so it needs no network, and registers its own cleanup. Nothing it
// creates outlives the test.
//
// It deliberately starts the same `localdb` path the gateway uses, so a test
// gets the commerce dataset and the restricted learner role already applied to
// the default database, plus the DSNs to talk to both.
package pgtest

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
)

// Password is the admin and learner password inside a test cluster. The cluster
// lives under t.TempDir() and is never written to the tree.
const Password = "pgtest-learner-password"

// EmbeddedBinariesEnv is the override the rest of the repo uses to point at an
// already-extracted PostgreSQL.
const EmbeddedBinariesEnv = "QC_PG_BINARIES"

// Server is a running embedded PostgreSQL plus the DSNs a test needs.
type Server struct {
	// DSN is the admin (superuser) connection string.
	DSN string
	// Database is the default database the dataset was applied to.
	Database string

	password string
}

// Start boots a cluster and returns it. The server stops and its data directory
// is removed with the test.
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

	cfg := localdb.Config{
		DataPath:     filepath.Join(root, "pg", "data"),
		RuntimePath:  filepath.Join(root, "pg", "runtime"),
		BinariesPath: binaries,
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

	return &Server{DSN: srv.DSN, Database: cfg.Database, password: Password}
}

// Password returns the password the restricted learner role was given.
func (s *Server) Password() string { return s.password }

// LearnerDSN builds a restricted-role DSN for a database: same host, port and
// password as the admin DSN, with the user swapped to the learner role.
func (s *Server) LearnerDSN(t *testing.T, database string) string {
	t.Helper()
	u, err := url.Parse(s.DSN)
	if err != nil {
		t.Fatalf("parse cluster DSN: %v", err)
	}
	u.User = url.UserPassword(dataset.LearnerRole, s.password)
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
	if v := os.Getenv(EmbeddedBinariesEnv); v != "" {
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
