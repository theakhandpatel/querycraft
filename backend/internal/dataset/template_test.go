package dataset_test

// The template-environment spike that lived here measured what it costs to give
// every generated challenge its own PostgreSQL database. It was a measurement,
// not a regression test, and its finding is recorded in
// docs/adr/0002-challenge-environments.md; the test is gone. The two helpers
// below stay, because writable_test.go still builds the read-only grant set and
// connects as the restricted role with them.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
)

// connectAsLearner opens an environment connection as the restricted role,
// which is the only way learner SQL is ever executed. The writable test builds
// its grant probes through it so a probe reads what a learner would see.
//
// TEST-ONLY: no production path connects as the learner directly — the gateway
// reaches learner connections through envdb.Manager. It stays here, in the test
// package, because TestWritableTemplateGrantsLearnerDDLAndDML asserts the
// withheld verbs (TRUNCATE, DROP, CREATE SCHEMA) and the read-only refusals over
// exactly this connection.
func connectAsLearner(ctx context.Context, admin *pgx.Conn, database, password string) (*pgx.Conn, error) {
	cfg := admin.Config().Copy()
	cfg.Database = database
	cfg.User = dataset.LearnerRole
	cfg.Password = password
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to %q as %s: %w", database, dataset.LearnerRole, err)
	}
	return conn, nil
}

// loadReadOnlyIntoTemplate builds the shared, read-only dataset into an empty
// template: the schema, the seed, and EnsureLearnerRole's SELECT-only grants.
// Grants are per-database objects, so they must exist *inside* the template for
// a copy to carry them.
//
// TEST-ONLY: production templates are built by LoadWritableIntoTemplate
// (challenge) and LoadPerfIntoTemplate (plan). This is the read-only grant set
// the gateway's own database uses, and
// TestWritableTemplateGrantsLearnerDDLAndDML builds a copy from it to assert
// that copy refuses every write the writable one allows.
func loadReadOnlyIntoTemplate(ctx context.Context, admin *pgx.Conn, template, learnerPassword string) error {
	conn, err := dataset.ConnectToDatabase(ctx, admin, template)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	// Schema and seed are multi-statement scripts with their own BEGIN/COMMIT,
	// so they must run over the simple query protocol; the order is load-bearing.
	for _, step := range []struct{ name, script string }{
		{"schema", dataset.SchemaSQL()},
		{"seed", dataset.SeedSQL()},
	} {
		if _, err := conn.PgConn().Exec(ctx, step.script).ReadAll(); err != nil {
			return fmt.Errorf("apply dataset %s to %q: %w", step.name, template, err)
		}
	}
	if err := dataset.EnsureLearnerRole(ctx, conn, learnerPassword); err != nil {
		return fmt.Errorf("prepare learner role in %q: %w", template, err)
	}
	return nil
}
