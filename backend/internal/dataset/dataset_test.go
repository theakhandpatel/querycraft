package dataset

import (
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenerate internal/dataset/schema.sql and seed.sql")

func table(t *testing.T, ds Dataset, name string) TableSpec {
	t.Helper()
	for _, tbl := range ds.Tables {
		if tbl.Name == name {
			return tbl
		}
	}
	t.Fatalf("table %q not found", name)
	return TableSpec{}
}

func TestCommerceRowCounts(t *testing.T) {
	ds := Commerce()
	want := map[string]int{"customers": 30, "products": 20, "orders": 80, "order_items": 160}
	for name, n := range want {
		if got := len(table(t, ds, name).Rows); got != n {
			t.Errorf("%s rows = %d, want %d", name, got, n)
		}
	}
	if ds.Version != DatasetVersion {
		t.Errorf("version = %d, want %d", ds.Version, DatasetVersion)
	}
}

func TestCommerceEdgeCases(t *testing.T) {
	ds := Commerce()
	customers := table(t, ds, "customers")
	products := table(t, ds, "products")
	orders := table(t, ds, "orders")
	items := table(t, ds, "order_items")

	ordersByCustomer := map[int]int{}
	for _, r := range orders.Rows {
		ordersByCustomer[r[1].(int)]++
	}
	customersWithOrders := map[int]bool{}
	for id := range ordersByCustomer {
		customersWithOrders[id] = true
	}

	noOrders, manyOrders := 0, 0
	for _, r := range customers.Rows {
		id := r[0].(int)
		n := ordersByCustomer[id]
		if n == 0 {
			noOrders++
		}
		if n >= 3 {
			manyOrders++
		}
	}
	if noOrders == 0 {
		t.Errorf("expected at least one customer with no orders")
	}
	if manyOrders == 0 {
		t.Errorf("expected at least one customer with several orders")
	}

	itemsByOrder := map[int]int{}
	for _, r := range items.Rows {
		itemsByOrder[r[1].(int)]++
	}
	multi, single := 0, 0
	for _, n := range itemsByOrder {
		if n >= 3 {
			multi++
		}
		if n == 1 {
			single++
		}
	}
	if multi == 0 {
		t.Errorf("expected at least one order with several items")
	}
	if single == 0 {
		t.Errorf("expected at least one single-item order")
	}

	orderedProducts := map[int]bool{}
	for _, r := range items.Rows {
		orderedProducts[r[2].(int)] = true
	}
	neverOrdered, zeroStock := 0, 0
	for _, r := range products.Rows {
		id := r[0].(int)
		if !orderedProducts[id] {
			neverOrdered++
		}
		if r[4].(int) == 0 {
			zeroStock++
		}
	}
	if neverOrdered == 0 {
		t.Errorf("expected at least one product never ordered")
	}
	if zeroStock == 0 {
		t.Errorf("expected a product with zero stock")
	}

	nullEmails, nullCities := 0, 0
	distinctCities := map[string]bool{}
	for _, r := range customers.Rows {
		if r[2] == nil {
			nullEmails++
		}
		if r[3] == nil {
			nullCities++
		} else {
			distinctCities[r[3].(string)] = true
		}
	}
	if nullEmails == 0 {
		t.Errorf("expected at least one NULL email")
	}
	if nullCities == 0 {
		t.Errorf("expected at least one NULL city")
	}
	if len(distinctCities) >= len(customers.Rows) {
		t.Errorf("expected repeated cities")
	}

	distinctCategories := map[string]bool{}
	for _, r := range products.Rows {
		distinctCategories[r[2].(string)] = true
	}
	if len(distinctCategories) >= len(products.Rows) {
		t.Errorf("expected repeated categories")
	}

	statuses := map[string]bool{}
	months := map[string]bool{}
	for _, r := range orders.Rows {
		statuses[r[3].(string)] = true
		if len(r[2].(string)) >= 7 {
			months[r[2].(string)[:7]] = true
		}
	}
	if len(statuses) < 2 {
		t.Errorf("expected several order statuses, got %d", len(statuses))
	}
	if len(months) < 3 {
		t.Errorf("expected orders across several months, got %d", len(months))
	}
}

func TestCommerceReferentialIntegrity(t *testing.T) {
	ds := Commerce()
	customers := table(t, ds, "customers")
	products := table(t, ds, "products")
	orders := table(t, ds, "orders")
	items := table(t, ds, "order_items")

	customerIDs := map[int]bool{}
	for _, r := range customers.Rows {
		customerIDs[r[0].(int)] = true
	}
	productIDs := map[int]bool{}
	for _, r := range products.Rows {
		productIDs[r[0].(int)] = true
	}
	orderIDs := map[int]bool{}
	for _, r := range orders.Rows {
		if !customerIDs[r[1].(int)] {
			t.Errorf("order %d references missing customer %d", r[0], r[1])
		}
		if orderIDs[r[0].(int)] {
			t.Errorf("duplicate order id %d", r[0])
		}
		orderIDs[r[0].(int)] = true
	}
	seenItems := map[int]bool{}
	for _, r := range items.Rows {
		if !orderIDs[r[1].(int)] {
			t.Errorf("item %d references missing order %d", r[0], r[1])
		}
		if !productIDs[r[2].(int)] {
			t.Errorf("item %d references missing product %d", r[0], r[2])
		}
		if r[3].(int) <= 0 {
			t.Errorf("item %d has non-positive quantity", r[0])
		}
		if seenItems[r[0].(int)] {
			t.Errorf("duplicate item id %d", r[0])
		}
		seenItems[r[0].(int)] = true
	}
}

func TestCommerceDeterministic(t *testing.T) {
	a := Commerce()
	b := Commerce()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Commerce() is not deterministic")
	}
}

func TestTablesIntrospection(t *testing.T) {
	resp, ok := Tables("commerce")
	if !ok {
		t.Fatalf("commerce dataset not found")
	}
	if resp.Dataset != "commerce" || resp.Version != 1 {
		t.Fatalf("envelope = %+v", resp)
	}
	if len(resp.Tables) != 4 {
		t.Fatalf("tables = %d, want 4", len(resp.Tables))
	}
	if resp.Rows != 290 {
		t.Fatalf("total rows = %d, want 290", resp.Rows)
	}
	names := map[string]bool{}
	for _, tbl := range resp.Tables {
		names[tbl.Name] = true
		if len(tbl.Preview) != 6 {
			t.Errorf("%s preview length = %d, want header + 5 rows", tbl.Name, len(tbl.Preview))
		}
		if len(tbl.Cols) == 0 || len(tbl.Cols[0]) != 3 {
			t.Errorf("%s cols shape is wrong: %v", tbl.Name, tbl.Cols)
		}
	}
	for _, want := range []string{"customers", "products", "orders", "order_items"} {
		if !names[want] {
			t.Errorf("missing table %s", want)
		}
	}
	if _, ok := Tables("nope"); ok {
		t.Errorf("unknown dataset should not be found")
	}
}

func TestTablesPreviewMatchesSeed(t *testing.T) {
	resp, _ := Tables("commerce")
	for _, tbl := range resp.Tables {
		if len(tbl.Preview) < 2 {
			continue
		}
		header := tbl.Preview[0]
		if header[0] != "id" {
			t.Errorf("%s preview header should start with id, got %v", tbl.Name, header)
		}
	}
}

func TestSeedSQLUpToDate(t *testing.T) {
	want := RenderSeedSQL(Commerce())
	if *update {
		if err := os.WriteFile("seed.sql", []byte(want), 0o644); err != nil {
			t.Fatalf("write seed.sql: %v", err)
		}
		return
	}
	if SeedSQL() != want {
		t.Fatalf("seed.sql is stale; regenerate with: go test ./internal/dataset -run TestSeedSQLUpToDate -update")
	}
}

func TestSchemaSQLUpToDate(t *testing.T) {
	want := RenderSchemaSQL(Commerce())
	if *update {
		if err := os.WriteFile("schema.sql", []byte(want), 0o644); err != nil {
			t.Fatalf("write schema.sql: %v", err)
		}
		return
	}
	if SchemaSQL() != want {
		t.Fatalf("schema.sql is stale; regenerate with: make dataset")
	}
}

func TestQuoteIdentAndLiteral(t *testing.T) {
	if got := QuoteIdent(`we"ird`); got != `"we""ird"` {
		t.Fatalf("quoteIdent = %s", got)
	}
	if got := quoteLiteral(`O'Brien`); got != `'O''Brien'` {
		t.Fatalf("quoteLiteral = %s", got)
	}
}

// Every identifier the generator emits is quoted and the dataset id is escaped
// inside its SQL literal, so neither can terminate the statement and inject SQL.
func TestRenderQuotesIdentifiersAndEscapesID(t *testing.T) {
	hostile := Dataset{ID: "x'; DROP TABLE dataset.customers; --", Version: 1}
	sql := RenderSchemaSQL(hostile)
	if strings.Contains(sql, "'x';") {
		t.Fatalf("dataset id was not escaped:\n%s", sql)
	}
	if !strings.Contains(sql, "'x''; DROP TABLE dataset.customers; --'") {
		t.Fatalf("escaped dataset id missing:\n%s", sql)
	}
	for _, want := range []string{
		`CREATE TABLE "dataset"."customers"`,
		`DROP TABLE IF EXISTS "dataset"."orders"`,
		`"customer_id" integer   NOT NULL REFERENCES "dataset"."customers"("id")`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("generated DDL does not quote identifiers (%q):\n%s", want, sql)
		}
	}
	seed := RenderSeedSQL(Commerce())
	if !strings.Contains(seed, `INSERT INTO "dataset"."customers" ("id", "name"`) {
		t.Fatalf("generated seed does not quote identifiers")
	}
}
