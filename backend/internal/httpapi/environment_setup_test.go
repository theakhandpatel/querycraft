package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* M9: the learner's database is built the way the verifier's was.
 *
 * A course whose setup creates scratch objects is verified in a throwaway
 * environment that had `setup.statements` applied. Before this, the learner's
 * environment was only the sealed template, so an exercise that verified green
 * died for the learner with "relation does not exist".
 * `POST /environment/reset?course=<id>` now re-applies the stored course's own
 * setup after the template is copied, owner-scoped and with nothing invented.
 *
 * The whole suite runs against a real PostgreSQL (pgtest), the same way the
 * ADR 0003 workspace test does. */

// setupResetEnvelope is the reset response with the applied count this file
// asserts on.
type setupResetEnvelope struct {
	OK              bool    `json:"ok"`
	DurationMS      float64 `json:"duration_ms"`
	SetupStatements int     `json:"setup_statements"`
}

// courseScratchStatements is a setup plan of the shape the gate admits: two
// statements that create and populate one scratch table, and nothing else.
var courseScratchStatements = []string{
	`CREATE TABLE course_scratch_notes (note text)`,
	`INSERT INTO course_scratch_notes (note) VALUES ('verified against this')`,
}

// seedSetupCourse stores a course for its owner through the same owner-scoped
// wrapper the pipeline uses, so the reset handler reads really stored state.
func seedSetupCourse(t *testing.T, depot schema.Depot, owner, id string) {
	t.Helper()
	err := ownerScopedDepot{Depot: depot, owner: owner}.SaveCourse(context.Background(), schema.CourseRecord{
		Plan: schema.CoursePlan{
			ID:    id,
			Title: "Scratch tables",
			Setup: schema.SetupPlan{
				Dataset:       "commerce",
				SchemaVersion: 1,
				SeedVersion:   1,
				Statements:    courseScratchStatements,
			},
		},
		Outcome:   schema.Outcome{Status: schema.StatusActive, Kind: "course"},
		Status:    schema.StatusActive,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed course %s: %v", id, err)
	}
}

// TestEnvironmentResetAppliesCourseSetup is the defect itself, over the real
// seam: after a reset that names a course, the course's scratch objects are
// queryable; a reset that names none leaves none behind; and a course that is
// not the caller's is refused and applies nothing.
func TestEnvironmentResetAppliesCourseSetup(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	shared, err := engine.NewPgxExecutor(ctx, srv.LearnerDSN(t, srv.Database))
	if err != nil {
		t.Fatalf("open shared read-only executor: %v", err)
	}
	defer shared.Close()

	m, err := envdb.New(ctx, envdb.Options{
		AdminDSN:        srv.DSN,
		LearnerPassword: srv.Password(),
		MaxEnvironments: 4,
		IdleTTL:         time.Hour,
		Log:             pgtest.QuietLogger(),
	})
	if err != nil {
		t.Fatalf("new environment manager: %v", err)
	}
	defer m.Close()

	depot := store.NewMemory()
	s := New(shared, store.NewMemory(), Config{MaxRows: 200, TimeoutMS: 10_000, MaxConcurrent: 2}, pgtest.QuietLogger())
	s.UseEnvironments(m)
	s.UseAI(ai.Config{Depot: depot})

	alice := signupUser(t, s, "setup-alice@example.test")
	bob := signupUser(t, s, "setup-bob@example.test")
	seedSetupCourse(t, depot, userIDFor(t, s, alice), "course-alice")
	seedSetupCourse(t, depot, userIDFor(t, s, bob), "course-bob")

	resetEnvelope := func(t *testing.T, token, query string) (int, setupResetEnvelope) {
		t.Helper()
		w := doAs(t, s, token, http.MethodPost, "/environment/reset"+query, "")
		var out setupResetEnvelope
		if w.Code == http.StatusOK {
			mustDecode(t, w, &out)
		}
		return w.Code, out
	}

	t.Run("a reset naming a course applies its setup", func(t *testing.T) {
		status, out := resetEnvelope(t, alice, "?course=course-alice")
		if status != http.StatusOK {
			t.Fatalf("reset status = %d, want 200", status)
		}
		t.Logf("POST /environment/reset?course=course-alice -> setup_statements=%d duration_ms=%.1f", out.SetupStatements, out.DurationMS)
		if out.SetupStatements != len(courseScratchStatements) {
			t.Fatalf("setup_statements = %d, want %d", out.SetupStatements, len(courseScratchStatements))
		}
		// The scratch table the course was verified against is really there,
		// with the row the setup inserted: the learner's database is the
		// verified one, not merely a reset one.
		res := querySQL(t, s, alice, "SELECT note FROM course_scratch_notes")
		if res.Status != "succeeded" || len(res.Rows) != 1 || res.Rows[0][0] != "verified against this" {
			t.Fatalf("SELECT after course reset = %+v, want the setup's row", res)
		}
	})

	t.Run("a reset naming no course applies no setup", func(t *testing.T) {
		status, out := resetEnvelope(t, alice, "")
		if status != http.StatusOK {
			t.Fatalf("reset status = %d, want 200", status)
		}
		if out.SetupStatements != 0 {
			t.Fatalf("setup_statements = %d, want 0 for a request naming no course", out.SetupStatements)
		}
		// The previous reset's scratch table is gone: a plain reset restores
		// the sealed dataset and nothing else.
		if res := querySQL(t, s, alice, "SELECT note FROM course_scratch_notes"); res.Status != "failed" {
			t.Fatalf("the scratch table survived a reset that named no course: %+v", res)
		}
		// The sealed rows are back, so the reset really took.
		if res := querySQL(t, s, alice, "SELECT count(*) FROM customers"); res.Status != "succeeded" || rowInt(res, 0, 0) != 30 {
			t.Fatalf("after reset customers = %+v, want the seeded 30", res)
		}
	})

	t.Run("another owner's course is refused and applies nothing", func(t *testing.T) {
		// Start from a clean workspace so "applies nothing" is unambiguous.
		if status, _ := resetEnvelope(t, alice, ""); status != http.StatusOK {
			t.Fatalf("pre-reset status = %d, want 200", status)
		}
		w := doAs(t, s, alice, http.MethodPost, "/environment/reset?course=course-bob", "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("reset with a foreign course status = %d, want 404; body = %s", w.Code, w.Body.String())
		}
		// 404, never 403: the id cannot be probed for existence, and no setup
		// from another learner's course ran here.
		if res := querySQL(t, s, alice, "SELECT note FROM course_scratch_notes"); res.Status != "failed" {
			t.Fatalf("a foreign course's setup was applied to the caller: %+v", res)
		}
	})

	t.Run("an unknown course id is refused", func(t *testing.T) {
		w := doAs(t, s, alice, http.MethodPost, "/environment/reset?course=not-a-course", "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("reset with an unknown course status = %d, want 404; body = %s", w.Code, w.Body.String())
		}
	})

	t.Run("a course combined with the perf dataset is refused", func(t *testing.T) {
		w := doAs(t, s, alice, http.MethodPost, "/environment/reset?course=course-alice&dataset=perf", "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("course plus perf status = %d, want 400; body = %s", w.Code, w.Body.String())
		}
		// The refusal must not have reset the challenge workspace the learner
		// did not ask about, so its scratch objects are exactly what they were
		// after the last plain reset: none.
		if res := querySQL(t, s, alice, "SELECT note FROM course_scratch_notes"); res.Status != "failed" {
			t.Fatalf("the refused request still built the scratch table: %+v", res)
		}
	})
}

// setupPrepareEnvelope is the prepare response: the applied count this call
// measured, and nothing it did not.
type setupPrepareEnvelope struct {
	OK              bool `json:"ok"`
	SetupStatements int  `json:"setup_statements"`
}

// TestEnvironmentPrepareAppliesCourseSetupOnce is item 6 over the real seam. A
// course open prepares its workspace, a second open is a no-op, a reset makes
// the course prepare again, another owner's course is refused with 404, and a
// gateway that can offer no workspace answers ENVIRONMENT_UNAVAILABLE.
func TestEnvironmentPrepareAppliesCourseSetupOnce(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	shared, err := engine.NewPgxExecutor(ctx, srv.LearnerDSN(t, srv.Database))
	if err != nil {
		t.Fatalf("open shared read-only executor: %v", err)
	}
	defer shared.Close()

	m, err := envdb.New(ctx, envdb.Options{
		AdminDSN:        srv.DSN,
		LearnerPassword: srv.Password(),
		MaxEnvironments: 4,
		IdleTTL:         time.Hour,
		Log:             pgtest.QuietLogger(),
	})
	if err != nil {
		t.Fatalf("new environment manager: %v", err)
	}
	defer m.Close()

	depot := store.NewMemory()
	s := New(shared, store.NewMemory(), Config{MaxRows: 200, TimeoutMS: 10_000, MaxConcurrent: 2}, pgtest.QuietLogger())
	s.UseEnvironments(m)
	s.UseAI(ai.Config{Depot: depot})

	alice := signupUser(t, s, "prepare-alice@example.test")
	bob := signupUser(t, s, "prepare-bob@example.test")
	seedSetupCourse(t, depot, userIDFor(t, s, alice), "course-alice")
	seedSetupCourse(t, depot, userIDFor(t, s, bob), "course-bob")

	prepare := func(t *testing.T, token, query string) (int, setupPrepareEnvelope) {
		t.Helper()
		w := doAs(t, s, token, http.MethodPost, "/environment/prepare"+query, "")
		var out setupPrepareEnvelope
		if w.Code == http.StatusOK {
			mustDecode(t, w, &out)
		}
		return w.Code, out
	}

	t.Run("opening a course prepares its setup", func(t *testing.T) {
		status, out := prepare(t, alice, "?course=course-alice")
		if status != http.StatusOK {
			t.Fatalf("prepare status = %d, want 200", status)
		}
		t.Logf("POST /environment/prepare?course=course-alice -> setup_statements=%d", out.SetupStatements)
		if out.SetupStatements != len(courseScratchStatements) {
			t.Fatalf("setup_statements = %d, want %d", out.SetupStatements, len(courseScratchStatements))
		}
		// The scratch table the course was verified against is really there,
		// with the row the setup inserted: the learner opened the verified
		// workspace, not merely a copied template.
		res := querySQL(t, s, alice, "SELECT note FROM course_scratch_notes")
		if res.Status != "succeeded" || len(res.Rows) != 1 || res.Rows[0][0] != "verified against this" {
			t.Fatalf("SELECT after prepare = %+v, want the setup's row", res)
		}
	})

	t.Run("a second open for the same course is a no-op", func(t *testing.T) {
		status, out := prepare(t, alice, "?course=course-alice")
		if status != http.StatusOK {
			t.Fatalf("second prepare status = %d, want 200", status)
		}
		t.Logf("POST /environment/prepare?course=course-alice (again) -> setup_statements=%d", out.SetupStatements)
		if out.SetupStatements != 0 {
			t.Fatalf("setup_statements = %d on the second prepare, want 0: the setup is not idempotent, so it must not run twice", out.SetupStatements)
		}
		// The single row the first prepare inserted is still the only one: the
		// no-op did not re-run the INSERT.
		res := querySQL(t, s, alice, "SELECT count(*) FROM course_scratch_notes")
		if res.Status != "succeeded" || rowInt(res, 0, 0) != 1 {
			t.Fatalf("after the second prepare notes = %+v, want exactly the one setup row", res)
		}
	})

	t.Run("a reset clears the mark so the course prepares again", func(t *testing.T) {
		// A manual reset applies the setup itself, so the workspace is ready
		// and the mark is set; a later prepare for the same course is a no-op
		// until the workspace is reset again.
		if w := doAs(t, s, alice, http.MethodPost, "/environment/reset?course=course-alice", ""); w.Code != http.StatusOK {
			t.Fatalf("reset status = %d, want 200; body = %s", w.Code, w.Body.String())
		}
		status, out := prepare(t, alice, "?course=course-alice")
		if status != http.StatusOK {
			t.Fatalf("prepare after reset status = %d, want 200", status)
		}
		if out.SetupStatements != 0 {
			t.Fatalf("setup_statements = %d after a course reset, want 0: the reset already applied it and marked it", out.SetupStatements)
		}

		// A plain reset restores the sealed dataset and clears the mark, so the
		// next course open prepares the workspace again.
		if w := doAs(t, s, alice, http.MethodPost, "/environment/reset", ""); w.Code != http.StatusOK {
			t.Fatalf("plain reset status = %d, want 200; body = %s", w.Code, w.Body.String())
		}
		if res := querySQL(t, s, alice, "SELECT note FROM course_scratch_notes"); res.Status != "failed" {
			t.Fatalf("the scratch table survived a plain reset: %+v", res)
		}
		status, out = prepare(t, alice, "?course=course-alice")
		if status != http.StatusOK {
			t.Fatalf("prepare after a plain reset status = %d, want 200", status)
		}
		if out.SetupStatements != len(courseScratchStatements) {
			t.Fatalf("setup_statements = %d after a plain reset, want %d: the mark must clear with the workspace", out.SetupStatements, len(courseScratchStatements))
		}
	})

	t.Run("another owner's course is refused and prepares nothing", func(t *testing.T) {
		if w := doAs(t, s, alice, http.MethodPost, "/environment/reset", ""); w.Code != http.StatusOK {
			t.Fatalf("pre-reset status = %d, want 200; body = %s", w.Code, w.Body.String())
		}
		w := doAs(t, s, alice, http.MethodPost, "/environment/prepare?course=course-bob", "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("prepare with a foreign course status = %d, want 404; body = %s", w.Code, w.Body.String())
		}
		if res := querySQL(t, s, alice, "SELECT note FROM course_scratch_notes"); res.Status != "failed" {
			t.Fatalf("a foreign course's setup was applied to the caller: %+v", res)
		}
	})

	t.Run("an unknown course id is refused", func(t *testing.T) {
		w := doAs(t, s, alice, http.MethodPost, "/environment/prepare?course=not-a-course", "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("prepare with an unknown course status = %d, want 404; body = %s", w.Code, w.Body.String())
		}
	})

	t.Run("a request that names no course is refused", func(t *testing.T) {
		w := doAs(t, s, alice, http.MethodPost, "/environment/prepare", "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("prepare with no course status = %d, want 400; body = %s", w.Code, w.Body.String())
		}
	})
}

// TestEnvironmentPrepareNoWorkspaceAnswersUnavailable pins the honest refusal:
// with no environment manager the course still resolves, and the route answers
// the contract's ENVIRONMENT_UNAVAILABLE instead of inventing a prepared
// workspace.
func TestEnvironmentPrepareNoWorkspaceAnswersUnavailable(t *testing.T) {
	depot := store.NewMemory()
	s := New(engine.UnavailableExecutor{}, depot, Config{MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2}, pgtest.QuietLogger())
	s.UseAI(ai.Config{Depot: depot})
	token := signupUser(t, s, "prepare-unavailable@example.test")
	seedSetupCourse(t, depot, userIDFor(t, s, token), "course-unavailable")

	w := doAs(t, s, token, http.MethodPost, "/environment/prepare?course=course-unavailable", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("prepare with no workspace status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	var e gatewayErrEnvelope
	mustDecode(t, w, &e)
	if e.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("prepare error.code = %q, want %s", e.Error.Code, engine.CodeEnvironmentUnavailable)
	}
	if e.OK {
		t.Fatalf("prepare refusal reported ok = true")
	}
}
