// Package dataset owns the deterministic, rebuildable `commerce` dataset used
// by every curated exercise. The Go generator is the source of truth: it feeds
// both the SQL seed files under this directory and GET /datasets/{id}/tables,
// so introspection always matches the seed without a live database.
package dataset

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DatasetVersion is shared by the schema and the seed.
const DatasetVersion = 1

// ColumnSpec is one column's name, SQL type and flags (PK/FK/nullable).
type ColumnSpec struct {
	Name  string
	Type  string
	Flags string
}

// TableSpec is one table: its columns, the columns shown in a preview, and its
// full rows (in column order).
type TableSpec struct {
	Name        string
	Columns     []ColumnSpec
	PreviewCols []string
	Rows        [][]any
}

// Dataset is a named, versioned set of tables.
type Dataset struct {
	ID      string
	Version int
	Tables  []TableSpec
}

// TableInfo is the JSON shape returned by GET /datasets/{id}/tables.
type TableInfo struct {
	Name    string     `json:"name"`
	Rows    int        `json:"rows"`
	Cols    [][]string `json:"cols"`
	Preview [][]string `json:"preview"`
}

// TablesResponse is the /datasets/{id}/tables body.
type TablesResponse struct {
	Dataset string      `json:"dataset"`
	Version int         `json:"version"`
	Tables  []TableInfo `json:"tables"`
	Rows    int         `json:"rows"`
}

// Commerce builds the commerce dataset deterministically. Calling it twice
// always produces identical data.
func Commerce() Dataset {
	products := commerceProducts()
	return Dataset{
		ID:      "commerce",
		Version: DatasetVersion,
		Tables: []TableSpec{
			commerceCustomers(),
			products,
			commerceOrders(),
			commerceOrderItems(products),
		},
	}
}

// ByID returns a dataset by id.
func ByID(id string) (Dataset, bool) {
	if id != "commerce" {
		return Dataset{}, false
	}
	return Commerce(), true
}

// perfTableRows is the declared row count of each perf relation, in the order
// perf.sql generates them. It is the same shape as the commerce Dataset, at the
// scale the plan lessons are about.
var perfTableRows = []struct {
	Name string
	Rows int
}{
	{"customers", 2000},
	{"products", 500},
	{"orders", PerfScaleFactorOrders},
	{"order_items", 20000},
}

// perfTablesResponse describes the perf fixture for the tables drawer.
//
// The four relations are the same four the commerce dataset has, with the same
// columns and types: perf.sql declares the same DDL at a larger scale, and the
// schema is the thing the drawer exists to show. The column metadata is taken
// from the commerce build rather than restated, so the two descriptions cannot
// drift; the row counts are the counts the seed generates.
//
// There is deliberately no row *preview*. A preview is learner data, and the
// perf rows live in the caller's own copy of the sealed template — an anonymous
// caller has no such copy, and a signed-in one may have changed it. Returning
// the header alone says "these are the columns" and shows no row, which is the
// only honest answer available without an environment; inventing five rows
// would be exactly the fabrication this endpoint must never commit.
func perfTablesResponse() TablesResponse {
	commerce := Commerce()
	out := TablesResponse{Dataset: PerfDatasetID, Version: PerfVersion}
	for _, spec := range perfTableRows {
		source, found := findTable(commerce, spec.Name)
		if !found {
			continue
		}
		info := TableInfo{Name: spec.Name, Rows: spec.Rows}
		info.Cols = make([][]string, len(source.Columns))
		for i, c := range source.Columns {
			info.Cols[i] = []string{c.Name, c.Type, c.Flags}
		}
		previewCols := source.PreviewCols
		if len(previewCols) == 0 {
			for _, c := range source.Columns {
				previewCols = append(previewCols, c.Name)
			}
		}
		info.Preview = [][]string{append([]string{}, previewCols...)}
		out.Tables = append(out.Tables, info)
		out.Rows += spec.Rows
	}
	return out
}

func findTable(ds Dataset, name string) (TableSpec, bool) {
	for _, t := range ds.Tables {
		if t.Name == name {
			return t, true
		}
	}
	return TableSpec{}, false
}

// Tables builds the introspection response for a dataset id.
func Tables(id string) (TablesResponse, bool) {
	if id == PerfDatasetID {
		return perfTablesResponse(), true
	}
	ds, ok := ByID(id)
	if !ok {
		return TablesResponse{}, false
	}
	out := TablesResponse{Dataset: ds.ID, Version: ds.Version}
	for _, t := range ds.Tables {
		out.Tables = append(out.Tables, t.Info())
		out.Rows += len(t.Rows)
	}
	return out, true
}

// Info renders a table's metadata and a preview (header plus up to five rows).
func (t TableSpec) Info() TableInfo {
	info := TableInfo{Name: t.Name, Rows: len(t.Rows)}
	info.Cols = make([][]string, len(t.Columns))
	colType := make(map[string]string, len(t.Columns))
	for i, c := range t.Columns {
		info.Cols[i] = []string{c.Name, c.Type, c.Flags}
		colType[c.Name] = c.Type
	}
	previewCols := t.PreviewCols
	if len(previewCols) == 0 {
		for _, c := range t.Columns {
			previewCols = append(previewCols, c.Name)
		}
	}
	idx := make([]int, len(previewCols))
	for i, name := range previewCols {
		idx[i] = t.columnIndex(name)
	}
	header := append([]string{}, previewCols...)
	info.Preview = append(info.Preview, header)
	limit := 5
	if len(t.Rows) < limit {
		limit = len(t.Rows)
	}
	for r := 0; r < limit; r++ {
		row := make([]string, len(idx))
		for k, ci := range idx {
			if ci < 0 || ci >= len(t.Rows[r]) {
				row[k] = "NULL"
				continue
			}
			row[k] = stringify(t.Rows[r][ci], colType[previewCols[k]])
		}
		info.Preview = append(info.Preview, row)
	}
	return info
}

func (t TableSpec) columnIndex(name string) int {
	for i, c := range t.Columns {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func stringify(v any, typ string) string {
	if v == nil {
		return "NULL"
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if typ == "numeric" {
			return strconv.FormatFloat(x, 'f', 2, 64)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", v)
}

var (
	firstNames = []string{
		"Ada", "Bruno", "Chidi", "Dara", "Emi", "Farid", "Gina", "Hiro", "Ines", "Jonas",
		"Kira", "Luca", "Mina", "Noor", "Omar", "Pia", "Quinn", "Rosa", "Sami", "Tess",
		"Uma", "Viktor", "Wren", "Xiu", "Yara", "Zane", "Aya", "Bo", "Cleo", "Dev",
	}
	lastNames = []string{
		"Whitfield", "Sala", "Okafor", "Novak", "Tanaka", "Haddad", "Rossi", "Kim", "Silva", "Berg",
		"Petrov", "Conti", "Park", "Aziz", "Farouk", "Lindqvist", "Doyle", "Marchetti", "Rahman", "Osei",
		"Nair", "Ivanov", "Blythe", "Chen", "Haidar", "Mbeki", "Sato", "Larsen", "Moreau", "Kumar",
	}
	cities = []string{
		"Leeds", "Milan", "Lagos", "Osaka", "Lisbon", "Berlin", "Seoul", "Sao Paulo", "Stockholm", "Rome",
		"Nairobi", "Toronto", "Leeds", "Milan", "Lagos", "Osaka", "Paris", "Madrid", "Austin", "Oslo",
		"Dublin", "Warsaw", "Cairo", "Sydney", "Leeds", "Milan", "Boston", "Porto", "Vienna", "Pune",
	}
	signupBase = time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	orderBase  = time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	statuses   = []string{"completed", "completed", "completed", "pending", "cancelled", "shipped", "refunded"}
)

func commerceCustomers() TableSpec {
	cols := []ColumnSpec{
		{"id", "integer", "PK"},
		{"name", "text", ""},
		{"email", "text", "nullable"},
		{"city", "text", "nullable"},
		{"signup_date", "date", ""},
	}
	rows := [][]any{
		{1, "Ada Whitfield", "ada@example.com", "Leeds", "2025-11-02"},
		{2, "Bruno Sala", "bruno@example.com", "Milan", "2025-11-15"},
		{3, "Chidi Okafor", nil, "Lagos", "2025-12-01"},
		{4, "Dara Novak", "dara@example.com", nil, "2025-12-08"},
		{5, "Emi Tanaka", "emi@example.com", "Osaka", "2025-12-20"},
	}
	for id := 6; id <= 30; id++ {
		i := id - 1
		first := firstNames[i%len(firstNames)]
		last := lastNames[(i*3)%len(lastNames)]
		name := first + " " + last
		var email any = strings.ToLower(first) + "." + strings.ToLower(last) + strconv.Itoa(id) + "@example.com"
		var city any = cities[(i*7)%len(cities)]
		if id == 10 || id == 17 || id == 24 {
			email = nil
		}
		if id == 12 || id == 20 || id == 28 {
			city = nil
		}
		signup := signupBase.AddDate(0, 0, id*11).Format("2006-01-02")
		rows = append(rows, []any{id, name, email, city, signup})
	}
	return TableSpec{
		Name:        "customers",
		Columns:     cols,
		PreviewCols: []string{"id", "name", "email", "city"},
		Rows:        rows,
	}
}

type product struct {
	id       int
	name     string
	category string
	price    float64
	stock    int
	active   bool
}

func productCatalog() []product {
	return []product{
		{1, "Aurora Wireless Earbuds", "Electronics", 59.00, 40, true},
		{2, "Nimbus Headphones", "Electronics", 129.00, 25, true},
		{3, "Linen Duvet Cover", "Home", 84.00, 15, true},
		{4, "Cedar Desk Lamp", "Home", 42.50, 30, true},
		{5, "Trail Runner Jacket", "Outdoor", 139.00, 12, true},
		{6, "Pulse Fitness Tracker", "Electronics", 74.25, 18, true},
		{7, "Vertex Mechanical Keyboard", "Electronics", 89.50, 22, true},
		{8, "Harbor Rain Shell", "Outdoor", 118.00, 9, true},
		{9, "Maple Cutting Board", "Kitchen", 24.00, 40, true},
		{10, "Copper Kettle", "Kitchen", 65.75, 14, true},
		{11, "Wool Throw Blanket", "Home", 55.00, 20, true},
		{12, "Granite Cookware Set", "Kitchen", 210.00, 6, true},
		{13, "Summit Hiking Poles", "Outdoor", 96.40, 11, true},
		{14, "Ceramic Vase", "Home", 33.20, 27, true},
		{15, "Espresso Tamper", "Kitchen", 19.99, 50, true},
		{16, "Solar Camp Lantern", "Outdoor", 47.00, 16, true},
		{17, "Bamboo Shelf", "Home", 72.30, 13, true},
		{18, "Noise-Cancel Earmuffs", "Electronics", 64.90, 21, true},
		{19, "Rope Dog Leash", "Outdoor", 15.50, 35, true},
		{20, "Discontinued Camp Stove", "Outdoor", 0.00, 0, false}, // never ordered, zero stock
	}
}

func commerceProducts() TableSpec {
	cols := []ColumnSpec{
		{"id", "integer", "PK"},
		{"name", "text", ""},
		{"category", "text", ""},
		{"price", "numeric", ""},
		{"stock", "integer", ""},
		{"active", "boolean", ""},
	}
	rows := make([][]any, 0, 20)
	for _, p := range productCatalog() {
		rows = append(rows, []any{p.id, p.name, p.category, p.price, p.stock, p.active})
	}
	return TableSpec{
		Name:        "products",
		Columns:     cols,
		PreviewCols: []string{"id", "name", "category", "price"},
		Rows:        rows,
	}
}

// orderCustomers assigns a customer to each of the eighty orders. Customers 1-3
// get fixed counts (5/2/4) so the joins exercises have stable answers; customer 4
// and customers 25-30 get no orders.
func orderCustomers() []int {
	ids := make([]int, 80)
	ids[0], ids[1], ids[2], ids[3], ids[4] = 1, 1, 2, 3, 1
	extras := []int{1, 1, 2, 3, 3, 3}
	copy(ids[5:], extras)
	for k := 0; k < 69; k++ {
		ids[11+k] = 5 + (k % 20)
	}
	return ids
}

func commerceOrders() TableSpec {
	cols := []ColumnSpec{
		{"id", "integer", "PK"},
		{"customer_id", "integer", "FK"},
		{"ordered_at", "timestamp", ""},
		{"status", "text", ""},
	}
	preview := [][]any{
		{1001, 1, "2026-01-04 10:12", "completed"},
		{1002, 1, "2026-01-19 16:40", "completed"},
		{1003, 2, "2026-01-22 09:03", "cancelled"},
		{1004, 3, "2026-02-02 14:20", "completed"},
		{1005, 1, "2026-02-11 11:55", "completed"},
	}
	customers := orderCustomers()
	rows := make([][]any, 0, 80)
	for i := 0; i < 80; i++ {
		id := 1001 + i
		if i < len(preview) {
			rows = append(rows, preview[i])
			continue
		}
		at := orderBase.Add(time.Duration(i*41) * time.Hour).Format("2006-01-02 15:04")
		rows = append(rows, []any{id, customers[i], at, statuses[i%len(statuses)]})
	}
	return TableSpec{
		Name:        "orders",
		Columns:     cols,
		PreviewCols: []string{"id", "customer_id", "ordered_at", "status"},
		Rows:        rows,
	}
}

// itemCounts distributes 160 line items across the 80 orders. The 1003 order has
// none; several orders have four, several have one, so multi-item and single-item
// orders both exist.
func itemCounts() []int {
	counts := make([]int, 80)
	counts[0], counts[1], counts[2], counts[3], counts[4] = 2, 1, 0, 1, 1
	for i := 5; i < 80; i++ {
		counts[i] = 1 + (i % 3)
	}
	for i := 5; i < 10; i++ {
		counts[i]++
	}
	return counts
}

func commerceOrderItems(products TableSpec) TableSpec {
	cols := []ColumnSpec{
		{"id", "integer", "PK"},
		{"order_id", "integer", "FK"},
		{"product_id", "integer", "FK"},
		{"quantity", "integer", ""},
		{"unit_price", "numeric", ""},
	}
	price := make(map[int]float64, 20)
	for _, p := range productCatalog() {
		price[p.id] = p.price
	}
	fixed := map[int][3]any{
		1: {1, 2, 59.00},
		2: {3, 1, 84.00},
		3: {5, 1, 139.00},
		4: {2, 1, 129.00},
		5: {1, 3, 59.00},
	}
	counts := itemCounts()
	rows := make([][]any, 0, 160)
	itemID := 0
	for i := 0; i < 80; i++ {
		orderID := 1001 + i
		for j := 0; j < counts[i]; j++ {
			itemID++
			if f, ok := fixed[itemID]; ok {
				rows = append(rows, []any{itemID, orderID, f[0], f[1], f[2]})
				continue
			}
			pid := 1 + ((itemID * 7) % 19) // 1..19: product 20 is never ordered
			qty := 1 + (itemID % 3)
			rows = append(rows, []any{itemID, orderID, pid, qty, price[pid]})
		}
	}
	return TableSpec{
		Name:        "order_items",
		Columns:     cols,
		PreviewCols: []string{"id", "order_id", "product_id", "quantity", "unit_price"},
		Rows:        rows,
	}
}
