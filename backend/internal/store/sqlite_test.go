package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

type storeFactory func(t *testing.T) Store

// TestPersistentStoreParity applies the same behavior cases to the Postgres
// reference and the opt-in SQLite implementation. This pins the additive
// backend to the contract already used by Postgres.
func TestPersistentStoreParity(t *testing.T) {
	srv := pgtest.Start(t)
	factories := map[string]storeFactory{
		"postgres": func(t *testing.T) Store {
			st, err := NewPostgres(context.Background(), srv.DSN)
			if err != nil {
				t.Fatalf("open postgres: %v", err)
			}
			if _, err := st.pool.Exec(context.Background(), `TRUNCATE app.runs, app.patches, app.courses, app.challenges, app.events, app.progress, app.sessions, app.users CASCADE`); err != nil {
				t.Fatalf("clear parity database: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			return st
		},
		"sqlite": func(t *testing.T) Store {
			st, err := NewSQLite(context.Background(), filepath.Join(t.TempDir(), "metadata.sqlite"))
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			return st
		},
		// Degraded mode is the SQLite backend over an in-memory database, so
		// the same cases cover it. It is listed here so that stays true.
		"memory": func(t *testing.T) Store {
			return NewMemory()
		},
	}

	tests := []struct {
		name string
		run  func(t *testing.T, st Store)
	}{
		{name: "round-trip and deep copy", run: parityRoundTrip},
		{name: "newest-first ordering", run: parityNewestFirst},
		{name: "owner carried", run: parityOwnerCarried},
		{name: "upsert", run: parityUpsert},
		{name: "legacy nullable request and absent rows", run: parityLegacyAndAbsent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for name, factory := range factories {
				t.Run(name, func(t *testing.T) { tc.run(t, factory(t)) })
			}
		})
	}
}

func parityRoundTrip(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	rec := sampleRecord("ch_parity", "Parity challenge", at)
	rec.OwnerID = "u_alice"
	if err := st.SaveChallenge(ctx, rec); err != nil {
		t.Fatalf("save challenge: %v", err)
	}
	got, ok, err := st.Challenge(ctx, rec.Artifact.ID)
	if err != nil || !ok {
		t.Fatalf("challenge ok=%v err=%v", ok, err)
	}
	if got.OwnerID != "u_alice" || !got.CreatedAt.Equal(at) || got.Artifact.Title != rec.Artifact.Title || got.Outcome.Stages[0].Detail != rec.Outcome.Stages[0].Detail {
		t.Fatalf("challenge round trip = %+v", got)
	}
	got.Artifact.Hints[0] = "caller mutation"
	got.Outcome.Stages[0].Detail = "caller mutation"
	again, _, err := st.Challenge(ctx, rec.Artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Artifact.Hints[0] != rec.Artifact.Hints[0] || again.Outcome.Stages[0].Detail != rec.Outcome.Stages[0].Detail {
		t.Fatalf("stored challenge was aliased: %+v", again)
	}

	course := sampleCourse("course_parity", "Course parity", at)
	course.OwnerID = "u_alice"
	if err := st.SaveCourse(ctx, course); err != nil {
		t.Fatalf("save course: %v", err)
	}
	gotCourse, ok, err := st.Course(ctx, course.Plan.ID)
	if err != nil || !ok || gotCourse.OwnerID != "u_alice" || !gotCourse.CreatedAt.Equal(at) || gotCourse.Request == nil {
		t.Fatalf("course round trip=%+v ok=%v err=%v", gotCourse, ok, err)
	}
	gotCourse.Request.Topics[0] = "caller mutation"
	againCourse, _, err := st.Course(ctx, course.Plan.ID)
	if err != nil || againCourse.Request.Topics[0] != course.Request.Topics[0] {
		t.Fatalf("stored course request was aliased: %+v err=%v", againCourse.Request, err)
	}

	run := sampleRun("run_deep", "u_alice", schema.StatusActive, at)
	if err := st.SaveRun(ctx, run); err != nil {
		t.Fatalf("save run: %v", err)
	}
	gotRun, ok, err := st.Run(ctx, run.ID)
	if err != nil || !ok {
		t.Fatalf("run round trip ok=%v err=%v", ok, err)
	}
	gotRun.Entries[0].Content = "caller mutation"
	againRun, _, err := st.Run(ctx, run.ID)
	if err != nil || againRun.Entries[0].Content != run.Entries[0].Content {
		t.Fatalf("stored run was aliased: %+v err=%v", againRun, err)
	}

	if err := st.CreateUser(ctx, User{ID: "u_progress", Email: "progress@example.test", Name: "Progress"}, "hash"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.CreateSession(ctx, "session_parity", "u_progress", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessionUser, ok, err := st.SessionUser(ctx, "session_parity")
	if err != nil || !ok || sessionUser.ID != "u_progress" {
		t.Fatalf("session user=%+v ok=%v err=%v", sessionUser, ok, err)
	}
	if err := st.SetPasswordHash(ctx, "u_progress", "new-hash"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	_, hash, ok, err := st.UserByEmail(ctx, "PROGRESS@example.test")
	if err != nil || !ok || hash != "new-hash" {
		t.Fatalf("user hash=%q ok=%v err=%v", hash, ok, err)
	}
	if err := st.DeleteSession(ctx, "session_parity"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, ok, err := st.SessionUser(ctx, "session_parity"); err != nil || ok {
		t.Fatalf("deleted session ok=%v err=%v", ok, err)
	}
	if err := st.CreateUser(ctx, User{ID: "u_progress_dup", Email: "PROGRESS@example.test", Name: "Duplicate"}, "hash"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email error=%v, want ErrEmailTaken", err)
	}
	snapshot := json.RawMessage(`{"lesson":3,"done":[true,false]}`)
	if err := st.SaveProgress(ctx, "u_progress", "course", snapshot); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	loaded, ok, err := st.LoadProgress(ctx, "u_progress", "course")
	if err != nil || !ok || !sameJSON(loaded, snapshot) {
		t.Fatalf("progress = %s ok=%v err=%v", loaded, ok, err)
	}
	loaded[0] = 'x'
	loaded, _, _ = st.LoadProgress(ctx, "u_progress", "course")
	if loaded[0] == 'x' {
		t.Fatal("progress read aliases stored JSON")
	}
}

func parityNewestFirst(t *testing.T, st Store) {
	ctx := context.Background()
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, id := range []string{"run_old", "run_mid", "run_new"} {
		if err := st.SaveRun(ctx, sampleRun(id, "u_alice", schema.StatusActive, base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("save run: %v", err)
		}
	}
	list, err := st.RunsFor(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].ID != "run_new" || list[2].ID != "run_old" {
		t.Fatalf("run order = %+v", list)
	}

	for i, id := range []string{"ch_old", "ch_new"} {
		if err := st.SaveChallenge(ctx, sampleRecord(id, id, base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("save challenge: %v", err)
		}
	}
	challenges, err := st.ChallengesFor(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(challenges) != 2 || challenges[0].Artifact.ID != "ch_new" || challenges[1].Artifact.ID != "ch_old" {
		t.Fatalf("challenge order = %+v", challenges)
	}
	for i, id := range []string{"course_old", "course_new"} {
		course := sampleCourse(id, id, base.Add(time.Duration(i)*time.Minute))
		if err := st.SaveCourse(ctx, course); err != nil {
			t.Fatalf("save course: %v", err)
		}
	}
	courses, err := st.Courses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(courses) != 2 || courses[0].Plan.ID != "course_new" || courses[1].Plan.ID != "course_old" {
		t.Fatalf("course order=%+v", courses)
	}
}

func parityOwnerCarried(t *testing.T, st Store) {
	ctx := context.Background()
	for _, owner := range []string{"u_alice", "u_bob"} {
		if err := st.SaveRun(ctx, sampleRun("run_"+owner, owner, schema.StatusActive, time.Now().UTC())); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	rec, ok, err := st.Run(ctx, "run_u_bob")
	if err != nil || !ok || rec.OwnerID != "u_bob" {
		t.Fatalf("run owner=%q ok=%v err=%v", rec.OwnerID, ok, err)
	}
	list, err := st.RunsFor(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, summary := range list {
		owners[summary.ID] = summary.OwnerID
	}
	if owners["run_u_alice"] != "u_alice" || owners["run_u_bob"] != "u_bob" {
		t.Fatalf("owners=%v", owners)
	}
}

func parityUpsert(t *testing.T, st Store) {
	ctx := context.Background()
	start := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	first := sampleRun("run_upsert", "u_alice", schema.StatusActive, start)
	if err := st.SaveRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := sampleRun("run_upsert", "u_bob", schema.StatusFailed, start.Add(time.Hour))
	second.Entries = second.Entries[:1]
	second.EntryCount = 1
	if err := st.SaveRun(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Run(ctx, "run_upsert")
	if err != nil || !ok {
		t.Fatalf("run ok=%v err=%v", ok, err)
	}
	if len(got.Entries) != 1 || got.OwnerID != "u_bob" || !got.StartedAt.Equal(start) {
		t.Fatalf("upsert result=%+v", got)
	}
	list, err := st.RunsFor(ctx, "")
	if err != nil || len(list) != 1 {
		t.Fatalf("run list=%d err=%v", len(list), err)
	}

	challenge := sampleRecord("ch_upsert", "first", start)
	if err := st.SaveChallenge(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	challenge.Artifact.Title = "second"
	if err := st.SaveChallenge(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	gotChallenge, ok, err := st.Challenge(ctx, "ch_upsert")
	if err != nil || !ok || gotChallenge.Artifact.Title != "second" {
		t.Fatalf("challenge upsert=%+v ok=%v err=%v", gotChallenge, ok, err)
	}

	proposal := schema.PatchProposal{ID: "patch_1", ChallengeID: "ch_upsert", Summary: "update contract", Changes: []schema.PatchChange{{Field: "row_order", From: "exact", To: "ignored"}}}
	if err := st.SavePatch(ctx, proposal); err != nil {
		t.Fatalf("save patch: %v", err)
	}
	if err := st.ApprovePatch(ctx, proposal.ID, "alice"); err != nil {
		t.Fatalf("approve patch: %v", err)
	}
	if err := st.ApprovePatch(ctx, proposal.ID, "bob"); err != nil {
		t.Fatalf("second approve patch: %v", err)
	}
	proposal.Summary = "updated text"
	if err := st.SavePatch(ctx, proposal); err != nil {
		t.Fatalf("upsert patch: %v", err)
	}
	gotPatch, ok, err := st.Patch(ctx, proposal.ID)
	if err != nil || !ok || gotPatch.Summary != "updated text" {
		t.Fatalf("patch=%+v ok=%v err=%v", gotPatch, ok, err)
	}
	var approvedBy string
	if shared := sharedStore(st); shared == nil {
		t.Fatalf("no shared statements for %T", st)
	} else if err := shared.queryRow(ctx, shared.stmt(`SELECT approved_by FROM %s WHERE id = ?`, "patches"), proposal.ID).Scan(&approvedBy); err != nil {
		t.Fatal(err)
	}
	if approvedBy != "alice" {
		t.Fatalf("first approver=%q, want alice", approvedBy)
	}
}

// sharedStore returns the sqlStore every backend embeds, so a test that needs
// one raw read does not spell a query per concrete type.
func sharedStore(st Store) *sqlStore {
	switch backend := st.(type) {
	case *Postgres:
		return &backend.sqlStore
	case *SQLite:
		return &backend.sqlStore
	case *Memory:
		return &backend.SQLite.sqlStore
	default:
		return nil
	}
}

func sameJSON(a, b []byte) bool {
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}

func parityLegacyAndAbsent(t *testing.T, st Store) {
	ctx := context.Background()
	if _, ok, err := st.Course(ctx, "missing-course"); err != nil || ok {
		t.Fatalf("missing course ok=%v err=%v", ok, err)
	}
	if _, ok, err := st.Run(ctx, "missing-run"); err != nil || ok {
		t.Fatalf("missing run ok=%v err=%v", ok, err)
	}
	rec := sampleCourse("course_legacy", "Old course", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	rec.Request = nil // Mirrors a legacy row whose nullable request column is NULL.
	if err := st.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("save legacy course: %v", err)
	}
	got, ok, err := st.Course(ctx, rec.Plan.ID)
	if err != nil || !ok {
		t.Fatalf("legacy course ok=%v err=%v", ok, err)
	}
	if got.Request != nil || got.Plan.Title != rec.Plan.Title {
		t.Fatalf("legacy course=%+v", got)
	}
	list, err := st.Courses(ctx)
	if err != nil || len(list) != 1 || list[0].Request != nil {
		t.Fatalf("legacy course list=%+v err=%v", list, err)
	}
}

func TestSQLiteStoreSurvivesCloseAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metadata", "app.sqlite")
	first, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if first.Kind() != "sqlite" {
		t.Fatalf("kind=%q", first.Kind())
	}
	if err := first.CreateUser(ctx, User{ID: "u_restart", Email: "restart@example.test", Name: "Restart"}, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveProgress(ctx, "u_restart", "course", json.RawMessage(`{"done":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveRun(ctx, sampleRun("run_restart", "u_restart", schema.StatusActive, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	u, hash, ok, err := second.UserByEmail(ctx, "RESTART@example.test")
	if err != nil || !ok || u.ID != "u_restart" || hash != "hash" {
		t.Fatalf("user=%+v hash=%q ok=%v err=%v", u, hash, ok, err)
	}
	snapshot, ok, err := second.LoadProgress(ctx, "u_restart", "course")
	if err != nil || !ok || string(snapshot) != `{"done":true}` {
		t.Fatalf("snapshot=%s ok=%v err=%v", snapshot, ok, err)
	}
	if _, ok, err := second.Run(ctx, "run_restart"); err != nil || !ok {
		t.Fatalf("run ok=%v err=%v", ok, err)
	}
}
