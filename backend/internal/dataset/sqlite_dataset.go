package dataset

import (
	_ "embed"
)

// The SQLite dialect's half of the commerce dataset. schema.sqlite.sql and
// seed.sqlite.sql are authored as the SQLite mirror of schema.sql/seed.sql —
// the same four relations at the same grain, with SQLite's own declared types
// (a type names an affinity, not a constraint) — and mapping.json is the
// checked manifest of every deliberate divergence. They are embedded here so
// the Go gateway reads one source of truth per dialect, exactly as it already
// does for the PostgreSQL pair in seed.go, instead of a second copy that could
// drift. scripts/dataset-map-audit.mjs checks the same files from the Node side.
//
//go:embed schema.sqlite.sql
var sqliteSchemaSQL string

//go:embed seed.sqlite.sql
var sqliteSeedSQL string

// SQLiteSchemaSQL returns the SQLite dialect DDL for the commerce dataset.
func SQLiteSchemaSQL() string { return sqliteSchemaSQL }

// SQLiteSeedSQL returns the committed SQLite dialect seed for the commerce
// dataset.
func SQLiteSeedSQL() string { return sqliteSeedSQL }

// There is deliberately no statement splitter in this package.
//
// The splitter exists for drivers that cannot run a script in one call —
// mattn/go-sqlite3 refuses more than one statement per Exec, which is why the
// usual advice is to split first. modernc.org/sqlite does not have that
// limitation, and that is measured rather than assumed: ExecContext hands the
// whole remaining string to sqlite3_prepare_v2 and loops, so SQLite's own
// parser — not a substring search — decides where one statement ends and the
// next begins. The two properties that make a hand-written splitter risky are
// therefore already handled by the engine: a semicolon inside a string literal,
// and a `--` that is only a comment outside a literal.
//
// Measured on modernc.org/sqlite v1.38.2 (go.sum pins it):
//
//   - a single Exec of an 8-statement script containing `--` comments, a
//     BEGIN;/COMMIT; pair and the literal 'hi; there -- not a comment'
//     executed every statement, stored the literal byte for byte, and left the
//     table with the two rows the script inserted;
//   - the same holds for the real pair this package embeds: one Exec of
//     SQLiteSchemaSQL() followed by one Exec of SQLiteSeedSQL() builds all four
//     tables with 30 customers, 20 products, 80 orders and 160 order_items.
//
// TestSQLiteDatasetLoadsInOneExecPerScript pins that property, so a future
// driver version that changed it would fail the suite loudly instead of
// silently skipping statements.
