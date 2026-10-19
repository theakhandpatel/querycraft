package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* M7: course generation.
 *
 * A challenge (M4) is one exercise. A course is a plan: chapters, each with
 * authored markdown sections and exercises, generated from the learner's own
 * goal. The two share everything that decides correctness: the same policy gate
 * (validateArtifact), the same SQL stages (reference, cross-check, adversarial
 * fixtures), the same evaluator, and the same dataset brief. A course exercise
 * is not a different kind of thing. What the course adds is grouping and prose,
 * plus one rule the single-challenge path need not state: every exercise in the
 * plan is executed and cross-checked, in order, before any of it is shown.
 *
 * Two consequences of plan-level verification are choices rather than
 * accidents:
 *
 *   - Setup is a property of the course, not of an exercise. Every exercise
 *     runs against one environment, so an exercise that inserted its own rows
 *     would silently change the expected result of every exercise after it.
 *     The plan therefore carries one setup plan, applied once, and exercises
 *     carry none. The challenge path follows the same rule over the sealed
 *     dataset: `setup.statements` is empty, and the dataset is checked.
 *
 *   - `expected` is filled by the pipeline from the reference run, never read
 *     from the provider. The rows a learner is graded against are the rows the
 *     engine returned during verification, so the grader and the verifier
 *     cannot disagree about the answer.
 *
 * This file holds the plan's derived shapes and validation. course_pipeline.go
 * runs the stages, course_agent.go drives the chapter conversation, and the
 * Service, Runner, evaluator and Depot are the same ones the challenge path
 * uses.
 */

/* ---------- bounds ---------- */

// Bounds on a generated course. They are constants rather than Limits fields
// because they describe the shape of a course, not the cost of a run; they are
// named here so the test table can assert against the same numbers the gate
// uses.
const (
	maxCourseChapters      = 8
	maxCourseSections      = 6
	maxCourseExercises     = 4
	maxCourseExerciseTotal = 12
	minSectionChars        = 40
	maxSectionBytes        = 16 * 1024
)

/* ---------- derived shapes ---------- */

// exerciseArtifact renders one plan exercise in the shape the policy gate and
// the SQL stages already understand. This is the whole reason a course needs no
// second verifier: the exercise becomes an Artifact and is checked by the code
// that checks artifacts.
func exerciseArtifact(c *schema.CoursePlan, ch schema.CourseChapter, ex schema.CourseExercise) schema.Artifact {
	title := strings.TrimSpace(ex.Title)
	if title == "" {
		title = ch.Title
	}
	dialect := c.Dialect
	if dialect == "" {
		dialect = "postgresql"
	}
	return schema.Artifact{
		ID:                 ex.ID,
		Dialect:            dialect,
		Type:               ex.Type,
		Title:              title,
		Prompt:             ex.Prompt,
		Difficulty:         ex.Difficulty,
		Objectives:         ex.Objectives,
		Hints:              ex.Hints,
		Setup:              c.Setup,
		ReferenceSolution:  ex.ReferenceSolution,
		CrossCheckSolution: ex.CrossCheckSolution,
		ResultContract:     ex.ResultContract,
		Checks:             ex.Checks,
	}
}

/* ---------- validation ---------- */

// validateCourseChapter applies the chapter and exercise rules to one chapter.
// It is a separate function because a chapter now arrives on its own: the
// course agent hands the gateway one chapter per tool call, and that chapter
// has to pass exactly the rules a whole plan's chapter passes. *total and seen
// are the plan-wide facts a single chapter cannot check by itself (the
// duplicate-id rule and the per-course exercise budget), so they are carried
// across calls rather than re-derived.
func validateCourseChapter(plan *schema.CoursePlan, ci int, seen map[string]int, total *int, l Limits) *schema.Error {
	ch := &plan.Chapters[ci]
	field := fmt.Sprintf("chapters[%d]", ci)
	if strings.TrimSpace(ch.Title) == "" {
		return coursePlanError(field+".title", "required and must be non-empty")
	}
	if strings.TrimSpace(ch.Summary) == "" {
		return coursePlanError(field+".summary", "required and must be non-empty")
	}
	if len(ch.Objectives) == 0 {
		return coursePlanError(field+".objectives", "at least one objective is required")
	}
	if len(ch.Sections) == 0 {
		return coursePlanError(field+".sections", "at least one section is required")
	}
	if len(ch.Sections) > maxCourseSections {
		return coursePlanError(field+".sections", fmt.Sprintf("%d sections exceeds the limit of %d", len(ch.Sections), maxCourseSections))
	}
	for si := range ch.Sections {
		sec := ch.Sections[si]
		sf := fmt.Sprintf("%s.sections[%d]", field, si)
		if strings.TrimSpace(sec.Title) == "" {
			return coursePlanError(sf+".title", "required and must be non-empty")
		}
		// A section is the teaching. A one-line section is a stub, and a
		// stub that passes the gate is the failure mode this bound exists
		// to prevent: the chapter would render as a heading with nothing
		// under it and still be called authored content.
		if n := utf8.RuneCountInString(strings.TrimSpace(sec.Body)); n < minSectionChars {
			return coursePlanError(sf+".body", fmt.Sprintf("%d characters is below the %d-character minimum", n, minSectionChars))
		}
		if len(sec.Body) > maxSectionBytes {
			return coursePlanError(sf+".body", fmt.Sprintf("%d bytes exceeds the limit of %d", len(sec.Body), maxSectionBytes))
		}
	}
	if len(ch.Exercises) == 0 {
		return coursePlanError(field+".exercises", "at least one exercise is required")
	}
	if len(ch.Exercises) > maxCourseExercises {
		return coursePlanError(field+".exercises", fmt.Sprintf("%d exercises exceeds the limit of %d per chapter", len(ch.Exercises), maxCourseExercises))
	}
	for ei := range ch.Exercises {
		ex := &ch.Exercises[ei]
		ef := fmt.Sprintf("%s.exercises[%d]", field, ei)
		if strings.TrimSpace(ex.ID) == "" {
			return coursePlanError(ef+".id", "required and must be non-empty")
		}
		// Progress and attempts are keyed by exercise id, so a duplicate
		// would make two exercises share one learner's history. The first
		// occurrence is named so the repair pass knows which to rename.
		if first, dup := seen[ex.ID]; dup {
			return coursePlanError(ef+".id", fmt.Sprintf("duplicate exercise id %q (first at exercise %d)", ex.ID, first))
		}
		seen[ex.ID] = *total + 1
		if verr := validateArtifact(exerciseArtifact(plan, *ch, *ex), l); verr != nil {
			return nestArtifactError(*ch, ci, ei, verr)
		}
		*total = *total + 1
	}
	return nil
}

// validateCourseSetup bounds the one setup plan every exercise runs against.
func validateCourseSetup(setup schema.SetupPlan, l Limits) *schema.Error {
	if strings.TrimSpace(setup.Dataset) == "" {
		return coursePlanError("setup.dataset", "required and must be non-empty")
	}
	if setup.SchemaVersion < 1 {
		return coursePlanError("setup.schema_version", "required and must be at least 1")
	}
	if setup.SeedVersion < 1 {
		return coursePlanError("setup.seed_version", "required and must be at least 1")
	}
	if len(setup.Statements) > l.MaxSetupStatements {
		return coursePlanError("setup.statements", fmt.Sprintf("%d statements exceeds the limit of %d", len(setup.Statements), l.MaxSetupStatements))
	}
	for i, stmt := range setup.Statements {
		if err := validateSetupStatement(stmt); err != nil {
			return coursePlanError("setup.statements", fmt.Sprintf("statement %d: %s", i, strings.TrimPrefix(err.Detail, "setup.statements: ")))
		}
	}
	return nil
}

// nestArtifactError re-roots an exercise error at the chapter and index it came
// from. validateArtifact names a field the way an artifact has it
// ("reference_solution"); a reader of a rejected course needs to know which
// exercise to look at, so the path is prefixed rather than replaced.
func nestArtifactError(ch schema.CourseChapter, ci, ei int, err *schema.Error) *schema.Error {
	field, detail := splitFieldDetail(err.Detail)
	return coursePlanError(fmt.Sprintf("chapters[%d].exercises[%d].%s", ci, ei, field), detail)
}

// splitFieldDetail splits "field: detail" at its first colon. Every detail it
// receives is built by artifactError as field + ": " + detail, so the colon is
// always there.
func splitFieldDetail(detail string) (string, string) {
	field, rest, _ := strings.Cut(detail, ": ")
	return field, rest
}

func coursePlanError(field, detail string) *schema.Error {
	return NewError(CodeInvalidArtifact, schema.StageValidateArtifact,
		"The generated course did not pass validation.", field+": "+detail)
}

/* ---------- the provider request ---------- */

// coursePromptMarker opens every course prompt, including a repair of one. It
// is a constant because the built-in provider recognizes a course request by
// the prompt it is given, the same thing a model does. A marker written twice
// would eventually be written differently twice.
const coursePromptMarker = "Design one complete course"

// courseExerciseBudget is the number of exercises a course asks for: the
// learner's exercise_count under the course's own floor and ceiling. It is one
// function because the prompt that tells the model the budget and the outline
// gate that explains an outline planned outside it must name the same number.
// A rule stated in one place and applied in another is a rule that drifts.
//
// The floor is 2, not "the chapter count", and that is not an oversight. The
// number of chapters is the model's choice, made in the outline call, so it is
// not known when a request is normalized: a request-time floor could only be a
// guess, and a guess that raised exercise_count to a course's real chapter count
// would rewrite the learner's ask. The mismatch between the budget and the
// chapter count is settled where both are known, in the outline gate
// (validateCourseOutline), and the floor here only keeps the ask at a number a
// course can be a course at.
func courseExerciseBudget(req schema.GenerationRequest) int {
	return clampInt(req.ExerciseCount, 2, maxCourseExerciseTotal)
}

// coursePrompt renders the request for a course. It carries the learner's own
// words and the same dataset brief the challenge prompt uses, because a course
// that invents a column name fails at the first exercise and spends the repair
// budget on a fact the server already knew.
func coursePrompt(req schema.GenerationRequest) string {
	var b strings.Builder
	b.WriteString(coursePromptMarker + ", not one exercise. The learner asked for:\n")
	if strings.TrimSpace(req.Title) != "" {
		fmt.Fprintf(&b, "Goal: %s\n", strings.TrimSpace(req.Title))
	}
	if strings.TrimSpace(req.Scenario) != "" {
		fmt.Fprintf(&b, "Level and context: %s\n", strings.TrimSpace(req.Scenario))
	}
	if topics := nonEmpty(req.Topics); len(topics) > 0 {
		fmt.Fprintf(&b, "Topics to cover: %s\n", strings.Join(topics, ", "))
	}
	fmt.Fprintf(&b, "Difficulty: %s\n", req.Difficulty)
	n := courseExerciseBudget(req)
	fmt.Fprintf(&b, "Exercises: %d, spread across the chapters.\n", n)
	b.WriteString(datasetBrief())
	b.WriteString("\nWrite the chapters in teaching order: each chapter's sections explain the idea in markdown, and each of its exercises is one query the learner writes against this dataset. Every exercise needs a reference solution, a second independent solution that returns the same result, and a result contract naming the exact column names in order.")
	b.WriteString("\nThe whole course runs against the dataset above. Do not create, alter or drop those tables: setup may only hold single CREATE TABLE or INSERT INTO statements for extra scratch objects, and an empty list is correct when the dataset alone is enough.")
	return b.String()
}
