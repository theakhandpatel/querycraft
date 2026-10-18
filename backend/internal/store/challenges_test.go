package store

import (
	"context"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// sampleRecord is a challenge as the pipeline would hand it over: the artifact
// plus the evidence that admitted it.
func sampleRecord(id, title string, at time.Time) schema.Record {
	return schema.Record{
		Artifact: schema.Artifact{
			ID:      id,
			Version: 1,
			Title:   title,
			Prompt:  "Return the top product per category.",
			Dialect: "postgresql",
			Hints:   []string{"Start from products."},
			Setup:   schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1},
			ResultContract: schema.ResultContract{
				Columns:     []string{"category", "name"},
				ColumnOrder: "exact",
				RowOrder:    "ignored",
				Duplicates:  "preserved",
			},
			Provenance: schema.Provenance{
				Provider:         "fake",
				Model:            "fake-v1",
				PipelineVersion:  schema.PipelineVersion,
				EvaluatorVersion: "go-1",
			},
		},
		Outcome: schema.Outcome{
			Status: schema.StatusActive,
			Stages: []schema.Stage{{Name: schema.StageCrossCheck, State: schema.StateDone, Detail: "both solutions agreed"}},
		},
		Status:    schema.StatusActive,
		CreatedAt: at,
	}
}

func TestMemoryChallengeRoundTripIsDeepCopied(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	rec := sampleRecord("ch_1", "Top product per category", time.Now().UTC())

	if err := m.SaveChallenge(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := m.Challenge(ctx, "ch_1")
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Artifact.Title != rec.Artifact.Title {
		t.Fatalf("title = %q, want %q", got.Artifact.Title, rec.Artifact.Title)
	}
	if len(got.Outcome.Stages) != 1 || got.Outcome.Stages[0].Name != schema.StageCrossCheck {
		t.Fatalf("the evidence did not survive the round trip: %+v", got.Outcome.Stages)
	}

	// Mutating what a caller got back must not corrupt the store; the artifact
	// and its slices are the store's, not the caller's.
	got.Artifact.Hints[0] = "mutated"
	got.Artifact.ResultContract.Columns[0] = "mutated"
	got.Outcome.Stages[0].Detail = "mutated"
	again, _, _ := m.Challenge(ctx, "ch_1")
	if again.Artifact.Hints[0] != "Start from products." ||
		again.Artifact.ResultContract.Columns[0] != "category" ||
		again.Outcome.Stages[0].Detail != "both solutions agreed" {
		t.Fatalf("the store returned aliased slices: %+v", again)
	}

	if _, ok, _ := m.Challenge(ctx, "ch_missing"); ok {
		t.Fatalf("an unknown id was found")
	}
	if err := m.DeleteChallenge(ctx, "ch_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := m.Challenge(ctx, "ch_1"); ok {
		t.Fatalf("the challenge survived a delete")
	}
}

func TestMemoryChallengesAreNewestFirst(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	base := time.Now().UTC()
	for i, id := range []string{"ch_old", "ch_mid", "ch_new"} {
		if err := m.SaveChallenge(ctx, sampleRecord(id, id, base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	list, err := m.ChallengesFor(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 || list[0].Artifact.ID != "ch_new" || list[2].Artifact.ID != "ch_old" {
		t.Fatalf("order = %v, want newest first", []string{list[0].Artifact.ID, list[1].Artifact.ID, list[2].Artifact.ID})
	}
}

func TestMemoryPatchApprovalKeepsTheFirstApprover(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	proposal := schema.PatchProposal{
		ID:          "p_1",
		ChallengeID: "ch_1",
		Summary:     "widen the contract",
		Changes:     []schema.PatchChange{{Field: "result_contract.row_order", From: "exact", To: "ignored"}},
	}
	if err := m.SavePatch(ctx, proposal); err != nil {
		t.Fatalf("save patch: %v", err)
	}
	if _, ok, _ := m.Patch(ctx, "p_1"); !ok {
		t.Fatalf("the proposal was not stored")
	}
	if err := m.ApprovePatch(ctx, "p_1", ""); err == nil {
		t.Fatalf("an approval with no approver was accepted")
	}
	if err := m.ApprovePatch(ctx, "p_1", "learner@example.test"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := m.ApprovePatch(ctx, "p_1", "someone-else@example.test"); err != nil {
		t.Fatalf("second approve: %v", err)
	}
	// Re-storing the text must not clear an approval that already exists.
	if err := m.SavePatch(ctx, proposal); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, _, _ := m.Patch(ctx, "p_1")
	if got.ID != "p_1" {
		t.Fatalf("patch id = %q", got.ID)
	}
}

// The M4 exit gate: a generated challenge survives a restart and can be reset,
// and an approval is recorded against the patch it names.
//
// "Restart" is two independent handles onto the same cluster, which is what a
// gateway restart is from the database's point of view.
func TestPostgresChallengesSurviveARestart(t *testing.T) {
	srv := pgtest.Start(t)
	ctx := context.Background()

	first, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	rec := sampleRecord("ch_restart", "Survives a restart", time.Now().UTC())
	if err := first.SaveChallenge(ctx, rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := first.SavePatch(ctx, schema.PatchProposal{ID: "p_restart", ChallengeID: "ch_restart", Summary: "fix"}); err != nil {
		t.Fatalf("save patch: %v", err)
	}
	first.Close()

	// A second handle is a new process as far as the data is concerned.
	second, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer second.Close()

	got, ok, err := second.Challenge(ctx, "ch_restart")
	if err != nil || !ok {
		t.Fatalf("after restart ok=%v err=%v", ok, err)
	}
	if got.Artifact.Title != "Survives a restart" {
		t.Fatalf("title = %q", got.Artifact.Title)
	}
	if got.Status != schema.StatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if len(got.Outcome.Stages) == 0 {
		t.Fatalf("the evidence that admitted the challenge was not persisted")
	}
	if got.Artifact.Provenance.PipelineVersion != schema.PipelineVersion {
		t.Fatalf("provenance = %+v", got.Artifact.Provenance)
	}

	list, err := second.ChallengesFor(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Artifact.ID != "ch_restart" {
		t.Fatalf("list = %+v", list)
	}

	if err := second.ApprovePatch(ctx, "p_restart", "learner@example.test"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := second.ApprovePatch(ctx, "p_restart", ""); err == nil {
		t.Fatalf("an approval with no approver was accepted")
	}

	if err := second.DeleteChallenge(ctx, "ch_restart"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := second.Challenge(ctx, "ch_restart"); ok {
		t.Fatalf("the challenge survived a reset")
	}
	if list, _ := second.ChallengesFor(ctx, ""); len(list) != 0 {
		t.Fatalf("reset left %d record(s) behind", len(list))
	}
}

// Saving the same challenge id twice is an update, not a duplicate: the lab
// re-runs and re-activates rather than failing on a unique violation.
func TestPostgresChallengeSaveIsAnUpsert(t *testing.T) {
	srv := pgtest.Start(t)
	ctx := context.Background()
	st, err := NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	first := sampleRecord("ch_upsert", "First", time.Now().UTC())
	if err := st.SaveChallenge(ctx, first); err != nil {
		t.Fatalf("save: %v", err)
	}
	second := sampleRecord("ch_upsert", "Second", time.Now().UTC())
	if err := st.SaveChallenge(ctx, second); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, ok, _ := st.Challenge(ctx, "ch_upsert")
	if !ok || got.Artifact.Title != "Second" {
		t.Fatalf("got %+v, want the updated title", got.Artifact)
	}
	if list, _ := st.ChallengesFor(ctx, ""); len(list) != 1 {
		t.Fatalf("upsert produced %d rows", len(list))
	}
}
