package ai

import (
	"fmt"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* course_agent_outline.go owns the course's first answer and the gate it passes.
 *
 * CourseOutline and OutlineChapter are the shapes the model returns from the
 * outline call; validateCourseOutline refuses an outline before a single chapter
 * is asked for. A run has no repair round on this call, so the gate's error is
 * the learner's last word, and courseOutlineExerciseError states the one coupling
 * the model cannot see otherwise: every chapter needs an exercise.
 *
 * The run in course_agent.go asks for the outline and calls the gate from
 * outlinePhase; course_build.go turns a valid outline into the plan the store
 * keeps while the chapters are written.
 */

/* ---------- the outline ---------- */

// CourseOutline is the small first answer: what the course is and what its
// chapters will be. It is asked for before any chapter is written, which is
// what gives the screen something to draw and the store something to keep while
// the chapters are still being generated.
type CourseOutline struct {
	Title      string           `json:"title"`
	Subtitle   string           `json:"subtitle"`
	Level      string           `json:"level"`
	Objectives []string         `json:"objectives"`
	Setup      schema.SetupPlan `json:"setup"`
	Chapters   []OutlineChapter `json:"chapters"`
}

// OutlineChapter is one planned chapter: enough to list it and to know what the
// chapter that arrives later has to be, and nothing about its content.
type OutlineChapter struct {
	ID         string   `json:"id"`
	N          int      `json:"n"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Kind       string   `json:"kind,omitempty"`
	Minutes    int      `json:"minutes,omitempty"`
	Objectives []string `json:"objectives"`
	// Exercises is how many exercises the chapter will contain. The gateway
	// keeps it and reports it as the planned count; the authoritative number is
	// always the chapter that is actually saved, because that is the one the
	// engine ran.
	Exercises int `json:"exercises"`
}

// validateCourseOutline is the gate the outline passes before a single chapter
// is asked for. It bounds the outline's shape (chapter count, ids, the exercise
// budget, the setup plan), so a plan cannot be built out of an outline the
// chapter gate would have refused.
//
// budget is the exercise count the request asked for (courseExerciseBudget:
// exercise_count under the course's floor and ceiling). It is passed in because
// this gate is the first and only place where the request's budget and the
// model's chapter count are both known. The chapter count is the model's choice,
// so no floor can be computed at request time. A request for fewer exercises
// than the outline plans chapters is therefore not impossible. It is a request
// whose outline must either widen the exercise count to one per chapter or
// shrink the chapter list, and the gate is what turns that fact into a sentence
// the learner can act on.
func validateCourseOutline(o *CourseOutline, budget int, l Limits) *schema.Error {
	if o == nil {
		return coursePlanError("outline", "no outline")
	}
	if strings.TrimSpace(o.Title) == "" {
		return coursePlanError("title", "required and must be non-empty")
	}
	if strings.TrimSpace(o.Subtitle) == "" {
		return coursePlanError("subtitle", "required and must be non-empty")
	}
	if strings.TrimSpace(o.Level) == "" {
		return coursePlanError("level", "required and must be non-empty")
	}
	if len(o.Objectives) == 0 {
		return coursePlanError("objectives", "at least one objective is required")
	}
	if err := validateCourseSetup(o.Setup, l); err != nil {
		return err
	}
	if len(o.Chapters) == 0 {
		return coursePlanError("chapters", "at least one chapter is required")
	}
	if len(o.Chapters) > maxCourseChapters {
		return coursePlanError("chapters", fmt.Sprintf("%d chapters exceeds the limit of %d", len(o.Chapters), maxCourseChapters))
	}
	total := 0
	seen := make(map[string]int, len(o.Chapters))
	for ci := range o.Chapters {
		ch := &o.Chapters[ci]
		field := fmt.Sprintf("chapters[%d]", ci)
		if strings.TrimSpace(ch.ID) == "" {
			return coursePlanError(field+".id", "required and must be non-empty")
		}
		if first, dup := seen[ch.ID]; dup {
			return coursePlanError(field+".id", fmt.Sprintf("duplicate chapter id %q (first at chapter %d)", ch.ID, first))
		}
		seen[ch.ID] = ci + 1
		if strings.TrimSpace(ch.Title) == "" {
			return coursePlanError(field+".title", "required and must be non-empty")
		}
		if strings.TrimSpace(ch.Summary) == "" {
			return coursePlanError(field+".summary", "required and must be non-empty")
		}
		if len(ch.Objectives) == 0 {
			return coursePlanError(field+".objectives", "at least one objective is required")
		}
		if ch.Exercises < 1 {
			/* A chapter with no exercise is not a course chapter, so the rule
			   stands unchanged. What changes is the sentence. A model that
			   reads "Exercises: N, spread across the chapters" as a total to
			   divide among more than N chapters produces exactly this outline.
			   The field-shaped reply ("chapters[0].exercises: a chapter must
			   plan at least one exercise") names the provider's schema, not
			   anything the learner can ask for. The outline has no repair
			   round, so this error is the last thing the learner reads. It
			   names the requested budget and both ways to satisfy it. */
			return courseOutlineExerciseError(o, budget)
		}
		if ch.Exercises > maxCourseExercises {
			return coursePlanError(field+".exercises", fmt.Sprintf("%d exercises exceeds the limit of %d per chapter", ch.Exercises, maxCourseExercises))
		}
		total += ch.Exercises
	}
	if total > maxCourseExerciseTotal {
		return coursePlanError("chapters", fmt.Sprintf("%d exercises exceeds the limit of %d per course", total, maxCourseExerciseTotal))
	}
	return nil
}

// courseOutlineExerciseError is the actionable form of the per-chapter minimum.
// The rule is not weakened: an outline that planned a chapter with no exercise
// is still refused, because a course chapter with no exercise is not a course
// chapter. The sentence replaces the provider's own schema path
// ("chapters[0].exercises: a chapter must plan at least one exercise", which no
// learner can act on) with one that names the exercise budget the request asked
// for and what to change instead.
//
// The outline call has no repair round, so this error is the run's last word.
// It is the only place the learner is told why a request they were allowed to
// make could not be built, and the reason is a coupling between two numbers:
// every chapter needs at least one exercise, so a request for N exercises
// cannot be satisfied by an outline of more than N chapters.
func courseOutlineExerciseError(o *CourseOutline, budget int) *schema.Error {
	chapters := len(o.Chapters)
	detail := fmt.Sprintf(
		"the request asked for %d exercise(s) but the outline planned %d chapter(s), and every chapter must contain at least one exercise",
		budget, chapters)
	if chapters > budget {
		// The requested budget cannot fill this many chapters: the mismatch is
		// structural, not an authoring slip.
		detail += fmt.Sprintf(
			"; this outline cannot be built — ask for at least %d exercise(s), or narrow the goal so the course needs %d chapter(s) or fewer",
			chapters, budget)
	} else {
		// The budget is large enough; the outline simply left a chapter empty.
		detail += fmt.Sprintf(
			"; give every chapter at least one exercise, or plan at most %d chapter(s)", budget)
	}
	return NewError(CodeInvalidArtifact, schema.StageValidateArtifact,
		fmt.Sprintf("The course's outline planned a chapter with no exercise. Every chapter must contain at least one exercise, and the request asked for %d exercise(s).", budget),
		detail)
}

// outlineExerciseCount totals what the outline planned.
func outlineExerciseCount(o CourseOutline) int {
	n := 0
	for _, ch := range o.Chapters {
		n += ch.Exercises
	}
	return n
}
