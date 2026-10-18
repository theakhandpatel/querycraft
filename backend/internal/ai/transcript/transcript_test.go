package transcript

/* The recorder's own promises: what it redacts, what it bounds and what it
 * clips. The tests that drive a real run through the service live beside the
 * pipeline, in backend/internal/ai/transcript_run_test.go, because they need
 * the pipeline's fixtures. */

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/schema"
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
	p := provider.NewHTTPProvider(provider.HTTPProviderConfig{BaseURL: "http://x", Model: "m", APIKey: key})
	rec := NewRecorder(schema.RunKindChallenge, "run_1", Redactor(p))
	rec.record(schema.EntryMessage, "assistant", "the key is "+key, "", "", 0)

	got := rec.Finish(schema.StatusActive, nil)
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
	rec := NewRecorder(schema.RunKindChallenge, "run_1", nil)
	for i := 0; i < schema.MaxTranscriptEntries+50; i++ {
		rec.Message("assistant", "turn")
	}
	got := rec.Finish(schema.StatusActive, nil)
	if len(got.Entries) != schema.MaxTranscriptEntries {
		t.Fatalf("stored %d entries, want the cap of %d", len(got.Entries), schema.MaxTranscriptEntries)
	}
	if got.EntryCount != schema.MaxTranscriptEntries+50 {
		t.Fatalf("entry_count = %d, want every entry the run produced", got.EntryCount)
	}
	if !got.Truncated {
		t.Fatalf("a bounded transcript must report itself truncated")
	}
}

// The byte cap bounds a run that produces few but enormous entries.
func TestRecorderBoundsTheBytes(t *testing.T) {
	rec := NewRecorder(schema.RunKindCourse, "run_1", nil)
	// Each entry is the per-entry maximum, so a handful reach the byte cap.
	big := strings.Repeat("x", maxTranscriptEntryChars)
	for i := 0; i < 64; i++ {
		rec.Message("assistant", big)
	}
	got := rec.Finish(schema.StatusActive, nil)
	if got.Bytes > schema.MaxTranscriptBytes {
		t.Fatalf("stored %d bytes, more than the cap of %d", got.Bytes, schema.MaxTranscriptBytes)
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
	rec := NewRecorder(schema.RunKindChallenge, "run_1", nil)
	rec.Message("tool", strings.Repeat("y", maxTranscriptEntryChars+500))
	got := rec.Finish(schema.StatusActive, nil)
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

// The run id a caller names — a job id — is the transcript's id, so the run a
// learner polled and the run they can later audit share one name.
func TestARunIDCanBeSuppliedByTheCaller(t *testing.T) {
	rec := NewRecorder(schema.RunKindCourse, "gen-abc123", nil)
	if got := rec.Finish(schema.StatusActive, nil).ID; got != "gen-abc123" {
		t.Fatalf("run id = %q, want the supplied gen-abc123", got)
	}
	ctx := WithRunID(context.Background(), "gen-xyz")
	if got := NewRecorder(schema.RunKindChallenge, RunIDFrom(ctx), nil).ID(); got != "gen-xyz" {
		t.Fatalf("run id from the context = %q, want gen-xyz", got)
	}
}

// A record's timestamps bracket the run: the store keeps the first start.
func TestRecorderTimestampsFrameTheRun(t *testing.T) {
	rec := NewRecorder(schema.RunKindChallenge, "run_1", nil)
	rec.Message("user", "hi")
	got := rec.Finish(schema.StatusRejected, &schema.Error{Code: "AI_REPAIR_EXHAUSTED", Message: "no"})
	if got.StartedAt.IsZero() || got.FinishedAt.IsZero() {
		t.Fatalf("a record must say when it ran: %+v", got)
	}
	if got.FinishedAt.Before(got.StartedAt) {
		t.Fatalf("finished before started: %+v", got)
	}
	if got.Error == nil || got.Error.Code != "AI_REPAIR_EXHAUSTED" {
		t.Fatalf("the terminal error did not travel: %+v", got.Error)
	}
	if got.Status != schema.StatusRejected {
		t.Fatalf("status = %q, want rejected", got.Status)
	}
}
