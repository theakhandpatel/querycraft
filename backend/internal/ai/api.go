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
	StageValidateRequest     = "validate_request"
	StageRequestBundle       = "request_bundle"
	StageValidateArtifact    = "validate_artifact"
	StageScratchEnvironment  = "scratch_environment"
	StageReferenceContract   = "reference_contract"
	StageCrossCheck          = "cross_check"
	StageAdversarialFixtures = "adversarial_fixtures"
	StageRepair              = "repair"
	StageActivate            = "activate"
)

// StageStates. Done, failed and skipped are final: they are what a finished run
// reports, and the outcome envelope's schema allows nothing else. Queued and
// running exist only in a live view of a run in flight (jobs.go), where "this
// has not started" and "this is happening now" are the two facts a poller
// needs and neither is a verdict about the stage.
const (
	StateQueued  = "queued"
	StateRunning = "running"
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
	// StatusBuilding is the stored state of a course that is still being
	// written, one chapter at a time. It is a status of a stored record, never
	// of a generation outcome: a run that returns is active, rejected, failed
	// or awaiting approval. It exists so GET /courses/{id} can answer with the
	// chapters that have already verified while later ones are still being
	// generated.
	StatusBuilding = "building"
	// StatusAwaitingApproval is the outcome of a run that was asked to pause
	// after its outline (GenerationRequest.RequireApproval). It is a
	// *non-terminal* stop: the run ends here, but the stored course it names is
	// a live thing, and POST /courses/{id}/build continues it into the chapter
	// phase. `active` stays the only status that puts a course in front of
	// learners, so nothing appears in a learner's library as a result of this
	// status alone — the building row the outline stored is all that exists.
	StatusAwaitingApproval = "awaiting_approval"
	// StatusDiscarded is a job-view status, not an outcome one. It says the
	// course a paused run was waiting on was removed, so a poller is told why
	// the run it was watching will never continue. The run's own transcript
	// still records the status the run ended with — it really did stop at
	// awaiting_approval; the discard happened afterwards, to the record.
	StatusDiscarded = "discarded"
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
	// RequireApproval asks a course run to stop after its outline is validated
	// and stored, so the learner can approve the index before the expensive
	// chapter phase runs. The run then ends StatusAwaitingApproval and the
	// stored `building` course waits for POST /courses/{id}/build. It is
	// deliberately off by default: a client that does not ask for the pause
	// gets exactly the one-request, outline-into-chapters run it always did.
	RequireApproval bool `json:"require_approval,omitempty"`
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
	ReferenceRows    int    `json:"reference_rows,omitempty"`
	CrossCheckRows   int    `json:"cross_check_rows,omitempty"`
	FixturesRejected int    `json:"fixtures_rejected,omitempty"`
	FixturesTotal    int    `json:"fixtures_total,omitempty"`
	Repairs          int    `json:"repairs,omitempty"`
	ScratchDatabase  string `json:"scratch_database,omitempty"`
}

// Outcome is the generation response. StatusActive is the only status that
// makes an artifact visible to learners.
type Outcome struct {
	Status   string    `json:"status"`
	Kind     string    `json:"kind,omitempty"`
	Artifact *Artifact `json:"artifact"`
	// Plan is set for a course that was activated, and for one that stopped at
	// StatusAwaitingApproval. A rejected course returns no plan for the same
	// reason a rejected challenge returns no artifact: nothing that did not
	// verify may be presented as something a learner can start. An
	// awaiting-approval plan is the *building* course's own skeleton — the
	// outline the learner is being asked to approve — and it is never read as
	// an activated course, because `status` says so.
	Plan *CoursePlan `json:"plan,omitempty"`
	// Chapters is what verification measured for each chapter's exercises. It
	// is reported for a rejection as well as an activation, because "which
	// exercise failed and why" is the useful part of a rejection.
	Chapters []ChapterResult `json:"chapters,omitempty"`
	Stages   []Stage         `json:"stages"`
	Attempts []Attempt       `json:"attempts"`
	Evidence Evidence        `json:"evidence"`
	Error    *Error          `json:"error"`
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
	Message         string     `json:"message"`
	HintLevel       int        `json:"hint_level"`
	RevealsSolution bool       `json:"reveals_solution"`
	Provenance      Provenance `json:"provenance"`
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

// PatchResult reports what happened to the approval.
type PatchResult struct {
	Applied bool           `json:"applied"`
	Patch   *PatchProposal `json:"patch"`
	Error   *Error         `json:"error"`
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
	// pipeline's own honest failure instead of a transport error. A browser
	// that picks a shorter deadline turns "still working" into "unreachable",
	// which is how a real course generation was reported as a dead gateway.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
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
	// ChallengesFor filters to one owner in the store, for the owner-scoped list
	// route (see CoursesFor).
	ChallengesFor(ctx context.Context, ownerID string) ([]Record, error)
	DeleteChallenge(ctx context.Context, id string) error
	// Courses are the M7 counterpart of challenges: a generated course is
	// stored whole, with the outcome that admitted it, for the same reason.
	SaveCourse(ctx context.Context, rec CourseRecord) error
	Course(ctx context.Context, id string) (CourseRecord, bool, error)
	Courses(ctx context.Context) ([]CourseRecord, error)
	// CoursesFor is Courses filtered to one owner by the STORE, so a list read
	// does not load every learner's courses and discard them in the handler. The
	// list routes are owner-scoped, so this is the query they should run.
	CoursesFor(ctx context.Context, ownerID string) ([]CourseRecord, error)
	DeleteCourse(ctx context.Context, id string) error
	// SavePatch stores an unapplied proposal, so an approval can find it later.
	SavePatch(ctx context.Context, p PatchProposal) error
	Patch(ctx context.Context, id string) (PatchProposal, bool, error)
	ApprovePatch(ctx context.Context, id, approvedBy string) error
	// Runs are the audit trail: each generation's transcript, owner-scoped and
	// bounded before it arrives here (see transcript.go). Run returns the whole
	// record including its owner, so the caller decides who may read it; Runs
	// returns summaries, newest first. SaveRun is an upsert, because one run
	// may be stored more than once (a building course updates its record as it
	// writes chapters).
	SaveRun(ctx context.Context, rec RunTranscript) error
	Run(ctx context.Context, id string) (RunTranscript, bool, error)
	Runs(ctx context.Context) ([]RunSummary, error)
	// RunsFor is Runs filtered to one owner by the store, for the same reason
	// CoursesFor exists: the list route is owner-scoped and should not load (and
	// then discard) every learner's runs.
	RunsFor(ctx context.Context, ownerID string) ([]RunSummary, error)
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
	MaxOutputBytes     int
	MaxSetupStatements int
	MaxRepairs         int
	MaxHints           int
	MaxConcurrent      int
	Timeout            time.Duration
	MaxPromptChars     int
}

// DefaultLimits are the bounds unless config says otherwise.
func DefaultLimits() Limits {
	return Limits{
		// 256 KiB, not 64: a hosted reasoning model puts its reasoning in the
		// reply body next to the answer, and this cap is measured on the whole
		// body (see HTTPProvider.Complete). A 64 KiB cap rejected a perfectly
		// good 5 KiB bundle because the reasoning around it was large.
		MaxOutputBytes:     256 * 1024,
		MaxSetupStatements: 8,
		MaxRepairs:         2,
		MaxHints:           3,
		MaxConcurrent:      2,
		// A run makes several provider calls (bundle, cross-check, adversarial
		// fixtures, then up to MaxRepairs more). A 20 s budget fit an offline
		// fake and cut a real model off mid-run, which surfaced as a failed
		// request rather than as a slow one.
		Timeout:        120 * time.Second,
		MaxPromptChars: 4000,
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
	// The budget is advertised even when generation is off: it describes the
	// pipeline, not whether a provider is attached, and a client that has read
	// it once keeps it across a provider that is temporarily unavailable.
	if s != nil {
		av.TimeoutMS = s.cfg.Limits.Merge(DefaultLimits()).Timeout.Milliseconds()
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

// Limits returns the effective pipeline limits, with defaults applied. The
// transport layer reads it to give the pipeline headroom inside its own
// deadline instead of cutting it off mid-stage.
func (s *Service) Limits() Limits {
	if s == nil {
		return DefaultLimits()
	}
	return s.cfg.Limits.Merge(DefaultLimits())
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

// ChallengesFor lists one owner's challenges, filtered by the store so the
// owner-scoped list route does not load every learner's records and discard them.
func (s *Service) ChallengesFor(ctx context.Context, ownerID string) ([]Record, error) {
	if s == nil || s.cfg.Depot == nil {
		return nil, newError(CodeUnavailable, "", "No challenge store is configured.", "")
	}
	return s.cfg.Depot.ChallengesFor(ctx, ownerID)
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
