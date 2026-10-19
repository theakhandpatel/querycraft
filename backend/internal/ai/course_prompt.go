package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* course_prompt.go owns the three course prompts: the outline ask, the chapter
 * system prompt, and the chapter conversation opener.
 *
 * courseOutlinePrompt and courseAgentPrompt are built from coursePrompt in
 * course.go, so the learner's own words reach both phases. course_agent.go sends
 * them; nothing here decides what the model is allowed to return, which is the
 * gates' work in course_agent_outline.go and course.go.
 */

/* ---------- the conversation ---------- */

// courseOutlinePrompt is the first ask: the plan, before any content. It opens
// with the course marker because the built-in provider recognizes a course
// request by the prompt it is given, the same thing a model does.
//
// The outline ask is also where the chapter budget is stated, because the
// outline call is where the model chooses the chapter count. Two numbers are
// coupled by a rule the model cannot see otherwise: every chapter must hold at
// least one exercise, so a request for N exercises cannot be satisfied by an
// outline of more than N chapters. Left unsaid, a model reads "Exercises: N,
// spread across the chapters" as a total to divide however it likes and plans
// more chapters than it has exercises for. That is the unsatisfiable outline
// validateCourseOutline explains in the learner's terms.
//
// The sentence is appended to coursePrompt, so the existing ask is a strict
// prefix of this one. The prompt's shape is unchanged, no bound moves, and the
// gate accepts exactly the outlines it accepted before. The chapter/exercise
// coupling only has to be stated where chapters are chosen.
func courseOutlinePrompt(req schema.GenerationRequest) string {
	budget := courseExerciseBudget(req)
	return coursePrompt(req) + "\n\nBefore writing any chapter, reply with the course's outline only: the title, the subtitle, the level, the course objectives, the setup, and the chapter list. Each chapter in the outline has an id, its position n, a title, a one-sentence summary, its objectives, and how many exercises it will contain. The outline is what the learner sees while the chapters are still being written, so it must stand on its own. Do not write any chapter body yet." +
		fmt.Sprintf("\n\nThis course's budget is %d exercise(s) in total, and every chapter must contain at least one exercise, so the outline may plan at most %d chapter(s).", budget, budget)
}

// courseAgentSystemPrompt is the course system prompt plus the protocol the
// agent speaks.
func courseAgentSystemPrompt() string {
	return courseSystemPrompt() + `

You now write the course one chapter at a time by calling a function instead of replying with the whole course. Call save_chapter once per chapter, with the id the outline gave it, its markdown sections, and its exercises. Call finish_course when every chapter has been written and accepted. Every save_chapter is answered with what the gateway measured when it ran your chapter against the real database; when a chapter is refused, that answer names the exercise, the stage and the database's own error, and you send the chapter again with that problem fixed and nothing else changed. Write the simplest SQL that answers the question.`
}

// goalOutlineMarker and goalOutlineEnd bracket the outline JSON inside the goal
// prompt. The built-in provider reads the outline back out of the prompt, so
// both sides share the symbols: one edit moves the contract, and no second
// literal can drift.
const (
	goalOutlineMarker = "The course's outline, already agreed and stored. Write exactly these chapters, in this order, with these ids:\n"
	goalOutlineEnd    = "\n\nCall save_chapter"
)

// courseAgentPrompt opens the conversation: the learner's own words, the outline
// that was already agreed and stored, and what to do first. The conversation
// starts from the outline rather than from the whole course because the outline
// is what the learner can already see.
func courseAgentPrompt(req schema.GenerationRequest, outline CourseOutline) string {
	var b strings.Builder
	b.WriteString(coursePrompt(req))
	b.WriteString("\n\n" + goalOutlineMarker)
	raw, err := json.MarshalIndent(outline, "", "  ")
	if err != nil {
		raw = []byte("{}")
	}
	b.WriteString(string(raw))
	b.WriteString(goalOutlineEnd + " now with the first chapter. Send one chapter per call. Each exercise needs a reference solution, an independent cross-check solution that returns the same rows, and a result contract naming the exact column names in order.")
	return b.String()
}
