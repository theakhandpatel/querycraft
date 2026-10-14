package dataset

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LearnerRole is the login role every learner statement runs as.
//
// The sandbox must not depend on the statement being well behaved. A superuser
// session is not contained by a READ ONLY transaction or a search_path: a fully
// qualified name walks straight past the search path, and superuser-only
// functions such as pg_read_file() ignore both. Running learner SQL as a role
// that holds SELECT on `dataset` and nothing else makes the database itself
// refuse catalog reads, gateway metadata and the server's file functions.
const LearnerRole = "querycraft_learner"

// EnsureLearnerRole creates or repairs the restricted role and grants it the
// dataset schema alone.
//
// It is idempotent and must run on every boot, not only when the dataset is
// first created: a cluster initialised before this role existed still needs it,
// and a container image may present a database the app has never seen.
func EnsureLearnerRole(ctx context.Context, conn *pgx.Conn, password string) error {
	role := quoteIdent(LearnerRole)

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)", LearnerRole).Scan(&exists); err != nil {
		return fmt.Errorf("check learner role: %w", err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, "CREATE ROLE "+role+" LOGIN"); err != nil {
			return fmt.Errorf("create learner role: %w", err)
		}
	}

	// Re-assert the attributes on every boot so an operator cannot leave the
	// role privileged, and so the password matches the admin DSN's.
	attrs := "ALTER ROLE " + role + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
	if password != "" {
		attrs += " PASSWORD " + quoteLiteral(password)
	}
	if _, err := conn.Exec(ctx, attrs); err != nil {
		return fmt.Errorf("configure learner role: %w", err)
	}

	// temp_file_limit is a superuser-only setting, so the learner cannot issue
	// `SET LOCAL temp_file_limit`. As a role default PostgreSQL applies it
	// without the learner needing the privilege.
	defaults := []string{
		"ALTER ROLE " + role + " SET temp_file_limit = '64MB'",
		// `public` is deliberately absent: it is PostgreSQL's default creation
		// schema, so anything an operator or extension later creates there would
		// otherwise become silently reachable from a learner statement.
		"ALTER ROLE " + role + " SET search_path = " + quoteIdent(SchemaName),
	}
	// Create the dataset schema if this call precedes the schema script. GRANT
	// fails hard with "schema dataset does not exist" on a brand-new cluster,
	// which would abort startup before schema.sql ever runs.
	grants := []string{
		"CREATE SCHEMA IF NOT EXISTS " + quoteIdent(SchemaName),
		"GRANT USAGE ON SCHEMA " + quoteIdent(SchemaName) + " TO " + role,
		"GRANT SELECT ON ALL TABLES IN SCHEMA " + quoteIdent(SchemaName) + " TO " + role,
		// Tables the dataset generator adds later must be readable too, without
		// a second grant pass.
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + quoteIdent(SchemaName) + " GRANT SELECT ON TABLES TO " + role,
	}
	for _, stmt := range append(defaults, grants...) {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply learner grant %q: %w", stmt, err)
		}
	}
	return nil
}
