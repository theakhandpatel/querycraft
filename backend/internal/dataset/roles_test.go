package dataset

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestLearnerRoleCannotEscapeTheSandbox is the regression test for the sandbox
// hole this role closes: learner SQL used to run as the database owner, so a
// fully qualified name reached `app`, and pg_read_file()/pg_authid were open.
//
//	export QC_TEST_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable'
//	go test ./internal/dataset -run TestLearnerRoleCan -v
func TestLearnerRoleCannotEscapeTheSandbox(t *testing.T) {
	adminDSN := os.Getenv("QC_TEST_DATABASE_URL")
	if adminDSN == "" {
		t.Skip("QC_TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	if _, err := admin.PgConn().Exec(ctx, SchemaSQL()).ReadAll(); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := admin.PgConn().Exec(ctx, SeedSQL()).ReadAll(); err != nil {
		t.Fatalf("apply seed: %v", err)
	}
	// A stand-in for gateway metadata, so the denial below is a privilege
	// check and not a missing table.
	if _, err := admin.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS app"); err != nil {
		t.Fatalf("create app schema: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE TABLE IF NOT EXISTS app.progress (id int)"); err != nil {
		t.Fatalf("create app.progress: %v", err)
	}
	// The matching stand-in inside `public`, PostgreSQL's default creation
	// schema. A learner must be refused by privilege, not merely by the table
	// being absent, and an unqualified name must not resolve to it either.
	if _, err := admin.Exec(ctx, "CREATE TABLE IF NOT EXISTS public.progress (id int)"); err != nil {
		t.Fatalf("create public.progress: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS public.progress")
	})

	const password = "probe-password"
	if err := EnsureLearnerRole(ctx, admin, password); err != nil {
		t.Fatalf("ensure learner role: %v", err)
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	owner := "querycraft"
	if u.User != nil {
		owner = u.User.Username()
	}
	u.User = url.UserPassword(LearnerRole, password)
	learner, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect as learner: %v", err)
	}
	defer func() { _ = learner.Close(context.WithoutCancel(ctx)) }()

	// The dataset itself must stay reachable, unqualified, through the role's
	// search_path default.
	var n int
	if err := learner.QueryRow(ctx, "SELECT count(*) FROM products").Scan(&n); err != nil {
		t.Fatalf("learner cannot read the dataset: %v", err)
	}
	if n == 0 {
		t.Fatalf("learner read %d products, want > 0", n)
	}

	// `public` is removed from the role's search_path, so a table created there
	// must not be visible to an unqualified learner name. Without this an
	// operator or extension that writes to the default schema would silently
	// extend what a learner can reach.
	if _, err := admin.Exec(ctx, "CREATE TABLE IF NOT EXISTS public.shadow_probe (id int)"); err != nil {
		t.Fatalf("create public.shadow_probe: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS public.shadow_probe")
	})
	var path []string
	if err := learner.QueryRow(ctx, "SELECT current_schemas(false)").Scan(&path); err != nil {
		t.Fatalf("read learner search path: %v", err)
	}
	for _, schema := range path {
		if schema == "public" {
			t.Fatalf("learner search path still contains public: %v", path)
		}
	}
	if _, err := learner.Exec(ctx, "SELECT count(*) FROM shadow_probe"); err == nil {
		t.Error("learner resolved an unqualified name from public; the search_path still includes public")
	}

	// Each probe must fail. `denied` probes must fail by privilege; a name that
	// is not on the search path fails with `undefined_table` (42P01) instead,
	// which is just as strong a result -- the object is not visible at all.
	for _, probe := range []struct {
		name, sql string
		missing   bool // true when 42P01 (undefined_table) is the expected refusal
	}{
		{name: "gateway metadata", sql: "SELECT count(*) FROM app.progress"},
		{name: "qualified default schema", sql: "SELECT count(*) FROM public.progress"},
		{name: "unqualified default schema", sql: "SELECT count(*) FROM progress", missing: true},
		{name: "superuser catalog", sql: "SELECT count(*) FROM pg_catalog.pg_authid"},
		{name: "password view", sql: "SELECT count(*) FROM pg_shadow"},
		{name: "file read", sql: "SELECT pg_read_file('/etc/hostname')"},
		{name: "directoy list", sql: "SELECT pg_ls_dir('/')"},
		{name: "escalate to owner", sql: "SET ROLE " + quoteIdent(owner)},
		{name: "assume owner session", sql: "SET SESSION AUTHORIZATION " + quoteIdent(owner)},
		{name: "write access", sql: "CREATE TABLE " + quoteIdent(SchemaName) + ".learner_wrote (id int)"},
	} {
		_, err := learner.Exec(ctx, probe.sql)
		if err == nil {
			t.Errorf("learner ran %q (%s); the sandbox is open", probe.sql, probe.name)
			continue
		}
		if probe.missing {
			if !strings.Contains(err.Error(), "does not exist") {
				t.Errorf("%s: got %v, want an undefined-table failure", probe.name, err)
			}
			continue
		}
		if !strings.Contains(err.Error(), "permission denied") && !strings.Contains(err.Error(), "must be superuser") {
			t.Errorf("%s: got %v, want a permission denial", probe.name, err)
		}
	}

	// A write must be refused as well, so a learner cannot mutate the dataset.
	if _, err := learner.Exec(ctx, "DELETE FROM products"); err == nil {
		t.Error("learner deleted rows from the dataset")
	}
}
