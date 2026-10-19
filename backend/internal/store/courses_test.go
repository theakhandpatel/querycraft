package store

import (
	"context"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* Generated courses, from the store's point of view.
 *
 * The one thing this package owns for M10 is the request that produced a
 * course. A course can pause after its outline and be continued later, and the
 * chapter phase rebuilds its own prompt from the learner's own words — so the
 * request has to survive the round trip, and a row written before it was kept
 * has to read back as a course with no request rather than failing. */

// sampleCourse is a paused course as the pipeline would store it: the outline
// as a `building` plan, the request that produced it, and the run report so far.
func sampleCourse(id, title string, at time.Time) schema.CourseRecord {
	req := schema.GenerationRequest{
		Kind:            "course",
		Title:           "Get me fluent with window functions",
		Topics:          []string{"window functions", "rankings"},
		Difficulty:      schema.Intermediate,
		Scenario:        "Count what each customer ordered, then rank the results.",
		ExerciseCount:   6,
		RequireApproval: true,
	}
	return schema.CourseRecord{
		Plan: schema.CoursePlan{
			ID:         id,
			Version:    1,
			Title:      title,
			Subtitle:   "Two chapters, planned but not yet written.",
			Level:      "intermediate",
			Dialect:    "postgresql",
			Objectives: []string{"rank within a group"},
			Setup:      schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1, Statements: []string{}},
			Chapters: []schema.CourseChapter{
				{ID: "gen.one", N: 1, Title: "Chapter one", Summary: "One.", Objectives: []string{"do one"}},
				{ID: "gen.two", N: 2, Title: "Chapter two", Summary: "Two.", Objectives: []string{"do two"}},
			},
		},
		Outcome:   schema.Outcome{Status: schema.StatusBuilding, Kind: "course", Chapters: []schema.ChapterResult{}},
		Status:    schema.StatusBuilding,
		CreatedAt: at,
		Request:   &req,
	}
}

func TestMemoryCourseRoundTripKeepsTheRequestDeepCopied(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	rec := sampleCourse("course_1", "Window functions", time.Now().UTC())

	if err := m.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := m.Course(ctx, "course_1")
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Request == nil || got.Request.Title != rec.Request.Title {
		t.Fatalf("the request did not survive: %+v", got.Request)
	}
	if !got.Request.RequireApproval {
		t.Fatal("the pause flag did not survive")
	}

	// Mutating what a caller got back must not corrupt the store: the request
	// is a pointer, so it is deep-copied like every other slice.
	got.Request.Title = "mutated"
	got.Request.Topics[0] = "mutated"
	again, _, _ := m.Course(ctx, "course_1")
	if again.Request.Title != rec.Request.Title || again.Request.Topics[0] != "window functions" {
		t.Fatalf("the store returned an aliased request: %+v", again.Request)
	}
}

// TestMemoryCourseWithoutARequestIsStillReadable is the legacy case: a row
// stored before the request was kept carries none, and reading it must not
// invent one or fail.
func TestMemoryCourseWithoutARequestIsStillReadable(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	rec := sampleCourse("course_legacy", "A course from before", time.Now().UTC())
	rec.Request = nil

	if err := m.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := m.Course(ctx, "course_legacy")
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Request != nil {
		t.Fatalf("a course stored without a request came back with one: %+v", got.Request)
	}
	if got.Plan.Title != "A course from before" {
		t.Fatalf("the plan did not survive: %+v", got.Plan)
	}
}

// TestPostgresCourseRequestSurvivesARestart is the load-bearing one: a paused
// course is continued by a later request, possibly in another process, so the
// request has to be on disk and not only in the memory store.
func TestPostgresCourseRequestSurvivesARestart(t *testing.T) {
	srv := pgtest.Start(t)
	ctx := context.Background()

	first, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	rec := sampleCourse("course_restart", "Survives a restart", time.Now().UTC())
	if err := first.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	first.Close()

	// A second handle is a new process as far as the data is concerned.
	second, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer second.Close()

	got, ok, err := second.Course(ctx, "course_restart")
	if err != nil || !ok {
		t.Fatalf("after restart ok=%v err=%v", ok, err)
	}
	if got.Status != schema.StatusBuilding {
		t.Fatalf("status = %q, want %q", got.Status, schema.StatusBuilding)
	}
	if got.Request == nil {
		t.Fatal("the request that produced the course was not persisted")
	}
	if got.Request.Title != rec.Request.Title || got.Request.Scenario != rec.Request.Scenario {
		t.Fatalf("the persisted request = %+v, want the learner's own words", got.Request)
	}
	if got.Request.ExerciseCount != rec.Request.ExerciseCount || !got.Request.RequireApproval {
		t.Fatalf("the persisted request lost its budget or the pause flag: %+v", got.Request)
	}
	if len(got.Request.Topics) != len(rec.Request.Topics) {
		t.Fatalf("the persisted request lost its topics: %+v", got.Request.Topics)
	}
	if len(got.Plan.Chapters) != len(rec.Plan.Chapters) {
		t.Fatalf("the persisted outline lost chapters: %+v", got.Plan.Chapters)
	}
}

// TestPostgresCourseWithoutARequestReadsBackWithNone: the request column is
// nullable, so a row written before it existed (or by a client that stored no
// request) reads back as a course with no request rather than as a storage
// fault. The resume path handles the nil, so the read must not fail.
func TestPostgresCourseWithoutARequestReadsBackWithNone(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx := context.Background()
	st, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rec := sampleCourse("course_no_request", "No request kept", time.Now().UTC())
	rec.Request = nil
	if err := st.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := st.Course(ctx, "course_no_request")
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Request != nil {
		t.Fatalf("a NULL request column came back as %+v, want nil", got.Request)
	}
	if got.Plan.Title != "No request kept" {
		t.Fatalf("the plan did not survive: %+v", got.Plan)
	}
	// And it is still listed, whole.
	list, err := st.Courses(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Plan.ID != "course_no_request" || list[0].Request != nil {
		t.Fatalf("list = %+v", list)
	}
}
