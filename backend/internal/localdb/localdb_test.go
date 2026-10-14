package localdb

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// adopt must never attach to a server it cannot prove is ours; a data directory
// without a postmaster.pid means nothing is running there.
func TestAdoptSkipsWithoutPostmasterPID(t *testing.T) {
	cfg := Config{DataPath: t.TempDir(), Port: 1, User: "u", Password: "p", Database: "d"}
	if srv, ok, err := adopt(context.Background(), quiet(), cfg); ok || srv != nil || err != nil {
		t.Fatalf("adopt returned (%v, %v, %v) for a data directory with no postmaster.pid", srv, ok, err)
	}
}

// A stale postmaster.pid (no server behind it) must not be adopted either.
func TestAdoptSkipsStalePIDFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "postmaster.pid"), []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataPath: dir, Port: 1, User: "u", Password: "p", Database: "d"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if srv, ok, err := adopt(ctx, quiet(), cfg); ok || srv != nil || err != nil {
		t.Fatalf("adopt returned (%v, %v, %v) for a stale pid file", srv, ok, err)
	}
}

// An adopted server must not be stopped: we did not start it.
func TestStopIsNoOpForAdoptedServer(t *testing.T) {
	srv := &Server{adopted: true, log: quiet()}
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop on an adopted server: %v", err)
	}
}

func TestDSNFor(t *testing.T) {
	cfg := Config{Port: 54329, User: "querycraft", Password: "p@ss word", Database: "db"}
	want := "postgres://querycraft:p%40ss+word@127.0.0.1:54329/db?sslmode=disable"
	if got := cfg.DSNFor(); got != want {
		t.Fatalf("DSNFor() = %q, want %q", got, want)
	}
}
