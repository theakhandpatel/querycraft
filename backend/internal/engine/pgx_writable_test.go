package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

// TestWritableExecutorCommitsAndReadOnlyRollsBack proves the two transaction
// modes are real, on one server and one grant set.
//
// The lesson this encodes: the shared dataset is read-only, so the course could
// never teach INSERT/UPDATE/DELETE/DDL. A challenge environment is writable, and
// the difference must be a transaction mode rather than a convention — if the
// read-only executor leaked a write, the shared dataset would be mutable.
func TestWritableExecutorCommitsAndReadOnlyRollsBack(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	// Grant the DML verbs so the only thing between the learner and a write is
	// the transaction mode, not a missing privilege. This widens the test
	// cluster's default database for the test alone; EnsureLearnerRole itself
	// stays SELECT-only.
	if _, err := admin.Exec(ctx,
		"GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA "+dataset.SchemaName+" TO "+dataset.LearnerRole,
	); err != nil {
		t.Fatalf("grant DML to the learner role: %v", err)
	}

	learnerDSN := srv.LearnerDSN(t, srv.Database)

	writable, err := NewPgxExecutorWithConfig(ctx, learnerDSN, PgxConfig{MaxConns: 2, MinConns: 1, Writable: true})
	if err != nil {
		t.Fatalf("open the writable executor: %v", err)
	}
	defer writable.Close()
	if !writable.Writable() {
		t.Fatal("a writable executor reports itself read-only")
	}

	// The default executor is a different pool on the same DSN. It is the only
	// proof of commit that a single connection cannot fake: a row written and
	// committed through one pool is visible through another.
	readonly, err := NewPgxExecutor(ctx, learnerDSN)
	if err != nil {
		t.Fatalf("open the read-only executor: %v", err)
	}
	defer readonly.Close()
	if readonly.Writable() {
		t.Fatal("NewPgxExecutor produced a writable executor; the default must stay read-only")
	}

	// 1. An INSERT through the writable executor succeeds.
	if res := Execute(ctx, writable,
		"INSERT INTO customers (id, name, signup_date) VALUES (9001, 'Writable Probe', '2026-01-01')",
		DefaultLimits()); res.Status != "succeeded" {
		t.Fatalf("writable INSERT failed: %+v", res.Error)
	}
	// 2. It is visible to a later transaction on the same pool...
	if got := scalarCount(t, ctx, writable, "SELECT count(*) FROM customers WHERE id = 9001"); got != 1 {
		t.Fatalf("a later statement on the writable pool saw %d row(s), want 1 (the write did not commit)", got)
	}
	// 3. ...and, crucially, on a different pool: the write really committed.
	if got := scalarCount(t, ctx, readonly, "SELECT count(*) FROM customers WHERE id = 9001"); got != 1 {
		t.Fatalf("a separate connection saw %d row(s) after a writable INSERT, want 1 (the write was not committed)", got)
	}

	// 4. A writable statement that fails leaves nothing behind.
	if res := Execute(ctx, writable,
		"INSERT INTO customers (id, name, signup_date) SELECT v.id, v.name, v.date FROM (VALUES (9002, 'Half Written', '2026-01-01'::date), (1, 'Duplicate Key', '2026-01-01'::date)) AS v(id, name, date)",
		DefaultLimits()); res.Status != "failed" {
		t.Fatalf("a statement with a duplicate key succeeded: %+v", res)
	}
	if got := scalarCount(t, ctx, writable, "SELECT count(*) FROM customers WHERE id = 9002"); got != 0 {
		t.Fatalf("a failed writable statement left %d row(s) behind", got)
	}

	// 5. A truncated writable statement does not persist the rows it produced.
	// The row cap makes execute.go cancel the request context the moment it
	// trips, and a cancelled statement is not a finished one.
	truncated := DefaultLimits()
	truncated.MaxRows = 10
	res := Execute(ctx, writable,
		"INSERT INTO customers (id, name, signup_date) SELECT 9100 + g, 'bulk ' || g, '2026-01-01'::date FROM generate_series(1, 1000) AS g RETURNING id",
		truncated)
	if res.Status != "succeeded" || !res.Truncated {
		t.Fatalf("capped INSERT: status=%q truncated=%v, want a truncated success", res.Status, res.Truncated)
	}
	if got := scalarCount(t, ctx, writable, "SELECT count(*) FROM customers WHERE id >= 9100"); got != 0 {
		t.Fatalf("a truncated statement persisted %d row(s); a capped result must not commit", got)
	}

	// 6. The read-only executor refuses the same INSERT at the server, with the
	// same grants in force, and persists nothing.
	res = Execute(ctx, readonly,
		"INSERT INTO customers (id, name, signup_date) VALUES (9003, 'Read Only Probe', '2026-01-01')",
		DefaultLimits())
	if res.Status != "failed" {
		t.Fatalf("the read-only executor ran an INSERT: %+v", res)
	}
	if res.Error == nil || res.Error.Code != CodeRuntime {
		t.Fatalf("read-only INSERT failed with %+v, want code %s", res.Error, CodeRuntime)
	}
	if got := scalarCount(t, ctx, readonly, "SELECT count(*) FROM customers WHERE id = 9003"); got != 0 {
		t.Fatalf("the refused read-only INSERT persisted %d row(s)", got)
	}
}

// scalarCount runs a single-value SELECT through ex and returns it as an int.
func scalarCount(t *testing.T, ctx context.Context, ex *PgxExecutor, sql string) int {
	t.Helper()
	res := Execute(ctx, ex, sql, DefaultLimits())
	if res.Status != "succeeded" {
		t.Fatalf("query %q failed: %+v", sql, res.Error)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("query %q returned %d row(s)", sql, len(res.Rows))
	}
	n, ok := res.Rows[0][0].(int64)
	if !ok {
		t.Fatalf("query %q returned %T, want int64", sql, res.Rows[0][0])
	}
	return int(n)
}
