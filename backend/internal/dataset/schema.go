package dataset

import (
	"strconv"
	"strings"
)

// quoteIdent renders a SQL identifier as a double-quoted string, doubling any
// embedded double quote. Nothing in this package comes from a request today,
// but every identifier is quoted anyway so the generated DDL/seed is provably
// injection-free even if a generator ever takes a caller-supplied name.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteLiteral renders a SQL string literal, doubling embedded single quotes.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// SchemaName is the PostgreSQL schema that holds the learner-visible commerce
// tables. Application metadata (progress, events) lives in a separate `app`
// schema so a restricted role can be granted SELECT on `dataset` only. See
// docs/database-layout.md.
const SchemaName = "dataset"

// schemaColumn is one column of a generated learner table.
type schemaColumn struct {
	name       string
	typ        string
	constraint string
}

// schemaTable is a generated learner table.
type schemaTable struct {
	name    string
	columns []schemaColumn
}

// schemaIndex is one generated index on a learner table.
type schemaIndex struct {
	name   string
	table  string
	column string
}

// commerceTables is the DDL spec for the learner-visible `dataset` schema. It is
// the single source of truth for schema.sql; regenerate with `make dataset`.
// Foreign keys name their target schema explicitly so the script applies with
// any search_path.
var commerceTables = []schemaTable{
	{name: "customers", columns: []schemaColumn{
		{"id", "integer", "PRIMARY KEY"},
		{"name", "text", "NOT NULL"},
		{"email", "text", ""},
		{"city", "text", ""},
		{"signup_date", "date", "NOT NULL"},
	}},
	{name: "products", columns: []schemaColumn{
		{"id", "integer", "PRIMARY KEY"},
		{"name", "text", "NOT NULL"},
		{"category", "text", "NOT NULL"},
		{"price", "numeric(10,2)", "NOT NULL"},
		{"stock", "integer", "NOT NULL DEFAULT 0"},
		{"active", "boolean", "NOT NULL DEFAULT true"},
	}},
	{name: "orders", columns: []schemaColumn{
		{"id", "integer", "PRIMARY KEY"},
		{"customer_id", "integer", "NOT NULL REFERENCES " + quoteIdent(SchemaName) + "." + quoteIdent("customers") + "(" + quoteIdent("id") + ")"},
		{"ordered_at", "timestamp", "NOT NULL"},
		{"status", "text", "NOT NULL"},
	}},
	{name: "order_items", columns: []schemaColumn{
		{"id", "integer", "PRIMARY KEY"},
		{"order_id", "integer", "NOT NULL REFERENCES " + quoteIdent(SchemaName) + "." + quoteIdent("orders") + "(" + quoteIdent("id") + ")"},
		{"product_id", "integer", "NOT NULL REFERENCES " + quoteIdent(SchemaName) + "." + quoteIdent("products") + "(" + quoteIdent("id") + ")"},
		{"quantity", "integer", "NOT NULL CHECK (quantity > 0)"},
		{"unit_price", "numeric(10,2)", "NOT NULL"},
	}},
}

// metaTable is created last and holds the dataset_version row.
var metaTable = schemaTable{name: "dataset_meta", columns: []schemaColumn{
	{"dataset", "text", "PRIMARY KEY"},
	{"version", "integer", "NOT NULL"},
}}

var commerceIndexes = []schemaIndex{
	{"orders_customer_id_idx", "orders", "customer_id"},
	{"orders_status_idx", "orders", "status"},
	{"order_items_order_id_idx", "order_items", "order_id"},
	{"order_items_product_id_idx", "order_items", "product_id"},
}

// RenderSchemaSQL builds schema.sql from the generator. The committed schema.sql
// is kept byte-identical to this by TestSchemaSQLUpToDate; regenerate it with
// `make dataset`.
func RenderSchemaSQL(ds Dataset) string {
	var b strings.Builder
	b.WriteString("-- Querycraft commerce dataset: schema.\n")
	b.WriteString("-- Generated dataset_version: " + strconv.Itoa(ds.Version) + ". Schema and seed share one dataset_version.\n")
	b.WriteString("-- Rebuild with: make dataset (regenerates schema.sql and seed.sql).\n\n")
	b.WriteString("BEGIN;\n\n")
	b.WriteString("CREATE SCHEMA IF NOT EXISTS " + quoteIdent(SchemaName) + ";\n\n")

	// Drop children before parents so re-running the script is always safe.
	for i := len(commerceTables) - 1; i >= 0; i-- {
		b.WriteString("DROP TABLE IF EXISTS " + quoteIdent(SchemaName) + "." + quoteIdent(commerceTables[i].name) + ";\n")
	}
	b.WriteString("DROP TABLE IF EXISTS " + quoteIdent(SchemaName) + "." + quoteIdent(metaTable.name) + ";\n\n")

	for _, t := range commerceTables {
		writeCreateTable(&b, t)
		b.WriteString("\n")
	}

	for _, idx := range commerceIndexes {
		// The index name is unqualified on purpose: PostgreSQL creates it in
		// the schema of its parent table and rejects `schema.index`.
		b.WriteString("CREATE INDEX " + quoteIdent(idx.name) +
			" ON " + quoteIdent(SchemaName) + "." + quoteIdent(idx.table) + " (" + quoteIdent(idx.column) + ");\n")
	}
	b.WriteString("\n")

	writeCreateTable(&b, metaTable)
	b.WriteString("\n")

	b.WriteString("INSERT INTO " + quoteIdent(SchemaName) + "." + quoteIdent(metaTable.name) +
		" (" + quoteIdent("dataset") + ", " + quoteIdent("version") + ") VALUES (" +
		quoteLiteral(ds.ID) + ", " + strconv.Itoa(ds.Version) + ");\n\n")
	b.WriteString("COMMIT;\n")
	return b.String()
}

func writeCreateTable(b *strings.Builder, t schemaTable) {
	names := make([]string, len(t.columns))
	nameWidth, typeWidth := 0, 0
	for i, c := range t.columns {
		names[i] = quoteIdent(c.name)
		if len(names[i]) > nameWidth {
			nameWidth = len(names[i])
		}
		if len(c.typ) > typeWidth {
			typeWidth = len(c.typ)
		}
	}
	b.WriteString("CREATE TABLE " + quoteIdent(SchemaName) + "." + quoteIdent(t.name) + " (\n")
	for i, c := range t.columns {
		line := names[i] + strings.Repeat(" ", nameWidth-len(names[i])+1) + c.typ
		if pad := typeWidth - len(c.typ); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		if c.constraint != "" {
			line += " " + c.constraint
		}
		line = strings.TrimRight(line, " ")
		if i < len(t.columns)-1 {
			line += ","
		}
		b.WriteString("    " + line + "\n")
	}
	b.WriteString(");\n")
}
