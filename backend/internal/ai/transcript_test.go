package ai

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

/* ---------- redaction ---------- */

// A credential must not reach the database, whatever shape it arrives in: a
// provider error can echo a DSN or an Authorization header, and a model can
// quote a key it was handed.
func TestRedactTextRemovesCredentials(t *testing.T) {
	const (
		key   = "sk-live-abcdefghijklmnop1234"
		dsnPW = "hunter2-should-never-appear"
	)
	cases := []struct {
		name string
		in   string
		gone string
	}{
		{"url userinfo DSN", "dial postgres://querycraft_learner:" + dsnPW + "@127.0.0.1:54329/qc failed", dsnPW},
		{"password keyword DSN", "password=" + dsnPW + " sslmode=disable", dsnPW},
		{"authorization header", "unauthorized: Authorization: Bearer " + key, key},
		{"named api key", `{"api_key":"` + key + `"}`, key},
		{"named secret with colon", "client_secret: " + key, key},
		{"prefixed key", "the model quoted " + key + " in its reply", key},
		{"token field", `error: "token":"` + key + `" rejected`, key},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactText(tc.in)
			if strings.Contains(got, tc.gone) {
				t.Fatalf("redactText(%q) = %q, still contains the credential", tc.in, got)
			}
		})
	}
}

// Redaction must not destroy an ordinary diagnostic: the transcript has to
// stay readable.
func TestRedactTextKeepsTheDiagnostic(t *testing.T) {
	in := "The reference solution returned 0 rows at the reference_contract stage."
	if got := redactText(in); got != in {
		t.Fatalf("redactText changed an innocent string:\n got %q\nwant %q", got, in)
	}
}

// A provider that knows a secret this package does not — the HTTP provider's
// key — must have it removed too, through the optional interface.
func TestRecorderUsesTheProvidersOwnScrub(t *testing.T) {
	const key = "provider-only-secret-1234567890"
	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: "http://x", Model: "m", APIKey: key})
	rec := newTranscriptRecorder(RunKindChallenge, "run_1", providerRedactor(p))
	rec.record(EntryMessage, "assistant", "the key is "+key, "", "", 0)

	got, ok := rec.finish(StatusActive, nil), true
	_ = ok
	if strings.Contains(got.Entries[0].Content, key) {
		t.Fatalf("the provider's own credential survived: %q", got.Entries[0].Content)
	}
	if !got.Entries[0].Redacted {
		t.Fatalf("a redacted entry must say so: %+v", got.Entries[0])
	}
}

/* ---------- the bound ---------- */

// A runaway run cannot bloat the database: once the entry cap is reached the
// rest are refused, and the stored record says it is a prefix.
func TestRecorderBoundsTheNumberOfEntries(t *testing.T) {
	rec := newTranscriptRecorder(RunKindChallenge, "run_1", nil)
	for i := 0; i < MaxTranscriptEntries+50; i++ {
		rec.message("assistant", "turn")
	}
	got := rec.finish(StatusActive, nil)
	if len(got.Entries) != MaxTranscriptEntries {
		t.Fatalf("stored %d entries, want the cap of %d", len(got.Entries), MaxTranscriptEntries)
	}
	if got.EntryCount != MaxTranscriptEntries+50 {
		t.Fatalf("entry_count = %d, want every entry the run produced", got.EntryCount)
	}
	if !got.Truncated {
		t.Fatalf("a bounded transcript must report itself truncated")
	}
}

// The byte cap bounds a run that produces few but enormous entries.
func TestRecorderBoundsTheBytes(t *testing.T) {
	rec := newTranscriptRecorder(RunKindCourse, "run_1", nil)
	// Each entry is the per-entry maximum, so a handful reach the byte cap.
	big := strings.Repeat("x", maxTranscriptEntryChars)
	for i := 0; i < 64; i++ {
		rec.message("assistant", big)
	}
	got := rec.finish(StatusActive, nil)
	if got.Bytes > MaxTranscriptBytes {
		t.Fatalf("stored %d bytes, more than the cap of %d", got.Bytes, MaxTranscriptBytes)
	}
	if !got.Truncated {
		t.Fatalf("a byte-bounded transcript must report itself truncated")
	}
	if len(got.Entries) == 0 {
		t.Fatalf("nothing was stored")
	}
}

// One entry larger than the per-entry bound is clipped, not dropped, and says
// so.
func TestRecorderClipsOneEntry(t *testing.T) {
	rec := newTranscriptRecorder(RunKindChallenge, "run_1", nil)
	rec.message("tool", strings.Repeat("y", maxTranscriptEntryChars+500))
	got := rec.finish(StatusActive, nil)
	if len(got.Entries) != 1 {
		t.Fatalf("stored %d entries, want 1", len(got.Entries))
	}
	if !got.Entries[0].Truncated {
		t.Fatalf("a clipped entry must say so")
	}
	if n := utf8.RuneCountInString(got.Entries[0].Content); n > maxTranscriptEntryChars+32 {
		t.Fatalf("clipped content is %d runes, want about the bound", n)
	}
}

/* ---------- the seam: a challenge run is recorded ---------- */

// The transcript is captured at the provider seam, so the challenge pipeline
// and the M8 course agent are both covered. A challenge run records what it
// sent and what the model replied.
func TestAChallengeRunIsRecorded(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	d := newTestDepot()
	svc := testService(NewFakeProvider(), d, ex)

	out := svc.GenerateChallenge(ctx, GenerationRequest{Topics: []string{"joins"}})
	if out.Status != StatusActive {
		t.Fatalf("status = %q, want active (error = %+v)", out.Status, out.Error)
	}
	if d.runCount() != 1 {
		t.Fatalf("stored %d transcripts, want 1", d.runCount())
	}
	var rec RunTranscript
	for _, r := range d.runs {
		rec = r
	}
	if rec.Kind != RunKindChallenge {
		t.Fatalf("kind = %q, want challenge", rec.Kind)
	}
	if rec.Status != StatusActive {
		t.Fatalf("status = %q, want active", rec.Status)
	}
	// The system prompt and the user brief are the run's first two turns.
	if len(rec.Entries) < 3 {
		t.Fatalf("transcript has %d entries, want the conversation", len(rec.Entries))
	}
	if rec.Entries[0].Kind != EntryMessage || rec.Entries[0].Role != "system" {
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
	out := svc.GenerateChallenge(ctx, GenerationRequest{})
	if out.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", out.Status)
	}
	rec, ok := d.runByID(onlyRunID(t, d))
	if !ok || rec.Status != StatusFailed {
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

	out := svc.GenerateCourse(ctx, GenerationRequest{
		Topics:   []string{"joins"},
		Scenario: "Count what each customer ordered.",
	})
	if out.Status != StatusActive {
		t.Fatalf("status = %q, want active (error = %+v)", out.Status, out.Error)
	}
	if d.runCount() != 1 {
		t.Fatalf("stored %d transcripts, want 1", d.runCount())
	}
	var rec RunTranscript
	for _, r := range d.runs {
		rec = r
	}
	if rec.Kind != RunKindCourse {
		t.Fatalf("kind = %q, want course", rec.Kind)
	}

	var calls, results int
	for _, e := range rec.Entries {
		switch e.Kind {
		case EntryToolCall:
			calls++
			if e.ToolName == "" || e.ToolCallID == "" {
				t.Fatalf("a tool call is missing its attribution: %+v", e)
			}
		case EntryToolResult:
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
		if e.Kind == EntryMessage && e.Role == "system" && e.Content == courseAgentSystemPrompt() {
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
	svc.GenerateChallenge(ctx, GenerationRequest{Topics: []string{"joins"}})
	if d.runCount() != 1 {
		t.Fatalf("stored %d transcripts, want 1", d.runCount())
	}
	var rec RunTranscript
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
	inner Provider
	leak  string
}

func (p *leakyProvider) Descriptor() ProviderDescriptor { return p.inner.Descriptor() }

func (p *leakyProvider) Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
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

// The run id a caller names — a job id — is the transcript's id, so the run a
// learner polled and the run they can later audit share one name.
func TestARunIDCanBeSuppliedByTheCaller(t *testing.T) {
	rec := newTranscriptRecorder(RunKindCourse, "gen-abc123", nil)
	if got := rec.finish(StatusActive, nil).ID; got != "gen-abc123" {
		t.Fatalf("run id = %q, want the supplied gen-abc123", got)
	}
	ctx := WithRunID(context.Background(), "gen-xyz")
	if got := newTranscriptRecorder(RunKindChallenge, runIDFrom(ctx), nil).id; got != "gen-xyz" {
		t.Fatalf("run id from the context = %q, want gen-xyz", got)
	}
}

// A record's timestamps bracket the run: the store keeps the first start.
func TestRecorderTimestampsFrameTheRun(t *testing.T) {
	rec := newTranscriptRecorder(RunKindChallenge, "run_1", nil)
	rec.message("user", "hi")
	got := rec.finish(StatusRejected, &Error{Code: CodeRepairExhausted, Message: "no"})
	if got.StartedAt.IsZero() || got.FinishedAt.IsZero() {
		t.Fatalf("a record must say when it ran: %+v", got)
	}
	if got.FinishedAt.Before(got.StartedAt) {
		t.Fatalf("finished before started: %+v", got)
	}
	if got.Error == nil || got.Error.Code != CodeRepairExhausted {
		t.Fatalf("the terminal error did not travel: %+v", got.Error)
	}
	if got.Status != StatusRejected {
		t.Fatalf("status = %q, want rejected", got.Status)
	}
}
