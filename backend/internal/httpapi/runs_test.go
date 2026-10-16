package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* The agent-run audit trail over HTTP.
 *
 * Two rules belong to this package and are proved here: a run is only readable
 * by the account that owns it (404, never 403, so an id cannot be probed), and
 * the transcript a client reads is what was stored — entries in order, with the
 * tool calls and their results attributed to each other. A run also requires a
 * session, like every other owner-scoped route.
 */

// runServer is a gateway whose store is the transcript store, with generation
// wired so the routes are reachable. The provider is the deterministic one, but
// nothing here runs a generation: the runs are seeded directly, so the test
// pins the read surface rather than the generator.
func runServer(t *testing.T) *Server {
	t.Helper()
	st := store.NewMemory()
	s := New(&fakeExecutor{}, st, Config{MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2}, nil)
	s.UseAI(ai.Config{Provider: ai.NewFakeProvider(), Depot: st})
	return s
}

// seedRun writes a transcript into the server's own store under an owner. It
// goes through the store seam, not through a container the handler could see.
func seedRun(t *testing.T, s *Server, rec schema.RunTranscript) {
	t.Helper()
	if err := s.store.(*store.Memory).SaveRun(context.Background(), rec); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

// userID returns the id of the account behind a token, read through the real
// route, so a test never guesses how an id is minted.
func userID(t *testing.T, s *Server, token string) string {
	t.Helper()
	w := doAs(t, s, token, http.MethodGet, "/auth/me", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	mustDecode(t, w, &out)
	if out.User.ID == "" {
		t.Fatalf("no user id in /auth/me")
	}
	return out.User.ID
}

func transcriptWithEntries(owner string, at time.Time) schema.RunTranscript {
	return schema.RunTranscript{
		ID:      "run-seeded",
		Kind:    schema.RunKindCourse,
		OwnerID: owner,
		Status:  schema.StatusActive,
		Entries: []schema.TranscriptEntry{
			{Seq: 0, Kind: schema.EntryMessage, Role: "system", Content: "You design SQL courses.", At: at},
			{Seq: 1, Kind: schema.EntryMessage, Role: "user", Content: "Design one complete course.", At: at},
			{Seq: 2, Kind: schema.EntryToolCall, Role: "assistant", ToolName: "save_chapter", ToolCallID: "call_1", Content: `{"id":"gen.joins"}`, At: at},
			{Seq: 3, Kind: schema.EntryToolResult, Role: "tool", ToolCallID: "call_1", Content: `{"status":"verified"}`, At: at},
		},
		EntryCount: 4,
		Bytes:      80,
		StartedAt:  at,
		FinishedAt: at.Add(2 * time.Second),
	}
}

func TestGetRunReturnsTheWholeTranscript(t *testing.T) {
	s := runServer(t)
	token := signupUser(t, s, "runs-reader@example.test")
	owner := userID(t, s, token)

	rec := transcriptWithEntries(owner, time.Now().UTC())
	seedRun(t, s, rec)

	w := doAs(t, s, token, http.MethodGet, "/runs/run-seeded", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		OK  bool `json:"ok"`
		Run struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Status  string `json:"status"`
			OwnerID string `json:"owner_id"`
			Entries []struct {
				Seq        int    `json:"seq"`
				Kind       string `json:"kind"`
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				ToolName   string `json:"tool_name"`
				Content    string `json:"content"`
			} `json:"entries"`
			EntryCount int  `json:"entry_count"`
			Truncated  bool `json:"truncated"`
		} `json:"run"`
	}
	mustDecode(t, w, &out)

	if !out.OK || out.Run.ID != "run-seeded" {
		t.Fatalf("run = %+v", out.Run)
	}
	// The owner is the session's own account; it does not travel in the body.
	if out.Run.OwnerID != "" {
		t.Fatalf("owner_id = %q, want it withheld from the body", out.Run.OwnerID)
	}
	if len(out.Run.Entries) != 4 || out.Run.EntryCount != 4 {
		t.Fatalf("entries = %d (count %d), want 4", len(out.Run.Entries), out.Run.EntryCount)
	}
	for i, e := range out.Run.Entries {
		if e.Seq != i {
			t.Fatalf("entry %d has seq %d: order is the run's", i, e.Seq)
		}
	}
	if out.Run.Entries[2].Kind != "tool_call" || out.Run.Entries[2].ToolName != "save_chapter" {
		t.Fatalf("the tool call was not returned: %+v", out.Run.Entries[2])
	}
	if out.Run.Entries[3].Kind != "tool_result" || out.Run.Entries[3].ToolCallID != "call_1" {
		t.Fatalf("the tool result lost its call attribution: %+v", out.Run.Entries[3])
	}
}

func TestGetRunIs404ForAnotherOwner(t *testing.T) {
	s := runServer(t)
	alice := signupUser(t, s, "runs-alice-404@example.test")
	bob := signupUser(t, s, "runs-bob-404@example.test")
	aliceID := userID(t, s, alice)

	seedRun(t, s, transcriptWithEntries(aliceID, time.Now().UTC()))

	// Alice reads her own run.
	if w := doAs(t, s, alice, http.MethodGet, "/runs/run-seeded", ""); w.Code != http.StatusOK {
		t.Fatalf("owner read status = %d, want 200", w.Code)
	}
	// Bob must not be able to confirm it exists: 404, never 403.
	bobW := doAs(t, s, bob, http.MethodGet, "/runs/run-seeded", "")
	if bobW.Code != http.StatusNotFound {
		t.Fatalf("another learner read a run: status = %d body = %s", bobW.Code, bobW.Body.String())
	}
	if bobW.Code == http.StatusForbidden {
		t.Fatalf("a foreign run was reported as forbidden, which confirms it exists")
	}
}

func TestRunRoutesNeedASession(t *testing.T) {
	s := runServer(t)
	if w := do(t, s, http.MethodGet, "/runs", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status = %d, want 401", w.Code)
	}
	if w := do(t, s, http.MethodGet, "/runs/run-seeded", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read status = %d, want 401", w.Code)
	}
}

func TestUnknownRunIsNotFound(t *testing.T) {
	s := runServer(t)
	token := signupUser(t, s, "runs-unknown@example.test")
	if w := doAs(t, s, token, http.MethodGet, "/runs/run-does-not-exist", ""); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

func TestListRunsIsOwnerScopedAndNewestFirst(t *testing.T) {
	s := runServer(t)
	alice := signupUser(t, s, "runs-list-alice@example.test")
	bob := signupUser(t, s, "runs-list-bob@example.test")
	aliceID := userID(t, s, alice)
	bobID := userID(t, s, bob)

	base := time.Now().UTC()
	old := transcriptWithEntries(aliceID, base.Add(-time.Hour))
	old.ID = "run-old"
	newer := transcriptWithEntries(aliceID, base)
	newer.ID = "run-new"
	foreign := transcriptWithEntries(bobID, base)
	foreign.ID = "run-bob"
	for _, rec := range []schema.RunTranscript{old, newer, foreign} {
		seedRun(t, s, rec)
	}

	w := doAs(t, s, alice, http.MethodGet, "/runs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		OK   bool `json:"ok"`
		Runs []struct {
			ID      string `json:"id"`
			OwnerID string `json:"owner_id"`
			Kind    string `json:"kind"`
		} `json:"runs"`
	}
	mustDecode(t, w, &out)

	if len(out.Runs) != 2 {
		t.Fatalf("alice sees %d runs, want her own 2: %+v", len(out.Runs), out.Runs)
	}
	if out.Runs[0].ID != "run-new" || out.Runs[1].ID != "run-old" {
		t.Fatalf("order = %v, want newest first", []string{out.Runs[0].ID, out.Runs[1].ID})
	}
	for _, r := range out.Runs {
		if r.ID == "run-bob" {
			t.Fatalf("another learner's run was listed")
		}
		if r.OwnerID != "" {
			t.Fatalf("owner_id = %q leaked into a list item", r.OwnerID)
		}
	}
}

// A gateway with no generation service has no transcript store either, and says
// so instead of inventing runs.
func TestRunRoutesAreOffWithAI(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "runs-off@example.test")

	w := doAs(t, s, token, http.MethodGet, "/runs", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("list status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	var list struct {
		OK    bool  `json:"ok"`
		Runs  []any `json:"runs"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	mustDecode(t, w, &list)
	if list.OK || len(list.Runs) != 0 {
		t.Fatalf("an AI-off list must not invent runs: %s", w.Body.String())
	}
	if list.Error == nil || list.Error.Code != ai.CodeUnavailable {
		t.Fatalf("error = %+v, want %s", list.Error, ai.CodeUnavailable)
	}

	if w := doAs(t, s, token, http.MethodGet, "/runs/run-anything", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("read status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}

// The response validates against the shared contract, which is the language-
// neutral seam both tracks code against.
func TestRunResponsesConformToContract(t *testing.T) {
	runContract := loadContract(t, "run.schema.json")
	s := runServer(t)
	token := signupUser(t, s, "runs-contract@example.test")
	owner := userID(t, s, token)
	seedRun(t, s, transcriptWithEntries(owner, time.Now().UTC()))

	for _, tc := range []struct {
		name string
		path string
		ptr  string
	}{
		{"transcript", "/runs/run-seeded", "#/$defs/runTranscript"},
		{"list", "/runs", "#/$defs/runSummary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doAs(t, s, token, http.MethodGet, tc.path, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
			}
			decoded := decodeResponseObject(t, w.Body.Bytes())
			var value any
			if tc.name == "list" {
				runs, _ := decoded["runs"].([]any)
				if len(runs) == 0 {
					t.Fatalf("no runs in the list")
				}
				value = runs[0]
			} else {
				value = decoded["run"]
			}
			sub := contractDef(t, runContract, tc.ptr)
			if vs := validateSchema(sub, value, ""); len(vs) > 0 {
				t.Fatalf("%s does not match contracts/run.schema.json %s:\n%s",
					tc.name, tc.ptr, formatViolations(vs))
			}
		})
	}
}

// contractDef resolves a local "$ref" such as "#/$defs/runTranscript" against a
// loaded contract document.
func contractDef(t *testing.T, contract map[string]any, ref string) map[string]any {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	cur := contract
	for _, p := range parts {
		next, ok := cur[p].(map[string]any)
		if !ok {
			t.Fatalf("contract has no %q (at %q)", p, ref)
		}
		cur = next
	}
	return cur
}

// The owner-scoped wrapper stamps the session, not the caller: a record that
// arrives claiming someone else is overwritten. This is the same rule the
// challenge and course records follow.
func TestOwnerScopedDepotStampsTheRunOwner(t *testing.T) {
	inner := store.NewMemory()
	wrapped := ownerScopedDepot{Depot: inner, owner: "user-1"}
	if err := wrapped.SaveRun(context.Background(), schema.RunTranscript{ID: "run-x", OwnerID: "someone-else"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	got, ok, _ := inner.Run(context.Background(), "run-x")
	if !ok || got.OwnerID != "user-1" {
		t.Fatalf("owner = %q, want the session owner", got.OwnerID)
	}
}

// The whole feature end to end: a real generation run against a real
// PostgreSQL stores a transcript that its owner reads back over GET /runs and
// GET /runs/{id}, and that another account cannot see. The run is submitted as
// a job, so the transcript's id is the job id a client already polled.
func TestAGenerationStoresATranscriptReadableByItsOwner(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "runs-generation@example.test")

	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", asyncCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)

	done := waitJob(t, s, token, submitted.Job.ID)
	if done.Job.Status != schema.StatusActive {
		t.Fatalf("terminal status = %q, want active (error = %+v)", done.Job.Status, done.Job.Error)
	}

	// The transcript is named after the job, and is readable by its owner.
	w = doAs(t, s, token, http.MethodGet, "/runs/"+submitted.Job.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs/%s status = %d body = %s", submitted.Job.ID, w.Code, w.Body.String())
	}
	var out struct {
		OK  bool `json:"ok"`
		Run struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Status  string `json:"status"`
			OwnerID string `json:"owner_id"`
			Entries []struct {
				Kind     string `json:"kind"`
				Role     string `json:"role"`
				ToolName string `json:"tool_name"`
			} `json:"entries"`
			EntryCount int `json:"entry_count"`
		} `json:"run"`
	}
	mustDecode(t, w, &out)
	if out.Run.ID != submitted.Job.ID {
		t.Fatalf("transcript id = %q, want the job id %q", out.Run.ID, submitted.Job.ID)
	}
	if out.Run.Kind != "course" || out.Run.Status != schema.StatusActive {
		t.Fatalf("run = %+v, want an active course run", out.Run)
	}
	if out.Run.OwnerID != "" {
		t.Fatalf("owner_id = %q leaked into the body", out.Run.OwnerID)
	}
	// The conversation is really in there: the system prompt, the tool calls
	// and their results.
	roles, calls := map[string]int{}, 0
	for _, e := range out.Run.Entries {
		roles[e.Role]++
		if e.Kind == "tool_call" {
			calls++
		}
	}
	if out.Run.EntryCount == 0 || len(out.Run.Entries) == 0 {
		t.Fatalf("the run stored no transcript: %+v", out.Run)
	}
	if roles["system"] == 0 || roles["user"] == 0 || calls == 0 {
		t.Fatalf("the transcript is missing its conversation: %+v", out.Run.Entries)
	}

	// The list names it too, for the owner only.
	w = doAs(t, s, token, http.MethodGet, "/runs", "")
	var list struct {
		Runs []struct {
			ID string `json:"id"`
		} `json:"runs"`
	}
	mustDecode(t, w, &list)
	found := false
	for _, r := range list.Runs {
		if r.ID == submitted.Job.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("GET /runs did not list the run just generated: %+v", list.Runs)
	}

	// Another account sees neither the transcript nor that it exists.
	other := signupUser(t, s, "runs-generation-other@example.test")
	if w := doAs(t, s, other, http.MethodGet, "/runs/"+submitted.Job.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another learner read the transcript: status = %d body = %s", w.Code, w.Body.String())
	}
	w = doAs(t, s, other, http.MethodGet, "/runs", "")
	var otherList struct {
		Runs []struct {
			ID string `json:"id"`
		} `json:"runs"`
	}
	mustDecode(t, w, &otherList)
	for _, r := range otherList.Runs {
		if r.ID == submitted.Job.ID {
			t.Fatalf("another learner's list named the run")
		}
	}
}
