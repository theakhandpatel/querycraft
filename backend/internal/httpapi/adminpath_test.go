package httpapi

// Exit-gate proof (M3 step 6.2): no path runs learner SQL on an administrative
// connection.
//
// The sandbox is only real if the SQL a learner submits through POST /query
// actually executes as the restricted `querycraft_learner` role, on a connection
// that holds SELECT on `dataset` and nothing else. This test proves that by
// behaviour rather than by reading the code: it boots its own PostgreSQL cluster
// (isolated port, t.TempDir() data directory), points a real engine.PgxExecutor
// at the learner DSN exactly as cmd/api does, drives the real gateway over
// httptest, then asks the connection who it is and what it can reach.
//
// It also proves the inverse, so it cannot pass by everything failing: a plain
// `SELECT count(*) FROM orders` must succeed on the same connection.
//
// One case is recorded as a documented exception: `RESET ROLE` resets the
// *current* role to the session user, which on a learner connection is already
// querycraft_learner. It is a legitimate no-op, not an escalation, so that case
// asserts the connection still runs as the restricted role afterwards rather
// than demanding an error PostgreSQL would be wrong to raise.
//
// Run it alone with:
//
//	export PATH=$PATH:/usr/local/go/bin
//	cd backend && go test ./internal/httpapi -run TestLearnerSQLNeverRunsOnTheAdminConnection -v -count=1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

// adminPathPassword is the throwaway password for this test's own cluster. The
// cluster lives under t.TempDir() and is destroyed with the test; nothing here
// is written into the tree.
const adminPathPassword = "adminpath-test-password"

// adminPathSentinel exists only on the administrative side of the wall. If it
// ever appears in a /query response, a learner statement read gateway metadata.
const adminPathSentinel = "qc-admin-sentinel-7b3d91f4"

// adminPathExpected is the committed commerce seed. The dataset must still hold
// exactly these rows after every hostile statement, because none of them may
// have taken effect.
var adminPathExpected = map[string]int{
	"customers":   30,
	"products":    20,
	"orders":      80,
	"order_items": 160,
}

func TestLearnerSQLNeverRunsOnTheAdminConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a PostgreSQL server; skipped under -short")
	}

	root := t.TempDir()
	port := adminPathFreePort(t)
	binaries := adminPathCachedBinaries()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	srv, err := localdb.Start(ctx, adminPathQuietLogger(), localdb.Config{
		DataPath:     filepath.Join(root, "pg", "data"),
		RuntimePath:  filepath.Join(root, "pg", "runtime"),
		BinariesPath: binaries,
		TempDir:      filepath.Join(root, "tmp"),
		Port:         port,
		User:         "querycraft",
		Password:     adminPathPassword,
		Database:     "querycraft",
	})
	if err != nil {
		// No cached binaries and no server: the machine cannot run this proof, so
		// skip instead of failing. With binaries present a boot failure is a real
		// defect and must fail loudly.
		if binaries == "" {
			t.Skipf("no cached PostgreSQL binaries and no server could be started: %v", err)
		}
		t.Fatalf("boot embedded PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	adminDSN := srv.DSN
	learnerDSN := adminPathSwapDSNUser(adminDSN, dataset.LearnerRole, adminPathPassword)

	// The dataset is applied over the admin connection, exactly as the gateway
	// does on first boot. The scripts carry their own BEGIN/COMMIT, so they need
	// the simple query protocol.
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}
	defer func() { _ = adminConn.Close(context.WithoutCancel(ctx)) }()

	for _, step := range []struct{ name, script string }{
		{"schema", dataset.SchemaSQL()},
		{"seed", dataset.SeedSQL()},
	} {
		if _, err := adminConn.PgConn().Exec(ctx, step.script).ReadAll(); err != nil {
			t.Fatalf("apply dataset %s: %v", step.name, err)
		}
	}
	// The restricted role must exist and hold SELECT on `dataset` alone. It is
	// (re)asserted after the dataset so the ALL TABLES grant sees the tables.
	if err := dataset.EnsureLearnerRole(ctx, adminConn, adminPathPassword); err != nil {
		t.Fatalf("ensure learner role: %v", err)
	}

	// The metadata store creates the real `app` schema the gateway uses, which
	// is also the schema a learner statement must not be able to reach.
	st, err := store.NewPostgres(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open postgres metadata store: %v", err)
	}
	defer st.Close()

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()

	// Two learner-unreachable tables hold a value that must never surface.
	// app.progress is the real metadata table the gateway writes; public.progress
	// is a stand-in for anything an operator or extension leaves in PostgreSQL's
	// default creation schema.
	for _, stmt := range []string{
		`INSERT INTO app.users (id, email, name) VALUES ('qc-sentinel', 'sentinel@querycraft.test', 'Sentinel')`,
		`INSERT INTO app.progress (user_id, course, snapshot) VALUES ('qc-sentinel', 'sentinel-course', jsonb_build_object('sentinel', '` + adminPathSentinel + `'))`,
		`CREATE TABLE public.progress (secret text)`,
		`INSERT INTO public.progress (secret) VALUES ('` + adminPathSentinel + `')`,
	} {
		if _, err := adminPool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed sentinel %q: %v", stmt, err)
		}
	}

	// The executor the gateway runs on: the learner DSN, never the admin DSN.
	exec, err := engine.NewPgxExecutor(ctx, learnerDSN)
	if err != nil {
		t.Fatalf("open learner executor: %v", err)
	}
	defer exec.Close()
	if err := exec.Ping(ctx); err != nil {
		t.Fatalf("ping learner connection: %v", err)
	}

	s := New(exec, st, Config{MaxRows: 200, MaxBytes: 1 << 20, TimeoutMS: 5000, MaxConcurrent: 2}, nil)

	t.Run("the learner connection is the one running the SQL", func(t *testing.T) {
		_, qr := adminPathRun(t, s, "SELECT current_user, session_user")
		if qr.Status != "succeeded" {
			t.Fatalf("status = %q, error = %+v", qr.Status, qr.Error)
		}
		if len(qr.Rows) != 1 || len(qr.Rows[0]) != 2 {
			t.Fatalf("rows = %+v, want one row of two columns", qr.Rows)
		}
		current, session := fmt.Sprint(qr.Rows[0][0]), fmt.Sprint(qr.Rows[0][1])
		if current != dataset.LearnerRole || session != dataset.LearnerRole {
			t.Fatalf("current_user = %q, session_user = %q; want both %q: learner SQL ran on the admin connection",
				current, session, dataset.LearnerRole)
		}
	})

	// The inverse: the proof cannot pass by every statement failing.
	t.Run("the dataset itself stays reachable", func(t *testing.T) {
		_, qr := adminPathRun(t, s, "SELECT count(*) FROM orders")
		if qr.Status != "succeeded" {
			t.Fatalf("status = %q, error = %+v", qr.Status, qr.Error)
		}
		if len(qr.Rows) != 1 || len(qr.Rows[0]) != 1 {
			t.Fatalf("rows = %+v, want one count", qr.Rows)
		}
		if got := adminPathInt(t, qr.Rows[0][0]); got != adminPathExpected["orders"] {
			t.Fatalf("count(*) FROM orders = %d, want %d", got, adminPathExpected["orders"])
		}
	})

	t.Run("SELECT on dataset, no INSERT", func(t *testing.T) {
		_, qr := adminPathRun(t, s,
			"SELECT has_table_privilege('dataset.orders','SELECT'), has_table_privilege('dataset.orders','INSERT')")
		if qr.Status != "succeeded" {
			t.Fatalf("status = %q, error = %+v", qr.Status, qr.Error)
		}
		if len(qr.Rows) != 1 || len(qr.Rows[0]) != 2 {
			t.Fatalf("rows = %+v, want [can_select can_insert]", qr.Rows)
		}
		canSelect, okSelect := qr.Rows[0][0].(bool)
		canInsert, okInsert := qr.Rows[0][1].(bool)
		if !okSelect || !okInsert {
			t.Fatalf("privilege flags = %+v, want booleans", qr.Rows[0])
		}
		if !canSelect {
			t.Error("learner cannot SELECT dataset.orders; the lesson would be unreadable")
		}
		if canInsert {
			t.Error("learner holds INSERT on dataset.orders")
		}
	})

	// Gateway metadata, qualified and unqualified. Both must be refused by
	// privilege, and neither may leak the sentinel.
	for _, probe := range []struct{ name, sql string }{
		{"app schema", "SELECT * FROM app.progress"},
		{"public schema", "SELECT * FROM public.progress"},
	} {
		t.Run("metadata is unreachable: "+probe.name, func(t *testing.T) {
			w, qr := adminPathRun(t, s, probe.sql)
			adminPathWantDenied(t, w, qr, probe.sql, learnerDSN, ctx)
			if qr.RowCount != 0 || len(qr.Rows) != 0 {
				t.Errorf("row_count = %d, rows = %d; want no rows", qr.RowCount, len(qr.Rows))
			}
			if strings.Contains(w.Body.String(), adminPathSentinel) {
				t.Errorf("the sentinel value leaked into the response: %s", w.Body.String())
			}
			adminPathAssertDatasetIntact(t, ctx, adminPool)
		})
	}

	// Every one of these is an escape attempt. Each must come back as a bounded
	// QUERY_RUNTIME_ERROR (HTTP 400, never a 5xx, never success), and the dataset
	// must be untouched afterwards.
	attacks := []struct {
		name string
		sql  string
		// noop marks a statement PostgreSQL legitimately accepts. RESET ROLE
		// resets the *current* role to the session user, which for a learner
		// connection is already querycraft_learner: it is not an escalation, so
		// the test proves the security property (the connection cannot be moved
		// onto a privileged role) instead of demanding an error it cannot
		// honestly demand. whyNot records that reason and is required when noop
		// is set.
		noop   bool
		whyNot string
	}{
		{"read a server file", "SELECT pg_read_file('/etc/passwd')", false, ""},
		{"list a server directory", "SELECT pg_ls_dir('/')", false, ""},
		{"copy to a program", "COPY (SELECT 1) TO PROGRAM 'id'", false, ""},
		{"create a role", "CREATE ROLE qc_attack", false, ""},
		{"create a database", "CREATE DATABASE qc_attack", false, ""},
		{"change server settings", "ALTER SYSTEM SET work_mem = '1GB'", false, ""},
		{"drop a dataset table", "DROP TABLE dataset.orders", false, ""},
		{"truncate a dataset table", "TRUNCATE TABLE dataset.orders", false, ""},
		{"assume the owner role", "SET ROLE querycraft", false, ""},
		{"reset to the session role", "RESET ROLE", true,
			"the session user of a learner connection is already querycraft_learner, so resetting to it changes nothing"},
		{"read password hashes", "SELECT rolpassword FROM pg_authid", false, ""},
	}
	for _, atk := range attacks {
		t.Run("refused: "+atk.name, func(t *testing.T) {
			w, qr := adminPathRun(t, s, atk.sql)
			if atk.noop {
				adminPathWantNoEscalation(t, s, w, qr, atk.sql, atk.whyNot, learnerDSN, ctx)
			} else {
				adminPathWantDenied(t, w, qr, atk.sql, learnerDSN, ctx)
			}
			if strings.Contains(w.Body.String(), adminPathSentinel) {
				t.Errorf("the sentinel value leaked into the response: %s", w.Body.String())
			}
			adminPathAssertDatasetIntact(t, ctx, adminPool)
		})
	}

	t.Run("nothing changed on the administrative side", func(t *testing.T) {
		adminPathAssertDatasetIntact(t, ctx, adminPool)

		var appSecret string
		if err := adminPool.QueryRow(ctx,
			`SELECT snapshot->>'sentinel' FROM app.progress WHERE user_id = 'qc-sentinel'`,
		).Scan(&appSecret); err != nil {
			t.Fatalf("re-read app.progress sentinel: %v", err)
		}
		if appSecret != adminPathSentinel {
			t.Errorf("app.progress sentinel = %q, want %q", appSecret, adminPathSentinel)
		}

		var publicSecret string
		if err := adminPool.QueryRow(ctx, `SELECT secret FROM public.progress`).Scan(&publicSecret); err != nil {
			t.Fatalf("re-read public.progress sentinel: %v", err)
		}
		if publicSecret != adminPathSentinel {
			t.Errorf("public.progress sentinel = %q, want %q", publicSecret, adminPathSentinel)
		}
	})
}

/* ---------- assertions ---------- */

// adminPathRun submits sql through POST /query and returns the recorder plus the
// decoded result. Every response must be a QueryResult document: a body the
// contract cannot describe, or a 5xx, is itself a failure.
func adminPathRun(t *testing.T, s *Server, sql string) (*httptest.ResponseRecorder, engine.QueryResult) {
	t.Helper()
	w := do(t, s, http.MethodPost, "/query", adminPathBody(sql))
	if w.Code >= 500 {
		t.Fatalf("POST /query %q answered HTTP %d (5xx): %s", sql, w.Code, w.Body.String())
	}
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	return w, qr
}

// adminPathWantDenied requires the statement to be refused as a bounded
// QUERY_RUNTIME_ERROR, with the actual SQLSTATE reported when it is not.
func adminPathWantDenied(t *testing.T, w *httptest.ResponseRecorder, qr engine.QueryResult, sql, learnerDSN string, ctx context.Context) {
	t.Helper()
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /query %q answered HTTP %d, want 400 (sqlstate %s): %s",
			sql, w.Code, adminPathProbeSQLState(ctx, learnerDSN, sql), w.Body.String())
	}
	if qr.Status == "succeeded" {
		t.Fatalf("POST /query %q succeeded; the statement was not refused (row_count = %d, sqlstate %s)",
			sql, qr.RowCount, adminPathProbeSQLState(ctx, learnerDSN, sql))
	}
	if qr.Error == nil {
		t.Fatalf("POST /query %q failed without an error envelope (sqlstate %s): %s",
			sql, adminPathProbeSQLState(ctx, learnerDSN, sql), w.Body.String())
	}
	if qr.Error.Code != engine.CodeRuntime {
		t.Fatalf("POST /query %q → code %s (sqlstate %s, message %q), want %s",
			sql, qr.Error.Code, adminPathProbeSQLState(ctx, learnerDSN, sql), qr.Error.Message, engine.CodeRuntime)
	}
}

// adminPathWantNoEscalation handles the one statement PostgreSQL may accept.
// A refusal is fine (and is what every other case demands); a success is fine
// only if the connection still runs as the restricted role afterwards. What is
// never fine is a success that changes who the connection is.
func adminPathWantNoEscalation(t *testing.T, s *Server, w *httptest.ResponseRecorder, qr engine.QueryResult, sql, why, learnerDSN string, ctx context.Context) {
	t.Helper()
	if qr.Status != "succeeded" {
		adminPathWantDenied(t, w, qr, sql, learnerDSN, ctx)
		return
	}
	if why == "" {
		t.Fatalf("POST /query %q succeeded without a documented reason in the table", sql)
	}
	// Read the identity back through the gateway: a role change that had stuck
	// would show up here.
	_, after := adminPathRun(t, s, "SELECT current_user, session_user")
	if after.Status != "succeeded" || len(after.Rows) != 1 || len(after.Rows[0]) != 2 {
		t.Fatalf("identity probe after %q = %+v", sql, after)
	}
	current, session := fmt.Sprint(after.Rows[0][0]), fmt.Sprint(after.Rows[0][1])
	if current != dataset.LearnerRole || session != dataset.LearnerRole {
		t.Fatalf("after %q the connection runs as current_user=%q session_user=%q; want both %q",
			sql, current, session, dataset.LearnerRole)
	}
	t.Logf("%q is a documented no-op (%s); the connection is still %s/%s", sql, why, current, session)
}

// adminPathAssertDatasetIntact re-counts the four commerce tables over the admin
// connection. Any successful DROP/TRUNCATE/CREATE would show up here.
func adminPathAssertDatasetIntact(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range []string{"customers", "products", "orders", "order_items"} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM dataset."+table).Scan(&n); err != nil {
			t.Fatalf("count dataset.%s: %v", table, err)
		}
		if want := adminPathExpected[table]; n != want {
			t.Errorf("dataset.%s has %d rows, want %d; a hostile statement changed the dataset", table, n, want)
		}
	}
}

/* ---------- diagnostics ---------- */

// adminPathProbeSQLState runs sql on a fresh learner connection to report the
// raw PostgreSQL SQLSTATE in a failure message. It is only called once an
// assertion has already failed: the assertion itself is always the HTTP
// response, and the contract's QueryError does not carry a SQLSTATE field.
func adminPathProbeSQLState(ctx context.Context, learnerDSN, sql string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(probeCtx, learnerDSN)
	if err != nil {
		return "unavailable: " + adminPathFirstLine(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(probeCtx)) }()

	// Reproduce the gateway's execution conditions: the same restricted role,
	// inside a read-only transaction. A plain autocommit probe would answer
	// 42501 where the gateway sees 25006 for a write attempt, so the SQLSTATE
	// reported in a failure message would not be the one classify saw.
	//
	// The Exec error must be its own variable: scoping it inside an if would
	// leave the outer (nil) connect error in scope and errors.As would never
	// see the PostgreSQL failure.
	tx, err := conn.BeginTx(probeCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "probe begin failed: " + adminPathFirstLine(err)
	}
	_, execErr := tx.Exec(probeCtx, sql)
	_ = tx.Rollback(context.WithoutCancel(probeCtx))
	if execErr == nil {
		// A read-only transaction accepted the statement: it is a no-op, not a
		// refusal.
		return "00000 (no error inside a read-only transaction either)"
	}
	var pgErr *pgconn.PgError
	if errors.As(execErr, &pgErr) && pgErr != nil {
		return pgErr.Code
	}
	return "no sqlstate: " + adminPathFirstLine(execErr)
}

func adminPathFirstLine(err error) string {
	if err == nil {
		return ""
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

/* ---------- helpers ---------- */

// adminPathSwapDSNUser rebuilds a DSN with a different login, the same way
// cmd/api's swapDSNUser derives the learner DSN from the admin DSN.
func adminPathSwapDSNUser(raw, user, password string) string {
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

// adminPathBody renders a query body without hand-quoting the SQL.
func adminPathBody(sql string) string {
	b, err := json.Marshal(map[string]string{"sql": sql})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// adminPathInt accepts the number shapes JSON decoding can produce for a count.
func adminPathInt(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			t.Fatalf("value %v is not an integer: %v", v, err)
		}
		return int(i)
	}
	t.Fatalf("value %v (%T) is not an integer", v, v)
	return 0
}

// adminPathQuietLogger discards the embedded server's log so the test's own
// output stays readable.
func adminPathQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// adminPathCachedBinaries finds an already-extracted PostgreSQL under the
// repository's .data directory so the test needs no network. An empty result
// lets embedded-postgres fall back to its own download cache.
func adminPathCachedBinaries() string {
	if v := os.Getenv("QC_PG_BINARIES"); v != "" {
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

// adminPathFreePort picks an unused TCP port so the test never collides with a
// gateway a developer may be running.
func adminPathFreePort(t *testing.T) uint32 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the free port: %v", err)
	}
	return uint32(port)
}
