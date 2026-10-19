package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

// TestMain gives the package one shared PostgreSQL cluster; every test that
// asks pgtest.Shared for a server gets its own database on it.
func TestMain(m *testing.M) { pgtest.Main(m) }

// TestValueEncodingIsPostgresText proves the gateway returns PostgreSQL's own
// text for the types where a decoded Go value cannot carry the engine's answer:
// `numeric` loses its scale through a float, and a naive `timestamp` picks up an
// invented offset when it is decoded and re-rendered.
//
// This is a parity property, not a formatting preference. The browser adapter
// (web/src/api/pglite.ts) returns the engine's text for these columns, so a
// learner who runs the same query against PGlite and against the gateway must
// see the same characters. The two failures this test locks down were both
// real: `118.00` arrived as `118`, and `2024-01-15 10:30:00` arrived as
// `2024-01-15T10:30:00Z` (and, under a non-UTC host timezone, as `05:00:00`).
//
// It runs against a real server because the property is about what the server
// prints; a fake cannot have an opinion about the text form.
func TestValueEncodingIsPostgresText(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ex, err := NewPgxExecutor(ctx, srv.LearnerDSN(t, srv.Database))
	if err != nil {
		t.Fatalf("open the learner executor: %v", err)
	}
	defer func() { _ = ex.Close() }()

	const sql = `SELECT
		date '2024-01-15'                       AS d,
		time '10:30:00'                         AS t,
		timestamp '2024-01-15 10:30:00'         AS ts,
		timestamptz '2024-01-15 10:30:00+05:30' AS tstz,
		interval '1 day 2 hours'                AS iv,
		numeric '118.00'                        AS num,
		118.00::numeric(10,2)                   AS price,
		42                                      AS i4,
		96.4::float8                            AS f8,
		true                                    AS b,
		NULL::numeric                           AS missing`

	res := Execute(ctx, ex, sql, Limits{})
	if res.Status != "succeeded" || res.Error != nil {
		t.Fatalf("status = %q error = %v, want a successful result", res.Status, res.Error)
	}
	if res.RowCount != 1 || len(res.Rows) != 1 {
		t.Fatalf("row count = %d, want 1", res.RowCount)
	}
	row := res.Rows[0]
	if len(row) != 11 {
		t.Fatalf("column count = %d, want 11", len(row))
	}

	// text asserts the value is the engine's text and returns it.
	text := func(i int, column string) string {
		t.Helper()
		s, ok := row[i].(string)
		if !ok {
			t.Fatalf("%s is %T (%v), want the engine's text", column, row[i], row[i])
		}
		return s
	}

	// The timezone-independent types have one right answer each. These are the
	// exact characters psql prints.
	for _, tc := range []struct {
		i      int
		column string
		want   string
	}{
		{0, "date", "2024-01-15"},
		{1, "time", "10:30:00"},
		{2, "timestamp", "2024-01-15 10:30:00"},
		{4, "interval", "1 day 02:00:00"},
		{5, "numeric", "118.00"},
		{6, "numeric(10,2)", "118.00"},
	} {
		if got := text(tc.i, tc.column); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.column, got, tc.want)
		}
	}

	// timestamptz is rendered in the session's timezone, so the ground truth is
	// whatever the server prints for it — read over a separate connection, not
	// through the code under test.
	admin, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	var wantTstz string
	if err := admin.QueryRow(ctx, "SELECT (timestamptz '2024-01-15 10:30:00+05:30')::text").Scan(&wantTstz); err != nil {
		t.Fatalf("read the server's timestamptz text: %v", err)
	}
	if got := text(3, "timestamptz"); got != wantTstz {
		t.Errorf("timestamptz = %q, want the server's own text %q", got, wantTstz)
	}
	if strings.ContainsAny(wantTstz, "TZ") {
		t.Fatalf("the server's text %q looks like RFC3339; the test is not measuring what it thinks", wantTstz)
	}

	// The types whose contract value is a JSON scalar stay JSON scalars: the
	// browser adapter returns numbers and booleans for these, so wrapping them
	// in strings would be the same divergence in the other direction.
	if got, ok := row[7].(int32); !ok || got != 42 {
		t.Errorf("int4 = %#v, want int32(42)", row[7])
	}
	if got, ok := row[8].(float64); !ok || got != 96.4 {
		t.Errorf("float8 = %#v, want float64(96.4)", row[8])
	}
	if got, ok := row[9].(bool); !ok || !got {
		t.Errorf("bool = %#v, want true", row[9])
	}
	if row[10] != nil {
		t.Errorf("NULL numeric = %#v, want nil", row[10])
	}

	// Column type names are shared with the browser adapter too: they are
	// PostgreSQL's own typnames, and docs/technical-design.md §4.2 uses `int8`
	// in its example, so this is the canonical spelling.
	for _, tc := range []struct {
		i      int
		column string
		want   string
	}{
		{0, "d", "date"},
		{2, "ts", "timestamp"},
		{3, "tstz", "timestamptz"},
		{4, "iv", "interval"},
		{5, "num", "numeric"},
		{7, "i4", "int4"},
		{8, "f8", "float8"},
		{9, "b", "bool"},
	} {
		if got := res.Columns[tc.i].Type; got != tc.want {
			t.Errorf("column %s type = %q, want %q", tc.column, got, tc.want)
		}
	}

	// Finally, the bug was visible in the bytes the learner receives, so assert
	// on the serialized body: a scale-preserving string, not a JSON number.
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal the result: %v", err)
	}
	if !strings.Contains(string(body), `"118.00"`) {
		t.Errorf("serialized result does not carry the numeric as text: %s", body)
	}
	if strings.Contains(string(body), `"2024-01-15T`) {
		t.Errorf("serialized result carries an RFC3339 timestamp instead of the engine's text: %s", body)
	}
}
