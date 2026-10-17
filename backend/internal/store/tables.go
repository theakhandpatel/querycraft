package store

import (
	"fmt"
	"strings"
)

/* The metadata schema, in its final shape, described once for both backends.
 *
 * This is greenfield: there is no pre-accounts layout to migrate from. A
 * database created before this shape existed is simply reset (`make db-reset`),
 * which is why there is no ALTER-based upgrade path here. Progress and events
 * are keyed by the account that produced them.
 *
 * postgresDDL renders the tables into the app schema, which stays distinct from
 * the learner-visible dataset schema (see AppSchema). sqliteDDL renders the same
 * tables unqualified: JSON values are TEXT there, written by this package, and
 * timestamps are canonical UTC RFC3339Nano text.
 */

// ddlKind is a column's type, independent of the dialect.
type ddlKind int

const (
	kindText ddlKind = iota
	kindJSON
	kindInteger
	kindBoolean
	kindTimestamp
)

// postgresType names the PostgreSQL type for a kind.
func (k ddlKind) postgresType() string {
	switch k {
	case kindText:
		return "text"
	case kindJSON:
		return "jsonb"
	case kindInteger:
		return "integer"
	case kindBoolean:
		return "boolean"
	case kindTimestamp:
		return "timestamptz"
	}
	return ""
}

// sqliteType names the SQLite type for a kind. SQLite stores JSON and
// timestamps as TEXT, and booleans as INTEGER.
func (k ddlKind) sqliteType() string {
	switch k {
	case kindText, kindJSON, kindTimestamp:
		return "TEXT"
	case kindInteger, kindBoolean:
		return "INTEGER"
	}
	return ""
}

// ddlDefault is a column's default, independent of the dialect.
type ddlDefault int

const (
	noDefault ddlDefault = iota
	// literalDefault writes the same SQL literal in both dialects.
	literalDefault
	// nowDefault is the insert time: now() in PostgreSQL, the canonical UTC
	// timestamp text in SQLite.
	nowDefault
	// falseDefault is false in PostgreSQL and 0 in SQLite.
	falseDefault
)

// sqliteNow is how SQLite writes the timestamp that timestamp() produces.
// SQLite strftime has millisecond precision; appending three zeroes gives the
// same fixed-width microsecond UTC representation.
const sqliteNow = "(strftime('%Y-%m-%dT%H:%M:%f000Z','now'))"

// ddlColumn is one column of one table.
type ddlColumn struct {
	name     string
	kind     ddlKind
	notNull  bool
	primary  bool       // a single-column key, written inline
	def      ddlDefault // noDefault when the column has none
	literal  string     // the SQL literal when def is literalDefault
	refs     string     // the referenced key, e.g. "users(id)"; empty for none
	onDelete string     // the reference action, e.g. "CASCADE"; empty for none
}

// ddlIndex is one index. Both dialects write the same expression, so the
// renderers only differ in how they qualify the table.
type ddlIndex struct {
	name    string
	unique  bool
	columns string
}

// ddlTable is one table: its columns, the composite key when it has one, and
// its indexes.
type ddlTable struct {
	name    string
	columns []ddlColumn
	primary []string // the table-level key, when it spans several columns
	indexes []ddlIndex
}

// appTables is the one description of the metadata tables. Both backends
// render their DDL from it, so a column added here exists on both. The order
// matters: a reference needs its target table to exist first.
var appTables = []ddlTable{
	{
		// Accounts. Emails are compared and made unique in lower case.
		name: "users",
		columns: []ddlColumn{
			{name: "id", kind: kindText, primary: true},
			{name: "email", kind: kindText, notNull: true},
			{name: "name", kind: kindText, notNull: true},
			{name: "role", kind: kindText, notNull: true, def: literalDefault, literal: "'learner'"},
			{name: "password_hash", kind: kindText, notNull: true, def: literalDefault, literal: "''"},
			{name: "created_at", kind: kindTimestamp, notNull: true, def: nowDefault},
		},
		indexes: []ddlIndex{
			{name: "users_email_lower_key", unique: true, columns: "lower(email)"},
		},
	},
	{
		// Bearer-token sessions. A token resolves to its account until it
		// expires; deleting the account deletes its sessions.
		name: "sessions",
		columns: []ddlColumn{
			{name: "token", kind: kindText, primary: true},
			{name: "user_id", kind: kindText, notNull: true, refs: "users(id)", onDelete: "CASCADE"},
			{name: "created_at", kind: kindTimestamp, notNull: true, def: nowDefault},
			{name: "expires_at", kind: kindTimestamp, notNull: true},
		},
		indexes: []ddlIndex{
			{name: "sessions_user_key", columns: "user_id"},
		},
	},
	{
		// One progress snapshot per account and course. The snapshot is opaque
		// to this package; the learner's own app writes it.
		name: "progress",
		columns: []ddlColumn{
			{name: "user_id", kind: kindText, notNull: true, refs: "users(id)", onDelete: "CASCADE"},
			{name: "course", kind: kindText, notNull: true},
			{name: "snapshot", kind: kindJSON, notNull: true},
			{name: "updated_at", kind: kindTimestamp, notNull: true, def: nowDefault},
		},
		primary: []string{"user_id", "course"},
	},
	{
		// Audit events, scoped to the account that produced them.
		name: "events",
		columns: []ddlColumn{
			{name: "user_id", kind: kindText, notNull: true, refs: "users(id)", onDelete: "CASCADE"},
			{name: "id", kind: kindText, notNull: true},
			{name: "payload", kind: kindJSON, notNull: true},
			{name: "created_at", kind: kindTimestamp, notNull: true, def: nowDefault},
		},
		primary: []string{"user_id", "id"},
	},
	{
		/* Generated challenges (M4). The status column is 'active' only for a
		 * challenge that passed every pipeline stage; the outcome column is the
		 * evidence captured at activation, kept beside the artifact so the two
		 * cannot drift. There is no foreign key to app.users: a challenge
		 * generated before sign-in is still a valid artifact, and owner_id
		 * records who asked for it when there was one. */
		name: "challenges",
		columns: []ddlColumn{
			{name: "id", kind: kindText, primary: true},
			{name: "owner_id", kind: kindText, notNull: true, def: literalDefault, literal: "''"},
			{name: "status", kind: kindText, notNull: true, def: literalDefault, literal: "'active'"},
			{name: "artifact", kind: kindJSON, notNull: true},
			{name: "outcome", kind: kindJSON, notNull: true},
			{name: "created_at", kind: kindTimestamp, notNull: true, def: nowDefault},
		},
		indexes: []ddlIndex{
			{name: "challenges_created_key", columns: "created_at DESC, id DESC"},
		},
	},
	{
		/* Generated courses (M7), the multi-chapter counterpart of
		 * app.challenges. The plan is stored whole: the chapters, the markdown
		 * and every exercise's expected rows are one artifact, and the outcome
		 * that admitted it sits beside it so a later reader can see what was
		 * measured before the course existed. request is the generation request
		 * that produced the plan; it is kept because a course can pause after
		 * its outline and be continued later, and the chapter phase rebuilds
		 * its own prompt from the learner's own words. A row written before the
		 * request was kept carries none, which the resume path handles. */
		name: "courses",
		columns: []ddlColumn{
			{name: "id", kind: kindText, primary: true},
			{name: "owner_id", kind: kindText, notNull: true, def: literalDefault, literal: "''"},
			{name: "status", kind: kindText, notNull: true, def: literalDefault, literal: "'active'"},
			{name: "plan", kind: kindJSON, notNull: true},
			{name: "outcome", kind: kindJSON, notNull: true},
			{name: "request", kind: kindJSON},
			{name: "created_at", kind: kindTimestamp, notNull: true, def: nowDefault},
		},
		indexes: []ddlIndex{
			{name: "courses_created_key", columns: "created_at DESC, id DESC"},
		},
	},
	{
		/* Patch proposals (M4). approved_at is null until a learner approves,
		 * so a proposal can never be mistaken for something that was applied. */
		name: "patches",
		columns: []ddlColumn{
			{name: "id", kind: kindText, primary: true},
			{name: "challenge_id", kind: kindText, notNull: true, def: literalDefault, literal: "''"},
			{name: "proposal", kind: kindJSON, notNull: true},
			{name: "approved_at", kind: kindTimestamp},
			{name: "approved_by", kind: kindText, notNull: true, def: literalDefault, literal: "''"},
			{name: "created_at", kind: kindTimestamp, notNull: true, def: nowDefault},
		},
	},
	{
		/* Agent-run transcripts (the audit trail). One row per generation run:
		 * the conversation the model had, stored as one JSON array with the
		 * run's attribution beside it. The entries are bounded before they
		 * arrive here (schema.MaxTranscriptBytes / schema.MaxTranscriptEntries),
		 * so a runaway run cannot grow the row without limit, and the whole
		 * record is redacted at the seam, so a credential never reaches this
		 * table. owner_id records who asked for the run; there is no foreign
		 * key, for the same reason app.challenges has none: a run made before
		 * sign-in is still a real record. */
		name: "runs",
		columns: []ddlColumn{
			{name: "id", kind: kindText, primary: true},
			{name: "kind", kind: kindText, notNull: true, def: literalDefault, literal: "'challenge'"},
			{name: "owner_id", kind: kindText, notNull: true, def: literalDefault, literal: "''"},
			{name: "status", kind: kindText, notNull: true, def: literalDefault, literal: "'failed'"},
			{name: "entries", kind: kindJSON, notNull: true},
			{name: "entry_count", kind: kindInteger, notNull: true, def: literalDefault, literal: "0"},
			{name: "bytes", kind: kindInteger, notNull: true, def: literalDefault, literal: "0"},
			{name: "truncated", kind: kindBoolean, notNull: true, def: falseDefault},
			{name: "error", kind: kindJSON},
			{name: "started_at", kind: kindTimestamp, notNull: true, def: nowDefault},
			{name: "finished_at", kind: kindTimestamp, notNull: true, def: nowDefault},
		},
		indexes: []ddlIndex{
			{name: "runs_owner_started_key", columns: "owner_id, started_at DESC, id DESC"},
		},
	},
}

// postgresDDL renders the tables as the PostgreSQL script the gateway applies
// on connect. Statement order follows the table order, so a reference names a
// table that already exists.
func postgresDDL(tables []ddlTable) string {
	var b strings.Builder
	b.WriteString("CREATE SCHEMA IF NOT EXISTS " + AppSchema + ";\n")
	for _, t := range tables {
		// Align each type under the longest column name in its table.
		width := 0
		for _, c := range t.columns {
			if len(c.name) > width {
				width = len(c.name)
			}
		}
		b.WriteString("\nCREATE TABLE IF NOT EXISTS " + AppSchema + "." + t.name + " (\n")
		b.WriteString(tableBody(t, func(c ddlColumn) string {
			return fmt.Sprintf("%-*s %s", width, c.name,
				columnSQL(c, c.kind.postgresType(), c.postgresDefault(), c.refsFor(AppSchema)))
		}))
		b.WriteString(");\n")
		for _, ix := range t.indexes {
			b.WriteString(indexSQL(ix, AppSchema+"."+t.name) + "\n")
		}
	}
	return b.String()
}

// sqliteDDL renders the tables as the script the SQLite backend applies to the
// metadata file it opens or creates.
func sqliteDDL(tables []ddlTable) string {
	var b strings.Builder
	b.WriteString("PRAGMA foreign_keys = ON;\n")
	for _, t := range tables {
		b.WriteString("\nCREATE TABLE IF NOT EXISTS " + t.name + " (\n")
		b.WriteString(tableBody(t, func(c ddlColumn) string {
			return c.name + " " + columnSQL(c, c.kind.sqliteType(), c.sqliteDefault(), c.refsFor(""))
		}))
		b.WriteString(");\n")
		for _, ix := range t.indexes {
			b.WriteString(indexSQL(ix, t.name) + "\n")
		}
	}
	return b.String()
}

// additiveColumns returns the declared columns a store created before those
// columns were declared can gain with ALTER TABLE: everything nullable or
// carrying a default. A column that is neither would reject every existing
// row, so widening one is a decision, not a migration. Primary keys and
// foreign keys are excluded for the same reason: adding either to a table
// with rows is backfill work, not a column append.
func additiveColumns(t ddlTable) []ddlColumn {
	out := make([]ddlColumn, 0, len(t.columns))
	for _, c := range t.columns {
		if c.primary || c.refs != "" || (c.notNull && c.def == noDefault) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// postgresMigrations renders the additive column migrations the gateway
// applies after the schema script. CREATE TABLE IF NOT EXISTS is a no-op
// against a table that already exists, so a metadata store created before a
// column was declared keeps failing every query that selects it with
// "column does not exist". PostgreSQL answers the gap with ADD COLUMN IF
// NOT EXISTS; SQLite cannot, so its store inspects table_info instead (see
// migrateAdditiveColumns).
func postgresMigrations(tables []ddlTable) string {
	var b strings.Builder
	for _, t := range tables {
		for _, c := range additiveColumns(t) {
			stmt := fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN IF NOT EXISTS %s %s", AppSchema, t.name, c.name, c.kind.postgresType())
			if c.notNull {
				stmt += " NOT NULL"
			}
			if d := c.postgresDefault(); d != "" {
				stmt += " DEFAULT " + d
			}
			b.WriteString(stmt + ";\n")
		}
	}
	return b.String()
}

// tableBody renders the body of one table: each column on its own line, with
// the composite key last when there is one. column renders one column without
// the indentation.
func tableBody(t ddlTable, column func(ddlColumn) string) string {
	lines := make([]string, 0, len(t.columns)+1)
	for _, c := range t.columns {
		lines = append(lines, "    "+column(c))
	}
	if len(t.primary) > 1 {
		lines = append(lines, "    PRIMARY KEY ("+strings.Join(t.primary, ", ")+")")
	}
	return strings.Join(lines, ",\n") + "\n"
}

// columnSQL renders one column definition, once the dialect has supplied its
// type name, its default and its reference target.
func columnSQL(c ddlColumn, kind, dflt, refs string) string {
	out := kind
	if c.notNull {
		out += " NOT NULL"
	}
	if c.primary {
		out += " PRIMARY KEY"
	}
	if dflt != "" {
		out += " DEFAULT " + dflt
	}
	if refs != "" {
		out += " REFERENCES " + refs
		if c.onDelete != "" {
			out += " ON DELETE " + c.onDelete
		}
	}
	return out
}

// indexSQL renders one index. Both dialects write the same statement, so the
// caller passes the table name qualified or bare.
func indexSQL(ix ddlIndex, table string) string {
	unique := ""
	if ix.unique {
		unique = "UNIQUE "
	}
	return fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS %s ON %s (%s);", unique, ix.name, table, ix.columns)
}

// postgresDefault is the PostgreSQL default clause body.
func (c ddlColumn) postgresDefault() string {
	switch c.def {
	case literalDefault:
		return c.literal
	case nowDefault:
		return "now()"
	case falseDefault:
		return "false"
	}
	return ""
}

// sqliteDefault is the SQLite default clause body.
func (c ddlColumn) sqliteDefault() string {
	switch c.def {
	case literalDefault:
		return c.literal
	case nowDefault:
		return sqliteNow
	case falseDefault:
		return "0"
	}
	return ""
}

// refsFor qualifies the referenced key with a schema name. The SQLite backend
// passes an empty qualifier because its tables are unqualified.
func (c ddlColumn) refsFor(qualifier string) string {
	if c.refs == "" {
		return ""
	}
	if qualifier == "" {
		return c.refs
	}
	return qualifier + "." + c.refs
}
