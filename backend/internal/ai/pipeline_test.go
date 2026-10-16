package ai

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/eval"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

func TestPipelineActivatesAPersistedChallenge(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	svc := testService(NewFakeProvider(), depot, ex)

	out := svc.GenerateChallenge(ctx, schema.GenerationRequest{
		Title:         "Orders per customer",
		Topics:        []string{"joins"},
		Difficulty:    schema.Beginner,
		ExerciseCount: 3,
	})
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %v, stages = %+v)", out.Status, schema.StatusActive, out.Error, out.Stages)
	}
	if out.Artifact == nil {
		t.Fatal("an active outcome must carry the artifact")
	}
	if out.Error != nil {
		t.Fatalf("an active outcome must not carry an error: %v", out.Error)
	}

	// Every stage that ran is reported, and the repair stage is reported as
	// skipped rather than omitted, because no repair was needed.
	for _, name := range stageOrder {
		want := schema.StateDone
		if name == schema.StageRepair {
			want = schema.StateSkipped
		}
		if got := stageState(out, name); got != want {
			t.Errorf("stage %s = %q, want %q", name, got, want)
		}
		if stageDetail(out, name) == "" {
			t.Errorf("stage %s has no detail; the client renders it verbatim", name)
		}
	}
	if got := stageDetail(out, schema.StageRepair); got != "not needed: no stage failed" {
		t.Errorf("repair detail = %q, want the reason it was skipped", got)
	}

	// The wire shape is part of the contract: `stages` and `attempts` are JSON
	// arrays, and a run with no repairs must not write `attempts: null`.
	assertArraysNotNull(t, out)
	if out.Evidence.ReferenceRows == 0 || out.Evidence.CrossCheckRows == 0 {
		t.Errorf("evidence = %+v, want measured reference and cross-check row counts", out.Evidence)
	}
	if out.Evidence.FixturesTotal == 0 || out.Evidence.FixturesRejected != out.Evidence.FixturesTotal {
		t.Errorf("evidence = %+v, want every fixture rejected", out.Evidence)
	}

	// Provenance is stamped by the service, not the provider.
	p := out.Artifact.Provenance
	if p.Provider != "fake" || p.Model != "fake-v1" || p.PipelineVersion != schema.PipelineVersion ||
		p.EvaluatorVersion != eval.EvaluatorVersion || p.DatasetVersion != 1 || p.CreatedAt.IsZero() {
		t.Fatalf("provenance = %+v, want it stamped from the provider and the evaluator", p)
	}

	// Persisted as active.
	id := activeID(t, depot)
	rec, found, err := depot.Challenge(ctx, id)
	if err != nil || !found {
		t.Fatalf("Challenge() found = %v err = %v", found, err)
	}
	if rec.Status != schema.StatusActive || rec.Artifact.ReferenceSolution == "" {
		t.Fatalf("stored record = %+v, want the active artifact", rec)
	}

	// The real evaluator grades it: the reference passes and a plausible wrong
	// answer fails.
	exercise := eval.Exercise{
		Prompt:            out.Artifact.Prompt,
		Contract:          evalContract(out.Artifact.ResultContract),
		ReferenceSolution: out.Artifact.ReferenceSolution,
	}
	right := eval.Evaluate(ctx, ex, eval.Request{SQL: out.Artifact.ReferenceSolution, Exercise: exercise})
	if !right.Passed {
		t.Fatalf("the reference was not graded as passing: %+v", right.Checks)
	}
	wrong := eval.Evaluate(ctx, ex, eval.Request{
		SQL:      `SELECT c.name AS customer_name, 0 AS order_count FROM "dataset"."customers" c`,
		Exercise: exercise,
	})
	if wrong.Passed {
		t.Fatal("a wrong learner query was graded as passing")
	}
}

// TestInvalidGeneratedSetupNeverBecomesActive is the M4 exit gate: a broken
// bundle is rejected, nothing is written as active, and the error names the
// stage that refused.
func TestInvalidGeneratedSetupNeverBecomesActive(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	fake := NewFakeProvider()

	tests := []struct {
		name      string
		mutate    func(*schema.Artifact)
		wantStage string
	}{
		{
			name:      "unrunnable reference",
			mutate:    func(a *schema.Artifact) { a.ReferenceSolution = `SELECT * FROM "dataset"."no_such_table"` },
			wantStage: schema.StageReferenceContract,
		},
		{
			name:      "missing result columns",
			mutate:    func(a *schema.Artifact) { a.ResultContract.Columns = []string{} },
			wantStage: schema.StageValidateArtifact,
		},
		{
			name: "a fixture that wrongly passes",
			mutate: func(a *schema.Artifact) {
				// The default wrong-columns fixture becomes a correct answer to
				// this contract, so the evaluator would accept a query that is
				// not the reference. The pipeline must notice.
				a.ReferenceSolution = `SELECT 1 AS "ai_fixture_wrong_column"`
				a.CrossCheckSolution = `SELECT (1 + 0) AS "ai_fixture_wrong_column"`
				a.ResultContract.Columns = []string{"ai_fixture_wrong_column"}
				a.ResultContract.RowOrder = "ignored"
				a.ResultContract.Duplicates = "ignored"
			},
			wantStage: schema.StageAdversarialFixtures,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			depot := newTestDepot()
			svc := testService(mutateProvider{base: fake, mutate: tc.mutate}, depot, ex)
			out := svc.GenerateChallenge(ctx, schema.GenerationRequest{Title: "Broken", Topics: []string{"joins"}})

			if out.Status == schema.StatusActive {
				t.Fatal("a broken bundle was activated")
			}
			if out.Artifact != nil {
				t.Fatal("a non-active outcome must not carry an artifact")
			}
			if out.Error == nil {
				t.Fatal("a non-active outcome must carry an error")
			}
			if out.Error.Stage != tc.wantStage {
				t.Fatalf("error stage = %q, want %q (error = %v)", out.Error.Stage, tc.wantStage, out.Error)
			}
			if !strings.Contains(out.Error.Message, tc.wantStage) {
				t.Fatalf("message %q does not name the failed stage %q", out.Error.Message, tc.wantStage)
			}
			if n := depot.activeCount(); n != 0 {
				t.Fatalf("depot holds %d active challenge(s), want 0", n)
			}
		})
	}
}

func TestCrossCheckDisagreementIsRejected(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()

	broken := mutateProvider{base: NewFakeProvider(), mutate: func(a *schema.Artifact) {
		// Same rows, wrong counts: the two solutions genuinely disagree.
		a.CrossCheckSolution = `SELECT c.name AS customer_name, count(o.id) + 1 AS order_count
FROM "dataset"."customers" c
JOIN "dataset"."orders" o ON o.customer_id = c.id
GROUP BY c.name
ORDER BY order_count DESC, customer_name ASC`
	}}
	svc := testService(broken, depot, ex)
	out := svc.GenerateChallenge(ctx, schema.GenerationRequest{Title: "Disagreeing", Topics: []string{"joins"}})

	if out.Status != schema.StatusRejected {
		t.Fatalf("status = %q, want %q", out.Status, schema.StatusRejected)
	}
	if out.Error == nil || out.Error.Code != CodeCrossCheckFailed {
		t.Fatalf("error = %+v, want code %q", out.Error, CodeCrossCheckFailed)
	}
	if out.Error.Stage != schema.StageCrossCheck {
		t.Fatalf("error stage = %q, want %q", out.Error.Stage, schema.StageCrossCheck)
	}
	if n := depot.activeCount(); n != 0 {
		t.Fatalf("depot holds %d active challenge(s), want 0", n)
	}
}

func TestRepairBudgetIsBounded(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()

	var calls int32
	limits := DefaultLimits()
	if limits.MaxRepairs != 2 {
		t.Fatalf("this test assumes the default repair budget of 2, got %d", limits.MaxRepairs)
	}
	broken := countingProvider{
		calls: &calls,
		base: mutateProvider{base: NewFakeProvider(), mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `DROP TABLE "dataset"."orders"`
		}},
	}
	svc := testService(broken, depot, ex)
	out := svc.GenerateChallenge(ctx, schema.GenerationRequest{Title: "Never valid", Topics: []string{"joins"}})

	if out.Status != schema.StatusRejected {
		t.Fatalf("status = %q, want %q", out.Status, schema.StatusRejected)
	}
	if out.Error == nil || out.Error.Code != CodeRepairExhausted {
		t.Fatalf("error = %+v, want code %q", out.Error, CodeRepairExhausted)
	}
	if out.Error.Stage != schema.StageValidateArtifact {
		t.Fatalf("error stage = %q, want %q", out.Error.Stage, schema.StageValidateArtifact)
	}
	if len(out.Attempts) != limits.MaxRepairs {
		t.Fatalf("attempts = %d, want exactly %d", len(out.Attempts), limits.MaxRepairs)
	}
	if out.Evidence.Repairs != limits.MaxRepairs {
		t.Fatalf("evidence repairs = %d, want %d", out.Evidence.Repairs, limits.MaxRepairs)
	}
	if got := int(atomic.LoadInt32(&calls)); got != 1+limits.MaxRepairs {
		t.Fatalf("provider completions = %d, want 1 initial + %d repairs", got, limits.MaxRepairs)
	}
	for i, a := range out.Attempts {
		if a.Number != i+1 {
			t.Errorf("attempt %d numbered %d, want %d", i, a.Number, i+1)
		}
		if a.Stage == "" || a.Detail == "" {
			t.Errorf("attempt %d = %+v, want the failed stage and a reason", i, a)
		}
	}
	if n := depot.activeCount(); n != 0 {
		t.Fatalf("depot holds %d active challenge(s), want 0", n)
	}
	if got := stageState(out, schema.StageActivate); got != schema.StateSkipped {
		t.Fatalf("activate stage = %q, want %q", got, schema.StateSkipped)
	}
	if got := stageState(out, schema.StageRepair); got != schema.StateDone {
		t.Fatalf("repair stage = %q, want %q (repairs were attempted)", got, schema.StateDone)
	}
}

// TestUnavailableEnvironmentIsReportedNotRepaired keeps "the database is down"
// distinct from "the content is bad": no repair can fix a missing engine, so
// the pipeline reports it once instead of spending the budget.
func TestUnavailableEnvironmentIsReportedNotRepaired(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	var calls int32
	svc := NewService(Config{
		Provider: countingProvider{calls: &calls, base: NewFakeProvider()},
		Depot:    depot,
		Runner:   failingRunner{},
	})
	out := svc.GenerateChallenge(ctx, schema.GenerationRequest{Title: "No database", Topics: []string{"joins"}})
	if out.Status != schema.StatusFailed {
		t.Fatalf("status = %q, want %q", out.Status, schema.StatusFailed)
	}
	if out.Error == nil || out.Error.Code != CodeEnvironmentDown {
		t.Fatalf("error = %+v, want code %q", out.Error, CodeEnvironmentDown)
	}
	if int(atomic.LoadInt32(&calls)) != 1 {
		t.Fatalf("provider completions = %d, want 1 (no repair for a missing engine)", calls)
	}
	_ = ex
}

// assertArraysNotNull locks down the JSON shape contracts/ai.schema.json
// requires: `stages` and `attempts` are arrays. A nil Go slice encodes as
// `null`, which the schema rejects, so this is a contract test rather than a
// formatting preference.
func assertArraysNotNull(t *testing.T, out schema.Outcome) {
	t.Helper()
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal outcome: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal outcome: %v", err)
	}
	if got := string(decoded["attempts"]); !strings.HasPrefix(got, "[") {
		t.Fatalf("attempts = %s, want a JSON array", got)
	}
	if got := string(decoded["stages"]); !strings.HasPrefix(got, "[") {
		t.Fatalf("stages = %s, want a JSON array", got)
	}
	for _, st := range out.Stages {
		if strings.TrimSpace(st.Detail) == "" {
			t.Errorf("stage %s has an empty detail", st.Name)
		}
	}
}

// TestOutcomeWithoutRepairsMarshalsArrays is the unit-level form of the same
// guarantee: a pipeline that never repaired and never got past its first stage
// still reports arrays and a reason on every row.
func TestOutcomeWithoutRepairsMarshalsArrays(t *testing.T) {
	p := newPipeline(nil, schema.GenerationRequest{Kind: "challenge"}, DefaultLimits())
	out := p.outcome(schema.StatusFailed, nil,
		NewError(CodeRepairExhausted, schema.StageValidateArtifact, "the bundle was refused", "hints: too many"))

	assertArraysNotNull(t, out)
	if got := stageState(out, schema.StageRepair); got != schema.StateSkipped {
		t.Fatalf("repair = %q, want %q", got, schema.StateSkipped)
	}
	if got := stageDetail(out, schema.StageRepair); got != "not needed: no stage failed" {
		t.Fatalf("repair detail = %q", got)
	}

	// A stage skipped because an earlier one failed says which one stopped it.
	p = newPipeline(nil, schema.GenerationRequest{Kind: "challenge"}, DefaultLimits())
	p.mark(schema.StageValidateRequest, schema.StateFailed, "bad request")
	out = p.outcome(schema.StatusFailed, nil, NewError(CodeInvalidRequest, schema.StageValidateRequest, "bad request", "title"))
	if got := stageDetail(out, schema.StageActivate); got != "skipped: validate_request failed" {
		t.Fatalf("activate detail = %q, want the stage that stopped the run", got)
	}
	if got := stageDetail(out, schema.StageRepair); got != "skipped: validate_request failed" {
		t.Fatalf("repair detail = %q, want the stage that stopped the run", got)
	}
	if got := stageDetail(out, schema.StageValidateRequest); got != "bad request" {
		t.Fatalf("validate_request detail = %q, want the failure detail", got)
	}
}
