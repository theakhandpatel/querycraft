package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// storeMethodsRequired derives the Store methods the parity script below must
// run: every method of the interface, minus the ones it intentionally leaves
// uncovered. The settings methods have no parity cases yet, and the
// schema.Depot methods have their own (sqlite_test.go), so only the account,
// progress and session statements run here.
func storeMethodsRequired() []string {
	st := reflect.TypeOf((*Store)(nil)).Elem()
	uncovered := map[string]bool{
		"UserSetting": true, "SaveUserSetting": true,
		"Setting": true, "SaveSetting": true,
	}
	depot := reflect.TypeOf((*schema.Depot)(nil)).Elem()
	for i := 0; i < depot.NumMethod(); i++ {
		uncovered[depot.Method(i).Name] = true
	}
	out := make([]string, 0, st.NumMethod())
	for i := 0; i < st.NumMethod(); i++ {
		if name := st.Method(i).Name; !uncovered[name] {
			out = append(out, name)
		}
	}
	return out
}

// TestStoreMethodsMatchAcrossBackends runs the same account, progress and
// session operations against Postgres and SQLite and compares what each
// answered. Both backends share one implementation (metadata.go), so this test
// pins the dialect it runs over: the placeholder style, the timestamp value
// and the JSON column.
func TestStoreMethodsMatchAcrossBackends(t *testing.T) {
	ctx := context.Background()
	srv := pgtest.Start(t)

	postgres, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = postgres.Close() })
	if _, err := postgres.pool.Exec(ctx, `TRUNCATE app.events, app.progress, app.sessions, app.users CASCADE`); err != nil {
		t.Fatalf("clear the app schema: %v", err)
	}

	sqlite, err := NewSQLite(ctx, filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })

	answers := map[string][]storeAnswer{}
	for _, backend := range []struct {
		name string
		st   Store
	}{{"postgres", postgres}, {"sqlite", sqlite}} {
		t.Run(backend.name, func(t *testing.T) {
			answers[backend.name] = runStoreScript(t, backend.st, backend.name)
		})
	}
	if t.Failed() {
		// One backend already reported the step that broke; comparing the rest
		// would bury that answer.
		return
	}
	compareStoreAnswers(t, answers["postgres"], answers["sqlite"])
}

// storeAnswer is one step's reported result. The step name makes a mismatch
// readable, and the value is what that step answered.
type storeAnswer struct {
	step  string
	value any
}

// runStoreScript runs the same operations against one backend in the same
// order and records what each answered.
func runStoreScript(t *testing.T, st Store, kind string) []storeAnswer {
	t.Helper()
	ctx := context.Background()
	started := time.Now().UTC()
	var answers []storeAnswer

	record := func(step string, value any) {
		answers = append(answers, storeAnswer{step: step, value: value})
	}
	must := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}

	const (
		course = "sql-analytics"
		event  = "evt_1"
	)

	// Accounts come first: progress and events carry a foreign key to the
	// account that owns them, on both backends.
	admin := User{ID: "u_admin", Email: "admin@example.test", Name: "Admin", Role: RoleAdmin}
	learner := User{ID: "u_learner", Email: "learner@example.test", Name: "Learner"}
	must("CreateUser", st.CreateUser(ctx, admin, "hash-admin"))
	must("CreateUser learner", st.CreateUser(ctx, learner, "hash-learner"))
	record("CreateUser duplicate email",
		errors.Is(st.CreateUser(ctx, User{ID: "u_dup", Email: "ADMIN@example.test", Name: "Dup"}, "hash-dup"), ErrEmailTaken))

	// The address matches in lower case, the role reads back, and the write
	// stamped created_at from this run.
	user, hash, ok, err := st.UserByEmail(ctx, "ADMIN@EXAMPLE.TEST")
	must("UserByEmail", err)
	record("UserByEmail", []any{
		user.ID, user.Email, user.Name, user.Role, user.IsAdmin(), hash, ok,
		writtenNow(t, "UserByEmail", user.CreatedAt, started),
	})

	user, hash, ok, err = st.UserByEmail(ctx, "nobody@example.test")
	must("UserByEmail missing", err)
	record("UserByEmail missing", []any{user.ID, hash, ok})

	must("SetPasswordHash", st.SetPasswordHash(ctx, learner.ID, "hash-changed"))
	_, hash, ok, err = st.UserByEmail(ctx, "learner@example.test")
	must("UserByEmail after SetPasswordHash", err)
	record("SetPasswordHash", []any{hash, ok})

	ids, err := st.UserIDs(ctx)
	must("UserIDs", err)
	record("UserIDs", slices.Sorted(slices.Values(ids)))

	// The document round-trips through either engine. jsonb renders the value
	// with spaces and orders keys by length; SQLite stores the compact text
	// normalizeJSON wrote. The test compares the decoded value, because the two
	// engines can hold the same document in different bytes.
	snapshot := json.RawMessage(`{ "a": 3, "bb": [true, false], "ccc": { "d": 1, "ee": 2 } }`)
	must("SaveProgress", st.SaveProgress(ctx, admin.ID, course, snapshot))
	loaded, ok, err := st.LoadProgress(ctx, admin.ID, course)
	must("LoadProgress", err)
	record("LoadProgress", []any{canonical(t, "LoadProgress", loaded), ok})

	must("SaveProgress upsert", st.SaveProgress(ctx, admin.ID, course, json.RawMessage(`{"a":4}`)))
	loaded, ok, err = st.LoadProgress(ctx, admin.ID, course)
	must("LoadProgress upsert", err)
	record("LoadProgress upsert", []any{canonical(t, "LoadProgress upsert", loaded), ok})

	// A missing course and a missing owner are both "nothing", not an error.
	loaded, ok, err = st.LoadProgress(ctx, admin.ID, "no-such-course")
	must("LoadProgress missing", err)
	record("LoadProgress missing", []any{loaded == nil, ok})

	loaded, ok, err = st.LoadProgress(ctx, "", course)
	must("LoadProgress without an owner", err)
	record("LoadProgress without an owner", []any{loaded == nil, ok})

	// Progress and events must name an owner; an unscoped write would land in
	// a row any account could read back.
	record("SaveProgress without an owner", st.SaveProgress(ctx, "", course, snapshot) != nil)
	record("SaveEvent without an owner", st.SaveEvent(ctx, "", event, snapshot) != nil)
	record("SaveEvent without an id", st.SaveEvent(ctx, admin.ID, "", snapshot) != nil)

	// Store declares no event reader, so the test reads the row back through
	// the same shim and compares the payload each engine stored.
	must("SaveEvent", st.SaveEvent(ctx, admin.ID, event, json.RawMessage(`{ "kind" : "passed" , "score" : 9 }`)))
	payload, err := storedEventPayload(ctx, st, admin.ID, event)
	must("read event", err)
	record("SaveEvent payload", canonical(t, "SaveEvent payload", json.RawMessage(payload)))

	must("SaveEvent upsert", st.SaveEvent(ctx, admin.ID, event, json.RawMessage(`{"kind":"failed"}`)))
	payload, err = storedEventPayload(ctx, st, admin.ID, event)
	must("read event upsert", err)
	record("SaveEvent upsert payload", canonical(t, "SaveEvent upsert payload", json.RawMessage(payload)))

	// A live token resolves, an expired one and an unknown one do not, and a
	// deleted one stops resolving. The expiry runs both directions through the
	// timestamp column.
	live := st.CreateSession(ctx, "tok_live", admin.ID, time.Now().UTC().Add(time.Hour))
	record("CreateSession", live == nil)
	must("CreateSession", live)
	dead := st.CreateSession(ctx, "tok_dead", admin.ID, time.Now().UTC().Add(-time.Hour))
	record("CreateSession expired", dead == nil)
	must("CreateSession expired", dead)
	user, ok, err = st.SessionUser(ctx, "tok_live")
	must("SessionUser", err)
	record("SessionUser", []any{
		user.ID, user.Email, user.Name, user.Role, ok,
		writtenNow(t, "SessionUser", user.CreatedAt, started),
	})

	user, ok, err = st.SessionUser(ctx, "tok_dead")
	must("SessionUser expired", err)
	record("SessionUser expired", []any{user.ID, ok})

	user, ok, err = st.SessionUser(ctx, "tok_unknown")
	must("SessionUser unknown", err)
	record("SessionUser unknown", []any{user.ID, ok})

	deleted := st.DeleteSession(ctx, "tok_live")
	record("DeleteSession", deleted == nil)
	must("DeleteSession", deleted)
	user, ok, err = st.SessionUser(ctx, "tok_live")
	must("SessionUser deleted", err)
	record("SessionUser deleted", []any{user.ID, ok})

	record("Kind", st.Kind() == kind)
	must("Close", st.Close())
	record("Close", true)

	return answers
}

// createdNow is what the script records for a timestamp a store wrote for
// itself. Two independent databases cannot return one instant, so the test
// proves the timestamp is real instead: it must fall inside this run.
const createdNow = "written during this test"

func writtenNow(t *testing.T, step string, at, started time.Time) string {
	t.Helper()
	if at.IsZero() || at.Before(started.Add(-time.Second)) || time.Since(at) > time.Minute {
		t.Fatalf("%s: timestamp %s does not come from this test run", step, at)
	}
	return createdNow
}

// canonical renders a stored JSON value for comparison. The two engines can
// hold the same document in different bytes, so the test compares the decoded
// value through the same normal form SQLite writes.
func canonical(t *testing.T, step string, raw json.RawMessage) string {
	t.Helper()
	out, err := normalizeJSON(raw)
	if err != nil {
		t.Fatalf("%s: stored JSON does not decode: %v (raw %s)", step, err, raw)
	}
	return out
}

// storedEventPayload reads back what SaveEvent wrote. It goes through the same
// shim the store uses rather than spelling one query per dialect.
func storedEventPayload(ctx context.Context, st Store, userID, id string) (string, error) {
	shared := sharedStore(st)
	if shared == nil {
		return "", fmt.Errorf("no shared statements for %T", st)
	}
	var raw []byte
	query := shared.stmt(`SELECT payload FROM %s WHERE user_id = ? AND id = ?`, "events")
	if err := shared.queryRow(ctx, query, userID, id).Scan(&raw); err != nil {
		return "", err
	}
	return string(raw), nil
}

// compareStoreAnswers compares one step at a time, so a mismatch names the
// operation that disagreed, and checks the script ran every method it is
// required to cover.
func compareStoreAnswers(t *testing.T, postgres, sqlite []storeAnswer) {
	t.Helper()
	if len(postgres) != len(sqlite) {
		t.Fatalf("postgres recorded %d steps, sqlite recorded %d", len(postgres), len(sqlite))
	}
	for i := range postgres {
		if postgres[i].step != sqlite[i].step {
			t.Fatalf("step %d: postgres ran %q, sqlite ran %q", i, postgres[i].step, sqlite[i].step)
		}
		if !reflect.DeepEqual(postgres[i].value, sqlite[i].value) {
			t.Errorf("step %q: postgres answered %#v, sqlite answered %#v",
				postgres[i].step, postgres[i].value, sqlite[i].value)
		}
	}
	for _, method := range storeMethodsRequired() {
		ran := slices.ContainsFunc(postgres, func(answer storeAnswer) bool {
			return answer.step == method || strings.HasPrefix(answer.step, method+" ")
		})
		if !ran {
			t.Errorf("the script never ran %s", method)
		}
	}
}
