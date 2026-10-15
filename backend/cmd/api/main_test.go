package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

// isLoopbackAddr decides whether an empty-password administrator is allowed to
// be served at all. Failing closed matters: a wildcard bind, a concrete LAN
// address, or a host that cannot be proven local must all come back false, so
// the gateway refuses to publish a blank-password admin rather than guessing.
func TestIsLoopbackAddr(t *testing.T) {
	loopback := []string{
		"127.0.0.1:8080",
		"127.0.0.1:0",
		"localhost:8080",
		"[::1]:8080",
		"127.5.5.5:80",
	}
	for _, addr := range loopback {
		if !isLoopbackAddr(addr) {
			t.Errorf("isLoopbackAddr(%q) = false, want true", addr)
		}
	}

	notLoopback := []string{
		":8080",           // wildcard host: every interface
		"0.0.0.0:8080",    // all interfaces, explicit
		"192.168.1.10:80", // a concrete LAN address
		"10.0.0.1:80",
		"[::]:8080",       // all interfaces, IPv6
		"example.test:80", // a name that is not localhost
		"",                // nothing to bind proved nothing
	}
	for _, addr := range notLoopback {
		if isLoopbackAddr(addr) {
			t.Errorf("isLoopbackAddr(%q) = true, want false", addr)
		}
	}
}

func TestNewStoreSQLiteIsExplicitAndDefaultStaysMemory(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("QC_ADMIN_DATABASE_URL", "")
	t.Setenv("QC_METADATA", "")
	st, err := newStore(context.Background(), log, "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind() != "memory" {
		t.Fatalf("default kind=%q, want memory", st.Kind())
	}
	_ = st.Close()

	path := filepath.Join(t.TempDir(), "nested", "metadata.sqlite")
	t.Setenv("QC_METADATA", "sqlite")
	t.Setenv("QC_METADATA_PATH", path)
	st, err = newStore(context.Background(), log, "postgres://ignored-by-sqlite-selection")
	if err != nil {
		t.Fatalf("select sqlite: %v", err)
	}
	if _, ok := st.(*store.SQLite); !ok || st.Kind() != "sqlite" {
		t.Fatalf("selected store=%T kind=%q", st, st.Kind())
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sqlite file was not created: %v", err)
	}

	// Explicit selection must fail visibly when its file cannot be opened,
	// rather than falsely reporting that metadata was made durable in memory.
	badPath := t.TempDir() // A directory cannot be opened as the database file.
	t.Setenv("QC_METADATA_PATH", badPath)
	if _, err := newStore(context.Background(), log, ""); err == nil {
		t.Fatal("unopenable explicit SQLite store silently succeeded")
	}
}

// newExecutor must not leak the pgx pool when the pool is created but the
// liveness probe fails: pgxpool.NewWithConfig starts a background goroutine per
// pool, and a pool that is never closed keeps that goroutine for the process
// lifetime.
func TestNewExecutorClosesPoolWhenPingFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// A closed port parses fine but refuses connections immediately, so Ping
	// fails fast after the pool has already started its background goroutine.
	const dsn = "postgres://u:p@127.0.0.1:1/qc?sslmode=disable&connect_timeout=1"

	baseline := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ex := newExecutor(ctx, log, dsn)
		cancel()
		if _, ok := ex.(engine.UnavailableExecutor); !ok {
			t.Fatalf("newExecutor = %T, want UnavailableExecutor", ex)
		}
	}

	// Closing the pool lets its background goroutine exit; poll briefly.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if n := runtime.NumGoroutine(); n <= baseline+1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines did not return to baseline: %d > %d", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
