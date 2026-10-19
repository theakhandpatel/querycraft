package ai

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* course_build.go owns the course under construction: the plan the gateway
 * stores, the state of every planned chapter, and the derivations a poller
 * reads.
 *
 * One courseBuild serves one run. course_agent.go writes it chapter by chapter
 * and reads the running totals back; course_pipeline.go activates the plan it
 * produced. Everything here is a pure function of the build, which is what lets
 * the live view and the stored record agree.
 */

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

// Exercise statuses in the per-exercise report, alongside the chapter statuses.
const (
	ExerciseVerified = "verified"
	ExerciseFailed   = "failed"
	ExerciseNotRun   = "not_run"
)

// courseBuild is the course as it is being written. It owns the plan the
// gateway stores, the state of every planned chapter, and the running totals a
// poller reads. One build serves one run; it is not shared and needs no lock,
// because only the run's own goroutine touches it and every snapshot handed out
// is a copy.
type courseBuild struct {
	request schema.GenerationRequest
	outline CourseOutline
	plan    schema.CoursePlan
	// written is indexed by outline position. A chapter that has not been
	// handed in yet has the zero write: no state, and zero writes.
	written []chapterWrite
	// report carries the resolved chapters in outline order, which is the shape
	// the outcome and the job view publish.
	report   []schema.ChapterResult
	repaired int
	finished bool
	// current is the id of the chapter the run started verifying most recently,
	// or "" before the first one. It is the fact a poller reads as the chapter
	// in flight: the stages cannot name it, so it travels here.
	current string
	// fatal is set when the run cannot continue for a reason no repair fixes:
	// the store failed. The loop stops and the run reports it instead of
	// pretending the course is still being written.
	fatal *schema.Error
}

type chapterWrite struct {
	writes int
	state  string
	// attempted and lastErr carry the most recent refusal, so a chapter that
	// was written and refused is reported as refused rather than as one the run
	// never reached.
	attempted bool
	lastErr   *schema.Error
}

// newCourseBuild turns a validated outline into the plan skeleton the store
// keeps while the chapters are written. The chapters exist from the first
// moment, so the learner sees the course's shape before its content, and they
// carry no sections until one passes.
func newCourseBuild(req schema.GenerationRequest, outline CourseOutline) *courseBuild {
	plan := schema.CoursePlan{
		ID:         courseID(outline),
		Version:    1,
		Title:      outline.Title,
		Subtitle:   outline.Subtitle,
		Level:      outline.Level,
		Dialect:    "postgresql",
		Objectives: outline.Objectives,
		Setup:      outline.Setup,
		Chapters:   make([]schema.CourseChapter, 0, len(outline.Chapters)),
	}
	for i, ch := range outline.Chapters {
		n := ch.N
		if n <= 0 {
			n = i + 1
		}
		plan.Chapters = append(plan.Chapters, ch.chapter(n, schema.CourseChapter{}))
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
func newCourseBuildFromRecord(rec schema.CourseRecord) *courseBuild {
	req := requestFromRecord(rec)
	outline := outlineFromRecord(rec)
	plan := rec.Plan
	if plan.Chapters == nil {
		plan.Chapters = []schema.CourseChapter{}
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
	b.report = append([]schema.ChapterResult(nil), rec.Outcome.Chapters...)
	b.repaired = rec.Outcome.Evidence.Repairs
	return b
}

// requestFromRecord recovers the generation request a stored course was built
// from. Every writer stores it: the outline save, the interrupted mark and the
// active save all set CourseRecord.Request before the row exists, so a stored
// course without one cannot be produced.
func requestFromRecord(rec schema.CourseRecord) schema.GenerationRequest {
	req := *rec.Request
	req.Kind = "course"
	return req
}

// outlineFromRecord recovers the outline the chapter phase is written against.
// Everything the outline gate reads is already in the stored plan's chapters
// (id, position, title, summary, kind, minutes, objectives). The one field the
// plan does not keep is how many exercises each chapter planned. That is
// reconstructed from the request's budget and, where a chapter already carries
// content, reported as the count it actually holds.
func outlineFromRecord(rec schema.CourseRecord) CourseOutline {
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
// the stored chapters, at least one each, the coupling validateCourseOutline
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
func (b *courseBuild) verifiedChapters() []schema.CourseChapter {
	out := make([]schema.CourseChapter, 0, len(b.outline.Chapters))
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
func (b *courseBuild) record(res schema.ChapterResult) {
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
	b.report = slices.Clone(b.report)
	slices.SortStableFunc(b.report, func(a, c schema.ChapterResult) int {
		return cmp.Compare(rank[a.ID], rank[c.ID])
	})
}

// fullReport is one entry per planned chapter, in order: what verified, what
// was dropped, and what the run never reached. A report that omitted the last
// kind would let "not checked" read as "fine".
func (b *courseBuild) fullReport() []schema.ChapterResult {
	out := make([]schema.ChapterResult, 0, len(b.outline.Chapters))
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
			out = append(out, schema.ChapterResult{ID: och.ID, Title: och.Title, Status: state, Exercises: []schema.ExerciseResult{}, Error: b.written[i].lastErr})
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

// courseHasWrittenChapter reports whether a stored plan carries any chapter
// with content, which is the mark a verified chapter leaves in the row.
func courseHasWrittenChapter(plan schema.CoursePlan) bool {
	for _, ch := range plan.Chapters {
		if len(ch.Sections) > 0 || len(ch.Exercises) > 0 {
			return true
		}
	}
	return false
}

// chapter overlays the outline's own facts (id, title, summary, objectives,
// kind, minutes) onto a base chapter at position n. The outline is what the
// learner has already been shown, so a chapter cannot rename itself in the
// middle of the course. newCourseBuild and buildingChapters pass a zero base and
// keep no content; saveChapterTool passes the chapter the model just sent, so
// its sections and exercises survive the overlay.
func (o OutlineChapter) chapter(n int, prev schema.CourseChapter) schema.CourseChapter {
	out := prev
	out.ID = o.ID
	out.N = n
	out.Title = o.Title
	out.Summary = o.Summary
	out.Objectives = o.Objectives
	out.Kind = o.Kind
	out.Minutes = o.Minutes
	if out.Sections == nil {
		out.Sections = []schema.CourseSection{}
	}
	if out.Exercises == nil {
		out.Exercises = []schema.CourseExercise{}
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
