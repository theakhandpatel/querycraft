package ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// recordingProvider captures the last request, so a test can assert what the
// service did — and did not — send to the model.
type recordingProvider struct {
	inner provider.Provider
	last  provider.ProviderRequest
}

func (r *recordingProvider) Descriptor() provider.ProviderDescriptor { return r.inner.Descriptor() }

func (r *recordingProvider) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	r.last = req
	return r.inner.Complete(ctx, req)
}

// seedChallenge runs the real pipeline once and returns the active artifact.
func seedChallenge(t *testing.T, ex engine.Executor, depot *testDepot, p provider.Provider) schema.Artifact {
	t.Helper()
	svc := testService(p, depot, ex)
	out := svc.GenerateChallenge(testContext(t), schema.GenerationRequest{
		Title:      "Orders per customer",
		Topics:     []string{"joins"},
		Difficulty: schema.Beginner,
	})
	if out.Status != schema.StatusActive {
		t.Fatalf("seed generation failed: status = %q error = %v", out.Status, out.Error)
	}
	return *out.Artifact
}

func TestTutorSendsNoSolutionToTheProvider(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	art := seedChallenge(t, ex, depot, NewFakeProvider())

	rec := &recordingProvider{inner: NewFakeProvider()}
	svc := testService(rec, depot, ex)
	resp, err := svc.Tutor(ctx, TutorRequest{
		ChallengeID: art.ID,
		SQL:         "SELECT 1",
		Objective:   "Count each customer's orders.",
		HintLevel:   99, // over the cap; the service clamps it
	})
	if err != nil {
		t.Fatalf("Tutor() error = %v", err)
	}
	if rec.last.Kind != "tutor" {
		t.Fatalf("provider kind = %q, want tutor", rec.last.Kind)
	}
	if strings.Contains(rec.last.Prompt, art.ReferenceSolution) || strings.Contains(rec.last.Prompt, art.CrossCheckSolution) {
		t.Fatal("the tutor request carried a solution")
	}
	if !strings.Contains(rec.last.Prompt, "hint_level: 3") {
		t.Fatalf("hint level was not clamped to the limit: %q", rec.last.Prompt)
	}
	if resp.HintLevel != 3 {
		t.Fatalf("response hint level = %d, want 3", resp.HintLevel)
	}
	if resp.RevealsSolution {
		t.Fatal("a hint must never report that it reveals the solution")
	}
}

// A hint is prose, so the service must not send it the exercise system prompt,
// which asks for one JSON object. The built-in provider answers by Kind and
// never reads the system message, so this asserts against the request the
// service built rather than against anything the provider did with it.
func TestTutorAndCritiqueSendTheProseSystemPrompt(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	art := seedChallenge(t, ex, depot, NewFakeProvider())

	rec := &recordingProvider{inner: NewFakeProvider()}
	svc := testService(rec, depot, ex)

	if _, err := svc.Tutor(ctx, TutorRequest{
		ChallengeID: art.ID,
		SQL:         "SELECT 1",
		Objective:   "Count each customer's orders.",
	}); err != nil {
		t.Fatalf("Tutor() error = %v", err)
	}
	if rec.last.System != feedbackSystemPrompt() {
		t.Errorf("tutor system prompt = %q, want the feedback prompt", rec.last.System)
	}

	if _, err := svc.Critique(ctx, CritiqueRequest{ChallengeID: art.ID, SQL: "SELECT 1"}); err != nil {
		t.Fatalf("Critique() error = %v", err)
	}
	if rec.last.System != feedbackSystemPrompt() {
		t.Errorf("critique system prompt = %q, want the feedback prompt", rec.last.System)
	}
}

func TestTutorNeverRevealsTheReference(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	art := seedChallenge(t, ex, depot, NewFakeProvider())

	// The provider cannot have seen the reference — the request does not carry
	// it — but a hostile provider that already knows it must still be refused.
	hostile := fixedProvider{text: "Sure, just run this:\n" + art.ReferenceSolution}
	svc := testService(hostile, depot, ex)
	resp, err := svc.Tutor(ctx, TutorRequest{
		ChallengeID: art.ID,
		SQL:         "SELECT 1",
		Objective:   "Count each customer's orders.",
		HintLevel:   1,
	})
	if err == nil {
		t.Fatalf("a reply containing the reference solution was returned: %+v", resp)
	}
	var aiErr *schema.Error
	if !errors.As(err, &aiErr) || aiErr.Code != CodeInvalidArtifact {
		t.Fatalf("error = %v, want an %s error", err, CodeInvalidArtifact)
	}
	if resp.Message != "" {
		t.Fatal("a refused hint must not carry the provider's text")
	}
	if containsSolution(err.Error(), art.ReferenceSolution) {
		t.Fatal("the refusal echoed the reference solution back to the caller")
	}

	// The same guard covers an on-demand critique.
	if _, cerr := svc.Critique(ctx, CritiqueRequest{ChallengeID: art.ID, SQL: "SELECT 1"}); cerr == nil {
		t.Fatal("a critique containing the reference solution was returned")
	}
}

func TestDefectIsVerifiedByExecutionBeforeAPatchIsProposed(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	art := seedChallenge(t, ex, depot, NewFakeProvider())
	svc := testService(NewFakeProvider(), depot, ex)

	// A report with no query is not a probe, so it cannot be verified.
	v, err := svc.ReportDefect(ctx, DefectReport{ChallengeID: art.ID, Description: "the exercise is wrong"})
	if err != nil {
		t.Fatalf("ReportDefect() error = %v", err)
	}
	if v.Verified || v.Patch != nil || v.Applied {
		t.Fatalf("verdict = %+v, want an unverified report with no patch", v)
	}

	// The reference is not a defect: it agrees with itself by definition.
	v, err = svc.ReportDefect(ctx, DefectReport{
		ChallengeID: art.ID,
		Description: "I think my correct query is graded wrong",
		SQL:         art.ReferenceSolution,
	})
	if err != nil {
		t.Fatalf("ReportDefect() error = %v", err)
	}
	if v.Verified || v.Patch != nil {
		t.Fatalf("verdict = %+v, want the claim not to reproduce", v)
	}

	// A query that disagrees with the reference does reproduce.
	wrong := `SELECT c.name AS customer_name, count(o.id) + 1 AS order_count
FROM "dataset"."customers" c
JOIN "dataset"."orders" o ON o.customer_id = c.id
GROUP BY c.name
ORDER BY order_count DESC, customer_name ASC`
	v, err = svc.ReportDefect(ctx, DefectReport{
		ChallengeID: art.ID,
		Description: "the row values should match mine",
		SQL:         wrong,
		Expected:    "count per customer",
		Observed:    "one more than the reference",
	})
	if err != nil {
		t.Fatalf("ReportDefect() error = %v", err)
	}
	if !v.Verified {
		t.Fatalf("verdict = %+v, want the defect verified by execution", v)
	}
	if v.Applied {
		t.Fatal("a defect verdict must never apply a patch")
	}
	if v.Patch == nil || v.Patch.ID == "" || len(v.Patch.Changes) == 0 {
		t.Fatalf("patch = %+v, want a stored proposal with changes", v.Patch)
	}
	if depot.patchCount() != 1 {
		t.Fatalf("stored patches = %d, want 1", depot.patchCount())
	}
	if _, found, _ := depot.Patch(ctx, v.Patch.ID); !found {
		t.Fatal("the proposal was not stored")
	}
}

func TestPatchIsNeverAppliedWithoutApproval(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	art := seedChallenge(t, ex, depot, NewFakeProvider())
	svc := testService(NewFakeProvider(), depot, ex)

	wrong := `SELECT c.name AS customer_name, count(o.id) + 1 AS order_count
FROM "dataset"."customers" c
JOIN "dataset"."orders" o ON o.customer_id = c.id
GROUP BY c.name
ORDER BY order_count DESC, customer_name ASC`
	v, err := svc.ReportDefect(ctx, DefectReport{ChallengeID: art.ID, Description: "values differ", SQL: wrong})
	if err != nil || !v.Verified || v.Patch == nil {
		t.Fatalf("could not seed a verified patch: verdict = %+v error = %v", v, err)
	}
	patchID := v.Patch.ID

	// Refused: no approval.
	res, err := svc.ApplyPatch(ctx, PatchDecision{PatchID: patchID, Approved: false, ApprovedBy: "learner@example.com"})
	if err == nil {
		t.Fatal("an unapproved patch was applied")
	}
	var aiErr *schema.Error
	if !errors.As(err, &aiErr) || aiErr.Code != CodePatchUnapproved {
		t.Fatalf("error = %v, want %s", err, CodePatchUnapproved)
	}
	if res.Applied {
		t.Fatal("PatchResult.Applied must be false on a refusal")
	}

	// Refused: approved but nobody signed it.
	if _, err := svc.ApplyPatch(ctx, PatchDecision{PatchID: patchID, Approved: true, ApprovedBy: "   "}); err == nil {
		t.Fatal("an approval without an approver was accepted")
	}

	// The stored artifact is untouched by both refusals.
	rec, _, _ := depot.Challenge(ctx, art.ID)
	if rec.Artifact.Version != art.Version || rec.Artifact.Prompt != art.Prompt {
		t.Fatalf("the challenge changed after a refused patch: %+v", rec.Artifact)
	}

	// Approved and signed: the patch is verified and then applied.
	res, err = svc.ApplyPatch(ctx, PatchDecision{PatchID: patchID, Approved: true, ApprovedBy: "learner@example.com"})
	if err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	if !res.Applied {
		t.Fatalf("PatchResult = %+v, want applied", res)
	}
	updated, found, _ := depot.Challenge(ctx, art.ID)
	if !found {
		t.Fatal("the challenge disappeared after the patch")
	}
	if updated.Artifact.Version != art.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Artifact.Version, art.Version+1)
	}
	if updated.Artifact.Prompt == art.Prompt {
		t.Fatal("the approved patch did not change the artifact")
	}
	if got := depot.approved[patchID]; got != "learner@example.com" {
		t.Fatalf("recorded approver = %q, want learner@example.com", got)
	}
}

func TestAPatchCannotBeAppliedThroughAnotherChallengesRoute(t *testing.T) {
	// Security regression: the patch is loaded by id and applied to the
	// challenge named *inside the patch*, so without a route binding an approval
	// sent to `/challenges/A/patches/<patch-for-B>` would modify B. The route
	// challenge id must be a binding, not decoration.
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	art := seedChallenge(t, ex, depot, NewFakeProvider())
	svc := testService(NewFakeProvider(), depot, ex)

	wrong := `SELECT c.name AS customer_name, count(o.id) + 1 AS order_count
FROM "dataset"."customers" c
JOIN "dataset"."orders" o ON o.customer_id = c.id
GROUP BY c.name
ORDER BY order_count DESC, customer_name ASC`
	v, err := svc.ReportDefect(ctx, DefectReport{ChallengeID: art.ID, Description: "values differ", SQL: wrong})
	if err != nil || v.Patch == nil {
		t.Fatalf("could not seed a verified patch: %+v / %v", v, err)
	}

	// Approved and signed, but sent under a DIFFERENT challenge's route: refuse,
	// and leave both the artifact and the approval record untouched.
	res, err := svc.ApplyPatch(ctx, PatchDecision{
		PatchID:     v.Patch.ID,
		ChallengeID: "some-other-challenge",
		Approved:    true,
		ApprovedBy:  "learner@example.com",
	})
	if err == nil {
		t.Fatal("a patch was applied through another challenge's route")
	}
	var aiErr *schema.Error
	if !errors.As(err, &aiErr) || aiErr.Code != CodeNotFound {
		t.Fatalf("error = %v, want %s", err, CodeNotFound)
	}
	if res.Applied {
		t.Fatal("PatchResult.Applied must be false on a mismatch")
	}
	rec, _, _ := depot.Challenge(ctx, art.ID)
	if rec.Artifact.Version != art.Version || rec.Artifact.Prompt != art.Prompt {
		t.Fatalf("the challenge changed after a mismatched apply: %+v", rec.Artifact)
	}
	if _, approved := depot.approved[v.Patch.ID]; approved {
		t.Fatal("the patch was recorded as approved despite the mismatch")
	}

	// The same patch, under its own challenge's route, still applies.
	res, err = svc.ApplyPatch(ctx, PatchDecision{
		PatchID:     v.Patch.ID,
		ChallengeID: art.ID,
		Approved:    true,
		ApprovedBy:  "learner@example.com",
	})
	if err != nil || !res.Applied {
		t.Fatalf("the correctly-routed apply failed: res=%+v err=%v", res, err)
	}
}

func TestAvailableExplainsWhatIsMissing(t *testing.T) {
	runner := failingRunner{} // non-nil and never called by Available
	tests := []struct {
		name    string
		cfg     Config
		wantSub string
	}{
		{name: "no provider", cfg: Config{Depot: newTestDepot(), Runner: runner}, wantSub: "no provider"},
		{name: "no database", cfg: Config{Provider: NewFakeProvider(), Depot: newTestDepot()}, wantSub: "database"},
		{name: "no depot", cfg: Config{Provider: NewFakeProvider(), Runner: runner}, wantSub: "stored"},
		{name: "http provider without a key", cfg: Config{
			Provider: provider.NewHTTPProvider(provider.HTTPProviderConfig{BaseURL: "http://127.0.0.1:1/v1", Model: "gpt-x"}),
			Depot:    newTestDepot(),
			Runner:   runner,
		}, wantSub: "API key"},
		{name: "http provider without an address", cfg: Config{
			Provider: provider.NewHTTPProvider(provider.HTTPProviderConfig{Model: "gpt-x", APIKey: "k"}),
			Depot:    newTestDepot(),
			Runner:   runner,
		}, wantSub: "not configured"},
	}
	seen := map[string]bool{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			av := NewService(tc.cfg).Available()
			if av.Available {
				t.Fatalf("Available() = %+v, want available=false", av)
			}
			if !strings.Contains(av.Reason, tc.wantSub) {
				t.Fatalf("reason %q does not mention %q", av.Reason, tc.wantSub)
			}
			seen[av.Reason] = true
		})
	}
	if len(seen) != len(tests) {
		t.Fatalf("reasons are not distinct: %d unique for %d cases", len(seen), len(tests))
	}

	// Everything present: the fake provider needs no key and the lab is on.
	av := NewService(Config{Provider: NewFakeProvider(), Depot: newTestDepot(), Runner: runner}).Available()
	if !av.Available || av.Reason != "" || av.Provider != "fake" || !av.Deterministic {
		t.Fatalf("Available() = %+v, want the lab available", av)
	}
}
