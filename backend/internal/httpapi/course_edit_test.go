package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* PUT /courses/{id}, from the transport's point of view. An edit revises the
 * authored fields of a stored plan and nothing else: the verification, the
 * setup and the provenance stay exactly as the pipeline left them, and a
 * course that is not the caller's stays a 404. */

// seedEditableCourse writes a course the way the pipeline does, with the
// authored fields an edit route changes plus the state it must preserve:
// a status, an outcome, a generation request and a created_at.
func seedEditableCourse(t *testing.T, depot schema.Depot, owner string, created time.Time) {
	t.Helper()
	err := ownerScopedDepot{Depot: depot, owner: owner}.SaveCourse(context.Background(), schema.CourseRecord{
		Plan: schema.CoursePlan{
			ID:       "course-edit",
			Title:    "Old title",
			Subtitle: "Old subtitle",
			Chapters: []schema.CourseChapter{
				{ID: "ch-1", N: 1, Title: "Old chapter", Summary: "Old summary"},
			},
		},
		Outcome:   schema.Outcome{Status: schema.StatusActive, Kind: "course"},
		Status:    schema.StatusActive,
		CreatedAt: created,
		Request:   &schema.GenerationRequest{Title: "the learner's goal"},
	})
	if err != nil {
		t.Fatalf("seed editable course: %v", err)
	}
}

func TestPutCourseEditsATitleAndAChapter(t *testing.T) {
	s, depot := courseServer(t)
	token := signupUser(t, s, "course-edit-owner@example.test")
	owner := userIDFor(t, s, token)
	created := time.Now().UTC().Truncate(time.Second)
	seedEditableCourse(t, depot, owner, created)

	body := `{"title":"New title","chapters":[{"id":"ch-1","title":"New chapter","summary":"New summary"}]}`
	w := doAs(t, s, token, http.MethodPut, "/courses/course-edit", body)
	if w.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", w.Code, w.Body.String())
	}
	var put struct {
		OK     bool                `json:"ok"`
		Course schema.CourseRecord `json:"course"`
	}
	mustDecode(t, w, &put)
	if !put.OK {
		t.Fatalf("put ok = false: %s", w.Body.String())
	}
	if put.Course.Plan.Title != "New title" || put.Course.Plan.Chapters[0].Title != "New chapter" || put.Course.Plan.Chapters[0].Summary != "New summary" {
		t.Fatalf("put plan = %+v, want both edits in the reply", put.Course.Plan)
	}
	if put.Course.OwnerID != "" {
		t.Fatalf("put leaks the owner: %q", put.Course.OwnerID)
	}

	// The write is real: a fresh read reflects both fields.
	w = doAs(t, s, token, http.MethodGet, "/courses/course-edit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Course schema.CourseRecord `json:"course"`
	}
	mustDecode(t, w, &got)
	if got.Course.Plan.Title != "New title" {
		t.Fatalf("stored title = %q, want the edit", got.Course.Plan.Title)
	}
	if got.Course.Plan.Subtitle != "Old subtitle" {
		t.Fatalf("stored subtitle = %q, want the untouched value", got.Course.Plan.Subtitle)
	}
	if got.Course.Plan.Chapters[0].Title != "New chapter" || got.Course.Plan.Chapters[0].Summary != "New summary" {
		t.Fatalf("stored chapter = %+v, want the edit", got.Course.Plan.Chapters[0])
	}

	// The edit changed the plan only: everything else the record carried is
	// still the value it was stored with.
	if got.Course.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q", got.Course.Status, schema.StatusActive)
	}
	if got.Course.Outcome.Status != schema.StatusActive || got.Course.Outcome.Kind != "course" {
		t.Fatalf("outcome = %+v, want the stored one", got.Course.Outcome)
	}
	if got.Course.Request == nil || got.Course.Request.Title != "the learner's goal" {
		t.Fatalf("request = %+v, want the stored one", got.Course.Request)
	}
	if got.Course.CreatedAt.Unix() != created.Unix() {
		t.Fatalf("created_at = %s, want %s", got.Course.CreatedAt, created)
	}
}

func TestPutCourseRefusesAnotherOwnersCourse(t *testing.T) {
	s, depot := courseServer(t)
	aliceToken := signupUser(t, s, "course-edit-alice@example.test")
	bobToken := signupUser(t, s, "course-edit-bob@example.test")
	bob := userIDFor(t, s, bobToken)
	created := time.Now().UTC().Truncate(time.Second)
	seedEditableCourse(t, depot, bob, created)

	// Alice cannot edit Bob's course, and the answer does not admit it exists.
	w := doAs(t, s, aliceToken, http.MethodPut, "/courses/course-edit", `{"title":"Hijacked"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner put status = %d body = %s, want 404", w.Code, w.Body.String())
	}
	rec, found, err := depot.Course(context.Background(), "course-edit")
	if err != nil || !found {
		t.Fatalf("bob's course is gone: found=%v err=%v", found, err)
	}
	if rec.Plan.Title != "Old title" {
		t.Fatalf("bob's title = %q, want it untouched", rec.Plan.Title)
	}
}

func TestPutCourseRefusesAnUnknownChapter(t *testing.T) {
	s, depot := courseServer(t)
	token := signupUser(t, s, "course-edit-chapter@example.test")
	owner := userIDFor(t, s, token)
	seedEditableCourse(t, depot, owner, time.Now().UTC())

	w := doAs(t, s, token, http.MethodPut, "/courses/course-edit", `{"chapters":[{"id":"nope","title":"Nowhere"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown chapter status = %d body = %s, want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "AI_INVALID_REQUEST") || !strings.Contains(w.Body.String(), "nope") {
		t.Fatalf("refusal = %s, want AI_INVALID_REQUEST naming the chapter", w.Body.String())
	}

	// A body that repeats the stored values is refused for the same reason as
	// one that names nothing: a write that changes nothing is not an edit.
	w = doAs(t, s, token, http.MethodPut, "/courses/course-edit", `{"title":"Old title"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no-op status = %d body = %s, want 400", w.Code, w.Body.String())
	}
}

func TestPutCourseRefusesAMalformedBody(t *testing.T) {
	s, depot := courseServer(t)
	token := signupUser(t, s, "course-edit-malformed@example.test")
	owner := userIDFor(t, s, token)
	seedEditableCourse(t, depot, owner, time.Now().UTC())

	w := doAs(t, s, token, http.MethodPut, "/courses/course-edit", `{"title":`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d body = %s, want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "AI_INVALID_REQUEST") {
		t.Fatalf("malformed refusal = %s, want AI_INVALID_REQUEST", w.Body.String())
	}
}
