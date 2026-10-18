package dataset

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The performance fixture: the second, larger dataset a plan lesson runs
// against. It is the same four relations and the same grain as the shared
// commerce dataset, at a scale where the access path is a real decision, and it
// is generated from generate_series arithmetic so every machine gets the same
// rows (the exit gate: plans are reproducible on pinned fixtures).
//
// It is served the same way as the shared dataset — a sealed template copied
// into a per-account writable environment (ADR 0002) — never merged into the
// read-only dataset the rest of the lessons use, so adding it changes nothing
// for an existing exercise.

const (
	// PerfDatasetID names the perf dataset in dataset_meta and in the
	// POST /explain `dataset` field.
	PerfDatasetID = "perf"

	// PerfVersion is the perf dataset's version, shared by its schema and seed
	// (the file is generated as one script, so the two cannot drift).
	PerfVersion = 1

	// PerfTemplateDatabaseName is the sealed template every plan environment is
	// copied from. It sits beside TemplateDatabaseName, not over it.
	PerfTemplateDatabaseName = "qc_perf_template"

	// PerfScaleFactorOrders is the perf dataset's order count, which is the
	// number FactRowCount reports as the perf scale.
	PerfScaleFactorOrders = 10000
)

//go:embed perf.sql
var perfSQL string

// PerfSQL returns the schema + deterministic seed for the perf dataset.
func PerfSQL() string { return perfSQL }

// LoadPerfIntoTemplate builds the perf dataset into an empty template database
// and grants the learner role writable access.
//
// Writable, not read-only: the perf lessons ask the learner to CREATE INDEX and
// watch the plan change, which only an environment they own can do (ADR 0003).
// The grants are the same set LoadWritableIntoTemplate establishes for a
// challenge environment, so a plan environment is exactly as contained as a
// challenge environment and no more.
func LoadPerfIntoTemplate(ctx context.Context, admin *pgx.Conn, template, learnerPassword string) error {
	conn, err := ConnectToDatabase(ctx, admin, template)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	// perf.sql is a multi-statement script with its own BEGIN/COMMIT, so it
	// must run over the simple query protocol.
	if _, err := conn.PgConn().Exec(ctx, PerfSQL()).ReadAll(); err != nil {
		return fmt.Errorf("apply perf dataset to %q: %w", template, err)
	}
	if err := EnsureLearnerRole(ctx, conn, learnerPassword); err != nil {
		return fmt.Errorf("prepare learner role in %q: %w", template, err)
	}
	if err := grantWritableDataset(ctx, conn); err != nil {
		return fmt.Errorf("grant writable access in %q: %w", template, err)
	}
	if err := transferPerfTablesToLearner(ctx, conn); err != nil {
		return fmt.Errorf("hand the perf tables to the learner in %q: %w", template, err)
	}
	return nil
}

// perfRelation is one relation the perf fixture creates: the name perf.sql
// declares, the row count its seed generates, the columns it declares, and the
// columns the tables drawer would preview.
type perfRelation struct {
	name    string
	rows    int
	columns []ColumnSpec
	preview []string
}

// perfRelations is the one list of the perf fixture's relations, in seed order.
// transferPerfTablesToLearner walks the names to hand the tables to the learner
// role, and perfTablesResponse describes the same relations for the drawer, so
// the two cannot disagree about what the fixture contains.
//
// The columns are perf.sql's, which are not commerce's: perf `orders` carries
// the columns a real order carries (carrier, tracking code, two addresses, a
// handling note) so the relation is wide enough for the access-path lessons to
// be about a real decision.
var perfRelations = []perfRelation{
	{
		name: "customers",
		rows: 2000,
		columns: []ColumnSpec{
			{"id", "integer", "PK"},
			{"name", "text", ""},
			{"email", "text", "nullable"},
			{"city", "text", "nullable"},
			{"signup_date", "date", ""},
		},
		preview: []string{"id", "name", "email", "city"},
	},
	{
		name: "products",
		rows: 500,
		columns: []ColumnSpec{
			{"id", "integer", "PK"},
			{"name", "text", ""},
			{"category", "text", ""},
			{"price", "numeric", ""},
			{"stock", "integer", ""},
			{"active", "boolean", ""},
		},
		preview: []string{"id", "name", "category", "price"},
	},
	{
		name: "orders",
		rows: PerfScaleFactorOrders,
		columns: []ColumnSpec{
			{"id", "integer", "PK"},
			{"customer_id", "integer", "FK"},
			{"ordered_at", "timestamp", ""},
			{"status", "text", ""},
			{"carrier", "text", ""},
			{"tracking_code", "text", ""},
			{"shipping_address", "text", ""},
			{"billing_address", "text", ""},
			{"notes", "text", ""},
		},
		preview: []string{"id", "customer_id", "ordered_at", "status"},
	},
	{
		name: "order_items",
		rows: 20000,
		columns: []ColumnSpec{
			{"id", "integer", "PK"},
			{"order_id", "integer", "FK"},
			{"product_id", "integer", "FK"},
			{"quantity", "integer", ""},
			{"unit_price", "numeric", ""},
		},
		preview: []string{"id", "order_id", "product_id", "quantity", "unit_price"},
	},
}

// transferPerfTablesToLearner makes the learner role the owner of the perf
// tables inside one template database.
//
// CREATE INDEX requires ownership of the table (there is no privilege that
// grants it), and "add the index that is missing, then watch the plan change" is
// the experiment the perf fixture exists for. Ownership of a per-account copy is
// safe: the environment is a throwaway copy with no neighbour, and Reset restores
// it from the sealed template. dataset_meta stays with the admin — it is
// gateway metadata, not a lesson object.
func transferPerfTablesToLearner(ctx context.Context, conn *pgx.Conn) error {
	for _, rel := range perfRelations {
		stmt := "ALTER TABLE " + QuoteIdent(SchemaName) + "." + QuoteIdent(rel.name) +
			" OWNER TO " + QuoteIdent(LearnerRole)
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply %q: %w", stmt, err)
		}
	}
	return nil
}
