// Package schema holds the persisted domain model shared by the generation
// pipeline (internal/ai) and its persistence seam (internal/store). It is a
// leaf: it imports nothing from this repository, so persistence never depends
// on generation.
package schema

import (
	"context"
	"fmt"
	"time"
)

// PipelineVersion identifies the pipeline implementation that produced an
// artifact. It is stamped into every artifact's provenance, so a change in
// policy is visible in the record rather than silent.
const PipelineVersion = "go-1"

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

/* ---------- patches ---------- */

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
// the generation package writes SQL itself.
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
