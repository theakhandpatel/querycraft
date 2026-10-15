package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* Agent-run audit trail.
 *
 * A generation run is minutes of work by an untrusted model. The artifact that
 * survives it is verified, but the conversation that produced it was not kept
 * anywhere: what the model was asked, what it answered, which function it
 * called and what the engine said back all vanished when the process moved on.
 * "Why was this course stored?" was only answerable from the stored plan, never
 * from the run.
 *
 * This file keeps that conversation. Every provider call a generation makes —
 * the challenge bundle, a repair, the outline, and every chapter turn of the M8
 * course agent — records the messages it sent, the reply it received, and each
 * tool call and tool result in the order they happened. The record is
 * owner-scoped (the write is stamped by the same wrapper that stamps a stored
 * course), redacted (a credential never reaches it), and bounded (a runaway run
 * cannot bloat the database).
 *
 * The seam is the provider call itself: complete() and completeRaw() record a
 * single-shot exchange, and the course agent records the initial turns and each
 * tool result explicitly, because it owns the conversation it builds. Reading
 * is a separate, owner-scoped surface (GET /runs, GET /runs/{id}).
 */

/* ---------- bounds ---------- */

const (
	// MaxTranscriptEntries caps how many transcript entries one run may store.
	// A course at the chapter and exercise bounds is a few hundred turns, so
	// this is generous for a real run and finite for a model that loops.
	MaxTranscriptEntries = 512
	// MaxTranscriptBytes caps the total content a run's transcript may hold.
	// 256 KiB holds several full course conversations in ordinary prose and
	// still bounds what a single run can leave in the database.
	MaxTranscriptBytes = 256 * 1024
	// maxTranscriptEntryChars clips one entry before it is counted. A tool
	// result carries the evaluator's own message and a reply can carry
	// reasoning; neither should be able to spend the whole budget alone.
	maxTranscriptEntryChars = 8000
	// MaxRunList bounds GET /runs. A list of summaries is cheap, but "all of
	// them" is not a bound.
	MaxRunList = 100
)

/* ---------- the record ---------- */

// Run kinds. A transcript is attributed to the kind of generation that made it,
// the same two kinds the outcome envelope carries.
const (
	RunKindChallenge = "challenge"
	RunKindCourse    = "course"
)

// Transcript entry kinds. A message is one turn the model saw or produced; a
// tool call is a function the model asked for; a tool result is what the
// gateway answered it with; an error is a transport or policy failure.
const (
	EntryMessage    = "message"
	EntryToolCall   = "tool_call"
	EntryToolResult = "tool_result"
	EntryError      = "error"
)

// TranscriptEntry is one ordered fact from a run. Exactly one of the kinds is
// set. Content is always present (possibly empty for a text-free assistant turn
// that only called a function).
type TranscriptEntry struct {
	Seq  int    `json:"seq"`
	Kind string `json:"kind"`
	// Role is set for a message: "system", "user", "assistant" or "tool".
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	// ToolCallID and ToolName attribute a tool call or its result.
	ToolCallID string    `json:"tool_call_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	At         time.Time `json:"at"`
	// MS is the provider latency for the assistant turn the entry belongs to.
	MS float64 `json:"ms,omitempty"`
	// Truncated marks content this entry clipped to fit, Redacted marks content
	// a credential pattern was removed from. Both are reported rather than
	// hidden: a reader is told the transcript is not verbatim.
	Truncated bool `json:"truncated,omitempty"`
	Redacted  bool `json:"redacted,omitempty"`
}

// RunTranscript is one generation run's stored conversation plus the facts that
// attribute it: which run, what kind, whose it is, when it ran and how it ended.
type RunTranscript struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	OwnerID string `json:"owner_id,omitempty"`
	// Status is the run's terminal outcome status: active, rejected, failed or
	// unavailable. It is the same word the outcome envelope uses.
	Status  string            `json:"status"`
	Entries []TranscriptEntry `json:"entries"`
	// EntryCount is how many entries the run produced; Entries is what was
	// stored. They differ only when Truncated is true.
	EntryCount int       `json:"entry_count"`
	Bytes      int       `json:"bytes"`
	Truncated  bool      `json:"truncated"`
	Error      *Error    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// RunSummary is a transcript without its entries, for the list route. It is the
// shape a reader scans to find the run they want to open.
type RunSummary struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	OwnerID    string    `json:"owner_id,omitempty"`
	Status     string    `json:"status"`
	EntryCount int       `json:"entry_count"`
	Bytes      int       `json:"bytes"`
	Truncated  bool      `json:"truncated"`
	Error      *Error    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Summary projects a stored transcript onto the list shape.
func (t RunTranscript) Summary() RunSummary {
	return RunSummary{
		ID:         t.ID,
		Kind:       t.Kind,
		OwnerID:    t.OwnerID,
		Status:     t.Status,
		EntryCount: t.EntryCount,
		Bytes:      t.Bytes,
		Truncated:  t.Truncated,
		Error:      t.Error,
		StartedAt:  t.StartedAt,
		FinishedAt: t.FinishedAt,
	}
}

/* ---------- redaction ---------- */

// A transcript is a durable, human-readable record, so it is a place a secret
// could end up: a provider error can echo a request body, a model can quote a
// key it was handed, a tool result can carry a connection string. Everything
// that is written goes through redactText first.
//
// engine.RedactSecrets already removes the DSN shapes (userinfo and
// password=/sslpassword=). The patterns here cover the credential shapes that
// appear in prose, headers and error bodies.
var (
	// Authorization: Bearer <token>
	bearerToken = regexp.MustCompile(`(?i)\b(bearer|token)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// key=value / key: value for the words that name a credential.
	namedSecret = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|access[_-]?key|secret[_-]?key|client[_-]?secret|auth[_-]?token|access[_-]?token|refresh[_-]?token|id[_-]?token|token|secret|password|passwd|pwd)\b\s*[:=]\s*"?[^\s"',;&]+`)
	// OpenAI- and vendor-style key prefixes, which carry no field name.
	prefixedKey = regexp.MustCompile(`\b(sk|pk|rk|api)-[A-Za-z0-9_-]{16,}`)
)

// redactText removes credential values from a string about to be stored. It is
// best-effort by design: a false positive costs a redacted word in a
// transcript, a false negative leaks a credential into the database and out
// through HTTP.
func redactText(s string) string {
	if s == "" {
		return s
	}
	s = engine.RedactSecrets(s)
	s = bearerToken.ReplaceAllString(s, "$1 xxxxx")
	s = namedSecret.ReplaceAllString(s, "$1=xxxxx")
	s = prefixedKey.ReplaceAllString(s, "$1-xxxxx")
	return s
}

// secretRedactor is implemented by a provider that knows a credential this
// package does not. The HTTP provider holds its API key unexported and already
// scrubs it from every error; exposing that scrub (and never the key) lets the
// transcript remove it from model content too.
type secretRedactor interface {
	Redact(string) string
}

func providerRedactor(p Provider) func(string) string {
	if r, ok := p.(secretRedactor); ok {
		return r.Redact
	}
	return nil
}

/* ---------- the recorder ---------- */

// transcriptKey carries the run's recorder to the provider seam. It travels in
// the context for the same reason the progress reporter does: it belongs to one
// run, not to the configuration that serves every run.
type transcriptKey struct{}

// WithTranscript attaches a recorder for one run. A provider call made with no
// recorder in context — tutoring, critique — is not recorded, because it is not
// a generation run.
func WithTranscript(ctx context.Context, r *transcriptRecorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, transcriptKey{}, r)
}

func transcriptFrom(ctx context.Context) *transcriptRecorder {
	r, _ := ctx.Value(transcriptKey{}).(*transcriptRecorder)
	return r
}

// runIDKey carries the id a caller has already given the run — the job id a
// client polls — so the transcript and the job share one identity. Absent, the
// recorder mints its own.
type runIDKey struct{}

// WithRunID names one run's transcript after the id the caller already used.
func WithRunID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, id)
}

// runIDFrom returns the caller's run id, or "".
func runIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(runIDKey{}).(string)
	return id
}

// transcriptRecorder accumulates one run's entries. It is owned by the run's
// own goroutine, so it needs no lock; every record method is called from that
// goroutine only.
type transcriptRecorder struct {
	id      string
	kind    string
	started time.Time
	extra   func(string) string

	entries []TranscriptEntry
	bytes   int
	// count is every entry the run produced, including ones the bound refused
	// to store. It is what makes "stored N of M" an honest statement.
	count     int
	truncated bool
}

// newTranscriptRecorder starts a run's record. runID, when non-empty, is the
// id the caller already named the run (a job id), so the transcript and the job
// a client polled share one identity.
func newTranscriptRecorder(kind, runID string, extra func(string) string) *transcriptRecorder {
	if kind == "" {
		kind = RunKindChallenge
	}
	if runID == "" {
		runID = newRunID()
	}
	return &transcriptRecorder{
		id:      runID,
		kind:    kind,
		started: time.Now().UTC(),
		extra:   extra,
	}
}

// record appends one entry, redacted and bounded. A run that exceeds either
// bound keeps the entries it already has and marks the rest refused: the
// transcript is then a prefix of the run, reported as such, rather than a
// database row that grows without limit.
func (r *transcriptRecorder) record(kind, role, content, toolCallID, toolName string, ms float64) {
	if r == nil {
		return
	}
	r.count++
	content, wasRedacted := r.scrub(content)
	content, wasClipped := clipEntry(content)
	if len(r.entries) >= MaxTranscriptEntries || r.bytes+len(content) > MaxTranscriptBytes {
		r.truncated = true
		return
	}
	r.entries = append(r.entries, TranscriptEntry{
		Seq:        len(r.entries),
		Kind:       kind,
		Role:       role,
		Content:    content,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		At:         time.Now().UTC(),
		MS:         ms,
		Truncated:  wasClipped,
		Redacted:   wasRedacted,
	})
	r.bytes += len(content)
}

// scrub redacts a value and reports whether it changed, so the entry can say so.
func (r *transcriptRecorder) scrub(s string) (string, bool) {
	if s == "" {
		return s, false
	}
	out := redactText(s)
	if r.extra != nil {
		out = r.extra(out)
	}
	return out, out != s
}

// message records one conversation turn the model saw or produced.
func (r *transcriptRecorder) message(role, content string) {
	r.record(EntryMessage, role, content, "", "", 0)
}

// recordRequestMessages records what a single-shot provider call sent: the
// system prompt and the user content, rendered exactly as the transport renders
// it (userContent), so the transcript is what the model saw and not a second
// guess at it.
//
// A call that carries Messages owns its own conversation: the course agent
// records the turns it assembles as it assembles them (see runCourseAgent), and
// recording them here as well would store every chapter turn once per round.
func (r *transcriptRecorder) recordRequestMessages(req ProviderRequest) {
	if r == nil || len(req.Messages) > 0 {
		return
	}
	if strings.TrimSpace(req.System) != "" {
		r.message("system", req.System)
	}
	if content := userContent(req); strings.TrimSpace(content) != "" {
		r.message("user", content)
	}
}

// recordResponse records one assistant turn: its text, when there is any, and
// every function call it asked for.
func (r *transcriptRecorder) recordResponse(resp ProviderResponse) {
	if r == nil {
		return
	}
	if strings.TrimSpace(resp.Text) != "" {
		r.record(EntryMessage, "assistant", resp.Text, "", "", resp.LatencyMS)
	}
	for _, call := range resp.ToolCalls {
		r.record(EntryToolCall, "assistant", call.Function.Arguments, call.ID, call.Function.Name, 0)
	}
}

// recordToolResult records what the gateway answered a tool call with.
func (r *transcriptRecorder) recordToolResult(callID, content string) {
	r.record(EntryToolResult, "tool", content, callID, "", 0)
}

// recordError records a transport or policy failure that ended or interrupted
// the run.
func (r *transcriptRecorder) recordError(err error) {
	if r == nil || err == nil {
		return
	}
	r.record(EntryError, "", err.Error(), "", "", 0)
}

// finish turns the accumulated entries into the stored record.
func (r *transcriptRecorder) finish(status string, err *Error) RunTranscript {
	entries := r.entries
	if entries == nil {
		entries = []TranscriptEntry{}
	}
	return RunTranscript{
		ID:         r.id,
		Kind:       r.kind,
		Status:     status,
		Entries:    entries,
		EntryCount: r.count,
		Bytes:      r.bytes,
		Truncated:  r.truncated,
		Error:      err,
		StartedAt:  r.started,
		FinishedAt: time.Now().UTC(),
	}
}

// clipEntry bounds one entry's content on a rune boundary. A cut in the middle
// of a multi-byte character would make the stored text invalid UTF-8.
func clipEntry(s string) (string, bool) {
	if len(s) <= maxTranscriptEntryChars {
		return s, false
	}
	runes := []rune(s)
	if len(runes) <= maxTranscriptEntryChars {
		return s, false
	}
	return string(runes[:maxTranscriptEntryChars]) + "\n... (truncated)", true
}

// newRunID names one run. It is random rather than sequential so one learner
// cannot guess another's id; owner scoping is still what decides who may read
// it.
func newRunID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%016x", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
