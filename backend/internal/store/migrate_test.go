package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// legacyCoursesDDL is the app.courses shape before `status` and `request`
// were declared: the shape a store created for the first course generation
// carries. Every course query selects both columns, so without the additive
// migration every Courses read fails with "column does not exist".
const legacyCoursesDDL = `
CREATE TABLE courses (
	id text PRIMARY KEY,
	owner_id text NOT NULL DEFAULT '',
	plan text NOT NULL,
	outcome text NOT NULL,
	created_at text NOT NULL
);`

func legacyPlan(id, title string) (string, string) {
	plan := map[string]any{
		"id": id, "version": 1, "title": title, "level": "beginner",
		"objectives": []string{"one"},
		"setup":      map[string]any{"dataset": "commerce", "schema_version": 1, "seed_version": 1, "statements": []string{}},
		"chapters": []map[string]any{{
			"id": id + ".1", "n": 1, "title": "Chapter one",
			"sections": []map[string]any{{"title": "Section one", "body": "Hello."}},
		}},
	}
	planJSON, _ := json.Marshal(plan)
	outcomeJSON, _ := json.Marshal(map[string]any{"status": "completed"})
	return string(planJSON), string(outcomeJSON)
}

// TestSQLiteMigratesLegacyCourseColumns: a metadata file written before
// status and request existed must open, migrate, and answer Courses, not
// fail every read with "column does not exist".
func TestSQLiteMigratesLegacyCourseColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, legacyCoursesDDL); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	plan, outcome := legacyPlan("course-old", "Legacy course")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO courses (id, owner_id, plan, outcome, created_at) VALUES (?, ?, ?, ?, ?)`,
		"course-old", "user-1", plan, outcome, timestamp(time.Now())); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	defer func() { _ = st.Close() }()

	recs, err := st.Courses(ctx)
	if err != nil {
		t.Fatalf("Courses on the migrated store: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("Courses = %d records, want the legacy row", len(recs))
	}
	if recs[0].Status != schema.StatusActive {
		t.Errorf("migrated status = %q, want the active default", recs[0].Status)
	}
	if recs[0].Request != nil {
		t.Errorf("migrated request = %+v, want nil (the row predates it)", recs[0].Request)
	}

	// The migrated store takes new writes too, and the new columns round-trip.
	planNew, outcomeNew := legacyPlan("course-new", "New course")
	save := schema.CourseRecord{
		Plan:      mustPlan(t, planNew),
		Outcome:   mustOutcome(t, outcomeNew),
		Request:   mustRequest(t, `{"kind":"course","title":"New course"}`),
		Status:    schema.StatusActive,
		OwnerID:   "user-1",
		CreatedAt: time.Now().UTC(),
	}
	if err := st.SaveCourse(ctx, save); err != nil {
		t.Fatalf("save on the migrated store: %v", err)
	}
	got, found, err := st.Course(ctx, "course-new")
	if err != nil || !found {
		t.Fatalf("Course after migration: found=%v err=%v", found, err)
	}
	if got.Request == nil || got.Request.Title != "New course" {
		t.Errorf("request did not round-trip: %+v", got.Request)
	}
}

// TestPostgresMigratesLegacyCourseColumns runs the same claim against a real
// PostgreSQL: a cluster whose app.courses predates the new columns must open
// and read through NewPostgres.
func TestPostgresMigratesLegacyCourseColumns(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a real PostgreSQL; skipped under -short")
	}
	ctx := context.Background()
	srv := pgtest.Start(t)
	conn, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE SCHEMA app;
CREATE TABLE app.courses (
	id text PRIMARY KEY,
	owner_id text NOT NULL DEFAULT '',
	plan jsonb NOT NULL,
	outcome jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	plan, outcome := legacyPlan("course-old", "Legacy course")
	if _, err := conn.Exec(ctx,
		`INSERT INTO app.courses (id, owner_id, plan, outcome, created_at) VALUES ($1, $2, $3, $4, now())`,
		"course-old", "user-1", plan, outcome); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}

	st, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	defer func() { _ = st.Close() }()

	recs, err := st.Courses(ctx)
	if err != nil {
		t.Fatalf("Courses on the migrated store: %v", err)
	}
	if len(recs) != 1 || recs[0].Status != schema.StatusActive || recs[0].Request != nil {
		t.Fatalf("migrated row = %+v, want the legacy row with the active default and no request", recs)
	}
}

// mustPlan/mustOutcome/mustRequest decode the JSON text the legacy helpers
// produce into the record fields.
func mustPlan(t *testing.T, raw string) schema.CoursePlan {
	t.Helper()
	var p schema.CoursePlan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	return p
}

func mustOutcome(t *testing.T, raw string) schema.Outcome {
	t.Helper()
	var o schema.Outcome
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("decode outcome: %v", err)
	}
	return o
}

func mustRequest(t *testing.T, raw string) *schema.GenerationRequest {
	t.Helper()
	var r schema.GenerationRequest
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return &r
}
