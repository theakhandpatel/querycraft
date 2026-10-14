package main

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

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
