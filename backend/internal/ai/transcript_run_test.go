package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The seam from the outside: a run through the service leaves a record.
 *
 * These tests stay beside the pipeline because they drive it: they need the
 * real database, the in-process depot and the service. The recorder's own
 * promises (redaction, the bounds, the clip) are tested in the transcript
 * package, next to the code that keeps them. */

/* ---------- the seam: a challenge run is recorded ---------- */

// The transcript is captured at the provider seam, so the challenge pipeline
// and the M8 course agent are both covered. A challenge run records what it
// sent and what the model replied.
func TestAChallengeRunIsRecorded(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	d := newTestDepot()
	svc := testService(NewFakeProvider(), d, ex)

	out := svc.GenerateChallenge(ctx, schema.GenerationRequest{Topics: []string{"joins"}})
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want active (error = %+v)", out.Status, out.Error)
	}
	if d.runCount() != 1 {
		t.Fatalf("stored %d transcripts, want 1", d.runCount())
	}
	var rec schema.RunTranscript
	for _, r := range d.runs {
		rec = r
	}
	if rec.Kind != schema.RunKindChallenge {
		t.Fatalf("kind = %q, want challenge", rec.Kind)
	}
	if rec.Status != schema.StatusActive {
		t.Fatalf("status = %q, want active", rec.Status)
	}
	// The system prompt and the user brief are the run's first two turns.
	if len(rec.Entries) < 3 {
		t.Fatalf("transcript has %d entries, want the conversation", len(rec.Entries))
	}
	if rec.Entries[0].Kind != schema.EntryMessage || rec.Entries[0].Role != "system" {
		t.Fatalf("entry 0 = %+v, want the system message", rec.Entries[0])
	}
	if rec.Entries[1].Role != "user" {
		t.Fatalf("entry 1 = %+v, want the user brief", rec.Entries[1])
	}
	// The last entry is the assistant's bundle, redacted and attributed.
	last := rec.Entries[len(rec.Entries)-1]
	if last.Role != "assistant" || !strings.Contains(last.Content, "reference_solution") {
		t.Fatalf("the reply was not recorded: %+v", last)
	}
	for i, e := range rec.Entries {
		if e.Seq != i {
			t.Fatalf("entry %d has seq %d: the order is the run's", i, e.Seq)
		}
	}
}

// A run that fails before the model answers still leaves a transcript naming
// the failure, because "the request was refused and here is why" is the same
// class of fact.
func TestARefusedRunIsStillRecorded(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	d := newTestDepot()
	svc := testService(NewFakeProvider(), d, ex)

	// A request with no title and no topics is refused at validate_request.
	out := svc.GenerateChallenge(ctx, schema.GenerationRequest{})
	if out.Status != schema.StatusFailed {
		t.Fatalf("status = %q, want failed", out.Status)
	}
	rec, ok := d.runByID(onlyRunID(t, d))
	if !ok || rec.Status != schema.StatusFailed {
		t.Fatalf("a refused run left no transcript: %+v", rec)
	}
}

// The M8 course agent owns its conversation, so its turns are recorded as it
// assembles them: the two opening messages once, each assistant turn and each
// tool call/result in order.
func TestACourseAgentRunRecordsItsToolCalls(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	d := newTestDepot()
	svc := testService(NewFakeProvider(), d, ex)

	out := svc.GenerateCourse(ctx, schema.GenerationRequest{
		Topics:   []string{"joins"},
		Scenario: "Count what each customer ordered.",
	})
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want active (error = %+v)", out.Status, out.Error)
	}
	if d.runCount() != 1 {
		t.Fatalf("stored %d transcripts, want 1", d.runCount())
	}
	var rec schema.RunTranscript
	for _, r := range d.runs {
		rec = r
	}
	if rec.Kind != schema.RunKindCourse {
		t.Fatalf("kind = %q, want course", rec.Kind)
	}

	var calls, results int
	for _, e := range rec.Entries {
		switch e.Kind {
		case schema.EntryToolCall:
			calls++
			if e.ToolName == "" || e.ToolCallID == "" {
				t.Fatalf("a tool call is missing its attribution: %+v", e)
			}
		case schema.EntryToolResult:
			results++
			if e.ToolCallID == "" {
				t.Fatalf("a tool result does not name the call it answers: %+v", e)
			}
		}
	}
	if calls == 0 || results == 0 {
		t.Fatalf("the course conversation recorded %d calls and %d results, want both", calls, results)
	}
	if calls != results {
		t.Fatalf("%d tool calls but %d results: every call must be answered", calls, results)
	}
	// The conversation's own system prompt appears exactly once: completeRaw
	// must not re-record the messages the agent already recorded. The outline
	// call that preceded the conversation has its own (different) system
	// prompt, which is a separate fact and legitimately its own entry.
	agentSystems := 0
	for _, e := range rec.Entries {
		if e.Kind == schema.EntryMessage && e.Role == "system" && e.Content == courseAgentSystemPrompt() {
			agentSystems++
		}
	}
	if agentSystems != 1 {
		t.Fatalf("the agent's system prompt was recorded %d times, want once", agentSystems)
	}
}

// The transcript is bounded and redacted even when the model misbehaves: a
// provider that returns a credential in its reply leaves a clean record.
func TestAProviderReplyIsRedactedInTheTranscript(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	d := newTestDepot()
	const leaked = "sk-should-never-be-stored-abcdef"
	p := &leakyProvider{inner: NewFakeProvider(), leak: leaked}
	svc := testService(p, d, ex)

	// The run itself may reject the tainted bundle; the point is only what was
	// stored.
	svc.GenerateChallenge(ctx, schema.GenerationRequest{Topics: []string{"joins"}})
	if d.runCount() != 1 {
		t.Fatalf("stored %d transcripts, want 1", d.runCount())
	}
	var rec schema.RunTranscript
	for _, r := range d.runs {
		rec = r
	}
	for _, e := range rec.Entries {
		if strings.Contains(e.Content, leaked) {
			t.Fatalf("a credential reached the transcript: %+v", e)
		}
	}
}

// onlyRunID fails the test unless exactly one transcript is stored, and returns
// its id.
func onlyRunID(t *testing.T, d *testDepot) string {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.runs) != 1 {
		t.Fatalf("stored %d transcripts, want 1", len(d.runs))
	}
	for id := range d.runs {
		return id
	}
	return ""
}

// leakyProvider appends a credential to every reply, the way a model quoting
// its own key would.
type leakyProvider struct {
	inner provider.Provider
	leak  string
}

func (p *leakyProvider) Descriptor() provider.ProviderDescriptor { return p.inner.Descriptor() }

func (p *leakyProvider) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	resp, err := p.inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	resp.Text += "\n\n(Note: my key is " + p.leak + ")"
	for i := range resp.ToolCalls {
		resp.ToolCalls[i].Function.Arguments += " key=" + p.leak
	}
	return resp, nil
}
