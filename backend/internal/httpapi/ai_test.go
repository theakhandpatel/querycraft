package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* The AI surface, from the transport's point of view.
 *
 * The pipeline's own behaviour is proved in backend/internal/ai against a real
 * PostgreSQL. What belongs here is the part this package owns: an anonymous
 * caller is refused, the owner of a record comes from the session and not from
 * the request, and "AI is off" is reported as a capability with a reason
 * instead of as a broken feature. */

// recordingDepot captures what the owner-scoped wrapper writes.
type recordingDepot struct {
	saved        []schema.Record
	savedCourses []schema.CourseRecord
	patched      []string
}

func (d *recordingDepot) SaveChallenge(_ context.Context, rec schema.Record) error {
	d.saved = append(d.saved, rec)
	return nil
}
func (d *recordingDepot) Challenge(context.Context, string) (schema.Record, bool, error) {
	return schema.Record{}, false, nil
}
func (d *recordingDepot) Challenges(context.Context) ([]schema.Record, error) { return nil, nil }
func (d *recordingDepot) ChallengesFor(context.Context, string) ([]schema.Record, error) {
	return nil, nil
}
func (d *recordingDepot) DeleteChallenge(context.Context, string) error { return nil }
func (d *recordingDepot) SavePatch(context.Context, schema.PatchProposal) error {
	return nil
}
func (d *recordingDepot) Patch(_ context.Context, id string) (schema.PatchProposal, bool, error) {
	d.patched = append(d.patched, id)
	return schema.PatchProposal{}, false, nil
}
func (d *recordingDepot) ApprovePatch(context.Context, string, string) error { return nil }

func (d *recordingDepot) SaveCourse(_ context.Context, rec schema.CourseRecord) error {
	d.savedCourses = append(d.savedCourses, rec)
	return nil
}
func (d *recordingDepot) Course(context.Context, string) (schema.CourseRecord, bool, error) {
	return schema.CourseRecord{}, false, nil
}
func (d *recordingDepot) Courses(context.Context) ([]schema.CourseRecord, error) {
	return nil, nil
}
func (d *recordingDepot) CoursesFor(context.Context, string) ([]schema.CourseRecord, error) {
	return nil, nil
}
func (d *recordingDepot) DeleteCourse(context.Context, string) error { return nil }

func (d *recordingDepot) SaveRun(context.Context, schema.RunTranscript) error { return nil }
func (d *recordingDepot) Run(context.Context, string) (schema.RunTranscript, bool, error) {
	return schema.RunTranscript{}, false, nil
}
func (d *recordingDepot) Runs(context.Context) ([]schema.RunSummary, error) { return nil, nil }
func (d *recordingDepot) RunsFor(context.Context, string) ([]schema.RunSummary, error) {
	return nil, nil
}

func TestOwnerScopedDepotStampsTheSessionOwner(t *testing.T) {
	inner := &recordingDepot{}
	depot := ownerScopedDepot{Depot: inner, owner: "user-1"}
	// A caller can never name the owner: a record that arrives claiming someone
	// else is overwritten with the session's id.
	err := depot.SaveChallenge(context.Background(), schema.Record{
		Artifact: schema.Artifact{ID: "c1"},
		OwnerID:  "someone-else",
	})
	if err != nil {
		t.Fatalf("SaveChallenge: %v", err)
	}
	if len(inner.saved) != 1 {
		t.Fatalf("saved %d records, want 1", len(inner.saved))
	}
	if got := inner.saved[0].OwnerID; got != "user-1" {
		t.Fatalf("owner = %q, want the session owner", got)
	}
}

// The run budget is part of the capability surface, not a client-side guess.
//
// The browser's transport waits for the advertised budget plus slack before it
// gives up on a provider-backed call, so this number is what keeps "still
// working" from being drawn as "unreachable". A real course generation took
// 72 s and was reported as a dead gateway because the client kept the 15 s
// query deadline; the fix is that the client reads the budget off this field.
func TestCapabilitiesAdvertiseTheRunBudget(t *testing.T) {
	// AI off: generation cannot run, and the gateway still describes the
	// pipeline it would enforce rather than leaving a client to guess.
	off := newTestServer(&fakeExecutor{})
	w := do(t, off, http.MethodGet, "/capabilities", "")
	var offCaps struct {
		AI ai.Availability `json:"ai"`
	}
	mustDecode(t, w, &offCaps)
	if want := ai.DefaultLimits().Timeout.Milliseconds(); offCaps.AI.TimeoutMS != want {
		t.Fatalf("ai.timeout_ms = %d with AI off, want the default %d", offCaps.AI.TimeoutMS, want)
	}

	// AI on: the advertised budget is the configured one, not the package
	// default, so a client outlasts the deadline this gateway enforces.
	budget := 7 * time.Minute
	on := newTestServer(&fakeExecutor{})
	on.UseAI(ai.Config{
		Provider: ai.NewFakeProvider(),
		Depot:    &recordingDepot{},
		Limits:   ai.Limits{Timeout: budget},
	})
	w = do(t, on, http.MethodGet, "/capabilities", "")
	var onCaps struct {
		AI ai.Availability `json:"ai"`
	}
	mustDecode(t, w, &onCaps)
	if onCaps.AI.TimeoutMS != budget.Milliseconds() {
		t.Fatalf("ai.timeout_ms = %d, want the configured %d", onCaps.AI.TimeoutMS, budget.Milliseconds())
	}
	if onCaps.AI.TimeoutMS <= 0 {
		t.Fatal("a client cannot outlast a budget of zero")
	}
}

func TestAIOffIsReportedAsACapabilityNotAFailure(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "ai-off@example.test")

	// /capabilities carries the reason, so the client can hide the lab and say
	// why instead of offering a button that cannot work.
	w := do(t, s, http.MethodGet, "/capabilities", "")
	var caps struct {
		AI ai.Availability `json:"ai"`
	}
	mustDecode(t, w, &caps)
	if caps.AI.Available || caps.AI.Configured {
		t.Fatalf("capabilities.ai = %+v, want unavailable", caps.AI)
	}
	if caps.AI.Reason == "" {
		t.Fatalf("capabilities.ai carries no reason: %+v", caps.AI)
	}

	// Generation answers with the honest envelope, never a fabricated artifact.
	for _, path := range []string{"/challenges/generate", "/courses/generate"} {
		w := doAs(t, s, token, http.MethodPost, path, `{"topics":["joins"]}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, w.Code)
		}
		var out schema.Outcome
		mustDecode(t, w, &out)
		if out.Status != schema.StatusUnavailable {
			t.Fatalf("%s status = %q, want %q", path, out.Status, schema.StatusUnavailable)
		}
		if out.Artifact != nil {
			t.Fatalf("%s returned an artifact while AI is off", path)
		}
		if out.Error == nil || out.Error.Code != ai.CodeUnavailable {
			t.Fatalf("%s error = %+v, want %s", path, out.Error, ai.CodeUnavailable)
		}
	}

	// The exit gate's rule: disabling AI leaves every other feature working.
	if w := do(t, s, http.MethodGet, "/health", ""); w.Code != http.StatusOK {
		t.Fatalf("health status = %d with AI off", w.Code)
	}
	if w := do(t, s, http.MethodGet, "/datasets/commerce/tables", ""); w.Code != http.StatusOK {
		t.Fatalf("tables status = %d with AI off", w.Code)
	}
	if w := do(t, s, http.MethodPost, "/query", `{"sql":"SELECT 1"}`); w.Code != http.StatusOK {
		t.Fatalf("query status = %d with AI off", w.Code)
	}
}

func TestAIChallengeRoutesRefuseAnonymousCallers(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/challenges/generate", `{"topics":["joins"]}`},
		{http.MethodPost, "/courses/generate", `{"title":"Joins"}`},
		{http.MethodGet, "/challenges", ""},
		{http.MethodGet, "/challenges/c1", ""},
		{http.MethodPost, "/challenges/c1/reset", ""},
		{http.MethodPost, "/challenges/c1/critique", `{"sql":"SELECT 1"}`},
		{http.MethodPost, "/challenges/c1/report", `{"description":"wrong rows"}`},
		{http.MethodPost, "/challenges/c1/patches/p1", `{"approved":true}`},
		{http.MethodPost, "/tutor", `{"sql":"SELECT 1","objective":"count"}`},
	}
	for _, rt := range routes {
		w := do(t, s, rt.method, rt.path, rt.body)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", rt.method, rt.path, w.Code)
		}
	}
}

func TestPatchDecisionIgnoresBodyIdentity(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/challenges/path-challenge/patches/path-patch", nil)
	r.SetPathValue("id", "path-challenge")
	r.SetPathValue("patchID", "path-patch")

	dec := patchRouteDecision(ai.PatchDecision{
		PatchID:     "body-patch",
		ChallengeID: "body-challenge",
		Approved:    true,
		ApprovedBy:  "attacker@example.test",
	}, r, "session@example.test")

	if dec.PatchID != "path-patch" {
		t.Errorf("PatchID = %q, want the path id", dec.PatchID)
	}
	if dec.ChallengeID != "path-challenge" {
		t.Errorf("ChallengeID = %q, want the path challenge", dec.ChallengeID)
	}
	if dec.ApprovedBy != "session@example.test" {
		t.Errorf("ApprovedBy = %q, want the session account", dec.ApprovedBy)
	}
	if !dec.Approved {
		t.Error("the body's approval flag was discarded")
	}
}

func TestPatchApprovalLoadsThePatchFromThePath(t *testing.T) {
	depot := &recordingDepot{}
	s := newTestServer(&fakeExecutor{})
	s.UseAI(ai.Config{
		Provider: ai.NewFakeProvider(),
		Depot:    depot,
		Runner:   stubRunner{exec: &fakeExecutor{}},
	})
	token := signupUser(t, s, "patch-path@example.test")

	w := doAs(t, s, token, http.MethodPost, "/challenges/path-challenge/patches/path-patch",
		`{"patch_id":"body-patch","challenge_id":"body-challenge","approved":true,"approved_by":"attacker@example.test"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404 for the missing path patch", w.Code, w.Body.String())
	}
	if len(depot.patched) != 1 || depot.patched[0] != "path-patch" {
		t.Fatalf("patch lookup = %v, want [path-patch]", depot.patched)
	}
}

func TestStoredChallengeRoutesAreHonestWithAIOff(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "ai-off-routes@example.test")

	// With no service attached there is nothing to read, and saying so is
	// better than an empty list that looks like "you have generated nothing".
	w := doAs(t, s, token, http.MethodGet, "/challenges", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("list status = %d, want 503", w.Code)
	}
	w = doAs(t, s, token, http.MethodGet, "/challenges/c1", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("get status = %d, want 503", w.Code)
	}
	w = doAs(t, s, token, http.MethodPost, "/tutor", `{"sql":"SELECT 1","objective":"count"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("tutor status = %d, want 503", w.Code)
	}
}

func TestOwnerScopedDepotStampsTheCourseOwner(t *testing.T) {
	inner := &recordingDepot{}
	depot := ownerScopedDepot{Depot: inner, owner: "user-1"}
	// The same rule as challenges: a generated course is stored for the
	// session's account, whatever the pipeline or the request claims.
	if err := depot.SaveCourse(context.Background(), schema.CourseRecord{
		Plan:    schema.CoursePlan{ID: "course-1"},
		OwnerID: "someone-else",
	}); err != nil {
		t.Fatalf("SaveCourse: %v", err)
	}
	if len(inner.savedCourses) != 1 {
		t.Fatalf("saved %d courses, want 1", len(inner.savedCourses))
	}
	if got := inner.savedCourses[0].OwnerID; got != "user-1" {
		t.Fatalf("owner = %q, want the session owner", got)
	}
}

/* Stored courses, from the transport's point of view. The pipeline's own
 * behaviour is proved in backend/internal/ai against a real PostgreSQL. What
 * belongs here is that a course is stored for the account that asked for it,
 * and that no id lets a learner read or delete another learner's course. */

// userIDFor reads the id of the account a token belongs to, the way a browser
// does: from the session, never from the request.
func userIDFor(t *testing.T, s *Server, token string) string {
	t.Helper()
	w := doAs(t, s, token, http.MethodGet, "/auth/me", "")
	if w.Code != http.StatusOK {
		t.Fatalf("me status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		User store.User `json:"user"`
	}
	mustDecode(t, w, &out)
	if out.User.ID == "" {
		t.Fatalf("me returned no id: %s", w.Body.String())
	}
	return out.User.ID
}

// seedCourse writes a course the way the pipeline does — through the
// owner-scoped wrapper — so the reads below exercise really stored state.
func seedCourse(t *testing.T, depot schema.Depot, owner, id, title string, created time.Time) {
	t.Helper()
	err := ownerScopedDepot{Depot: depot, owner: owner}.SaveCourse(context.Background(), schema.CourseRecord{
		Plan:      schema.CoursePlan{ID: id, Title: title},
		Outcome:   schema.Outcome{Status: schema.StatusActive, Kind: "course"},
		Status:    schema.StatusActive,
		CreatedAt: created,
	})
	if err != nil {
		t.Fatalf("seed course %s: %v", id, err)
	}
}

// courseServer is a gateway with AI attached to an in-memory depot, so the
// stored-course routes have somewhere real to read from.
func courseServer(t *testing.T) (*Server, schema.Depot) {
	t.Helper()
	depot := store.NewMemory()
	s := newTestServer(&fakeExecutor{})
	s.UseAI(ai.Config{Depot: depot})
	return s, depot
}

func TestStoredCoursesAreScopedToTheirOwner(t *testing.T) {
	s, depot := courseServer(t)
	aliceToken := signupUser(t, s, "courses-alice@example.test")
	bobToken := signupUser(t, s, "courses-bob@example.test")
	alice := userIDFor(t, s, aliceToken)
	bob := userIDFor(t, s, bobToken)
	now := time.Now().UTC()
	seedCourse(t, depot, alice, "course-alice", "Joins for Alice", now)
	seedCourse(t, depot, bob, "course-bob", "Windows for Bob", now.Add(time.Second))

	// Alice's list holds her course and nothing else, with no owner field in
	// the body: the account is the session's, not the payload's.
	w := doAs(t, s, aliceToken, http.MethodGet, "/courses", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", w.Code, w.Body.String())
	}
	var list struct {
		OK      bool                  `json:"ok"`
		Courses []schema.CourseRecord `json:"courses"`
	}
	mustDecode(t, w, &list)
	if !list.OK {
		t.Fatalf("list ok = false: %s", w.Body.String())
	}
	if len(list.Courses) != 1 || list.Courses[0].Plan.ID != "course-alice" {
		t.Fatalf("alice's list = %+v, want only course-alice", list.Courses)
	}
	if list.Courses[0].Plan.Title != "Joins for Alice" {
		t.Fatalf("plan = %+v, want the stored plan", list.Courses[0].Plan)
	}
	if list.Courses[0].OwnerID != "" {
		t.Fatalf("course body leaks the owner: %q", list.Courses[0].OwnerID)
	}

	// Her own course is fetchable by its id.
	w = doAs(t, s, aliceToken, http.MethodGet, "/courses/course-alice", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", w.Code, w.Body.String())
	}
	var got struct {
		OK     bool                `json:"ok"`
		Course schema.CourseRecord `json:"course"`
	}
	mustDecode(t, w, &got)
	if !got.OK || got.Course.Plan.ID != "course-alice" {
		t.Fatalf("get = %s, want course-alice", w.Body.String())
	}
	if got.Course.OwnerID != "" {
		t.Fatalf("get leaks the owner: %q", got.Course.OwnerID)
	}

	// Bob's course does not exist as far as Alice can tell: 404, never 403,
	// because a 403 would confirm that the id names something.
	w = doAs(t, s, aliceToken, http.MethodGet, "/courses/course-bob", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner get status = %d, want 404", w.Code)
	}

	// And Bob still sees exactly his own course.
	w = doAs(t, s, bobToken, http.MethodGet, "/courses", "")
	mustDecode(t, w, &list)
	if len(list.Courses) != 1 || list.Courses[0].Plan.ID != "course-bob" {
		t.Fatalf("bob's list = %+v, want only course-bob", list.Courses)
	}
}

func TestCourseListIsNewestFirst(t *testing.T) {
	s, depot := courseServer(t)
	token := signupUser(t, s, "courses-order@example.test")
	owner := userIDFor(t, s, token)
	now := time.Now().UTC()
	seedCourse(t, depot, owner, "course-old", "First", now.Add(-time.Hour))
	seedCourse(t, depot, owner, "course-new", "Second", now)

	w := doAs(t, s, token, http.MethodGet, "/courses", "")
	var list struct {
		Courses []schema.CourseRecord `json:"courses"`
	}
	mustDecode(t, w, &list)
	if len(list.Courses) != 2 {
		t.Fatalf("list = %s, want two courses", w.Body.String())
	}
	if list.Courses[0].Plan.ID != "course-new" || list.Courses[1].Plan.ID != "course-old" {
		t.Fatalf("order = %q, %q; want newest first", list.Courses[0].Plan.ID, list.Courses[1].Plan.ID)
	}
}

func TestDeleteCourseRemovesOnlyTheCallersCourse(t *testing.T) {
	s, depot := courseServer(t)
	aliceToken := signupUser(t, s, "courses-del-alice@example.test")
	bobToken := signupUser(t, s, "courses-del-bob@example.test")
	alice := userIDFor(t, s, aliceToken)
	bob := userIDFor(t, s, bobToken)
	now := time.Now().UTC()
	seedCourse(t, depot, alice, "course-alice", "Joins for Alice", now)
	seedCourse(t, depot, bob, "course-bob", "Windows for Bob", now)

	// Alice cannot delete Bob's course, and the answer does not admit it
	// exists.
	w := doAs(t, s, aliceToken, http.MethodDelete, "/courses/course-bob", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete status = %d, want 404", w.Code)
	}
	if _, found, err := depot.Course(context.Background(), "course-bob"); err != nil || !found {
		t.Fatalf("bob's course was removed by alice: found=%v err=%v", found, err)
	}

	// Her own course goes, and the answer says so.
	w = doAs(t, s, aliceToken, http.MethodDelete, "/courses/course-alice", "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body = %s", w.Code, w.Body.String())
	}
	var okResp struct {
		OK bool `json:"ok"`
	}
	mustDecode(t, w, &okResp)
	if !okResp.OK {
		t.Fatalf("delete = %s, want ok", w.Body.String())
	}
	if _, found, err := depot.Course(context.Background(), "course-alice"); err != nil || found {
		t.Fatalf("course still stored after delete: found=%v err=%v", found, err)
	}

	// Bob's list is untouched by any of it.
	w = doAs(t, s, bobToken, http.MethodGet, "/courses", "")
	var list struct {
		Courses []schema.CourseRecord `json:"courses"`
	}
	mustDecode(t, w, &list)
	if len(list.Courses) != 1 || list.Courses[0].Plan.ID != "course-bob" {
		t.Fatalf("bob's list = %+v, want only course-bob", list.Courses)
	}
}

func TestStoredCourseRoutesRefuseAnonymousCallers(t *testing.T) {
	s, _ := courseServer(t)
	for _, rt := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/courses"},
		{http.MethodGet, "/courses/c1"},
		{http.MethodDelete, "/courses/c1"},
	} {
		if w := do(t, s, rt.method, rt.path, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", rt.method, rt.path, w.Code)
		}
	}
}

func TestStoredCourseRoutesAreHonestWithAIOff(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "ai-off-courses@example.test")

	// With no service attached every route says so, and the answer is 503
	// rather than an empty list that looks like "you have generated nothing".
	w := doAs(t, s, token, http.MethodGet, "/courses", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("list status = %d, want 503", w.Code)
	}
	// `courses` is an empty array, not null: a client can iterate it without
	// a shape test, and nothing was invented.
	if !strings.Contains(w.Body.String(), `"courses":[]`) {
		t.Fatalf("list body = %s, want an empty courses array", w.Body.String())
	}
	var list struct {
		OK      bool                  `json:"ok"`
		Courses []schema.CourseRecord `json:"courses"`
	}
	mustDecode(t, w, &list)
	if list.OK || len(list.Courses) != 0 {
		t.Fatalf("list with AI off = %s, want ok=false and no courses", w.Body.String())
	}

	for _, rt := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/courses/c1"},
		{http.MethodDelete, "/courses/c1"},
	} {
		if w := doAs(t, s, token, rt.method, rt.path, ""); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503", rt.method, rt.path, w.Code)
		}
	}
}
