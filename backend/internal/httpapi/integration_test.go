package httpapi

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

// TestGatewayIntegration drives the whole HTTP surface against a live
// PostgreSQL database seeded with the commerce dataset. It is skipped unless
// QC_TEST_DATABASE_URL is set, so the default `go test ./...` run stays green
// with no database available.
//
//	export QC_TEST_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable'
//	go test ./internal/httpapi -run TestGatewayIntegration -v
func TestGatewayIntegration(t *testing.T) {
	dsn := os.Getenv("QC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("QC_TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	seedCommerce(t, ctx, dsn)

	exec, err := engine.NewPgxExecutor(ctx, dsn)
	if err != nil {
		t.Fatalf("open pgx executor: %v", err)
	}
	defer exec.Close()
	if err := exec.Ping(ctx); err != nil {
		t.Fatalf("ping execution database: %v", err)
	}

	st, err := store.NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("open postgres metadata store: %v", err)
	}
	defer st.Close()

	s := New(exec, st, Config{MaxRows: 100, TimeoutMS: 5000, MaxConcurrent: 2}, nil)

	t.Run("health", func(t *testing.T) {
		w := do(t, s, http.MethodGet, "/health", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		var out map[string]any
		mustDecode(t, w, &out)
		if out["status"] != "ok" || out["engine"] != engine.EngineName {
			t.Fatalf("unexpected health: %v", out)
		}
	})

	t.Run("query", func(t *testing.T) {
		w := do(t, s, http.MethodPost, "/query",
			`{"sql":"SELECT status, count(*) AS order_count FROM orders GROUP BY status ORDER BY status"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		var qr engine.QueryResult
		mustDecode(t, w, &qr)
		if qr.Status != "succeeded" {
			t.Fatalf("status = %q, error = %+v", qr.Status, qr.Error)
		}
		if qr.RowCount == 0 || len(qr.Rows) != qr.RowCount {
			t.Fatalf("row_count = %d, len(rows) = %d", qr.RowCount, len(qr.Rows))
		}
	})

	t.Run("syntax error", func(t *testing.T) {
		w := do(t, s, http.MethodPost, "/query", `{"sql":"SELECT * FROM"}`)
		var qr engine.QueryResult
		mustDecode(t, w, &qr)
		if qr.Error == nil || qr.Error.Code != engine.CodeSyntax {
			t.Fatalf("want QUERY_SYNTAX_ERROR, got %+v", qr.Error)
		}
	})

	t.Run("tables", func(t *testing.T) {
		w := do(t, s, http.MethodGet, "/datasets/commerce/tables", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		var out dataset.TablesResponse
		mustDecode(t, w, &out)
		if out.Dataset != "commerce" || len(out.Tables) != 4 {
			t.Fatalf("unexpected tables response: %+v", out)
		}
	})

	t.Run("progress round trip", func(t *testing.T) {
		token := signupUser(t, s, "integration@example.test")
		put := doAs(t, s, token, http.MethodPut, "/progress/sql-analytics", `{"version":1,"passed":{"where.1":true}}`)
		if put.Code != http.StatusOK {
			t.Fatalf("put status = %d, body = %s", put.Code, put.Body.String())
		}
		w := doAs(t, s, token, http.MethodGet, "/progress/sql-analytics", "")
		var out struct {
			OK       bool           `json:"ok"`
			Snapshot map[string]any `json:"snapshot"`
		}
		mustDecode(t, w, &out)
		if !out.OK || out.Snapshot == nil {
			t.Fatalf("progress not persisted: %s", w.Body.String())
		}
	})
}

// seedCommerce applies the committed schema and seed so integration queries have
// deterministic data. DDL and seeding use a plain pool and never run on the
// read-only learner executor.
func seedCommerce(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	// pgx sends argument-free Exec calls with the simple protocol, so the
	// multi-statement schema and seed scripts are applied in one round trip.
	if _, err := pool.Exec(ctx, dataset.SchemaSQL()); err != nil {
		t.Fatalf("apply dataset schema: %v", err)
	}
	if _, err := pool.Exec(ctx, dataset.SeedSQL()); err != nil {
		t.Fatalf("apply dataset seed: %v", err)
	}
}
