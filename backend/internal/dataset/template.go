package dataset

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Challenge environments: the dataset side of one database per challenge.
//
// A template database is a complete, learner-ready copy of the dataset; envdb
// creates, seals, copies and drops the databases themselves. This file owns the
// fixture loaders that fill a template, because the fixture is dataset content:
// which rows the learner sees and which grants they carry.
//
// Design and the measurements behind it: docs/adr/0002-challenge-environments.md.

const (
	// EnvironmentDatabasePrefix names every per-challenge environment database,
	// so a reaper can tell its own databases from the gateway's and PostgreSQL's.
	EnvironmentDatabasePrefix = "qc_env_"

	// TemplateDatabaseName is the database every environment is copied from. It
	// is never a learner's session database: it exists only to be copied.
	TemplateDatabaseName = "qc_template"
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
//
// The gateway's own database uses the narrower grant set instead: it is built by
// EnsureLearnerRole alone, which grants SELECT and nothing else (roles.go). The
// two are deliberately different grant sets, and
// dataset.TestWritableTemplateGrantsLearnerDDLAndDML pins the difference by
// building both and probing every withheld verb.
func LoadWritableIntoTemplate(ctx context.Context, admin *pgx.Conn, template, learnerPassword string) error {
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
	if err := grantWritableDataset(ctx, conn); err != nil {
		return fmt.Errorf("grant writable access in %q: %w", template, err)
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
	role := QuoteIdent(LearnerRole)
	schema := QuoteIdent(SchemaName)
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
