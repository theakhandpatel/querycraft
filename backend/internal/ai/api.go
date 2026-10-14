// Package ai implements the M4 "AI Challenge Lab" generation service
// (technical design §11). The model is an untrusted content generator and this
// package is the policy: nothing becomes visible to a learner until every stage
// of the §11.1 pipeline has passed against a real engine, and a generated
// challenge is never graded by anything except the deterministic evaluator.
//
// The package is provider-neutral on purpose (§11.2): it depends on the small
// Provider interface and a deterministic in-memory provider ships with it, so
// generation can be exercised with no network, no key and no vendor SDK. The
// provider decision is settled in an ADR after a spike, not by preference.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// PipelineVersion identifies this pipeline implementation. It is stamped into
// every artifact's provenance, so a change in policy is visible in the record
// rather than silent.
const PipelineVersion = "go-1"

// The AI envelope's error codes. These are separate from the closed QueryResult
// enum (contracts/query-result.schema.json): a generation is not a query. Any
// QueryResult embedded in an AI response still uses the closed enum.
const (
	CodeAIAvailable      = "AI_AVAILABLE"
	CodeUnavailable      = "AI_UNAVAILABLE"
	CodeInvalidRequest   = "AI_INVALID_REQUEST"
	CodeInvalidArtifact  = "AI_INVALID_ARTIFACT"
	CodeReferenceFailed  = "AI_REFERENCE_FAILED"
	CodeCrossCheckFailed = "AI_CROSS_CHECK_FAILED"
	CodeRepairExhausted  = "AI_REPAIR_EXHAUSTED"
	CodeNotFound         = "AI_NOT_FOUND"
	CodePatchUnverified  = "AI_PATCH_UNVERIFIED"
	CodePatchUnapproved  = "AI_PATCH_UNAPPROVED"
	CodeEnvironmentDown  = "ENVIRONMENT_UNAVAILABLE"
)

// Stage names, in pipeline order. They are stable strings because the client
// renders them and the tests assert on them.
const (
	StageValidateRequest = "validate_request"
	StageRequestBundle   = "request_bundle"
	StageValidateArtifact = "validate_artifact"
	StageScratchEnvironment = "scratch_environment"
	StageReferenceContract = "reference_contract"
	StageCrossCheck      = "cross_check"
	StageAdversarialFixtures = "adversarial_fixtures"
	StageRepair          = "repair"
	StageActivate        = "activate"
)

// StageStates.
const (
	StateDone    = "done"
	StateFailed  = "failed"
	StateSkipped = "skipped"
)

// OutcomeStatus values. Only StatusActive puts an artifact in front of learners.
const (
	StatusActive      = "active"
	StatusRejected    = "rejected"
	StatusUnavailable = "unavailable"
	StatusFailed      = "failed"
)

// Error is an AI-envelope failure. It never carries a learner's secret, and the
// stage it names is the stage that refused.
type Error struct {
	Code    string `json:"code"`
	Stage   string `json:"stage,omitempty"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Stage != "" {
		return fmt.Sprintf("%s at %s: %s", e.Code, e.Stage, e.Message)
	}
	return e.Code + ": " + e.Message
}

// newError builds an envelope error.
func newError(code, stage, message, detail string) *Error {
	return &Error{Code: code, Stage: stage, Message: message, Detail: detail}
}

// ErrProviderUnavailable is returned by a Provider that is configured but
// cannot serve a request (no key, network down, quota exhausted). It is
// distinguishable from a bug so the caller can report AI_UNAVAILABLE honestly.
var ErrProviderUnavailable = errors.New("ai: provider unavailable")

// ErrNotConfigured is returned when no provider is wired at all.
var ErrNotConfigured = errors.New("ai: no provider configured")

/* ---------- generation request ---------- */

// Difficulty is the request's difficulty band.
type Difficulty string

const (
	Beginner     Difficulty = "beginner"
	Intermediate Difficulty = "intermediate"
	Advanced     Difficulty = "advanced"
)

// GenerationRequest is a validated-in-progress request. The service, not the
// caller, decides which fields are acceptable.
type GenerationRequest struct {
	Kind          string     `json:"kind,omitempty"`
	Title         string     `json:"title,omitempty"`
	Topics        []string   `json:"topics,omitempty"`
	Difficulty    Difficulty `json:"difficulty,omitempty"`
	Scenario      string     `json:"scenario,omitempty"`
	Constraints   []string   `json:"constraints,omitempty"`
	ExerciseCount int        `json:"exercise_count,omitempty"`
	Steps         []string   `json:"steps,omitempty"`
}

/* ---------- artifact ---------- */

// ResultContract mirrors eval.Contract on the wire.
type ResultContract struct {
	Columns          []string `json:"columns"`
	ColumnOrder      string   `json:"column_order"`
	RowOrder         string   `json:"row_order"`
	Duplicates       string   `json:"duplicates"`
	NumericTolerance float64  `json:"numeric_tolerance"`
}

// SetupPlan says what a challenge runs against. Statements is empty for a
// challenge over the sealed dataset; when present it must only create and
// populate scratch objects, and it is applied in a throwaway environment.
type SetupPlan struct {
	Dataset       string   `json:"dataset"`
	SchemaVersion int      `json:"schema_version"`
	SeedVersion   int      `json:"seed_version"`
	Statements    []string `json:"statements"`
}

// Check is one authored grading check, using the evaluator's own shape.
type Check struct {
	Label   string `json:"label"`
	Code    string `json:"code"`
	Pattern string `json:"pattern,omitempty"`
}

// Provenance is everything needed to reproduce or invalidate an artifact.
type Provenance struct {
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	ProviderVersion  string    `json:"provider_version"`
	PipelineVersion  string    `json:"pipeline_version"`
	EvaluatorVersion string    `json:"evaluator_version"`
	DatasetVersion   int       `json:"dataset_version"`
	CreatedAt        time.Time `json:"created_at"`
	Repairs          int       `json:"repairs,omitempty"`
}

// Artifact is a challenge the service may activate. It is only written to the
// active set after every pipeline stage has passed.
type Artifact struct {
	ID                 string         `json:"id"`
	Version            int            `json:"version"`
	LessonID           string         `json:"lesson_id,omitempty"`
	Dialect            string         `json:"dialect,omitempty"`
	Type               string         `json:"type,omitempty"`
	Title              string         `json:"title"`
	Prompt             string         `json:"prompt"`
	Difficulty         Difficulty     `json:"difficulty"`
	Objectives         []string       `json:"objectives"`
	Hints              []string       `json:"hints"`
	Setup              SetupPlan      `json:"setup"`
	ReferenceSolution  string         `json:"reference_solution"`
	CrossCheckSolution string         `json:"cross_check_solution,omitempty"`
	ResultContract     ResultContract `json:"result_contract"`
	Checks             []Check        `json:"checks,omitempty"`
	Provenance         Provenance     `json:"provenance"`
}

/* ---------- pipeline evidence ---------- */

// Stage is one step of the §11.1 pipeline as it actually ran.
type Stage struct {
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Detail string  `json:"detail,omitempty"`
	MS     float64 `json:"ms,omitempty"`
}

// Attempt records one trip through the repair loop.
type Attempt struct {
	Number int    `json:"number"`
	Stage  string `json:"stage"`
	Detail string `json:"detail"`
}

// Evidence is measured fact from a run, never a claim. Zero means the check did
// not run or found nothing.
type Evidence struct {
	ReferenceRows   int    `json:"reference_rows,omitempty"`
	CrossCheckRows  int    `json:"cross_check_rows,omitempty"`
	FixturesRejected int   `json:"fixtures_rejected,omitempty"`
	FixturesTotal   int    `json:"fixtures_total,omitempty"`
	Repairs         int    `json:"repairs,omitempty"`
	ScratchDatabase string `json:"scratch_database,omitempty"`
}

// Outcome is the generation response. StatusActive is the only status that
// makes an artifact visible to learners.
type Outcome struct {
	Status   string    `json:"status"`
	Kind     string    `json:"kind,omitempty"`
	Artifact *Artifact `json:"artifact"`
	Stages   []Stage   `json:"stages"`
	Attempts []Attempt `json:"attempts"`
	Evidence Evidence  `json:"evidence"`
	Error    *Error    `json:"error"`
}

// StageIndex returns the position of a stage in the reported slice, or -1.
func (o Outcome) StageIndex(name string) int {
	for i, st := range o.Stages {
		if st.Name == name {
			return i
		}
	}
	return -1
}

/* ---------- tutoring, critique, defects ---------- */

// TutorRequest is the material a hint may use. It deliberately carries no
// reference solution: the service cannot leak what it is not given.
type TutorRequest struct {
	ChallengeID   string `json:"challenge_id,omitempty"`
	SQL           string `json:"sql"`
	Objective     string `json:"objective"`
	ErrorCode     string `json:"error_code,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	HintLevel     int    `json:"hint_level,omitempty"`
	ResultSummary string `json:"result_summary,omitempty"`
}

// TutorResponse is a hint. RevealsSolution must be false.
type TutorResponse struct {
	Message          string     `json:"message"`
	HintLevel        int        `json:"hint_level"`
	RevealsSolution  bool       `json:"reveals_solution"`
	Provenance       Provenance `json:"provenance"`
}

// CritiqueRequest asks for a review of the learner's own query.
type CritiqueRequest struct {
	ChallengeID string `json:"challenge_id,omitempty"`
	SQL         string `json:"sql"`
	Objective   string `json:"objective,omitempty"`
}

// CritiqueResponse is the on-demand critique.
type CritiqueResponse struct {
	Message    string     `json:"message"`
	Notes      []string   `json:"notes"`
	Provenance Provenance `json:"provenance"`
}

// DefectReport is a learner's claim that a challenge is wrong.
type DefectReport struct {
	ChallengeID string `json:"challenge_id"`
	Description string `json:"description"`
	SQL         string `json:"sql,omitempty"`
	Expected    string `json:"expected,omitempty"`
	Observed    string `json:"observed,omitempty"`
}

// PatchChange is one field edit proposed by a verified defect.
type PatchChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// PatchProposal is evidence, not an action. Applied stays false until the
// learner approves it.
type PatchProposal struct {
	ID          string        `json:"id"`
	ChallengeID string        `json:"challenge_id"`
	Summary     string        `json:"summary"`
	Changes     []PatchChange `json:"changes"`
}

// DefectVerdict is the answer to a defect report. Verified is only true after
// the service has executed the reproduction.
type DefectVerdict struct {
	Verified bool           `json:"verified"`
	Applied  bool           `json:"applied"`
	Summary  string         `json:"summary"`
	Patch    *PatchProposal `json:"patch"`
	Evidence Evidence       `json:"evidence"`
	Error    *Error         `json:"error"`
}

// PatchDecision is the learner's approval. A patch is never applied without it.
type PatchDecision struct {
	PatchID    string `json:"patch_id"`
	Approved   bool   `json:"approved"`
	ApprovedBy string `json:"approved_by"`
}

// PatchResult reports what happened to the approval.
type PatchResult struct {
	Applied bool     `json:"applied"`
	Patch   *PatchProposal `json:"patch"`
	Error   *Error   `json:"error"`
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
}

/* ---------- persistence ---------- */

// Record is a stored challenge plus the evidence captured when it was allowed
// in, so a later reader can see why.
type Record struct {
	Artifact  Artifact  `json:"artifact"`
	Outcome   Outcome   `json:"outcome"`
	Status    string    `json:"status"`
	OwnerID   string    `json:"owner_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Depot is the persistence seam. The store package implements it; nothing in
// this package writes SQL itself.
type Depot interface {
	SaveChallenge(ctx context.Context, rec Record) error
	Challenge(ctx context.Context, id string) (Record, bool, error)
	Challenges(ctx context.Context) ([]Record, error)
	DeleteChallenge(ctx context.Context, id string) error
	// SavePatch stores an unapplied proposal, so an approval can find it later.
	SavePatch(ctx context.Context, p PatchProposal) error
	Patch(ctx context.Context, id string) (PatchProposal, bool, error)
	ApprovePatch(ctx context.Context, id, approvedBy string) error
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

// ProvidedRunner is a test seam: it returns an executor directly.
type ProvidedRunner struct {
	Exec engine.Executor
}

// Scratch implements Runner.
func (p ProvidedRunner) Scratch(context.Context, string) (engine.Executor, func(), error) {
	if p.Exec == nil {
		return nil, nil, ErrNotConfigured
	}
	return p.Exec, func() {}, nil
}

/* ---------- limits ---------- */

// Limits bound the cost of a generation run. Every field has a default; a zero
// value means "use the default", never "unlimited".
type Limits struct {
	MaxOutputBytes  int
	MaxSetupStatements int
	MaxRepairs      int
	MaxHints        int
	MaxConcurrent   int
	Timeout         time.Duration
	MaxPromptChars  int
}

// DefaultLimits are the bounds unless config says otherwise.
func DefaultLimits() Limits {
	return Limits{
		MaxOutputBytes:     64 * 1024,
		MaxSetupStatements: 8,
		MaxRepairs:         2,
		MaxHints:           3,
		MaxConcurrent:      2,
		Timeout:            20 * time.Second,
		MaxPromptChars:     4000,
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
	return l
}

/* ---------- config and service ---------- */

// Config wires the service. Provider and Depot are required for generation;
// Runner is required to validate anything. Missing pieces are reported as
// Availability rather than as a crash, because "AI is off" is a supported mode
// (§11.2: core features work without it).
type Config struct {
	Provider Provider
	Depot    Depot
	Runner   Runner
	Logger   *slog.Logger
	Limits   Limits
	// DatasetVersion is stamped into provenance. It is a field rather than an
	// import so this package does not depend on the dataset's contents.
	DatasetVersion int
	// AdversarialFixtures are wrong queries that the evaluator must reject for
	// every generated artifact. Injected so the caller owns the fixtures.
	AdversarialFixtures func(a Artifact) []Fixture
}

// Fixture is one deliberately wrong query.
type Fixture struct {
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

// GenerateChallenge runs the §11.1 pipeline for one challenge.
func (s *Service) GenerateChallenge(ctx context.Context, req GenerationRequest) Outcome {
	req.Kind = "challenge"
	return s.generate(ctx, req)
}

// GenerateCourse runs the same pipeline for a course outline.
func (s *Service) GenerateCourse(ctx context.Context, req GenerationRequest) Outcome {
	req.Kind = "course"
	return s.generate(ctx, req)
}

// Tutor answers with a hint that never reveals the reference solution.
func (s *Service) Tutor(ctx context.Context, req TutorRequest) (TutorResponse, error) {
	return s.tutor(ctx, req)
}

// Critique reviews the learner's query on demand.
func (s *Service) Critique(ctx context.Context, req CritiqueRequest) (CritiqueResponse, error) {
	return s.critique(ctx, req)
}

// ReportDefect verifies a claimed defect by execution before proposing a patch.
func (s *Service) ReportDefect(ctx context.Context, rep DefectReport) (DefectVerdict, error) {
	return s.reportDefect(ctx, rep)
}

// ApplyPatch applies a previously verified patch, and only with approval.
func (s *Service) ApplyPatch(ctx context.Context, dec PatchDecision) (PatchResult, error) {
	return s.applyPatch(ctx, dec)
}

// Challenges lists stored challenges, newest first.
func (s *Service) Challenges(ctx context.Context) ([]Record, error) {
	if s == nil || s.cfg.Depot == nil {
		return nil, newError(CodeUnavailable, "", "No challenge store is configured.", "")
	}
	return s.cfg.Depot.Challenges(ctx)
}

// Challenge fetches one stored challenge.
func (s *Service) Challenge(ctx context.Context, id string) (Record, bool, error) {
	if s == nil || s.cfg.Depot == nil {
		return Record{}, false, newError(CodeUnavailable, "", "No challenge store is configured.", "")
	}
	return s.cfg.Depot.Challenge(ctx, id)
}

// Reset forgets one generated challenge. Learner attempts are the learner's
// data and are not touched here.
func (s *Service) Reset(ctx context.Context, id string) error {
	if s == nil || s.cfg.Depot == nil {
		return newError(CodeUnavailable, "", "No challenge store is configured.", "")
	}
	if _, ok, err := s.cfg.Depot.Challenge(ctx, id); err != nil {
		return err
	} else if !ok {
		return newError(CodeNotFound, "", "No such challenge.", id)
	}
	return s.cfg.Depot.DeleteChallenge(ctx, id)
}

// unavailable is the honest "AI is off" outcome, used by every entry point
// that cannot proceed without a provider.
func (s *Service) unavailable(kind string, avail Availability) Outcome {
	code := CodeUnavailable
	msg := avail.Reason
	if s != nil && s.cfg.Provider != nil && s.cfg.Runner == nil {
		code = CodeEnvironmentDown
	}
	if msg == "" {
		msg = "AI generation is not available."
	}
	return Outcome{
		Status:   StatusUnavailable,
		Kind:     kind,
		Artifact: nil,
		Stages:   []Stage{},
		Attempts: []Attempt{},
		Error:    newError(code, "", msg, ""),
	}
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
