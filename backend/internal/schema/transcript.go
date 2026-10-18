package schema

import "time"

/* The stored transcript of one generation run: the audit trail. RunTranscript is
 * what the recorder writes and schema.Depot persists; RunSummary is the same
 * record without its entries, for the list route. MaxTranscriptEntries and
 * MaxTranscriptBytes are the bounds the recorder enforces before a record
 * reaches the store.
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
