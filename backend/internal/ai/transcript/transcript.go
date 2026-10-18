// Package transcript keeps the agent-run audit trail: the messages a
// generation sent, the replies it received, and every tool call the engine
// answered, redacted and bounded before they are stored.
package transcript

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
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
 * This file keeps that conversation. Every provider call a generation makes,
 * covering the challenge bundle, a repair, the outline, and every chapter turn
 * of the course agent, records the messages it sent, the reply it received, and
 * each tool call and tool result in the order they happened. The record is
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
	// maxTranscriptEntryChars clips one entry before it is counted. A tool
	// result carries the evaluator's own message and a reply can carry
	// reasoning; neither should be able to spend the whole budget alone.
	maxTranscriptEntryChars = 8000
	// MaxRunList bounds GET /runs. A list of summaries is cheap, but "all of
	// them" is not a bound.
	MaxRunList = 100
)

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

// Redactor returns the provider's own credential scrub, or nil when it has
// none. The caller passes whatever provider.Provider it holds, which is why the
// argument is any: the recorder records any provider, and only some of them
// know a secret.
func Redactor(p any) func(string) string {
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

// WithRecorder attaches a recorder for one run. A provider call made with no
// recorder in context (tutoring, critique) is not recorded, because it is not
// a generation run.
func WithRecorder(ctx context.Context, r *Recorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, transcriptKey{}, r)
}

func From(ctx context.Context) *Recorder {
	r, _ := ctx.Value(transcriptKey{}).(*Recorder)
	return r
}

// runIDKey carries the id a caller has already given the run, the job id a
// client polls, so the transcript and the job share one identity. Absent, the
// recorder mints its own.
type runIDKey struct{}

// WithRunID names one run's transcript after the id the caller already used.
func WithRunID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, id)
}

// RunIDFrom returns the caller's run id, or "".
func RunIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(runIDKey{}).(string)
	return id
}

// Recorder accumulates one run's entries. It is owned by the run's
// own goroutine, so it needs no lock; every record method is called from that
// goroutine only.
type Recorder struct {
	id      string
	kind    string
	started time.Time
	extra   func(string) string

	entries []schema.TranscriptEntry
	bytes   int
	// count is every entry the run produced, including ones the bound refused
	// to store. It is what makes "stored N of M" an honest statement.
	count     int
	truncated bool
}

// NewRecorder starts a run's record. runID, when non-empty, is the
// id the caller already named the run (a job id), so the transcript and the job
// a client polled share one identity.
func NewRecorder(kind, runID string, extra func(string) string) *Recorder {
	if kind == "" {
		kind = schema.RunKindChallenge
	}
	if runID == "" {
		runID = NewID("run-")
	}
	return &Recorder{
		id:      runID,
		kind:    kind,
		started: time.Now().UTC(),
		extra:   extra,
	}
}

// ID is the run's name: the id the caller supplied, or the one minted here.
func (r *Recorder) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

// record appends one entry, redacted and bounded. A run that exceeds either
// bound keeps the entries it already has and marks the rest refused: the
// transcript is then a prefix of the run, reported as such, rather than a
// database row that grows without limit.
func (r *Recorder) record(kind, role, content, toolCallID, toolName string, ms float64) {
	if r == nil {
		return
	}
	r.count++
	content, wasRedacted := r.scrub(content)
	content, wasClipped := clipEntry(content)
	if len(r.entries) >= schema.MaxTranscriptEntries || r.bytes+len(content) > schema.MaxTranscriptBytes {
		r.truncated = true
		return
	}
	r.entries = append(r.entries, schema.TranscriptEntry{
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
func (r *Recorder) scrub(s string) (string, bool) {
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
func (r *Recorder) Message(role, content string) {
	r.record(schema.EntryMessage, role, content, "", "", 0)
}

// RecordRequestMessages records what a single-shot provider call sent: the
// system prompt and the user content, rendered exactly as the transport renders
// it (provider.UserContent), so the transcript is what the model saw and not a
// guess at it.
//
// A call that carries Messages owns its own conversation: the course agent
// records the turns it assembles as it assembles them (see runCourseAgent), and
// recording them here as well would store every chapter turn once per round.
func (r *Recorder) RecordRequestMessages(req provider.ProviderRequest) {
	if r == nil {
		return
	}
	if strings.TrimSpace(req.System) != "" {
		r.Message("system", req.System)
	}
	if content := provider.UserContent(req); strings.TrimSpace(content) != "" {
		r.Message("user", content)
	}
}

// RecordResponse records one assistant turn: its text, when there is any, and
// every function call it asked for.
func (r *Recorder) RecordResponse(resp provider.ProviderResponse) {
	if r == nil {
		return
	}
	if strings.TrimSpace(resp.Text) != "" {
		r.record(schema.EntryMessage, "assistant", resp.Text, "", "", resp.LatencyMS)
	}
	for _, call := range resp.ToolCalls {
		r.record(schema.EntryToolCall, "assistant", call.Function.Arguments, call.ID, call.Function.Name, 0)
	}
}

// RecordToolResult records what the gateway answered a tool call with.
func (r *Recorder) RecordToolResult(callID, content string) {
	r.record(schema.EntryToolResult, "tool", content, callID, "", 0)
}

// RecordError records a transport or policy failure that ended or interrupted
// the run.
func (r *Recorder) RecordError(err error) {
	if r == nil || err == nil {
		return
	}
	r.record(schema.EntryError, "", err.Error(), "", "", 0)
}

// finish turns the accumulated entries into the stored record.
func (r *Recorder) Finish(status string, err *schema.Error) schema.RunTranscript {
	entries := r.entries
	if entries == nil {
		entries = []schema.TranscriptEntry{}
	}
	return schema.RunTranscript{
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

/* ---------- run identity ---------- */

// NewID names one run. It is random rather than sequential so one learner
// cannot guess another's id; owner scoping is still what decides who may read
// it. A job id and a transcript id come from here, because they are one name
// for one run.
func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%016x", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}
