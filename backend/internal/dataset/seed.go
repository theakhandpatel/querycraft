package dataset

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed schema.sql
var schemaSQL string

//go:embed seed.sql
var seedSQL string

// SchemaSQL returns the DDL for the commerce dataset.
func SchemaSQL() string { return schemaSQL }

// SeedSQL returns the committed seed for the commerce dataset.
func SeedSQL() string { return seedSQL }

// RenderSeedSQL builds the seed SQL from the generator. The committed seed.sql is
// kept byte-identical to this by TestSeedSQLUpToDate; regenerate it with
// `make dataset` (or `go test ./internal/dataset -run TestSeedSQLUpToDate -update`).
//
// Inserts name the `dataset` schema explicitly so the seed applies under any
// search_path (the gateway seeds over a plain connection, PGlite over its
// default one).
func RenderSeedSQL(ds Dataset) string {
	var b strings.Builder
	b.WriteString("-- Querycraft commerce dataset: seed.\n")
	b.WriteString("-- Generated dataset_version: " + strconv.Itoa(ds.Version) + ". Do not edit by hand.\n")
	b.WriteString("-- Regenerate with: make dataset\n\n")
	b.WriteString("BEGIN;\n\n")
	for _, t := range ds.Tables {
		b.WriteString("-- " + t.Name + " (" + strconv.Itoa(len(t.Rows)) + " rows)\n")
		b.WriteString("INSERT INTO " + quoteIdent(SchemaName) + "." + quoteIdent(t.Name) + " (" + columnList(t) + ") VALUES\n")
		for i, row := range t.Rows {
			b.WriteString("\t(")
			for j, v := range row {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(sqlLiteral(v))
			}
			b.WriteString(")")
			if i < len(t.Rows)-1 {
				b.WriteString(",")
			} else {
				b.WriteString(";")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("COMMIT;\n")
	return b.String()
}

func columnList(t TableSpec) string {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = quoteIdent(c.Name)
	}
	return strings.Join(names, ", ")
}

func sqlLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', 2, 64)
	}
	return fmt.Sprintf("%v", v)
}
