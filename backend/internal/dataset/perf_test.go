package dataset_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

// TestMain gives the package one shared PostgreSQL cluster; every test that
// asks pgtest.Shared for a server gets its own database on it. The extra
// template and environment databases these tests build carry names no other
// test uses, so they cannot meet.
func TestMain(m *testing.M) { pgtest.Main(m) }

// TestPerfDatasetIsDeterministic loads the perf dataset into two independent
// databases and compares every byte of every row.
//
// This is the M5 exit gate's first clause ("plans are reproducible on pinned
// fixtures") at its root: a plan is only reproducible if the rows under it are,
// and a fixture that used random() would give a learner a different plan from
// the one the lesson was written against.
func TestPerfDatasetIsDeterministic(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	// The determinism claim is in the SQL, not only in the result: the seed must
	// not be drawn from the server's random generator at all. Comments are
	// stripped first so the header's own explanation cannot satisfy the check.
	var statements []string
	for _, line := range strings.Split(dataset.PerfSQL(), "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		statements = append(statements, line)
	}
	if strings.Contains(strings.ToLower(strings.Join(statements, "\n")), "random(") {
		t.Fatal("perf.sql uses random(); the fixture must be derived from generate_series arithmetic")
	}

	loads := map[string]*pgx.Conn{}
	for _, name := range []string{"qc_perf_a", "qc_perf_b"} {
		if err := envdb.CreateDatabase(ctx, admin, name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := dataset.LoadPerfIntoTemplate(ctx, admin, name, pgtest.Password); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		conn, err := dataset.ConnectToDatabase(ctx, admin, name)
		if err != nil {
			t.Fatalf("connect to %s: %v", name, err)
		}
		defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
		loads[name] = conn
	}

	// A whole-row digest ordered by primary key is the whole table, cheaply.
	for _, table := range []string{"customers", "products", "orders", "order_items"} {
		digest := func(conn *pgx.Conn) string {
			var out string
			// string_agg needs a text argument, so the whole row is cast rather
			// than expanded column by column, and the digest stays schema-agnostic.
			sql := "SELECT md5(string_agg(t::text, E'\\n' ORDER BY id)) FROM dataset." + table + " t"
			if err := conn.QueryRow(ctx, sql).Scan(&out); err != nil {
				t.Fatalf("digest %s: %v", table, err)
			}
			return out
		}
		a, b := digest(loads["qc_perf_a"]), digest(loads["qc_perf_b"])
		if a != b {
			t.Errorf("dataset.%s digests differ between two loads: %s != %s", table, a, b)
		}
	}

	// The fixture also reports itself, so the gateway can name the dataset the
	// rows came from.
	var id string
	var version int
	if err := loads["qc_perf_a"].QueryRow(ctx,
		"SELECT dataset, version FROM dataset.dataset_meta").Scan(&id, &version); err != nil {
		t.Fatalf("read dataset_meta: %v", err)
	}
	if id != dataset.PerfDatasetID || version != dataset.PerfVersion {
		t.Errorf("dataset_meta = (%q, %d), want (%q, %d)", id, version, dataset.PerfDatasetID, dataset.PerfVersion)
	}
}

// TestPerfDatasetCarriesTheTaughtFacts pins the numbers the `plans` course
// states to the rows the fixture actually generates.
//
// The perf fixture is the one dataset a lesson makes quantitative claims about:
// "90% of the table is shipped and 0.5% is refunded", "customer 7 has 184
// orders", "the same index is useless for one value and decisive for the
// other". Those claims are teaching material, so a change to the generator that
// quietly breaks one of them breaks the course. This test is the Go half of
// that gate; scripts/perf-content-audit.mjs is the other half, and it runs the
// authored SQL itself.
func TestPerfDatasetCarriesTheTaughtFacts(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	const db = "qc_perf_facts"
	if err := envdb.CreateDatabase(ctx, admin, db); err != nil {
		t.Fatalf("create %s: %v", db, err)
	}
	if err := dataset.LoadPerfIntoTemplate(ctx, admin, db, pgtest.Password); err != nil {
		t.Fatalf("load %s: %v", db, err)
	}
	conn, err := dataset.ConnectToDatabase(ctx, admin, db)
	if err != nil {
		t.Fatalf("connect to %s: %v", db, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	one := func(sql string) int {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}

	// The scale line every later estimate is a fraction of.
	for _, tc := range []struct {
		table string
		want  int
	}{
		{"customers", 2000}, {"products", 500}, {"orders", 10000}, {"order_items", 20000},
	} {
		if got := one("SELECT count(*) FROM dataset." + tc.table); got != tc.want {
			t.Errorf("dataset.%s holds %d rows, want %d", tc.table, got, tc.want)
		}
	}

	// The selectivity lesson: five distinct values, 90% / 0.5%.
	wantStatus := map[string]int{"shipped": 9000, "completed": 400, "cancelled": 300, "pending": 250, "refunded": 50}
	gotStatus := map[string]int{}
	rows, err := conn.Query(ctx, "SELECT status, count(*) FROM dataset.orders GROUP BY status")
	if err != nil {
		t.Fatalf("read status distribution: %v", err)
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			t.Fatalf("scan status row: %v", err)
		}
		gotStatus[status] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate status distribution: %v", err)
	}
	if len(gotStatus) != len(wantStatus) {
		t.Errorf("orders.status has %d distinct values, want %d (%v)", len(gotStatus), len(wantStatus), gotStatus)
	}
	for status, want := range wantStatus {
		if got := gotStatus[status]; got != want {
			t.Errorf("orders with status %q: %d, want %d", status, got, want)
		}
	}

	// The customer skew, and the worked example in modules 2 and 3.
	if got := one("SELECT count(*) FROM dataset.orders WHERE customer_id = 7"); got != 184 {
		t.Errorf("customer 7 owns %d orders, want 184", got)
	}
	if got := one("SELECT customer_id FROM dataset.orders GROUP BY customer_id ORDER BY count(*) DESC, customer_id LIMIT 1"); got != 1 {
		t.Errorf("the heaviest customer is %d, want 1", got)
	}
	if got := one("SELECT count(*) FROM dataset.orders WHERE customer_id = 1"); got != 490 {
		t.Errorf("customer 1 owns %d orders, want 490", got)
	}
	if got := one("SELECT count(*) FROM (SELECT customer_id FROM dataset.orders WHERE customer_id BETWEEN 11 AND 50 GROUP BY customer_id HAVING count(*) = 152) x"); got != 40 {
		t.Errorf("%d of customers 11..50 own exactly 152 orders, want 40", got)
	}
	// "A handful of customers with hundreds of orders" is only true if no
	// customer's orders are all one status: the two columns are seeded from
	// independent permutations, and this is the check that keeps them so.
	if got := one("SELECT count(DISTINCT status) FROM dataset.orders WHERE customer_id = 1"); got < 3 {
		t.Errorf("customer 1's orders cover %d distinct statuses, want at least 3 (the skew must not be correlated with status)", got)
	}

	// Two lines per order, which is what makes "20 000 order items" and "order
	// 1050 has two lines" the same fact.
	if got := one("SELECT count(DISTINCT order_id) FROM dataset.order_items"); got != 10000 {
		t.Errorf("order_items cover %d orders, want 10000", got)
	}
	if got := one("SELECT count(*) FROM (SELECT order_id FROM dataset.order_items GROUP BY order_id HAVING count(*) <> 2) x"); got != 0 {
		t.Errorf("%d orders have a line count other than two", got)
	}
	if got := one("SELECT count(*) FROM dataset.order_items WHERE order_id = 1050"); got != 2 {
		t.Errorf("order 1050 has %d lines, want 2", got)
	}

	// A monotonic ordered_at is what makes a top-N sort a determinate answer.
	if got := one("SELECT count(*) FROM dataset.orders a JOIN dataset.orders b ON b.id = a.id + 1 WHERE b.ordered_at <= a.ordered_at"); got != 0 {
		t.Errorf("%d consecutive order pairs are not increasing in ordered_at", got)
	}
	if got := one("SELECT id FROM dataset.orders ORDER BY ordered_at DESC, id DESC LIMIT 1"); got != 10000 {
		t.Errorf("the newest order is %d, want 10000", got)
	}

	// The deliberate index gap: the only index the "most recent orders" lesson
	// asks the learner to add must not already be there.
	if got := one("SELECT count(*) FROM pg_indexes WHERE schemaname = 'dataset' AND indexdef ILIKE '%ordered_at%'"); got != 0 {
		t.Errorf("%d indexes on dataset cover ordered_at; the lesson needs that index to be missing", got)
	}
	if got := one("SELECT count(*) FROM pg_indexes WHERE schemaname = 'dataset' AND indexname IN ('orders_customer_id_idx','orders_status_idx','order_items_order_id_idx','order_items_product_id_idx')"); got != 4 {
		t.Errorf("%d of the four deliberate indexes exist, want 4", got)
	}

	// A ranking, not a tie: the "top products" report has to have an order.
	top := []int{}
	productRows, err := conn.Query(ctx, "SELECT product_id FROM dataset.order_items GROUP BY product_id ORDER BY count(*) DESC, product_id LIMIT 3")
	if err != nil {
		t.Fatalf("read top products: %v", err)
	}
	for productRows.Next() {
		var id int
		if err := productRows.Scan(&id); err != nil {
			t.Fatalf("scan top product: %v", err)
		}
		top = append(top, id)
	}
	productRows.Close()
	if err := productRows.Err(); err != nil {
		t.Fatalf("iterate top products: %v", err)
	}
	if len(top) != 3 || top[0] == top[1] || top[1] == top[2] {
		t.Errorf("top products are %v; the fixture needs a ranking, not a tie", top)
	}
}

// TestTablesServesBothDatasets pins the tables-drawer endpoint for the second
// fixture. Before M5 the endpoint only knew commerce, so a plan lesson's drawer
// answered 404 and the learner was shown nothing; the perf course is the one
// course whose whole subject is the shape of these relations, so "no tables"
// there is worse than useless.
//
// The preview is asserted to be a *header and nothing else*: the perf rows are
// not ours to show (they live in the learner's own copy, which may have been
// re-indexed or edited), and a preview that invented five rows would be the one
// thing this endpoint must never do.
func TestTablesServesBothDatasets(t *testing.T) {
	// perf is the same four relations as commerce at a larger scale, but perf's
	// `orders` carries the columns a real order carries (carrier, tracking code,
	// two addresses, a handling note). The drawer must describe perf.sql, not
	// commerce's narrower schema, so the expected columns are pinned here rather
	// than borrowed from the other dataset.
	want := []struct {
		name    string
		rows    int
		columns [][3]string
	}{
		{"customers", 2000, [][3]string{
			{"id", "integer", "PK"},
			{"name", "text", ""},
			{"email", "text", "nullable"},
			{"city", "text", "nullable"},
			{"signup_date", "date", ""},
		}},
		{"products", 500, [][3]string{
			{"id", "integer", "PK"},
			{"name", "text", ""},
			{"category", "text", ""},
			{"price", "numeric", ""},
			{"stock", "integer", ""},
			{"active", "boolean", ""},
		}},
		{"orders", dataset.PerfScaleFactorOrders, [][3]string{
			{"id", "integer", "PK"},
			{"customer_id", "integer", "FK"},
			{"ordered_at", "timestamp", ""},
			{"status", "text", ""},
			{"carrier", "text", ""},
			{"tracking_code", "text", ""},
			{"shipping_address", "text", ""},
			{"billing_address", "text", ""},
			{"notes", "text", ""},
		}},
		{"order_items", 20000, [][3]string{
			{"id", "integer", "PK"},
			{"order_id", "integer", "FK"},
			{"product_id", "integer", "FK"},
			{"quantity", "integer", ""},
			{"unit_price", "numeric", ""},
		}},
	}

	commerce, ok := dataset.Tables("commerce")
	if !ok {
		t.Fatal(`Tables("commerce") is not served`)
	}
	perf, ok := dataset.Tables(dataset.PerfDatasetID)
	if !ok {
		t.Fatalf("Tables(%q) is not served", dataset.PerfDatasetID)
	}
	if perf.Dataset != dataset.PerfDatasetID || perf.Version != dataset.PerfVersion {
		t.Errorf("perf tables = (%q, %d), want (%q, %d)", perf.Dataset, perf.Version, dataset.PerfDatasetID, dataset.PerfVersion)
	}
	if len(perf.Tables) != len(want) {
		t.Fatalf("perf tables = %d relations, want %d", len(perf.Tables), len(want))
	}
	if len(commerce.Tables) != len(want) {
		t.Fatalf("commerce tables = %d relations, want %d", len(commerce.Tables), len(want))
	}

	for i, expect := range want {
		got := perf.Tables[i]
		if got.Name != expect.name {
			t.Errorf("perf relation %d = %q, want %q", i, got.Name, expect.name)
		}
		if got.Rows != expect.rows {
			t.Errorf("%s rows = %d, want %d", got.Name, got.Rows, expect.rows)
		}
		if len(got.Preview) != 1 {
			t.Errorf("%s preview = %d rows, want the header alone (no invented rows)", got.Name, len(got.Preview))
		}
		if len(got.Cols) != len(expect.columns) {
			t.Errorf("%s has %d columns, want %d", got.Name, len(got.Cols), len(expect.columns))
			continue
		}
		for c, col := range expect.columns {
			if got.Cols[c][0] != col[0] || got.Cols[c][1] != col[1] || got.Cols[c][2] != col[2] {
				t.Errorf("%s column %d = %v, want %v", got.Name, c, got.Cols[c], col)
			}
		}
	}

	// The perf `orders` is wider than commerce's: that width is what puts the
	// relation at the page count the access-path lessons reason about, so the
	// drawer must report it rather than commerce's four columns.
	if len(perf.Tables[2].Cols) != 9 {
		t.Errorf("perf orders has %d columns, want 9 (carrier, tracking code, two addresses, notes)", len(perf.Tables[2].Cols))
	}
	if len(commerce.Tables[2].Cols) != 4 {
		t.Errorf("commerce orders has %d columns, want 4", len(commerce.Tables[2].Cols))
	}

	// The declared scale is the one a plan reports, so the drawer and the plan
	// environment block cannot disagree about how much data there is.
	if perf.Rows != 2000+500+dataset.PerfScaleFactorOrders+20000 {
		t.Errorf("perf total rows = %d, want %d", perf.Rows, 2000+500+dataset.PerfScaleFactorOrders+20000)
	}
	if _, ok := dataset.Tables("nope"); ok {
		t.Error(`Tables("nope") was served; an unknown dataset must 404`)
	}
}
