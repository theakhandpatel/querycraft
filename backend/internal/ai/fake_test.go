package ai

import (
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
)

func TestFakeProviderIsDeterministic(t *testing.T) {
	p := NewFakeProvider()
	ctx := testContext(t)
	req := ProviderRequest{Kind: "challenge", Prompt: "Topics: joins\nDifficulty: beginner"}

	first, err := p.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	second, err := p.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if first.Text != second.Text {
		t.Fatal("the same request produced two different bundles")
	}

	other, err := p.Complete(ctx, ProviderRequest{Kind: "challenge", Prompt: "Topics: window functions"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if other.Text == first.Text {
		t.Fatal("a different topic produced the same bundle; the request does not change the outcome")
	}

	d := p.Descriptor()
	if d.Name != "fake" || d.Model != "fake-v1" || d.Version != "1" || !d.Deterministic || !d.Configured {
		t.Fatalf("descriptor = %+v, want {fake fake-v1 1 deterministic configured}", d)
	}
}

func TestFakeProviderServesEveryKind(t *testing.T) {
	p := NewFakeProvider()
	ctx := testContext(t)
	for _, kind := range []string{"challenge", "course", "repair", "tutor", "critique"} {
		t.Run(kind, func(t *testing.T) {
			resp, err := p.Complete(ctx, ProviderRequest{
				Kind:     kind,
				Prompt:   "Topics: joins\nhint_level: 1",
				Previous: `{"title":"old"}`,
				Failure:  "validate_artifact: bad",
			})
			if err != nil {
				t.Fatalf("Complete(%s) error = %v", kind, err)
			}
			if strings.TrimSpace(resp.Text) == "" {
				t.Fatalf("Complete(%s) returned an empty reply", kind)
			}
		})
	}
	if _, err := p.Complete(ctx, ProviderRequest{Kind: "haiku"}); err == nil {
		t.Fatal("an unsupported kind should be an error, not an empty reply")
	}
}

// TestFakeProviderTopicsProduceRunnableReferences is the generator's own
// contract: every topic's reference runs on the real dataset and its
// independent cross-check agrees under the artifact's result contract.
func TestFakeProviderTopicsProduceRunnableReferences(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	p := NewFakeProvider()

	for _, topic := range fakeTopics {
		t.Run(topic.name, func(t *testing.T) {
			resp, err := p.Complete(ctx, ProviderRequest{
				Kind:   "challenge",
				Prompt: "Topics: " + topic.name + " (" + strings.Join(topic.keywords, ", ") + ")\nDifficulty: intermediate",
			})
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			var art Artifact
			if derr := decodeBundle(resp.Text, &art); derr != nil {
				t.Fatalf("decode bundle: %v", derr)
			}
			if art.Title != topic.title {
				t.Fatalf("topic = %q, want %q", art.Title, topic.title)
			}
			if verr := validateArtifact(art, DefaultLimits()); verr != nil {
				t.Fatalf("generated bundle failed its own policy: %v (%s)", verr, verr.Detail)
			}
			if art.Setup.Dataset != "commerce" || len(art.Setup.Statements) != 0 {
				t.Fatalf("setup = %+v, want the sealed commerce dataset with no statements", art.Setup)
			}

			ref := engine.Execute(ctx, ex, art.ReferenceSolution, engine.DefaultLimits())
			if ref.Status != "succeeded" || ref.Truncated {
				t.Fatalf("reference status = %q truncated = %v error = %v", ref.Status, ref.Truncated, ref.Error)
			}
			if ref.RowCount == 0 {
				t.Fatal("reference returned no rows")
			}
			cross := engine.Execute(ctx, ex, art.CrossCheckSolution, engine.DefaultLimits())
			if cross.Status != "succeeded" || cross.Truncated {
				t.Fatalf("cross-check status = %q error = %v", cross.Status, cross.Error)
			}
			contract := evalContract(art.ResultContract)
			cmp := eval.CompareDetailed(eval.ExpectedResult{Columns: contract.Columns, Rows: ref.Rows}, contract, cross)
			if !cmp.OK {
				t.Fatalf("cross-check disagrees with the reference: %v", cmp.Codes)
			}
		})
	}
}
