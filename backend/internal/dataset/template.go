package dataset

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// D11 challenge environments: one database per challenge, created as a physical
// copy of one sealed template database.
//
// A database is the unit PostgreSQL can copy in a single statement, the unit a
// role can be granted or refused access to, and the unit `DROP ... WITH (FORCE)`
// can delete without touching a neighbour. A schema cannot be copied without
// re-running the whole seed, and a shared schema with row scoping is defeated by
// a learner typing `SELECT * FROM orders` with no filter at all.
//
// The helpers here are the create/seal/drop/reap primitives M4 needs; the
// measurements behind the decision live in template_test.go and
// docs/adr/0002-challenge-environments.md.

const (
	// EnvironmentDatabasePrefix names every per-challenge environment database,
	// so a reaper can tell its own databases from the gateway's and PostgreSQL's.
	EnvironmentDatabasePrefix = "qc_env_"

	// TemplateDatabaseName is the database every environment is copied from. It
	// is never a learner's session database: it exists only to be copied.
	TemplateDatabaseName = "qc_template"

	// baseTemplateDatabase is the empty database a fresh template is built from.
	// template0 is the only database PostgreSQL guarantees nobody is connected
	// to, which is exactly the condition it imposes on a database used as a copy
	// source.
	baseTemplateDatabase = "template0"
)

// databaseNameMax is PostgreSQL's identifier limit (NAMEDATALEN - 1). A name over
// it is silently truncated by the server, which would make two challenge ids
// collide on one database, so the helper truncates deliberately instead.
const databaseNameMax = 63

var nonNameChars = regexp.MustCompile(`[^a-z0-9_]+`)

// EnvironmentDatabaseName turns a challenge id into a database name. The result
// is lower-case, contains only [a-z0-9_], always carries the environment prefix,
// and never exceeds PostgreSQL's identifier limit.
func EnvironmentDatabaseName(challengeID string) string {
	name := nonNameChars.ReplaceAllString(strings.ToLower(challengeID), "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "challenge"
	}
	full := EnvironmentDatabasePrefix + name
	if len(full) > databaseNameMax {
		full = full[:databaseNameMax]
	}
	return full
}

// ConnectToDatabase opens a second connection to the same server as admin, but
// pointed at another database. Used for the template (which the admin connection
// must not sit on while it is being copied) and for environments.
func ConnectToDatabase(ctx context.Context, admin *pgx.Conn, database string) (*pgx.Conn, error) {
	cfg := admin.Config().Copy()
	cfg.Database = database
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database %q: %w", database, err)
	}
	return conn, nil
}

// ConnectAsLearner opens an environment connection as the restricted role, which
// is the only way learner SQL is ever executed. It exists so a spike or an
// integration test can prove the grants travelled with the template.
func ConnectAsLearner(ctx context.Context, admin *pgx.Conn, database, password string) (*pgx.Conn, error) {
	cfg := admin.Config().Copy()
	cfg.Database = database
	cfg.User = LearnerRole
	cfg.Password = password
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to %q as %s: %w", database, LearnerRole, err)
	}
	return conn, nil
}

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
	if err := execDDL(ctx, admin, "CREATE DATABASE "+quoteIdent(name)+" TEMPLATE "+baseTemplateDatabase); err != nil {
		return fmt.Errorf("create database %q: %w", name, err)
	}
	return nil
}

// CreateDatabaseFromTemplate copies a sealed template database into a new
// environment database. PostgreSQL refuses this while any other session is
// attached to the source, so the template must have been sealed first (see
// SealTemplate).
func CreateDatabaseFromTemplate(ctx context.Context, admin *pgx.Conn, name, template string) error {
	if err := execDDL(ctx, admin, "CREATE DATABASE "+quoteIdent(name)+" TEMPLATE "+quoteIdent(template)); err != nil {
		return fmt.Errorf("create database %q from template %q: %w", name, template, err)
	}
	return nil
}

// LoadIntoTemplate connects to the template database and makes it a complete
// learner-ready copy of the dataset: schema, seed, and the restricted role's
// read-only grants. Grants are per-database objects, so they must exist *inside*
// the template for a copy to carry them; a copy of an ungranted database is an
// environment no learner can query.
//
// This is the shared, read-only dataset: every learner sees the same rows and
// none of them can change them. A challenge environment needs the learner to
// write, and uses LoadWritableIntoTemplate instead. The two are deliberately
// different grant sets on purpose — see roles.go for the gateway's database.
func LoadIntoTemplate(ctx context.Context, admin *pgx.Conn, template, learnerPassword string) error {
	return loadIntoTemplate(ctx, admin, template, learnerPassword, false)
}

// LoadWritableIntoTemplate builds a template whose learner role can also write:
// it is copied into a per-account challenge environment where the course teaches
// INSERT/UPDATE/DELETE, DDL and transactions. Without this a learner could only
// ever run SELECT, which is not a course that teaches SQL.
//
// The extra grants are exactly the DML verbs plus CREATE on the dataset schema,
// and sequence usage for the generated-key case. TRUNCATE, REFERENCES, TRIGGER
// and ownership are withheld: they would let one statement take a whole table
// away from the environment rather than teach anything, and the seeded tables
// stay owned by the admin role so a learner still cannot drop them.
//
// `public` is never touched, and no schema other than `dataset` is created: the
// writable scope is the dataset and nothing else.
func LoadWritableIntoTemplate(ctx context.Context, admin *pgx.Conn, template, learnerPassword string) error {
	return loadIntoTemplate(ctx, admin, template, learnerPassword, true)
}

func loadIntoTemplate(ctx context.Context, admin *pgx.Conn, template, learnerPassword string, writable bool) error {
	conn, err := ConnectToDatabase(ctx, admin, template)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	// Schema and seed are multi-statement scripts with their own BEGIN/COMMIT,
	// so they must run over the simple query protocol; the order is load-bearing.
	for _, step := range []struct{ name, script string }{
		{"schema", SchemaSQL()},
		{"seed", SeedSQL()},
	} {
		if _, err := conn.PgConn().Exec(ctx, step.script).ReadAll(); err != nil {
			return fmt.Errorf("apply dataset %s to %q: %w", step.name, template, err)
		}
	}
	if err := EnsureLearnerRole(ctx, conn, learnerPassword); err != nil {
		return fmt.Errorf("prepare learner role in %q: %w", template, err)
	}
	if writable {
		if err := grantWritableDataset(ctx, conn); err != nil {
			return fmt.Errorf("grant writable access in %q: %w", template, err)
		}
	}
	return nil
}

// grantWritableDataset widens the learner role's privileges inside one database
// to the set a challenge environment needs. It runs after EnsureLearnerRole,
// which has already created the role and granted USAGE + SELECT; this adds the
// write half and is idempotent, so it can run on every template build.
//
// ALTER DEFAULT PRIVILEGES is stated without FOR ROLE, so it applies to objects
// created by the role running it (the admin), which is the same object-owner
// rule EnsureLearnerRole relies on.
func grantWritableDataset(ctx context.Context, conn *pgx.Conn) error {
	role := quoteIdent(LearnerRole)
	schema := quoteIdent(SchemaName)
	for _, stmt := range []string{
		"GRANT USAGE, CREATE ON SCHEMA " + schema + " TO " + role,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + schema + " TO " + role,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + schema + " GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + role,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA " + schema + " TO " + role,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + schema + " GRANT USAGE, SELECT ON SEQUENCES TO " + role,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply writable grant %q: %w", stmt, err)
		}
	}
	return nil
}

// SealTemplate makes a database usable as a copy source *and* unusable as a
// session database. IS_TEMPLATE marks the intent (and lets a non-superuser copy
// it); ALLOW_CONNECTIONS false is what actually satisfies PostgreSQL's rule that
// no other session may be attached to the source of a copy.
func SealTemplate(ctx context.Context, admin *pgx.Conn, template string) error {
	ddl := "ALTER DATABASE " + quoteIdent(template) + " IS_TEMPLATE true ALLOW_CONNECTIONS false"
	if err := execDDL(ctx, admin, ddl); err != nil {
		return fmt.Errorf("seal template %q: %w", template, err)
	}
	return nil
}

// DropDatabase removes an environment. FORCE terminates any session still
// attached, so one abandoned learner connection cannot pin a database forever.
func DropDatabase(ctx context.Context, admin *pgx.Conn, name string) error {
	if err := execDDL(ctx, admin, "DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)"); err != nil {
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
		EnvironmentDatabasePrefix+"%")
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
// M4 calls this on boot with the environments the store still references, so a
// crash that happened between "create" and "record" leaks one database until the
// next boot instead of leaking it forever.
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

// EnvironmentDatabaseSize reports the bytes a database occupies on disk, which
// is what makes a leaked environment visible: an orphan costs disk forever and
// nothing on the boot path notices it.
func EnvironmentDatabaseSize(ctx context.Context, admin *pgx.Conn, name string) (int64, error) {
	var size int64
	if err := admin.QueryRow(ctx, "SELECT pg_database_size($1)", name).Scan(&size); err != nil {
		return 0, fmt.Errorf("size of database %q: %w", name, err)
	}
	return size, nil
}

// ConnectionFacts are the server's connection-slot budget, which is the hard
// ceiling on how many environments can be active at once.
type ConnectionFacts struct {
	MaxConnections       int
	SuperuserReserved    int
	LearnableConnections int // what a non-superuser role may hold at once
}

// ServerConnectionFacts reads max_connections and the reserved superuser slots.
func ServerConnectionFacts(ctx context.Context, admin *pgx.Conn) (ConnectionFacts, error) {
	var facts ConnectionFacts
	if err := admin.QueryRow(ctx, "SELECT current_setting('max_connections')::int").Scan(&facts.MaxConnections); err != nil {
		return facts, fmt.Errorf("read max_connections: %w", err)
	}
	if err := admin.QueryRow(ctx, "SELECT current_setting('superuser_reserved_connections')::int").Scan(&facts.SuperuserReserved); err != nil {
		return facts, fmt.Errorf("read superuser_reserved_connections: %w", err)
	}
	facts.LearnableConnections = facts.MaxConnections - facts.SuperuserReserved
	if facts.LearnableConnections < 0 {
		facts.LearnableConnections = 0
	}
	return facts, nil
}
