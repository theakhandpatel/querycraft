package schema

import "time"

/* Course plans and their persistence record. The generation pipeline writes a
 * CoursePlan (chapters of markdown and exercises); CourseRecord pairs that plan
 * with the outcome that admitted it, the way Record does for a challenge.
 * ChapterResult and ExerciseResult are the verification report the pipeline
 * returns, and ExpectedResult is the engine's own answer kept per exercise.
 */

/* ---------- the course plan ---------- */

// CoursePlan is a whole course as the service will store it. ID, Version and
// Provenance are the service's to stamp; a provider that sends them is ignored
// and overwritten, exactly like Artifact.
type CoursePlan struct {
	ID         string          `json:"id"`
	Version    int             `json:"version"`
	Title      string          `json:"title"`
	Subtitle   string          `json:"subtitle"`
	Level      string          `json:"level"`
	Dialect    string          `json:"dialect,omitempty"`
	Objectives []string        `json:"objectives"`
	Setup      SetupPlan       `json:"setup"`
	Chapters   []CourseChapter `json:"chapters"`
	Provenance Provenance      `json:"provenance"`
}

// CourseChapter is one chapter: markdown to read, exercises to pass.
type CourseChapter struct {
	ID         string           `json:"id"`
	N          int              `json:"n"`
	Title      string           `json:"title"`
	Summary    string           `json:"summary"`
	Kind       string           `json:"kind,omitempty"`
	Minutes    int              `json:"minutes,omitempty"`
	Objectives []string         `json:"objectives"`
	Sections   []CourseSection  `json:"sections"`
	Exercises  []CourseExercise `json:"exercises"`
}

// CourseSection is authored prose. Body is markdown; the client renders it with
// the same renderer the authored courses use, so a generated chapter can carry
// a table or a mermaid diagram without a second pipeline.
type CourseSection struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// CourseExercise is one exercise in a chapter. It is verified by building the
// Artifact the challenge pipeline already understands, so every rule that
// applies to a generated challenge applies here unchanged.
type CourseExercise struct {
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Prompt             string         `json:"prompt"`
	Difficulty         Difficulty     `json:"difficulty"`
	Type               string         `json:"type,omitempty"`
	Objectives         []string       `json:"objectives"`
	Hints              []string       `json:"hints"`
	ReferenceSolution  string         `json:"reference_solution"`
	CrossCheckSolution string         `json:"cross_check_solution"`
	ResultContract     ResultContract `json:"result_contract"`
	Checks             []Check        `json:"checks,omitempty"`
	// Expected is the engine's own answer for ReferenceSolution, measured while
	// the course was verified. Never returned by a provider.
	Expected *ExpectedResult `json:"expected,omitempty"`
}

// ExpectedResult is a result set as the engine returned it: the columns in
// order, and every row. It is what a learner's answer is compared against.
type ExpectedResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

/* ---------- the report ---------- */

// ChapterResult is what verification measured for one chapter. It is reported
// whether the course was activated or rejected, because "which exercise failed
// and why" is the useful part of a rejection.
//
// Status is the chapter's own verdict and is not the same statement as its
// exercises': a chapter is verified only when every exercise in it passed,
// dropped when it could not be repaired, and skipped when the run ended before
// it was written. A chapter is never partly verified: verification decides
// whether it is kept at all.
type ChapterResult struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Status    string           `json:"status,omitempty"`
	Exercises []ExerciseResult `json:"exercises"`
	Error     *Error           `json:"error,omitempty"`
}

// ExerciseResult is the per-exercise evidence. Zero means the check did not
// run, exactly as in Evidence.
type ExerciseResult struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	Status           string  `json:"status"`
	ReferenceRows    int     `json:"reference_rows,omitempty"`
	CrossCheckRows   int     `json:"cross_check_rows,omitempty"`
	FixturesRejected int     `json:"fixtures_rejected,omitempty"`
	FixturesTotal    int     `json:"fixtures_total,omitempty"`
	MS               float64 `json:"ms,omitempty"`
	Error            *Error  `json:"error,omitempty"`
}

// CourseRecord is a stored course plus the outcome that admitted it.
type CourseRecord struct {
	Plan      CoursePlan `json:"plan"`
	Outcome   Outcome    `json:"outcome"`
	Status    string     `json:"status"`
	OwnerID   string     `json:"owner_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// Request is the generation request that produced the plan. It is kept
	// because a course can pause after its outline (StatusAwaitingApproval) and
	// be continued by a later request, and the chapter phase rebuilds its own
	// prompt from the request (`courseAgentPrompt`): the goal, the scenario, the
	// difficulty, the topics and the exercise budget all live there, and none of
	// them is derivable from the plan alone.
	//
	// A record stored before this field existed carries none, which is legal:
	// the resume path reconstructs what the plan itself knows (the goal from
	// the plan's title and its planned exercise count) and omits the rest, so an
	// old row is still readable and cannot be mistaken for a fresh one.
	Request *GenerationRequest `json:"request,omitempty"`
}
