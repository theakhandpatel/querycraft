package envdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
)

// Challenge environments: one database per challenge, created as a physical
// copy of one sealed template database.
//
// A database is the unit PostgreSQL can copy in a single statement, the unit a
// role can be granted or refused access to, and the unit `DROP ... WITH (FORCE)`
// can delete without touching a neighbour. A schema cannot be copied without
// re-running the whole seed, and a shared schema with row scoping is defeated by
// a learner typing `SELECT * FROM orders` with no filter at all.
//
// These are the create/seal/drop/reap primitives the manager runs against its
// own cluster (manager.go); the fixture loaders that fill a template live in
// dataset (template.go). The measurements behind the decision live in
// dataset/template_test.go and docs/adr/0002-challenge-environments.md.

// baseTemplateDatabase is the empty database a fresh template is built from.
// template0 is the only database PostgreSQL guarantees nobody is connected
// to, which is exactly the condition it imposes on a database used as a copy
// source.
const baseTemplateDatabase = "template0"

// execDDL runs database-level DDL over the simple query protocol. CREATE and
// DROP DATABASE are utility statements that cannot be prepared, and they must not
// run inside a transaction block.
func execDDL(ctx context.Context, conn *pgx.Conn, sql string) error {
	_, err := conn.PgConn().Exec(ctx, sql).ReadAll()
	return err
}

// CreateDatabase creates an empty database from template0. Used once per cluster
// to build the template database itself.
func CreateDatabase(ctx context.Context, admin *pgx.Conn, name string) error {
	if err := execDDL(ctx, admin, "CREATE DATABASE "+dataset.QuoteIdent(name)+" TEMPLATE "+baseTemplateDatabase); err != nil {
		return fmt.Errorf("create database %q: %w", name, err)
	}
	return nil
}

// CreateDatabaseFromTemplate copies a sealed template database into a new
// environment database. PostgreSQL refuses this while any other session is
// attached to the source, so the template must have been sealed first (see
// SealTemplate).
func CreateDatabaseFromTemplate(ctx context.Context, admin *pgx.Conn, name, template string) error {
	if err := execDDL(ctx, admin, "CREATE DATABASE "+dataset.QuoteIdent(name)+" TEMPLATE "+dataset.QuoteIdent(template)); err != nil {
		return fmt.Errorf("create database %q from template %q: %w", name, template, err)
	}
	return nil
}

// SealTemplate makes a database usable as a copy source *and* unusable as a
// session database. IS_TEMPLATE marks the intent (and lets a non-superuser copy
// it); ALLOW_CONNECTIONS false is what actually satisfies PostgreSQL's rule that
// no other session may be attached to the source of a copy.
func SealTemplate(ctx context.Context, admin *pgx.Conn, template string) error {
	ddl := "ALTER DATABASE " + dataset.QuoteIdent(template) + " IS_TEMPLATE true ALLOW_CONNECTIONS false"
	if err := execDDL(ctx, admin, ddl); err != nil {
		return fmt.Errorf("seal template %q: %w", template, err)
	}
	return nil
}

// DropDatabase removes an environment. FORCE terminates any session still
// attached, so one abandoned learner connection cannot pin a database forever.
func DropDatabase(ctx context.Context, admin *pgx.Conn, name string) error {
	if err := execDDL(ctx, admin, "DROP DATABASE IF EXISTS "+dataset.QuoteIdent(name)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop database %q: %w", name, err)
	}
	return nil
}

// ResetEnvironment deletes an environment and rebuilds it from the template.
// Reset is not a truncate-and-reseed: the copy is the reseed, so a corrupted or
// half-written environment is replaced rather than repaired.
func ResetEnvironment(ctx context.Context, admin *pgx.Conn, name, template string) error {
	if err := DropDatabase(ctx, admin, name); err != nil {
		return err
	}
	return CreateDatabaseFromTemplate(ctx, admin, name, template)
}

// ListEnvironmentDatabases returns every environment database on the server.
func ListEnvironmentDatabases(ctx context.Context, admin *pgx.Conn) ([]string, error) {
	rows, err := admin.Query(ctx,
		"SELECT datname FROM pg_database WHERE datname LIKE $1 ORDER BY datname",
		dataset.EnvironmentDatabasePrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("list environment databases: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan environment database: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// ReapEnvironmentDatabases drops every environment database not named in keep.
// The manager calls this on boot with the environments the store still
// references, so a crash that happened between "create" and "record" leaks one
// database until the next boot instead of leaking it forever.
func ReapEnvironmentDatabases(ctx context.Context, admin *pgx.Conn, keep []string) ([]string, error) {
	names, err := ListEnvironmentDatabases(ctx, admin)
	if err != nil {
		return nil, err
	}
	kept := make(map[string]bool, len(keep))
	for _, name := range keep {
		kept[name] = true
	}
	var dropped []string
	for _, name := range names {
		if kept[name] {
			continue
		}
		if err := DropDatabase(ctx, admin, name); err != nil {
			return dropped, err
		}
		dropped = append(dropped, name)
	}
	return dropped, nil
}
