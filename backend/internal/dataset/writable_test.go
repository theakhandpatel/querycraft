package dataset_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
)

// TestWritableTemplateGrantsLearnerDDLAndDML proves the two template grant sets
// are not the same one applied twice.
//
// A challenge environment is copied from the writable template, and the copy
// carries its grants: the learner role there can run INSERT/UPDATE/DELETE, and
// create and drop its own objects in the dataset schema. The gateway's own
// database is copied from the read-only template and still refuses every write —
// so widening the environment did not widen the shared dataset.
func TestWritableTemplateGrantsLearnerDDLAndDML(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a PostgreSQL server; skipped under -short")
	}

	root := t.TempDir()
	port := freePort(t)
	binaries := cachedBinaries()
	const clusterPassword = "writable-template-password"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	srv, err := localdb.Start(ctx, quietLogger(), localdb.Config{
		DataPath:     filepath.Join(root, "pg", "data"),
		RuntimePath:  filepath.Join(root, "pg", "runtime"),
		BinariesPath: binaries,
		TempDir:      filepath.Join(root, "tmp"),
		Port:         port,
		User:         "querycraft",
		Password:     clusterPassword,
		Database:     "querycraft",
	})
	if err != nil {
		if binaries == "" {
			t.Skipf("no cached PostgreSQL binaries and no server could be started: %v", err)
		}
		t.Fatalf("boot embedded PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	admin, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	/* ---- the writable template a challenge environment is copied from ---- */

	writableTemplate := dataset.TemplateDatabaseName
	if err := envdb.CreateDatabase(ctx, admin, writableTemplate); err != nil {
		t.Fatalf("create the writable template: %v", err)
	}
	if err := dataset.LoadWritableIntoTemplate(ctx, admin, writableTemplate, clusterPassword); err != nil {
		t.Fatalf("load the writable template: %v", err)
	}
	if err := envdb.SealTemplate(ctx, admin, writableTemplate); err != nil {
		t.Fatalf("seal the writable template: %v", err)
	}

	writableEnv := dataset.EnvironmentDatabaseName("writable_probe")
	if err := envdb.CreateDatabaseFromTemplate(ctx, admin, writableEnv, writableTemplate); err != nil {
		t.Fatalf("copy the writable environment: %v", err)
	}
	learner, err := connectAsLearner(ctx, admin, writableEnv, clusterPassword)
	if err != nil {
		t.Fatalf("connect to the writable environment as the learner role: %v", err)
	}
	defer func() { _ = learner.Close(context.WithoutCancel(ctx)) }()

	// The copy is seeded and readable.
	if got := scalarCount(t, ctx, learner); got != 30 {
		t.Fatalf("the copied environment has %d customers, want 30", got)
	}

	// DML the course needs to teach.
	for _, stmt := range []string{
		"INSERT INTO customers (id, name, signup_date) VALUES (9001, 'Writable Probe', '2026-01-01')",
		"UPDATE customers SET city = 'Testville' WHERE id = 9001",
		"DELETE FROM customers WHERE id = 9001",
		"UPDATE products SET stock = stock + 1 WHERE id = 1",
		"UPDATE products SET stock = stock - 1 WHERE id = 1",
	} {
		mustExec(t, ctx, learner, stmt)
	}

	// DDL on the learner's own object.
	mustExec(t, ctx, learner, "CREATE TABLE dataset.learner_notes (id integer PRIMARY KEY, note text)")
	mustExec(t, ctx, learner, "INSERT INTO dataset.learner_notes (id, note) VALUES (1, 'hello')")
	mustExec(t, ctx, learner, "ALTER TABLE dataset.learner_notes ADD COLUMN created_at date")
	mustExec(t, ctx, learner, "DROP TABLE dataset.learner_notes")

	// The verbs withheld on purpose are still withheld: the writable scope is
	// the dataset schema's data and the learner's own objects, not the tables
	// the dataset owns.
	for _, probe := range []struct{ name, sql string }{
		{"truncate a seeded table", "TRUNCATE dataset.customers"},
		{"drop a seeded table", "DROP TABLE dataset.customers"},
		{"create a schema", "CREATE SCHEMA learner_schema"},
		{"create in the default schema", "CREATE TABLE public.learner_leak (id integer)"},
		{"grant a privilege with grant option", "GRANT SELECT ON dataset.customers TO PUBLIC WITH GRANT OPTION"},
	} {
		if _, err := learner.Exec(ctx, probe.sql); err == nil {
			t.Errorf("%s: %q was allowed; the writable grant set is too wide", probe.name, probe.sql)
		}
	}

	// A plain GRANT is the one case where the *error* is the wrong assertion.
	//
	// PostgreSQL answers `GRANT SELECT ON dataset.customers TO PUBLIC` with a
	// WARNING — "no privileges were granted for \"customers\"" — and a success
	// result, because the learner does not hold the privilege WITH GRANT OPTION
	// and so has none to hand on. pgx does not surface notices by default, so
	// the statement looks like it did something. What matters is not whether the
	// statement failed but whether anything changed: the table's ACL must be
	// byte-for-byte what it was, and PUBLIC must hold no privilege. Asserting an
	// error here would be asserting a property PostgreSQL does not have.
	before := relACL(t, ctx, admin, "customers")
	if _, err := learner.Exec(ctx, "GRANT SELECT ON dataset.customers TO PUBLIC"); err != nil {
		// Refusing outright is just as good, and the check below still holds.
		t.Logf("GRANT … TO PUBLIC was refused outright: %v", err)
	}
	after := relACL(t, ctx, admin, "customers")
	if before != after {
		t.Errorf("the learner's GRANT … TO PUBLIC changed the table ACL: %q -> %q", before, after)
	}
	if aclGrantsToPublic(after) {
		t.Errorf("PUBLIC holds a privilege on dataset.customers after the learner's GRANT: %q", after)
	}
	// And the sentinel the ACL protects is still there, so a widened grant did
	// not quietly come with a widened data change.
	if got := scalarCount(t, ctx, learner); got != 30 {
		t.Errorf("the table the learner tried to grant on now has %d customers, want 30", got)
	}

	/* ---- the read-only template the gateway's own database uses ---- */

	readOnlyTemplate := "qc_ro_template"
	if err := envdb.CreateDatabase(ctx, admin, readOnlyTemplate); err != nil {
		t.Fatalf("create the read-only template: %v", err)
	}
	if err := loadReadOnlyIntoTemplate(ctx, admin, readOnlyTemplate, clusterPassword); err != nil {
		t.Fatalf("load the read-only template: %v", err)
	}
	if err := envdb.SealTemplate(ctx, admin, readOnlyTemplate); err != nil {
		t.Fatalf("seal the read-only template: %v", err)
	}

	readOnlyEnv := dataset.EnvironmentDatabaseName("readonly_probe")
	if err := envdb.CreateDatabaseFromTemplate(ctx, admin, readOnlyEnv, readOnlyTemplate); err != nil {
		t.Fatalf("copy the read-only environment: %v", err)
	}
	reader, err := connectAsLearner(ctx, admin, readOnlyEnv, clusterPassword)
	if err != nil {
		t.Fatalf("connect to the read-only environment as the learner role: %v", err)
	}
	defer func() { _ = reader.Close(context.WithoutCancel(ctx)) }()

	if got := scalarCount(t, ctx, reader); got != 30 {
		t.Fatalf("the read-only environment has %d customers, want 30", got)
	}
	for _, sql := range []string{
		"INSERT INTO customers (id, name, signup_date) VALUES (9001, 'Nope', '2026-01-01')",
		"UPDATE customers SET city = 'Nope' WHERE id = 1",
		"DELETE FROM customers WHERE id = 1",
		"CREATE TABLE dataset.learner_notes (id integer)",
	} {
		if _, err := reader.Exec(ctx, sql); err == nil {
			t.Errorf("the read-only grant set allowed %q; the shared dataset became mutable", sql)
		}
	}
}

// scalarCount reads the customer count over a plain learner connection.
func scalarCount(t *testing.T, ctx context.Context, conn *pgx.Conn) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM customers").Scan(&n); err != nil {
		t.Fatalf("read the customer count: %v", err)
	}
	return n
}

// relACL reads a table's ACL exactly as PostgreSQL prints it. This is the only
// place a no-op GRANT is visible: the statement itself succeeds with a warning
// that pgx does not surface.
func relACL(t *testing.T, ctx context.Context, conn *pgx.Conn, table string) string {
	t.Helper()
	var acl *string
	err := conn.QueryRow(ctx, `
		SELECT c.relacl::text
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2`,
		dataset.SchemaName, table).Scan(&acl)
	if err != nil {
		t.Fatalf("read the ACL of %s.%s: %v", dataset.SchemaName, table, err)
	}
	if acl == nil {
		// NULL means "the owner's default privileges", i.e. nobody else holds
		// anything. Spelled as an empty string so two NULLs compare equal.
		return ""
	}
	return *acl
}

// aclGrantsToPublic reports whether an ACL string grants anything to PUBLIC.
// PUBLIC has no role name, so its entry is the one that starts with '=' —
// `{querycraft=arwdDxtm/querycraft,=r/querycraft}` grants PUBLIC a read.
func aclGrantsToPublic(acl string) bool {
	for _, entry := range strings.Split(strings.Trim(acl, "{}"), ",") {
		if strings.HasPrefix(strings.TrimSpace(entry), "=") {
			return true
		}
	}
	return false
}

// mustExec runs a statement that is expected to succeed.
func mustExec(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}
