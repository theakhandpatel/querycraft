// Package ai implements the M4 "AI Challenge Lab" generation service
// (technical design §11). The model is an untrusted content generator and this
// package is the policy: nothing becomes visible to a learner until every stage
// of the §11.1 pipeline has passed against a real engine, and a generated
// challenge is never graded by anything except the deterministic evaluator.
//
// The package is provider-neutral (§11.2): it depends on the small Provider
// interface, and a deterministic in-memory provider ships with it, so
// generation can run with no network, no key and no vendor SDK.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// The AI envelope's error codes. These are separate from the closed QueryResult
// enum (contracts/query-result.schema.json): a generation is not a query. Any
// QueryResult embedded in an AI response still uses the closed enum.
const (
	// CodeUnavailable: the provider is off, a provider call failed, or a store
	// write failed.
	CodeUnavailable = "AI_UNAVAILABLE"
	// CodeInvalidRequest: the caller's request is malformed.
	CodeInvalidRequest = "AI_INVALID_REQUEST"
	// CodeInvalidArtifact: the generated bundle failed the policy gate, or a
	// reply carried a stored solution and was discarded.
	CodeInvalidArtifact = "AI_INVALID_ARTIFACT"
	// CodeReferenceFailed: the reference solution does not satisfy its own
	// result contract.
	CodeReferenceFailed = "AI_REFERENCE_FAILED"
	// CodeCrossCheckFailed: the two solutions disagree under the contract.
	CodeCrossCheckFailed = "AI_CROSS_CHECK_FAILED"
	// CodeRepairExhausted: no more specific stage code applies and the repair
	// budget is spent.
	CodeRepairExhausted = "AI_REPAIR_EXHAUSTED"
	// CodeNotFound: no such challenge or patch.
	CodeNotFound = "AI_NOT_FOUND"
	// CodePatchUnverified: a patch passed the approval but failed verification.
	CodePatchUnverified = "AI_PATCH_UNVERIFIED"
	// CodePatchUnapproved: a patch was submitted without an approval.
	CodePatchUnapproved = "AI_PATCH_UNAPPROVED"
	// CodeEnvironmentDown: no database is available to verify against, or a
	// scratch environment could not be created.
	CodeEnvironmentDown = "ENVIRONMENT_UNAVAILABLE"
	// CodeRateLimited: the caller's account spent its tutor budget. It is not
	// CodeUnavailable: the provider is up, the account asked too often.
	CodeRateLimited = "AI_RATE_LIMITED"
)

// NewError builds an envelope error. It is the one constructor for the closed
// code enum, so a caller outside this package (the job registry) reports the
// same envelope a blocking request reports.
func NewError(code, stage, message, detail string) *schema.Error {
	return &schema.Error{Code: code, Stage: stage, Message: message, Detail: detail}
}

/* ---------- tutoring, critique, defects ---------- */

// TutorTurn is one message in a tutor conversation, oldest first. Role is
// "learner" or "tutor"; Text is untrusted prose.
type TutorTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// TutorRequest is the material a hint may use. It deliberately carries no
// reference solution: the service cannot leak what it is not given.
//
// Context and Guards are set by the gateway and never decoded from the body, so
// a client cannot hand the model an assembled context or a solution to compare
// against. Context is the rendered, fenced block list the handler built from the
// exercise, the dataset schema, the learner's own run and the expected result.
// Guards are the reference queries the reply is checked against.
type TutorRequest struct {
	ChallengeID   string `json:"challenge_id,omitempty"`
	CourseID      string `json:"course_id,omitempty"`
	ExerciseID    string `json:"exercise_id,omitempty"`
	SQL           string `json:"sql"`
	Objective     string `json:"objective"`
	ErrorCode     string `json:"error_code,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	HintLevel     int    `json:"hint_level,omitempty"`
	ResultSummary string `json:"result_summary,omitempty"`
	// Turns is the conversation so far, oldest first, capped at MaxTutorTurns.
	Turns []TutorTurn `json:"turns,omitempty"`

	// Context is the gateway-rendered context block list. json:"-" keeps it off
	// the wire: the client sends facts, the gateway derives the context.
	Context string `json:"-"`
	// Guards are the reference queries a reply must not contain. json:"-" for
	// the same reason.
	Guards []string `json:"-"`
}

// TutorContextSummary is the one thing the wire response carries about the
// assembled context, so the drawer renders what the tutor saw instead of
// guessing it.
type TutorContextSummary struct {
	ExerciseID      string   `json:"exercise_id"`
	Dataset         string   `json:"dataset"`
	Dialect         string   `json:"dialect"`
	Tables          []string `json:"tables"`
	TablesTruncated bool     `json:"tables_truncated"`
	Expected        string   `json:"expected"`
	ExpectedColumns int      `json:"expected_columns"`
	ExpectedRows    int      `json:"expected_rows"`
	LearnerResult   string   `json:"learner_result"`
}

// TutorResponse is a hint. RevealsSolution must be false.
type TutorResponse struct {
	Message         string              `json:"message"`
	HintLevel       int                 `json:"hint_level"`
	RevealsSolution bool                `json:"reveals_solution"`
	Provenance      schema.Provenance   `json:"provenance"`
	Context         TutorContextSummary `json:"context"`
}

// CritiqueRequest asks for a review of the learner's own query.
type CritiqueRequest struct {
	ChallengeID string `json:"challenge_id,omitempty"`
	SQL         string `json:"sql"`
	Objective   string `json:"objective,omitempty"`
}

// CritiqueResponse is the on-demand critique.
type CritiqueResponse struct {
	Message    string            `json:"message"`
	Notes      []string          `json:"notes"`
	Provenance schema.Provenance `json:"provenance"`
}

// DefectReport is a learner's claim that a challenge is wrong.
type DefectReport struct {
	ChallengeID string `json:"challenge_id"`
	Description string `json:"description"`
	SQL         string `json:"sql,omitempty"`
	Expected    string `json:"expected,omitempty"`
	Observed    string `json:"observed,omitempty"`
}

// DefectVerdict is the answer to a defect report. Verified is only true after
// the service has executed the reproduction.
type DefectVerdict struct {
	Verified bool                  `json:"verified"`
	Applied  bool                  `json:"applied"`
	Summary  string                `json:"summary"`
	Patch    *schema.PatchProposal `json:"patch"`
	Evidence schema.Evidence       `json:"evidence"`
	Error    *schema.Error         `json:"error"`
}

// PatchDecision is the learner's approval. A patch is never applied without it.
type PatchDecision struct {
	PatchID string `json:"patch_id"`
	// ChallengeID is the challenge the route named. It is a safety binding, not
	// decoration: the patch is loaded by PatchID and applied to the challenge
	// *inside the patch*, so without this a request to approve a patch under one
	// challenge could modify a different one. It is set from the route path,
	// never from the body, and a patch whose ChallengeID disagrees is refused.
	ChallengeID string `json:"challenge_id"`
	Approved    bool   `json:"approved"`
	ApprovedBy  string `json:"approved_by"`
}

// PatchResult reports what happened to the approval. A refusal carries the
// reason in the returned error, so the struct never repeats it.
type PatchResult struct {
	Applied bool                  `json:"applied"`
	Patch   *schema.PatchProposal `json:"patch"`
}

/* ---------- availability ---------- */

// Availability is reported through GET /capabilities so the UI can hide the
// lab instead of offering something that cannot work.
type Availability struct {
	Configured    bool   `json:"configured"`
	Available     bool   `json:"available"`
	Provider      string `json:"provider"`
	Model         string `json:"model,omitempty"`
	Deterministic bool   `json:"deterministic"`
	Reason        string `json:"reason"`
	// TimeoutMS is the run budget: the wall clock within which a generation
	// always answers, whether it activated an artifact or refused one. The
	// gateway hands the pipeline this budget plus headroom on the request
	// context, so a client that waits at least this long always reads the
	// pipeline's own failure instead of a transport error. A browser that
	// picks a shorter deadline turns "still working" into "unreachable".
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

/* ---------- scratch environments ---------- */

// Runner hands the pipeline a throwaway environment. Statements run there
// never touch a learner's workspace or the shared dataset, and the release
// function is always called.
//
// The pipeline takes an Executor rather than a connection string so the
// isolation policy stays in one place (envdb, ADR 0003) and this package cannot
// accidentally acquire administrative reach.
type Runner interface {
	Scratch(ctx context.Context, name string) (engine.Executor, func(), error)
}

/* ---------- the per-call timeout ---------- */

// DefaultCallTimeoutMS is the shipped per-call AI timeout, in milliseconds. It
// is the one home for the number: config.Settings.AITimeoutMS starts from it,
// and a saved AI setting that names no timeout gets the same one
// (httpapi/admin_ai.go). It is sized for the largest legitimate answer, not the
// smallest: a course bundle is four chapters plus every exercise in one reply,
// and a hosted model needed 58.5 s for one. The pipeline's run budget derives
// from this value, so the two cannot disagree.
const DefaultCallTimeoutMS = 120000

/* ---------- limits ---------- */

// Limits bound the cost of a generation run. Every field has a default; a zero
// value means "use the default", never "unlimited".
type Limits struct {
	MaxOutputBytes       int           // largest provider reply or artifact prompt, in bytes
	MaxSetupStatements   int           // most setup statements one bundle may carry
	MaxRepairs           int           // most repair calls per run
	MaxHints             int           // most hints a challenge may carry
	MaxConcurrent        int           // most generation runs in flight at once
	Timeout              time.Duration // wall clock for one whole run
	MaxPromptChars       int           // most characters in one request text field
	MaxTutorTurns        int           // most turns in one tutor conversation
	TutorTimeout         time.Duration // wall clock for one tutor turn
	MaxTutorContextBytes int           // whole rendered tutor context, in bytes
}

// DefaultLimits are the bounds unless config says otherwise.
func DefaultLimits() Limits {
	return Limits{
		// The transport's own reply cap, so a provider with no configured
		// limit and a pipeline with no configured limit agree on one number.
		MaxOutputBytes:     provider.DefaultMaxOutputBytes,
		MaxSetupStatements: 8,
		MaxRepairs:         2,
		MaxHints:           3,
		MaxConcurrent:      2,
		// A run makes several provider calls (bundle, cross-check, adversarial
		// fixtures, then up to MaxRepairs more), so the budget must cover all
		// of them. A shorter budget cuts a real model off mid-run, which the
		// client then reads as a failed request rather than a slow one.
		Timeout:        120 * time.Second,
		MaxPromptChars: 4000,
		// A tutor turn is one provider call over material the gateway already
		// has, so it gets a shorter budget than a whole generation run. A
		// learner who waits past 30 s has moved on.
		MaxTutorTurns:        8,
		TutorTimeout:         30 * time.Second,
		MaxTutorContextBytes: 32 << 10,
	}
}

// Merge fills zero fields from fallback.
func (l Limits) Merge(fallback Limits) Limits {
	if l.MaxOutputBytes <= 0 {
		l.MaxOutputBytes = fallback.MaxOutputBytes
	}
	if l.MaxSetupStatements <= 0 {
		l.MaxSetupStatements = fallback.MaxSetupStatements
	}
	if l.MaxRepairs <= 0 {
		l.MaxRepairs = fallback.MaxRepairs
	}
	if l.MaxHints <= 0 {
		l.MaxHints = fallback.MaxHints
	}
	if l.MaxConcurrent <= 0 {
		l.MaxConcurrent = fallback.MaxConcurrent
	}
	if l.Timeout <= 0 {
		l.Timeout = fallback.Timeout
	}
	if l.MaxPromptChars <= 0 {
		l.MaxPromptChars = fallback.MaxPromptChars
	}
	if l.MaxTutorTurns <= 0 {
		l.MaxTutorTurns = fallback.MaxTutorTurns
	}
	if l.TutorTimeout <= 0 {
		l.TutorTimeout = fallback.TutorTimeout
	}
	if l.MaxTutorContextBytes <= 0 {
		l.MaxTutorContextBytes = fallback.MaxTutorContextBytes
	}
	return l
}

/* ---------- config and service ---------- */

// Config wires the service. Provider and Depot are required for generation;
// Runner is required to validate anything. Missing pieces are reported as
// Availability rather than as a crash, because "AI is off" is a supported mode
// (§11.2: core features work without it).
//
// Provider is the model seam. A provider that also implements
// provider.CourseProvider serves the course chapter conversation; see
// provider.CourseCapability. A provider that does not is refused for a course
// request with a reason, rather than guessed at.
type Config struct {
	Provider provider.Provider
	Depot    schema.Depot
	Runner   Runner
	Logger   *slog.Logger
	Limits   Limits
	// DatasetVersion is stamped into provenance. It is a field rather than an
	// import so this package does not depend on the dataset's contents.
	DatasetVersion int
}

// fixture is one deliberately wrong query.
type fixture struct {
	Name string
	SQL  string
}

// Service is the generation policy. It is safe for concurrent use.
type Service struct {
	cfg Config
}

// NewService builds a service, applying default limits.
func NewService(cfg Config) *Service {
	cfg.Limits = cfg.Limits.Merge(DefaultLimits())
	return &Service{cfg: cfg}
}

// Available reports whether generation can run right now, and why not.
func (s *Service) Available() Availability {
	av := Availability{Reason: "AI is off: no provider is configured."}
	// The budget is advertised even when generation is off: it describes the
	// pipeline, not whether a provider is attached, and a client that has read
	// it once keeps it across a provider that is temporarily unavailable.
	if s != nil {
		av.TimeoutMS = s.cfg.Limits.Timeout.Milliseconds()
	}
	if s == nil || s.cfg.Provider == nil {
		return av
	}
	d := s.cfg.Provider.Descriptor()
	av.Provider = d.Name
	av.Model = d.Model
	av.Deterministic = d.Deterministic
	if !d.Configured {
		av.Reason = "AI is off: the " + d.Name + " provider is not configured."
		return av
	}
	av.Configured = true
	// A provider can be addressable and still unable to answer because it has
	// no credential. The optional interface keeps that fact here rather than in
	// the provider's Descriptor, so a local server that needs no key reports
	// configured and available while a keyed API reports why it cannot run.
	if mc, ok := s.cfg.Provider.(interface{ MissingCredential() bool }); ok && mc.MissingCredential() {
		av.Reason = "AI is off: the " + d.Name + " provider has no API key."
		return av
	}
	if s.cfg.Runner == nil {
		av.Reason = "AI is configured but no database is available to verify against."
		return av
	}
	if s.cfg.Depot == nil {
		av.Reason = "AI is configured but generated challenges cannot be stored."
		return av
	}
	av.Available = true
	av.Reason = ""
	return av
}

// Limits returns the effective pipeline limits. The transport layer reads it to
// give the pipeline headroom inside its own deadline instead of cutting it off
// mid-stage.
func (s *Service) Limits() Limits {
	if s == nil {
		return DefaultLimits()
	}
	return s.cfg.Limits
}

// GenerateChallenge runs the §11.1 pipeline for one challenge.
func (s *Service) GenerateChallenge(ctx context.Context, req schema.GenerationRequest) schema.Outcome {
	req.Kind = "challenge"
	return s.generate(ctx, req)
}

// GenerateCourse runs the same pipeline for a course outline.
func (s *Service) GenerateCourse(ctx context.Context, req schema.GenerationRequest) schema.Outcome {
	req.Kind = "course"
	return s.generate(ctx, req)
}

// ChallengesFor lists one owner's challenges, filtered by the store so the
// owner-scoped list route does not load every learner's records and discard them.
func (s *Service) ChallengesFor(ctx context.Context, ownerID string) ([]schema.Record, error) {
	if s == nil || s.cfg.Depot == nil {
		return nil, NewError(CodeUnavailable, "", "No challenge store is configured.", "")
	}
	return s.cfg.Depot.ChallengesFor(ctx, ownerID)
}

// Challenge fetches one stored challenge.
func (s *Service) Challenge(ctx context.Context, id string) (schema.Record, bool, error) {
	if s == nil || s.cfg.Depot == nil {
		return schema.Record{}, false, NewError(CodeUnavailable, "", "No challenge store is configured.", "")
	}
	return s.cfg.Depot.Challenge(ctx, id)
}

// Reset forgets one generated challenge. Learner attempts are the learner's
// data and are not touched here.
func (s *Service) Reset(ctx context.Context, id string) error {
	if s == nil || s.cfg.Depot == nil {
		return NewError(CodeUnavailable, "", "No challenge store is configured.", "")
	}
	if _, ok, err := s.cfg.Depot.Challenge(ctx, id); err != nil {
		return err
	} else if !ok {
		return NewError(CodeNotFound, "", "No such challenge.", id)
	}
	return s.cfg.Depot.DeleteChallenge(ctx, id)
}

// UnavailableOutcome is the honest "AI is off" outcome for a caller that asked
// for a kind. It takes the availability the caller already read, so the job
// registry refuses a submission with the same envelope and the same reason a
// blocking request gives.
func (s *Service) UnavailableOutcome(kind string, avail Availability) schema.Outcome {
	return s.unavailable(kind, avail)
}

// unavailable is the honest "AI is off" outcome, used by every entry point
// that cannot proceed without a provider.
func (s *Service) unavailable(kind string, avail Availability) schema.Outcome {
	msg := avail.Reason
	if msg == "" {
		msg = "AI generation is not available."
	}
	return schema.Outcome{
		Status:   schema.StatusUnavailable,
		Kind:     kind,
		Artifact: nil,
		Stages:   []schema.Stage{},
		Attempts: []schema.Attempt{},
		Error:    NewError(s.unavailableCode(), "", msg, ""),
	}
}

// unavailableCode picks the envelope code for an AI-off outcome. A configured
// provider with no database is an environment failure; anything else is the
// provider being off.
func (s *Service) unavailableCode() string {
	if s != nil && s.cfg.Provider != nil && s.cfg.Runner == nil {
		return CodeEnvironmentDown
	}
	return CodeUnavailable
}

// decodeJSON decodes a schema-constrained provider reply into v, rejecting
// trailing content. Provider output is untrusted input, so the error is a
// policy failure rather than a crash.
func decodeJSON(raw string, v any) error {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("provider output is not valid JSON: %w", err)
	}
	if dec.More() {
		return errors.New("provider output has trailing content after the JSON value")
	}
	return nil
}
