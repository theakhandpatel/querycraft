package dataset_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

// TestScaleFactorMatchesTheSeed counts the rows in each dataset's fact relation
// and compares them to the scale the plan lab reports.
//
// The reported scale is a declaration in Go, and a declaration that drifts from
// the seed is worse than no scale at all: the number would be specific,
// plausible and wrong. This test is what keeps the two in step.
func TestScaleFactorMatchesTheSeed(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	cases := []struct {
		name    string
		id      string
		load    func(context.Context, *pgx.Conn, string) error
		want    int
		reports int
	}{
		{"commerce", "", func(ctx context.Context, admin *pgx.Conn, name string) error {
			return dataset.LoadWritableIntoTemplate(ctx, admin, name, pgtest.Password)
		}, dataset.CommerceScaleFactor, dataset.FactRowCount("")},
		{"perf", dataset.PerfDatasetID, func(ctx context.Context, admin *pgx.Conn, name string) error {
			return dataset.LoadPerfIntoTemplate(ctx, admin, name, pgtest.Password)
		}, dataset.PerfScaleFactorOrders, dataset.FactRowCount(dataset.PerfDatasetID)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := envdb.CreateDatabase(ctx, admin, "qc_scale_"+tc.name); err != nil {
				t.Fatalf("create: %v", err)
			}
			if err := tc.load(ctx, admin, "qc_scale_"+tc.name); err != nil {
				t.Fatalf("load: %v", err)
			}
			conn, err := dataset.ConnectToDatabase(ctx, admin, "qc_scale_"+tc.name)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

			var orders int
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM dataset.orders").Scan(&orders); err != nil {
				t.Fatalf("count orders: %v", err)
			}
			if orders != tc.want {
				t.Errorf("dataset %q has %d orders, declaration says %d", tc.id, orders, tc.want)
			}
			if tc.reports != orders {
				t.Errorf("FactRowCount(%q) = %d but the database holds %d orders", tc.id, tc.reports, orders)
			}
		})
	}
}
