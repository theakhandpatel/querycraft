package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* M8 — the course agent.
 *
 * M7 asked the model for a whole course in one reply and blocked until the
 * reply arrived. A live run measured 147 s of that, and every fact about the
 * course was decided at the end: nothing was stored while it ran, a single
 * rejected exercise threw away every chapter around it, and the screen had
 * nothing to draw until the last byte.
 *
 * M8 is the same pipeline driven one chapter at a time, in one conversation,
 * through tool calls:
 *
 *   1. one small outline call (title, level, objectives, chapter list);
 *   2. the model then calls save_chapter once per chapter;
 *   3. the gateway runs that chapter's references, cross-checks and adversarial
 *      fixtures in the course's one throwaway environment and answers the call
 *      with what the engine measured;
 *   4. a chapter that passes is appended to the stored plan immediately, so a
 *      poller and a reloaded tab can see it while later chapters are written;
 *   5. the model repairs a refused chapter against the engine's own message,
 *      and a chapter that still does not pass is dropped with its failure
 *      reported;
 *   6. finish_course closes the course, and the stored row moves from
 *      "building" to "active".
 *
 * What this file does NOT do is weaken a rule. A chapter that arrives through
 * save_chapter is placed into the plan and handed to validateCourseChapter —
 * the same function validateCoursePlan calls — and each exercise is built into
 * an Artifact and handed to verifyInEnv, the same stages a challenge passes. A
 * course therefore still cannot contain an exercise that could not have been a
 * challenge, and the rows a learner is graded against are still the rows the
 * engine returned while checking the chapter.
 */

/* ---------- tool names ---------- */

const (
	toolSaveChapter  = "save_chapter"
	toolFinishCourse = "finish_course"
)

/* ---------- bounds ---------- */

const (
	// maxChapterWrites bounds how many times the model may hand in one chapter:
	// the first write plus its in-session repairs. A chapter that still does not
	// verify after this is dropped rather than allowed to end the whole course,
	// because the chapters around it are not the reason it failed.
	maxChapterWrites = 3
	// maxAgentRounds bounds the whole conversation. Every chapter costs one
	// turn per write plus the closing turn, so this is generous for a course at
	// the chapter and exercise bounds and still finite for a model that loops.
	maxAgentRounds = 40
	// maxToolResultChars bounds one tool result before it goes back to the
	// model. A result carries the evaluator's own message, which is what makes
	// the repair possible; it is clipped rather than dropped, and on a rune
	// boundary because a body that is not valid UTF-8 is one some servers
	// reject outright.
	maxToolResultChars = 4000
)

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
	Setup      SetupPlan        `json:"setup"`
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
// is asked for. It bounds the same things validateCoursePlan bounds — chapter
// count, ids, the exercise budget, the setup plan — so a plan cannot be built
// out of an outline the whole-plan gate would have refused.
//
// budget is the exercise count the request asked for (courseExerciseBudget:
// exercise_count under the course's floor and ceiling). It is passed in because
// this gate is the first and only place where the request's budget and the
// model's chapter count are both known: the chapter count is the model's choice,
// so no floor can be computed at request time. A request for fewer exercises
// than the outline plans chapters is therefore not impossible — it is a request
// whose outline must either widen the exercise count to one per chapter or
// shrink the chapter list — and the gate is what turns that fact into a
// sentence the learner can act on.
func validateCourseOutline(o *CourseOutline, budget int, l Limits) *Error {
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
			   divide among more than N chapters produces exactly this outline,
			   and the field-shaped reply it used to get — "chapters[0].exercises:
			   a chapter must plan at least one exercise" — names the provider's
			   schema, not anything the learner can ask for. The outline has no
			   repair round, so this error is the last thing the learner reads;
			   it names the requested budget and both ways to satisfy it. */
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
// The rule is not weakened — an outline that planned a chapter with no exercise
// is still refused, because a course chapter with no exercise is not a course
// chapter — but the sentence changes from the provider's own schema path
// ("chapters[0].exercises: a chapter must plan at least one exercise", which no
// learner can act on) to one that names the exercise budget the request asked
// for and what to change instead.
//
// The outline call has no repair round, so this error is the run's last word:
// it is the only place the learner is told why a request they were allowed to
// make could not be built, and the reason is a coupling between two numbers —
// every chapter needs at least one exercise, so a request for N exercises
// cannot be satisfied by an outline of more than N chapters.
func courseOutlineExerciseError(o *CourseOutline, budget int) *Error {
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
	return newError(CodeInvalidArtifact, StageValidateArtifact,
		fmt.Sprintf("The course's outline planned a chapter with no exercise. Every chapter must contain at least one exercise, and the request asked for %d exercise(s).", budget),
		detail)
}

/* ---------- the course under construction ---------- */

// Chapter statuses as a client reads them in the per-chapter report. A chapter
// is verified when it was written and every exercise in it passed, dropped when
// it could not be repaired, and skipped when the run ended before it was
// written. Nothing else is possible: a chapter is never "partly verified",
// because verification is the thing that decides whether it is kept at all.
const (
	ChapterVerified = "verified"
	ChapterDropped  = "dropped"
	// ChapterRefused is a chapter that was written and refused but whose repair
	// budget had not run out when the run ended. It is reported rather than
	// hidden: "this chapter was tried and did not pass" and "this chapter was
	// never written" are different facts about a course.
	ChapterRefused = "refused"
	ChapterSkipped = "skipped"
)

// courseBuild is the course as it is being written. It owns the plan the
// gateway stores, the state of every planned chapter, and the running totals a
// poller reads. One build serves one run; it is not shared and needs no lock,
// because only the run's own goroutine touches it and every snapshot handed out
// is a copy.
type courseBuild struct {
	request GenerationRequest
	outline CourseOutline
	plan    CoursePlan
	// written is indexed by outline position. A chapter that has not been
	// handed in yet has the zero write: no state, and zero writes.
	written []chapterWrite
	// report carries the resolved chapters in outline order, which is the shape
	// the outcome and the job view publish.
	report   []ChapterResult
	repaired int
	finished bool
	// fatal is set when the run cannot continue for a reason no repair fixes —
	// the store failed. The loop stops and the run reports it instead of
	// pretending the course is still being written.
	fatal *Error
}

type chapterWrite struct {
	writes int
	state  string
	// attempted and lastErr carry the most recent refusal, so a chapter that
	// was written and refused is reported as refused rather than as one the run
	// never reached.
	attempted bool
	lastErr   *Error
}

// newCourseBuild turns a validated outline into the plan skeleton the store
// keeps while the chapters are written. The chapters exist from the first
// moment — the learner sees the course's shape before its content — and carry
// no sections until one passes.
func newCourseBuild(req GenerationRequest, outline CourseOutline) *courseBuild {
	plan := CoursePlan{
		ID:         courseID(outline),
		Version:    1,
		Title:      outline.Title,
		Subtitle:   outline.Subtitle,
		Level:      outline.Level,
		Dialect:    "postgresql",
		Objectives: outline.Objectives,
		Setup:      outline.Setup,
		Chapters:   make([]CourseChapter, 0, len(outline.Chapters)),
	}
	for i, ch := range outline.Chapters {
		n := ch.N
		if n <= 0 {
			n = i + 1
		}
		plan.Chapters = append(plan.Chapters, CourseChapter{
			ID:         ch.ID,
			N:          n,
			Title:      ch.Title,
			Summary:    ch.Summary,
			Kind:       ch.Kind,
			Minutes:    ch.Minutes,
			Objectives: ch.Objectives,
			Sections:   []CourseSection{},
			Exercises:  []CourseExercise{},
		})
	}
	b := &courseBuild{request: req, outline: outline, plan: plan}
	b.written = make([]chapterWrite, len(outline.Chapters))
	return b
}

// newCourseBuildFromRecord rebuilds the in-memory build from a stored row, so a
// chapter phase can continue where an earlier run stopped. The stored plan IS
// the state: its chapters carry the outline the learner approved, and a chapter
// that already verified carries its content. The request is stored beside it
// (CourseRecord.Request) because the chapter prompt is built from the learner's
// own words, which the plan does not keep.
//
// The skip condition the design names lives here: a chapter the plan already
// carries exercises for verified, so it is marked resolved and the conversation
// is never asked to write it again.
func newCourseBuildFromRecord(rec CourseRecord) *courseBuild {
	req := requestFromRecord(rec)
	outline := outlineFromRecord(rec)
	plan := rec.Plan
	if plan.Chapters == nil {
		plan.Chapters = []CourseChapter{}
	}
	b := &courseBuild{request: req, outline: outline, plan: plan}
	b.written = make([]chapterWrite, len(outline.Chapters))
	for i := range b.plan.Chapters {
		// A chapter only carries content after it verified: that is the store's
		// own record of "this one is done", and the whole reason a resumed build
		// can cost nothing for the chapters it already has.
		if len(b.plan.Chapters[i].Exercises) > 0 {
			b.written[i].state = ChapterVerified
		}
	}
	// The stored report says what was measured, including any chapter that was
	// written and dropped; a dropped chapter must not be re-asked for either.
	for _, res := range rec.Outcome.Chapters {
		idx := b.index(res.ID)
		if idx < 0 {
			continue
		}
		switch res.Status {
		case ChapterVerified:
			b.written[idx].state = ChapterVerified
		case ChapterDropped:
			b.written[idx].state = ChapterDropped
		}
	}
	b.report = append([]ChapterResult(nil), rec.Outcome.Chapters...)
	b.repaired = rec.Outcome.Evidence.Repairs
	return b
}

// requestFromRecord recovers the generation request a stored course was built
// from. A record stored before the request was persisted has none; the fields
// the plan itself knows — the goal, as the plan's title — are used and the rest
// are left at their defaults rather than invented, so an old record is still
// readable and resumes with a prompt built only from what is really known.
func requestFromRecord(rec CourseRecord) GenerationRequest {
	if rec.Request != nil {
		req := *rec.Request
		req.Kind = "course"
		return req
	}
	return GenerationRequest{
		Kind:          "course",
		Title:         rec.Plan.Title,
		Difficulty:    Intermediate,
		ExerciseCount: rec.Plan.exerciseCount(),
	}
}

// outlineFromRecord recovers the outline the chapter phase is written against.
// Everything the outline gate reads is already in the stored plan's chapters —
// id, position, title, summary, kind, minutes, objectives — and the one field
// the plan does not keep is how many exercises each chapter planned. That is
// reconstructed from the request's budget and, where a chapter already carries
// content, reported as the count it actually holds.
func outlineFromRecord(rec CourseRecord) CourseOutline {
	req := requestFromRecord(rec)
	share := plannedExercisesPerChapter(courseExerciseBudget(req), len(rec.Plan.Chapters))
	outline := CourseOutline{
		Title:      rec.Plan.Title,
		Subtitle:   rec.Plan.Subtitle,
		Level:      rec.Plan.Level,
		Objectives: rec.Plan.Objectives,
		Setup:      rec.Plan.Setup,
	}
	for i, ch := range rec.Plan.Chapters {
		n := ch.N
		if n <= 0 {
			n = i + 1
		}
		planned := len(ch.Exercises)
		if planned < 1 {
			planned = share
		}
		outline.Chapters = append(outline.Chapters, OutlineChapter{
			ID:         ch.ID,
			N:          n,
			Title:      ch.Title,
			Summary:    ch.Summary,
			Kind:       ch.Kind,
			Minutes:    ch.Minutes,
			Objectives: ch.Objectives,
			Exercises:  planned,
		})
	}
	return outline
}

// plannedExercisesPerChapter distributes the request's exercise budget across
// the stored chapters, at least one each — the coupling validateCourseOutline
// enforces. It is only ever a prompt hint for a resumed build (the authoritative
// count is the chapter that is actually saved), which is why a reconstruction is
// acceptable where the stored plan lost the number.
func plannedExercisesPerChapter(budget, chapters int) int {
	if chapters < 1 {
		return 1
	}
	if n := budget / chapters; n >= 1 {
		return n
	}
	return 1
}

// courseID names a course from its own shape, so the same request produces the
// same id and an interrupted run reuses the row it already stored rather than
// leaving a second one behind.
func courseID(outline CourseOutline) string {
	seed := outline.Title
	if len(outline.Chapters) > 0 {
		seed += "\x00" + outline.Chapters[0].ID
	}
	return fmt.Sprintf("course-%016x", hashPrompt(seed))
}

// index returns the outline position of a chapter id, or -1.
func (b *courseBuild) index(id string) int {
	for i := range b.outline.Chapters {
		if b.outline.Chapters[i].ID == id {
			return i
		}
	}
	return -1
}

// next is the first planned chapter that has not been resolved, or -1.
func (b *courseBuild) next() int {
	for i := range b.written {
		if b.written[i].state == "" {
			return i
		}
	}
	return -1
}

// missing lists the planned chapters that were never written.
func (b *courseBuild) missing() []string {
	out := []string{}
	for i := range b.written {
		if b.written[i].state == "" {
			out = append(out, b.outline.Chapters[i].ID)
		}
	}
	return out
}

// resolved reports whether every planned chapter reached a final state.
func (b *courseBuild) resolved() bool { return b.next() < 0 }

// verifiedChapters returns the chapters that verified, in outline order.
func (b *courseBuild) verifiedChapters() []CourseChapter {
	out := make([]CourseChapter, 0, len(b.outline.Chapters))
	for i := range b.plan.Chapters {
		if b.written[i].state == ChapterVerified {
			out = append(out, b.plan.Chapters[i])
		}
	}
	return out
}

// exerciseCount and measuredRows total what the engine actually ran.
func (b *courseBuild) exerciseCount() int {
	n := 0
	for _, ch := range b.report {
		n += len(ch.Exercises)
	}
	return n
}

func (b *courseBuild) measuredRows() int {
	n := 0
	for _, ch := range b.report {
		for _, ex := range ch.Exercises {
			n += ex.ReferenceRows
		}
	}
	return n
}

// record appends or replaces one chapter's report entry, keeping outline order.
// The report is rebuilt in outline order rather than appended as chapters
// arrive, because a chapter may arrive out of order and the report a learner
// reads is the course's order, not the conversation's.
func (b *courseBuild) record(res ChapterResult) {
	for i := range b.report {
		if b.report[i].ID == res.ID {
			b.report[i] = res
			return
		}
	}
	b.report = append(b.report, res)
	b.sortReport()
}

// sortReport orders the report by the outline.
func (b *courseBuild) sortReport() {
	rank := make(map[string]int, len(b.outline.Chapters))
	for i, ch := range b.outline.Chapters {
		rank[ch.ID] = i
	}
	ordered := make([]ChapterResult, len(b.report))
	copy(ordered, b.report)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && rank[ordered[j].ID] < rank[ordered[j-1].ID]; j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	b.report = ordered
}

// fullReport is one entry per planned chapter, in order: what verified, what
// was dropped, and what the run never reached. A report that omitted the last
// kind would let "not checked" read as "fine".
func (b *courseBuild) fullReport() []ChapterResult {
	out := make([]ChapterResult, 0, len(b.outline.Chapters))
	for i, och := range b.outline.Chapters {
		found := false
		for _, res := range b.report {
			if res.ID == och.ID {
				out = append(out, res)
				found = true
				break
			}
		}
		if !found {
			state := ChapterSkipped
			if b.written[i].state != "" {
				state = b.written[i].state
			} else if b.written[i].attempted {
				state = ChapterRefused
			}
			out = append(out, ChapterResult{ID: och.ID, Title: och.Title, Status: state, Exercises: []ExerciseResult{}, Error: b.written[i].lastErr})
		}
	}
	return out
}

func (b *courseBuild) verifiedCount() int {
	n := 0
	for _, ch := range b.fullReport() {
		if ch.Status == ChapterVerified {
			n++
		}
	}
	return n
}

/* ---------- the conversation ---------- */

// courseOutlinePrompt is the first ask: the plan, before any content. It opens
// with the course marker because the built-in provider recognizes a course
// request by the prompt it is given — the same thing a model does.
//
// The outline ask is also where the chapter budget is stated, because the
// outline call is where the model chooses the chapter count. The two numbers
// are coupled by a rule the model cannot see otherwise: every chapter must hold
// at least one exercise, so a request for N exercises cannot be satisfied by an
// outline of more than N chapters. Left unsaid, a model reads "Exercises: N,
// spread across the chapters" as a total to divide however it likes and plans
// more chapters than it has exercises for — the unsatisfiable outline
// validateCourseOutline now explains in the learner's terms.
//
// The sentence is appended, so the pre-existing ask is a strict prefix of this
// one: the prompt's shape is unchanged, no bound moves, and the gate accepts
// exactly the outlines it accepted before. coursePrompt itself is untouched —
// it is shared with the whole-plan path, and the chapter/exercise coupling only
// has to be stated where chapters are chosen.
func courseOutlinePrompt(req GenerationRequest) string {
	budget := courseExerciseBudget(req)
	return coursePrompt(req) + "\n\nBefore writing any chapter, reply with the course's outline only: the title, the subtitle, the level, the course objectives, the setup, and the chapter list. Each chapter in the outline has an id, its position n, a title, a one-sentence summary, its objectives, and how many exercises it will contain. The outline is what the learner sees while the chapters are still being written, so it must stand on its own. Do not write any chapter body yet." +
		fmt.Sprintf("\n\nThis course's budget is %d exercise(s) in total, and every chapter must contain at least one exercise, so the outline may plan at most %d chapter(s).", budget, budget)
}

// courseOutlineSchema is the JSON Schema of the outline call. It is a separate
// schema from courseChapterSchema because the outline is a different object:
// asking one question and validating a different one is how a repair round gets
// spent explaining a shape nobody asked for.
const courseOutlineSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "courseOutline",
  "type": "object",
  "required": ["title", "subtitle", "level", "objectives", "setup", "chapters"],
  "properties": {
    "title": {"type": "string", "minLength": 1},
    "subtitle": {"type": "string", "minLength": 1},
    "level": {"type": "string", "minLength": 1},
    "objectives": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
    "setup": {
      "type": "object",
      "required": ["dataset", "schema_version", "seed_version", "statements"],
      "properties": {
        "dataset": {"type": "string", "minLength": 1},
        "schema_version": {"type": "integer"},
        "seed_version": {"type": "integer"},
        "statements": {"type": "array", "items": {"type": "string", "minLength": 1}}
      }
    },
    "chapters": {
      "type": "array", "minItems": 1, "maxItems": 8,
      "items": {
        "type": "object",
        "required": ["id", "title", "summary", "objectives", "exercises"],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "n": {"type": "integer"},
          "title": {"type": "string", "minLength": 1},
          "summary": {"type": "string", "minLength": 1},
          "kind": {"type": "string", "minLength": 1},
          "minutes": {"type": "integer"},
          "objectives": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
          "exercises": {"type": "integer"}
        }
      }
    }
  }
}`

// courseTools is what the model may call. Both names are constants because the
// built-in provider answers the same protocol a model does, and a name written
// twice would eventually be written differently twice.
func courseTools() []Tool {
	return []Tool{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        toolSaveChapter,
				Description: "Save one finished chapter of the course: its markdown sections and its exercises, using the id the outline gave it. The gateway runs every exercise against the real database and answers with what it measured, so a chapter that does not run comes back with the database's own error and must be sent again.",
				Parameters:  json.RawMessage(courseChapterSchema),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        toolFinishCourse,
				Description: "Close the course once every chapter in the outline has been saved and accepted.",
				Parameters:  json.RawMessage(finishCourseSchema),
			},
		},
	}
}

// courseAgentSystemPrompt is the challenge system prompt plus the protocol the
// agent speaks.
func courseAgentSystemPrompt() string {
	return courseSystemPrompt() + `

You now write the course one chapter at a time by calling a function instead of replying with the whole course. Call save_chapter once per chapter, with the id the outline gave it, its markdown sections, and its exercises. Call finish_course when every chapter has been written and accepted. Every save_chapter is answered with what the gateway measured when it ran your chapter against the real database; when a chapter is refused, that answer names the exercise, the stage and the database's own error, and you send the chapter again with that problem fixed and nothing else changed. Write the simplest SQL that answers the question.`
}

// courseAgentPrompt opens the conversation: the learner's own words, the
// outline that was already agreed and stored, and what to do first. The
// conversation starts from the outline rather than from the whole course
// because the outline is what the learner can already see.
func courseAgentPrompt(req GenerationRequest, outline CourseOutline) string {
	var b strings.Builder
	b.WriteString(coursePrompt(req))
	b.WriteString("\n\nThe course's outline, already agreed and stored. Write exactly these chapters, in this order, with these ids:\n")
	raw, err := json.MarshalIndent(outline, "", "  ")
	if err != nil {
		raw = []byte("{}")
	}
	b.WriteString(string(raw))
	b.WriteString("\n\nCall save_chapter now with the first chapter. Send one chapter per call. Each exercise needs a reference solution, an independent cross-check solution that returns the same rows, and a result contract naming the exact column names in order.")
	return b.String()
}

// storedChapterTurns renders the chapters a resumed build already has as the
// conversation turns that put them there. It is the difference between a fresh
// run and a continuation: a fresh build has none and this returns nothing, so
// today's conversation is unchanged. A resumed build replays each resolved
// chapter as an assistant save_chapter call and the gateway's own verdict for
// it, which is exactly the protocol the model speaks — so the model sees what
// is done and is never asked to write it again.
func storedChapterTurns(build *courseBuild) []ProviderMessage {
	var out []ProviderMessage
	for i := range build.outline.Chapters {
		state := build.written[i].state
		if state != ChapterVerified && state != ChapterDropped {
			continue
		}
		och := build.outline.Chapters[i]
		out = append(out,
			ProviderMessage{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{{
					ID:       "stored_" + och.ID,
					Type:     "function",
					Function: ToolCallFunction{Name: toolSaveChapter, Arguments: `{"id":"` + och.ID + `"}`},
				}},
			},
			ProviderMessage{
				Role:       "tool",
				ToolCallID: "stored_" + och.ID,
				Content: toolResult(map[string]any{
					"status":  state,
					"chapter": och.ID,
					"title":   och.Title,
					"message": fmt.Sprintf("Chapter %q was already %s in the stored course.", och.ID, state),
				}),
			},
		)
	}
	return out
}

/* ---------- the run ---------- */

// generateCourse runs the M8 course agent. By default it runs both phases in
// one request: the outline is validated and stored, and the chapter
// conversation follows immediately, so nothing a client did before sees a
// different run. When the request asks for the outline to be approved first
// (RequireApproval), the run stops after the outline: the stored `building`
// course is the state, the outcome is `awaiting_approval`, and BuildCourse (a
// later, separate request) continues it into the chapter phase.
func (s *Service) generateCourse(ctx context.Context, req GenerationRequest) Outcome {
	limits := s.cfg.Limits.Merge(DefaultLimits())
	runCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	p := newPipeline(s, req, limits)
	p.watch(ctx)

	start := time.Now()
	build, failed := p.outlinePhase(runCtx, req, start)
	if failed != nil {
		return *failed
	}
	if req.RequireApproval {
		return p.awaitApproval(build)
	}
	if aerr := p.runCourseAgent(runCtx, build); aerr != nil {
		out := p.failedOutcome(StatusFailed, aerr)
		out.Plan = nil
		out.Chapters = build.fullReport()
		return out
	}
	return p.finalizeCourse(runCtx, build)
}

// outlinePhase is the first half of a course run, and the whole of a paused one:
// it validates the request, asks for the outline, validates that against the
// same gate the whole-plan path uses, and stores it as a `building` course
// before a single chapter is written. It returns the build, or the failed
// outcome that ended the run — never both.
func (p *pipeline) outlinePhase(runCtx context.Context, req GenerationRequest, start time.Time) (*courseBuild, *Outcome) {
	p.begin(StageValidateRequest)
	if verr := validateRequest(req, p.limits); verr != nil {
		p.timed(StageValidateRequest, StateFailed, verr.Detail, start)
		out := p.failedOutcome(StatusFailed, verr)
		return nil, &out
	}
	req = normalizeRequest(req, p.limits)
	p.timed(StageValidateRequest, StateDone, requestSummary(req), start)

	// The outline. It may use the strict schema path, because it is one small
	// answer with no tool calls in it.
	p.begin(StageRequestBundle)
	raw, perr := p.svc.complete(runCtx, ProviderRequest{
		Kind:           "course",
		System:         courseSystemPrompt(),
		Prompt:         courseOutlinePrompt(req),
		Schema:         []byte(courseOutlineSchema),
		MaxOutputBytes: p.limits.MaxOutputBytes,
	})
	if perr != nil {
		out := p.failedOutcome(StatusFailed, perr)
		return nil, &out
	}
	var outline CourseOutline
	if derr := decodeJSON(raw, &outline); derr != nil {
		e := newError(CodeInvalidArtifact, StageRequestBundle,
			"The provider's reply is not a usable course outline.", "outline: "+derr.Error())
		p.timed(StageRequestBundle, StateFailed, e.Detail, start)
		out := p.failedOutcome(StatusFailed, e)
		return nil, &out
	}
	p.begin(StageValidateArtifact)
	if verr := validateCourseOutline(&outline, courseExerciseBudget(req), p.limits); verr != nil {
		p.timed(StageValidateArtifact, StateFailed, verr.Detail, start)
		out := p.failedOutcome(StatusFailed, verr)
		return nil, &out
	}
	p.timed(StageValidateArtifact, StateDone,
		fmt.Sprintf("outline accepted: %d chapters, %d exercises planned", len(outline.Chapters), outlineExerciseCount(outline)), start)

	build := newCourseBuild(req, outline)
	p.planned = len(outline.Chapters)
	p.courseID = build.plan.ID

	// Stored before a single chapter is written, so a reload, a second device
	// or a poll sees the course's shape rather than nothing.
	if oerr := p.storeBuilding(runCtx, build); oerr != nil {
		out := p.failedOutcome(StatusFailed, oerr)
		return nil, &out
	}
	p.timed(StageRequestBundle, StateDone,
		fmt.Sprintf("outline received: %d chapters planned", len(outline.Chapters)), start)
	return build, nil
}

// awaitApproval is the outcome of a run that stopped after storing its outline.
// The stored course is named in Plan, because that is the index the learner is
// being asked to approve; the chapter phase runs later, as its own request. The
// activate stage is reported as skipped rather than failed: nothing went wrong,
// the run simply stopped where the learner asked it to.
func (p *pipeline) awaitApproval(build *courseBuild) Outcome {
	p.mark(StageActivate, StateSkipped,
		"waiting for the learner to approve the outline before the chapters are written")
	out := p.outcome(StatusAwaitingApproval, nil, nil)
	plan := build.plan
	plan.Chapters = buildingChapters(build)
	out.Plan = &plan
	out.Chapters = build.fullReport()
	return out
}

// BuildCourse continues a stored course that stopped awaiting approval. It
// reloads the outline from the row, rebuilds the chapter conversation from the
// request stored beside it, and runs the chapter phase to completion. A chapter
// that already verified is not re-asked for: the stored plan's own content is
// the skip condition (newCourseBuildFromRecord).
func (s *Service) BuildCourse(ctx context.Context, id string) Outcome {
	avail := s.Available()
	if !avail.Available {
		return s.unavailable("course", avail)
	}
	rec := newTranscriptRecorder(RunKindCourse, runIDFrom(ctx), providerRedactor(s.cfg.Provider))
	ctx = WithTranscript(ctx, rec)
	out := s.buildCourseRecorded(ctx, id)
	s.saveTranscript(ctx, rec, out)
	return out
}

// buildCourseRecorded is BuildCourse with the transcript recorder already
// attached — the same split generate/generateRecorded use, for the same reason:
// the recorder has to exist before the first provider call and be finished once,
// whatever the outcome.
func (s *Service) buildCourseRecorded(ctx context.Context, id string) Outcome {
	limits := s.cfg.Limits.Merge(DefaultLimits())
	runCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	if s.cfg.Depot == nil {
		return s.unavailable("course", s.Available())
	}
	rec, found, err := s.cfg.Depot.Course(runCtx, id)
	if err != nil {
		return courseBuildRefusal(CodeUnavailable, "The stored course could not be read.")
	}
	if !found {
		return courseBuildRefusal(CodeNotFound, "No such course.")
	}
	req := requestFromRecord(rec)
	p := newPipeline(s, req, limits)
	p.watch(ctx)
	build := newCourseBuildFromRecord(rec)
	p.planned = len(build.outline.Chapters)
	p.courseID = build.plan.ID

	if aerr := p.runCourseAgent(runCtx, build); aerr != nil {
		out := p.failedOutcome(StatusFailed, aerr)
		out.Plan = nil
		out.Chapters = build.fullReport()
		return out
	}
	return p.finalizeCourse(runCtx, build)
}

// courseBuildRefusal is the honest answer when a build cannot even start: the
// row is gone, or the store cannot be read. It stores nothing and reports the
// reason instead of pretending a chapter phase ran.
func courseBuildRefusal(code, message string) Outcome {
	return Outcome{
		Status:   StatusFailed,
		Kind:     "course",
		Artifact: nil,
		Stages:   []Stage{},
		Attempts: []Attempt{},
		Error:    newError(code, StageActivate, message, ""),
	}
}

// outlineExerciseCount totals what the outline planned.
func outlineExerciseCount(o CourseOutline) int {
	n := 0
	for _, ch := range o.Chapters {
		n += ch.Exercises
	}
	return n
}

// runCourseAgent drives the chapter conversation. It returns nil when the
// conversation ended (however it ended), and an error only when the run itself
// could not continue: the provider is gone, or the run ran out of time.
func (p *pipeline) runCourseAgent(ctx context.Context, build *courseBuild) *Error {
	if p.svc.cfg.Depot == nil {
		return newError(CodeUnavailable, StageActivate,
			"The course could not be stored while it was being written.", "")
	}
	ex, release, err := p.openScratch(ctx, &build.plan)
	if err != nil {
		return err
	}
	defer release()

	messages := []ProviderMessage{
		{Role: "system", Content: courseAgentSystemPrompt()},
		{Role: "user", Content: courseAgentPrompt(build.request, build.outline)},
	}
	// A resumed build already has chapters in the store. The conversation says
	// so, as the turns that put them there, because a model that is simply
	// asked for "the outline's chapters" would write them all again and spend
	// the run re-saving work that already verified. A fresh run has none, so
	// this adds nothing to today's conversation.
	messages = append(messages, storedChapterTurns(build)...)
	// The conversation is recorded as it is assembled, because the agent owns
	// it: completeRaw records the reply, but a turn's *input* is the whole
	// conversation so far, which is not a single request. Recording the two
	// opening turns here and each assistant turn and tool result below keeps
	// the transcript one entry per fact rather than one per round.
	rec := transcriptFrom(ctx)
	for _, m := range messages {
		rec.message(m.Role, m.Content)
	}
	tools := courseTools()
	turns := 0

	for round := 0; round < maxAgentRounds; round++ {
		if err := ctx.Err(); err != nil {
			return p.agentTimeout(nil)
		}
		if build.finished && build.resolved() {
			break
		}
		p.begin(StageRequestBundle)
		resp, perr := p.svc.completeRaw(ctx, ProviderRequest{
			Kind:           "course",
			Prompt:         courseOutlinePrompt(build.request),
			Messages:       messages,
			Tools:          tools,
			ToolChoice:     "auto",
			MaxOutputBytes: p.limits.MaxOutputBytes,
		})
		if perr != nil {
			return perr
		}
		turns++
		p.mark(StageRequestBundle, StateDone,
			fmt.Sprintf("chapter conversation: %d turn(s), %d of %d chapters written", turns, build.verifiedCount(), len(build.outline.Chapters)))

		// The assistant turn itself is recorded by completeRaw. A reply with no
		// tool call is the protocol being missed; the nudge back is a real
		// message the model is sent, so it is recorded too.
		messages = append(messages, ProviderMessage{Role: "assistant", Content: resp.Text, ToolCalls: resp.ToolCalls})
		if len(resp.ToolCalls) == 0 {
			// The protocol asks for a tool call. Saying so once is a repair; a
			// model that keeps replying with prose is bounded by the rounds.
			nudge := "Call save_chapter with the next chapter, or finish_course when every chapter has been written and accepted."
			rec.message("user", nudge)
			messages = append(messages, ProviderMessage{Role: "user", Content: nudge})
			continue
		}
		for _, call := range resp.ToolCalls {
			result := p.handleToolCall(ctx, build, ex, call)
			rec.recordToolResult(call.ID, result)
			messages = append(messages, ProviderMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
		if build.fatal != nil {
			return build.fatal
		}
	}

	if !build.finished && !build.resolved() {
		// The conversation ran out of rounds with chapters still unwritten.
		// Every one of them is reported as skipped by fullReport, so the course
		// is a smaller honest one rather than a course with a hole in it.
		p.mark(StageRepair, StateDone,
			fmt.Sprintf("the conversation ended after %d turn(s) with %d chapter(s) unwritten", turns, len(build.missing())))
	}
	return nil
}

// handleToolCall executes one function call and returns the tool result the
// model reads next. The result is the engine's own verdict, never a summary of
// it: "the cross-check disagreed" is not something a model can repair against,
// and the evaluator's message is.
func (p *pipeline) handleToolCall(ctx context.Context, build *courseBuild, ex engine.Executor, call ToolCall) string {
	switch strings.TrimSpace(call.Function.Name) {
	case toolSaveChapter:
		return p.saveChapterTool(ctx, build, ex, call)
	case toolFinishCourse:
		return p.finishCourseTool(build)
	default:
		return toolResult(map[string]any{
			"status":  "unknown_function",
			"message": fmt.Sprintf("There is no function %q. The functions are %s and %s.", call.Function.Name, toolSaveChapter, toolFinishCourse),
		})
	}
}

// saveChapterTool is the load-bearing call. It validates the chapter with the
// same gate the whole-plan path uses, runs it in the course's scratch
// environment, and answers with the engine's verdict.
func (p *pipeline) saveChapterTool(ctx context.Context, build *courseBuild, ex engine.Executor, call ToolCall) string {
	args := strings.TrimSpace(call.Function.Arguments)
	if args == "" {
		return toolResult(map[string]any{"status": "invalid", "message": "save_chapter was called with no arguments."})
	}
	var chapter CourseChapter
	if jerr := decodeJSON(args, &chapter); jerr != nil {
		return toolResult(map[string]any{
			"status":  "invalid",
			"message": "save_chapter's arguments are not one chapter object.",
			"detail":  jerr.Error(),
		})
	}
	pos := build.index(chapter.ID)
	if pos < 0 {
		return toolResult(map[string]any{
			"status":  "unknown_chapter",
			"message": fmt.Sprintf("There is no chapter %q in the outline. Its chapters are %s.", chapter.ID, strings.Join(build.chapterIDs(), ", ")),
		})
	}
	if build.written[pos].state != "" {
		return toolResult(map[string]any{
			"status":  "already_written",
			"chapter": chapter.ID,
			"message": fmt.Sprintf("Chapter %q was already %s. Call save_chapter with the next chapter that has not been written.", chapter.ID, build.written[pos].state),
		})
	}

	// The chapter is placed in the plan before it is validated, because the
	// exercise gate reads the plan's own dialect and setup. The previous
	// skeleton is kept so a refusal leaves the stored plan exactly as it was:
	// a rejected chapter must not leave half of itself behind.
	skeleton := build.plan.Chapters[pos]
	candidate := withChapterSkeleton(chapter, build.outline.Chapters[pos], skeleton)
	build.plan.Chapters[pos] = candidate
	seen, total := build.gateFacts()
	if verr := validateCourseChapter(&build.plan, pos, seen, &total, p.limits); verr != nil {
		build.plan.Chapters[pos] = skeleton
		return p.chapterRefused(ctx, build, pos, candidate, verr, "")
	}
	if total > maxCourseExerciseTotal {
		build.plan.Chapters[pos] = skeleton
		return p.chapterRefused(ctx, build, pos, candidate,
			coursePlanError("chapters", fmt.Sprintf("%d exercises exceeds the limit of %d per course", total, maxCourseExerciseTotal)), "")
	}

	p.setChapter(build.outline.Chapters[pos].ID)
	res, verr := p.verifyChapterInEnv(ctx, ex, &build.plan, pos)
	if verr != nil {
		build.plan.Chapters[pos] = skeleton
		// The engine refused one exercise and has already named it: that id is
		// the single most useful thing the model can be told, because without
		// it a repair pass is a rewrite of the whole chapter.
		return p.chapterRefused(ctx, build, pos, candidate, verr, failedExerciseID(res))
	}

	build.written[pos].state = ChapterVerified
	build.record(res)
	// The store is written before the live view is published, and that order is
	// load-bearing: publishing first let a poller read "1 chapter verified"
	// from the job while GET /courses/{id} still carried the chapter as an
	// empty skeleton — the job's counter ran ahead of the record. A poller must
	// never be told a chapter is stored before it is. storeBuilding notifies on
	// its own, and at that moment p.verified still holds the previous count, so
	// the live view can only ever lag the store, never lead it.
	if err := p.storeBuilding(ctx, build); err != nil {
		// The chapter verified but could not be kept. That is not a content
		// problem and no repair fixes it, so it ends the run rather than
		// spending a write: the outcome says the course could not be stored.
		build.written[pos].state = ""
		build.report = removeReport(build.report, skeleton.ID)
		p.publish(build)
		return toolResult(map[string]any{
			"status":  "storage_failed",
			"chapter": chapter.ID,
			"message": "The chapter verified but could not be stored, so the run cannot continue.",
		})
	}
	p.publish(build)
	rows := chapterRows(res)
	return toolResult(map[string]any{
		"status":         ChapterVerified,
		"chapter":        chapter.ID,
		"title":          chapter.Title,
		"exercises":      len(res.Exercises),
		"rows_measured":  rows,
		"chapters_done":  build.verifiedCount(),
		"chapters_total": len(build.outline.Chapters),
		"measured":       exerciseVerdicts(res),
		"message":        fmt.Sprintf("Chapter %q is saved: %d exercise(s) executed and cross-checked, %d row(s) measured.", chapter.ID, len(res.Exercises), rows),
	})
}

// publish copies the build's live facts onto the run, which is what a poller
// reads. It is called on every accepted or dropped chapter, never on a refusal
// that will be repaired: a chapter that is still being repaired has not
// resolved, and reporting it as resolved would be a claim about a chapter the
// engine has already refused.
func (p *pipeline) publish(build *courseBuild) {
	p.chapters = append([]ChapterResult(nil), build.report...)
	p.verified = build.verifiedCount()
	p.notify()
}

// chapterRows totals one chapter's measured reference rows.
func chapterRows(res ChapterResult) int {
	n := 0
	for _, ex := range res.Exercises {
		n += ex.ReferenceRows
	}
	return n
}

// chapterRefused turns a failed write into the model's next instruction. Before
// the write budget is spent it is a repair; after it, the chapter is dropped,
// because the chapters around it are not the reason it failed.
func (p *pipeline) chapterRefused(ctx context.Context, build *courseBuild, pos int, candidate CourseChapter, verr *Error, failed string) string {
	build.written[pos].writes++
	build.written[pos].attempted = true
	build.written[pos].lastErr = verr
	och := build.outline.Chapters[pos]
	result := map[string]any{
		"status":  "refused",
		"chapter": och.ID,
		"stage":   verr.Stage,
		"message": verr.Message,
		"detail":  verr.Detail,
	}
	exID, exErr := failingExercise(verr, &candidate)
	if exID == "" {
		// The gate did not name an exercise; the engine ran the chapter and
		// stopped at one, and that is the exercise the repair is about.
		exID = failed
	}
	if exID != "" {
		result["exercise"] = exID
		result["exercise_error"] = exErr
	}
	if build.written[pos].writes >= maxChapterWrites {
		// The chapter never verified. It is dropped so the chapters that did
		// verify can still be handed to a learner — a smaller honest course,
		// never a course with an unverified exercise in it.
		build.written[pos].state = ChapterDropped
		build.record(ChapterResult{
			ID:        och.ID,
			Title:     och.Title,
			Status:    ChapterDropped,
			Exercises: failedExercises(&candidate, verr, exID),
			Error:     verr,
		})
		p.publish(build)
		// The drop has to reach the store: a chapter the gateway dropped but
		// the row still shows would be a chapter a learner opens and finds
		// missing. A store failure here therefore ends the run rather than
		// being swallowed, and the run says the course could not be stored.
		if serr := p.storeBuilding(ctx, build); serr != nil {
			build.fatal = serr
			result["status"] = "storage_failed"
			result["message"] = "The chapter was dropped but the course could not be stored, so the run cannot continue."
		}
		result["status"] = ChapterDropped
		result["message"] = fmt.Sprintf("Chapter %q was dropped after %d write(s): it never verified. The course will be stored without it.", och.ID, build.written[pos].writes)
		return toolResult(result)
	}
	build.repaired++
	result["attempts_left"] = maxChapterWrites - build.written[pos].writes
	result["message"] = fmt.Sprintf("%s: %s %s Send chapter %q again with that fixed and everything else unchanged.", verr.Stage, verr.Message, verr.Detail, och.ID)
	return toolResult(result)
}

// finishCourseTool closes the course. A course with unwritten chapters is not
// closed: the answer names what is missing, and the model writes it.
func (p *pipeline) finishCourseTool(build *courseBuild) string {
	if missing := build.missing(); len(missing) > 0 {
		return toolResult(map[string]any{
			"status":  "incomplete",
			"missing": missing,
			"message": fmt.Sprintf("The outline's chapter(s) %s have not been saved yet. Call save_chapter for them, then call finish_course.", strings.Join(missing, ", ")),
		})
	}
	build.finished = true
	return toolResult(map[string]any{
		"status":    "closed",
		"chapters":  build.verifiedCount(),
		"exercises": build.exerciseCount(),
		"message":   "The course is closed. Every chapter that verified is stored.",
	})
}

// finalizeCourse assembles the plan from the chapters that verified and decides
// whether the course can be handed to a learner.
func (p *pipeline) finalizeCourse(ctx context.Context, build *courseBuild) Outcome {
	report := build.fullReport()
	verified := build.verifiedChapters()

	p.evidence.Repairs = build.repaired
	p.evidence.ReferenceRows = build.measuredRows()
	verifiedExercises := 0
	dropped := []string{}
	for _, ch := range report {
		if ch.Status == ChapterVerified {
			verifiedExercises += len(ch.Exercises)
		} else if ch.Status == ChapterDropped {
			dropped = append(dropped, ch.ID)
		}
	}
	dropNote := ""
	if len(dropped) > 0 {
		dropNote = fmt.Sprintf("; %d chapter(s) dropped (%s)", len(dropped), strings.Join(dropped, ", "))
	}
	stageStart := time.Now()
	p.timed(StageReferenceContract, StateDone,
		fmt.Sprintf("%d/%d reference solutions returned rows (%d rows measured)", verifiedExercises, verifiedExercises, p.evidence.ReferenceRows), stageStart)
	p.timed(StageCrossCheck, StateDone,
		fmt.Sprintf("%d independent solutions agreed with their reference%s", verifiedExercises, dropNote), stageStart)
	p.timed(StageAdversarialFixtures, StateDone,
		fmt.Sprintf("wrong queries were rejected for all %d exercises%s", verifiedExercises, dropNote), stageStart)
	if build.repaired > 0 {
		p.mark(StageRepair, StateDone, fmt.Sprintf("%d chapter write(s) repaired in session", build.repaired))
	}

	if len(verified) == 0 {
		// Nothing verified, so there is nothing to keep: the row written at
		// the outline is deleted rather than left as a course a learner can
		// open, because "a course with no chapters" is not a course.
		if err := p.svc.cfg.Depot.DeleteCourse(ctx, build.plan.ID); err != nil {
			p.mark(StageActivate, StateFailed, "no chapter verified and the outline row could not be removed")
		}
		return p.courseRefused(build, report)
	}
	return p.activateCourse(ctx, &build.plan, report, build)
}

// courseRefused is the outcome of a run in which no chapter verified. It names
// the failure that stopped the last chapter and counts what was attempted.
func (p *pipeline) courseRefused(build *courseBuild, report []ChapterResult) Outcome {
	// The row the outline wrote is gone, so the run stops naming it: a poller
	// that reads after the delete must not be handed an id that no longer
	// answers.
	p.courseID = ""
	refused, dropped := 0, 0
	var last *Error
	for _, ch := range report {
		switch ch.Status {
		case ChapterDropped:
			dropped++
		case ChapterRefused:
			refused++
		}
		for _, ex := range ch.Exercises {
			if ex.Status != "verified" && ex.Error != nil {
				last = ex.Error
			}
		}
		if last == nil && ch.Error != nil {
			last = ch.Error
		}
	}
	code := CodeRepairExhausted
	stage := StageRequestBundle
	if last != nil {
		stage = last.Stage
		switch last.Stage {
		case StageCrossCheck:
			code = CodeCrossCheckFailed
		case StageReferenceContract:
			code = CodeReferenceFailed
		case StageValidateArtifact:
			code = CodeInvalidArtifact
		}
	}
	e := newError(code, stage,
		fmt.Sprintf("No chapter verified, so the course was refused and nothing was stored: %d chapter(s) dropped, %d written and refused.",
			dropped, refused), "")
	p.evidence.Repairs = build.repaired
	out := p.failedOutcome(StatusRejected, e)
	out.Chapters = report
	return out
}

/* ---------- chapter helpers ---------- */

// withChapterSkeleton copies the outline's own facts — id, position, title,
// summary, objectives, kind, minutes — alongside the content the model sent.
// The outline is what the learner has already been shown, so the chapter cannot
// rename itself in the middle of the course.
func withChapterSkeleton(chapter CourseChapter, outline OutlineChapter, prev CourseChapter) CourseChapter {
	out := chapter
	out.ID = outline.ID
	out.N = prev.N
	out.Title = outline.Title
	out.Summary = outline.Summary
	out.Objectives = outline.Objectives
	out.Kind = outline.Kind
	out.Minutes = outline.Minutes
	if out.Sections == nil {
		out.Sections = []CourseSection{}
	}
	if out.Exercises == nil {
		out.Exercises = []CourseExercise{}
	}
	return out
}

// gateFacts rebuilds the plan-wide facts validateCourseChapter needs: the
// exercise ids already used by verified chapters, and how many exercises the
// course has so far. A chapter that was refused contributes neither, because it
// is not in the plan.
func (b *courseBuild) gateFacts() (map[string]int, int) {
	seen := map[string]int{}
	total := 0
	for i := range b.plan.Chapters {
		if b.written[i].state != ChapterVerified {
			continue
		}
		for _, ex := range b.plan.Chapters[i].Exercises {
			total++
			seen[ex.ID] = total
		}
	}
	return seen, total
}

// chapterIDs lists the outline's chapter ids, for a repair that has to name
// them.
func (b *courseBuild) chapterIDs() []string {
	out := make([]string, 0, len(b.outline.Chapters))
	for _, ch := range b.outline.Chapters {
		out = append(out, ch.ID)
	}
	return out
}

// failingExercise names the exercise a stage error is about. The gate reports
// the field it refused as "chapters[2].exercises[0].id"; the engine reports the
// statement it could not run. Both are needed by a repair, and the first one
// names the exercise.
func failingExercise(verr *Error, ch *CourseChapter) (string, string) {
	if verr == nil {
		return "", ""
	}
	if idx := exerciseIndexFromField(verr.Detail); idx >= 0 && idx < len(ch.Exercises) {
		return ch.Exercises[idx].ID, strings.TrimSpace(verr.Message + " " + verr.Detail)
	}
	return "", strings.TrimSpace(verr.Message + " " + verr.Detail)
}

func exerciseIndexFromField(detail string) int {
	for _, part := range strings.Split(detail, ".exercises[") {
		if part == detail {
			continue
		}
		end := strings.IndexByte(part, ']')
		if end <= 0 {
			continue
		}
		n := 0
		digits := true
		for _, r := range part[:end] {
			if r < '0' || r > '9' {
				digits = false
				break
			}
			n = n*10 + int(r-'0')
		}
		if digits {
			return n
		}
	}
	return -1
}

// failedExerciseID is the exercise a chapter's verification stopped on: the
// last one it recorded, which is the one it refused. An empty string means the
// chapter was refused before any exercise ran.
func failedExerciseID(res ChapterResult) string {
	for i := len(res.Exercises) - 1; i >= 0; i-- {
		if res.Exercises[i].Status == "failed" {
			return res.Exercises[i].ID
		}
	}
	return ""
}

// failedExercises renders the exercises of a dropped chapter: the one the
// engine refused, carrying the engine's own error, and the ones the run never
// reached. A chapter is verified exercise by exercise and stops at the first
// refusal, so reporting an exercise that never ran as failed would be a claim
// about something the engine never did.
func failedExercises(ch *CourseChapter, verr *Error, failedID string) []ExerciseResult {
	out := make([]ExerciseResult, 0, len(ch.Exercises))
	for i := range ch.Exercises {
		res := ExerciseResult{
			ID:     ch.Exercises[i].ID,
			Title:  ch.Exercises[i].Title,
			Status: "not_run",
		}
		if ch.Exercises[i].ID == failedID {
			res.Status = "failed"
			res.Error = verr
		}
		out = append(out, res)
	}
	return out
}

// exerciseVerdicts is the per-exercise evidence inside one tool result: what
// the model needs to know about what it just wrote.
func exerciseVerdicts(res ChapterResult) []map[string]any {
	out := make([]map[string]any, 0, len(res.Exercises))
	for _, ex := range res.Exercises {
		out = append(out, map[string]any{
			"id":             ex.ID,
			"status":         ex.Status,
			"reference_rows": ex.ReferenceRows,
			"cross_rows":     ex.CrossCheckRows,
			"fixtures":       fmt.Sprintf("%d/%d", ex.FixturesPassed, ex.FixturesTotal),
		})
	}
	return out
}

// removeReport drops one chapter's entry from the report slice.
func removeReport(report []ChapterResult, id string) []ChapterResult {
	out := report[:0]
	for _, ch := range report {
		if ch.ID != id {
			out = append(out, ch)
		}
	}
	return out
}

/* ---------- storage ---------- */

// storeBuilding writes the course as it stands: the outline with the chapters
// that verified, under the "building" status. GET /courses/{id} answers with
// it, so a learner who reloads mid-run sees the chapters that already passed.
func (p *pipeline) storeBuilding(ctx context.Context, build *courseBuild) *Error {
	if p.svc.cfg.Depot == nil {
		return newError(CodeUnavailable, StageActivate, "There is nowhere to store the course.", "")
	}
	plan := build.plan
	plan.Chapters = buildingChapters(build)
	out := Outcome{
		Status:   StatusBuilding,
		Kind:     "course",
		Plan:     &plan,
		Chapters: build.report,
		Stages:   p.snapshotStages(),
		Attempts: []Attempt{},
	}
	if err := p.svc.cfg.Depot.SaveCourse(ctx, CourseRecord{
		Plan:      plan,
		Outcome:   out,
		Status:    StatusBuilding,
		CreatedAt: time.Now().UTC(),
		Request:   &build.request,
	}); err != nil {
		return newError(CodeUnavailable, StageActivate,
			"The course could not be stored while it was being written.", engine.RedactSecrets(err.Error()))
	}
	p.courseID = plan.ID
	p.notify()
	return nil
}

// buildingChapters is what a stored building course carries: every planned
// chapter, with content only where it verified. The sections are empty until
// then, which is exactly what "this chapter has not been written yet" looks
// like — the client renders the shape and fills it in.
func buildingChapters(build *courseBuild) []CourseChapter {
	out := make([]CourseChapter, 0, len(build.plan.Chapters))
	for i := range build.plan.Chapters {
		ch := build.plan.Chapters[i]
		if build.written[i].state != ChapterVerified {
			ch = CourseChapter{
				ID:         build.outline.Chapters[i].ID,
				N:          build.plan.Chapters[i].N,
				Title:      build.outline.Chapters[i].Title,
				Summary:    build.outline.Chapters[i].Summary,
				Kind:       build.outline.Chapters[i].Kind,
				Minutes:    build.outline.Chapters[i].Minutes,
				Objectives: build.outline.Chapters[i].Objectives,
				Sections:   []CourseSection{},
				Exercises:  []CourseExercise{},
			}
		}
		out = append(out, ch)
	}
	return out
}

/* ---------- tool results ---------- */

// toolResult renders one tool result. It is JSON rather than prose because both
// readers are machines: the model reads it to repair against, and a test reads
// it to check that the engine's message really travelled.
func toolResult(v map[string]any) string {
	v["tool_msg"] = true
	raw, err := json.Marshal(v)
	if err != nil {
		return `{"status":"internal_error","message":"the tool result could not be rendered"}`
	}
	return clipRunes(string(raw), maxToolResultChars)
}
