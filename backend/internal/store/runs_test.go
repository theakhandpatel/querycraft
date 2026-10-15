package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
)

// sampleRun is a transcript as the pipeline would hand it over: the entries it
// recorded plus the attribution the store keeps beside them.
func sampleRun(id, owner, status string, at time.Time) ai.RunTranscript {
	return ai.RunTranscript{
		ID:      id,
		Kind:    ai.RunKindCourse,
		OwnerID: owner,
		Status:  status,
		Entries: []ai.TranscriptEntry{
			{Seq: 0, Kind: ai.EntryMessage, Role: "system", Content: "You design SQL courses.", At: at},
			{Seq: 1, Kind: ai.EntryMessage, Role: "user", Content: "Design one complete course.", At: at},
			{Seq: 2, Kind: ai.EntryToolCall, Role: "assistant", ToolName: "save_chapter", ToolCallID: "call_1", Content: `{"id":"gen.joins"}`, At: at},
			{Seq: 3, Kind: ai.EntryToolResult, Role: "tool", ToolCallID: "call_1", Content: `{"status":"verified"}`, At: at},
		},
		EntryCount: 4,
		Bytes:      80,
		Error:      nil,
		StartedAt:  at,
		FinishedAt: at.Add(2 * time.Second),
	}
}

/* ---------- memory ---------- */

func TestMemoryRunRoundTripIsDeepCopied(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	rec := sampleRun("run_1", "u_alice", ai.StatusActive, time.Now().UTC())

	if err := m.SaveRun(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := m.Run(ctx, "run_1")
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Kind != ai.RunKindCourse || got.Status != ai.StatusActive {
		t.Fatalf("attribution did not survive: %+v", got)
	}
	if len(got.Entries) != 4 || got.Entries[2].ToolName != "save_chapter" {
		t.Fatalf("entries did not survive: %+v", got.Entries)
	}

	// The order of entries is the order they happened in; a tool result names
	// the call it answers.
	if got.Entries[3].ToolCallID != "call_1" || got.Entries[3].Kind != ai.EntryToolResult {
		t.Fatalf("the tool result lost its call attribution: %+v", got.Entries[3])
	}

	// Mutating what a caller got back must not corrupt the store.
	got.Entries[0].Content = "mutated"
	again, _, _ := m.Run(ctx, "run_1")
	if again.Entries[0].Content == "mutated" {
		t.Fatalf("the store returned an aliased slice")
	}

	if _, ok, _ := m.Run(ctx, "run_missing"); ok {
		t.Fatalf("an unknown run id was found")
	}
}

func TestMemoryRunsAreNewestFirst(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	base := time.Now().UTC()
	for i, id := range []string{"run_old", "run_mid", "run_new"} {
		rec := sampleRun(id, "u_alice", ai.StatusActive, base.Add(time.Duration(i)*time.Minute))
		if err := m.SaveRun(ctx, rec); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	list, err := m.Runs(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 || list[0].ID != "run_new" || list[2].ID != "run_old" {
		t.Fatalf("order = %v, want newest first", []string{list[0].ID, list[1].ID, list[2].ID})
	}
}

// Re-storing a run is an update, not a duplicate: a building course writes its
// transcript more than once, and the second write must not create a second row
// or move the run's start.
func TestMemoryRunSaveIsAnUpsert(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Minute)

	first := sampleRun("run_upsert", "u_alice", ai.StatusActive, start)
	if err := m.SaveRun(ctx, first); err != nil {
		t.Fatalf("save: %v", err)
	}
	second := sampleRun("run_upsert", "u_alice", ai.StatusActive, time.Now().UTC())
	second.Entries = second.Entries[:2]
	second.EntryCount = 2
	if err := m.SaveRun(ctx, second); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, ok, _ := m.Run(ctx, "run_upsert")
	if !ok || len(got.Entries) != 2 {
		t.Fatalf("the update did not replace the entries: %+v", got.Entries)
	}
	if !got.StartedAt.Equal(start) {
		t.Fatalf("started_at = %v, want the original %v", got.StartedAt, start)
	}
	if list, _ := m.Runs(ctx); len(list) != 1 {
		t.Fatalf("upsert produced %d rows", len(list))
	}
}

// The store's reads are unfiltered: Run returns the record with its owner, so
// the caller enforces ownership. This is the seam the HTTP layer relies on.
func TestMemoryRunCarriesItsOwner(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	_ = m.SaveRun(ctx, sampleRun("run_a", "u_alice", ai.StatusActive, time.Now().UTC()))
	_ = m.SaveRun(ctx, sampleRun("run_b", "u_bob", ai.StatusActive, time.Now().UTC()))

	rec, ok, _ := m.Run(ctx, "run_b")
	if !ok || rec.OwnerID != "u_bob" {
		t.Fatalf("owner = %q, want u_bob; owner scoping is the caller's to apply", rec.OwnerID)
	}
	list, _ := m.Runs(ctx)
	for _, sm := range list {
		if sm.ID == "run_b" && sm.OwnerID != "u_bob" {
			t.Fatalf("the summary lost its owner: %+v", sm)
		}
	}
}

func TestMemoryRunRequiresID(t *testing.T) {
	m := NewMemory()
	if err := m.SaveRun(context.Background(), ai.RunTranscript{Kind: ai.RunKindChallenge}); err == nil {
		t.Fatalf("a run with no id was accepted")
	}
}

/* ---------- postgres ---------- */

func TestPostgresRunsSurviveARestart(t *testing.T) {
	srv := pgtest.Start(t)
	ctx := context.Background()

	first, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	rec := sampleRun("run_restart", "u_alice", ai.StatusActive, time.Now().UTC())
	rec.Error = &ai.Error{Code: "AI_UNAVAILABLE", Message: "the provider could not be reached"}
	if err := first.SaveRun(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	first.Close()

	second, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer second.Close()

	got, ok, err := second.Run(ctx, "run_restart")
	if err != nil || !ok {
		t.Fatalf("after restart ok=%v err=%v", ok, err)
	}
	if got.Kind != ai.RunKindCourse || got.OwnerID != "u_alice" || got.Status != ai.StatusActive {
		t.Fatalf("attribution = %+v", got)
	}
	if len(got.Entries) != 4 || got.Entries[2].Kind != ai.EntryToolCall {
		t.Fatalf("entries did not survive: %+v", got.Entries)
	}
	if got.Error == nil || got.Error.Code != "AI_UNAVAILABLE" {
		t.Fatalf("error = %+v, want the stored failure", got.Error)
	}

	list, err := second.Runs(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "run_restart" {
		t.Fatalf("list = %+v", list)
	}
}

func TestPostgresRunSaveIsAnUpsert(t *testing.T) {
	srv := pgtest.Start(t)
	ctx := context.Background()
	st, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	start := time.Now().UTC().Add(-time.Minute)
	if err := st.SaveRun(ctx, sampleRun("run_upsert", "u_alice", ai.StatusActive, start)); err != nil {
		t.Fatalf("save: %v", err)
	}
	second := sampleRun("run_upsert", "u_alice", ai.StatusActive, time.Now().UTC())
	second.Entries = second.Entries[:1]
	second.EntryCount = 1
	if err := st.SaveRun(ctx, second); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, ok, _ := st.Run(ctx, "run_upsert")
	if !ok || len(got.Entries) != 1 {
		t.Fatalf("the update did not replace the entries: %+v", got.Entries)
	}
	if !got.StartedAt.Equal(start.Truncate(time.Microsecond)) {
		t.Fatalf("started_at = %v, want the original %v", got.StartedAt, start)
	}
	if list, _ := st.Runs(ctx); len(list) != 1 {
		t.Fatalf("upsert produced %d rows", len(list))
	}
}

// The whole point of the owner rule: a stored run can name the owner it was
// written with, and the HTTP layer turns a mismatch into 404. The store itself
// must not lose or invent the owner.
func TestPostgresRunsCarryTheirOwner(t *testing.T) {
	srv := pgtest.Start(t)
	ctx := context.Background()
	st, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.SaveRun(ctx, sampleRun("run_a", "u_alice", ai.StatusActive, time.Now().UTC())); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := st.SaveRun(ctx, sampleRun("run_b", "u_bob", ai.StatusActive, time.Now().UTC())); err != nil {
		t.Fatalf("save b: %v", err)
	}
	rec, ok, _ := st.Run(ctx, "run_a")
	if !ok || rec.OwnerID != "u_alice" {
		t.Fatalf("owner = %q, want u_alice", rec.OwnerID)
	}
	list, _ := st.Runs(ctx)
	owners := map[string]string{}
	for _, sm := range list {
		owners[sm.ID] = sm.OwnerID
	}
	if owners["run_a"] != "u_alice" || owners["run_b"] != "u_bob" {
		t.Fatalf("summaries lost their owners: %+v", owners)
	}
}

/* ---------- redaction and the bound (the ai seam's own tests live there) ---------- */

// The store never redacts: redaction is the recorder's job before the value
// arrives. This pins that the store keeps whatever transcript it is handed
// verbatim, so a reader is never shown a silently-altered record.
func TestStoreDoesNotAlterStoredContent(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	rec := sampleRun("run_verbatim", "u_alice", ai.StatusActive, time.Now().UTC())
	rec.Entries[0].Content = "the exact text the recorder stored"
	if err := m.SaveRun(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _, _ := m.Run(ctx, "run_verbatim")
	if got.Entries[0].Content != "the exact text the recorder stored" {
		t.Fatalf("content = %q, want it stored verbatim", got.Entries[0].Content)
	}
	if !strings.Contains(got.Entries[0].Content, "exact") {
		t.Fatalf("content was altered")
	}
}
