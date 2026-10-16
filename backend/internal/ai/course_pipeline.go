package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The course pipeline's shared pieces.
 *
 * M8 writes a course one chapter at a time (course_agent.go), but the stages a
 * chapter passes are the M7 stages unchanged: one throwaway environment for the
 * whole course, the plan's setup applied once, and every exercise turned into
 * an Artifact and checked by verifyInEnv — the same reference, cross-check and
 * adversarial-fixture rules a challenge passes. Nothing here weakens a rule to
 * make a chapter fit; what changed is only when the work happens.
 */

// openScratch opens the one throwaway environment a course is verified in and
// applies the plan's setup to it, once. Every exercise of every chapter runs
// here, so an exercise that ran its own setup would silently change what the
// next one returns; that is why setup belongs to the plan and not to a chapter.
func (p *pipeline) openScratch(ctx context.Context, plan *schema.CoursePlan) (engine.Executor, func(), *schema.Error) {
	if p.svc.cfg.Runner == nil {
		e := NewError(CodeEnvironmentDown, schema.StageScratchEnvironment, "No database is available to verify against.", "")
		p.mark(schema.StageScratchEnvironment, schema.StateFailed, e.Message)
		return nil, nil, e
	}
	start := time.Now()
	p.begin(schema.StageScratchEnvironment)
	scratch := courseScratchName(plan)
	ex, release, err := p.svc.cfg.Runner.Scratch(ctx, scratch)
	if err != nil {
		e := NewError(CodeEnvironmentDown, schema.StageScratchEnvironment,
			"A throwaway environment could not be created.", engine.RedactSecrets(err.Error()))
		p.timed(schema.StageScratchEnvironment, schema.StateFailed, e.Detail, start)
		return nil, nil, e
	}
	p.evidence.ScratchDatabase = scratch
	if serr := p.prepare(ctx, ex, schema.Artifact{Setup: plan.Setup}); serr != nil {
		release()
		p.timed(schema.StageScratchEnvironment, schema.StateFailed, serr.Detail, start)
		return nil, nil, serr
	}
	p.timed(schema.StageScratchEnvironment, schema.StateDone,
		fmt.Sprintf("%d setup statement(s) applied; dataset %q readable", len(plan.Setup.Statements), plan.Setup.Dataset), start)
	return ex, release, nil
}

// verifyChapterInEnv executes and cross-checks one chapter's exercises in the
// course's scratch environment, and writes each exercise's measured rows into
// the plan. The first failure ends the chapter: the report still names the
// exercise that failed, because "which one refused and what the engine said" is
// the fact a repair — or a drop — is decided on.
func (p *pipeline) verifyChapterInEnv(ctx context.Context, ex engine.Executor, plan *schema.CoursePlan, ci int) (schema.ChapterResult, *schema.Error) {
	ch := &plan.Chapters[ci]
	scratch := p.evidence.ScratchDatabase
	cr := schema.ChapterResult{
		ID:        ch.ID,
		Title:     ch.Title,
		Status:    ChapterVerified,
		Exercises: make([]schema.ExerciseResult, 0, len(ch.Exercises)),
	}
	for ei := range ch.Exercises {
		art := exerciseArtifact(plan, *ch, ch.Exercises[ei])
		/* Each exercise gets its own evidence: the aggregate belongs to the
		   course, and one exercise's row count must not be read as another's. */
		p.evidence = schema.Evidence{ScratchDatabase: scratch}
		p.lastExpected = nil
		exStart := time.Now()
		verr := p.verifyInEnv(ctx, ex, art)
		measured := p.evidence
		res := schema.ExerciseResult{
			ID:               art.ID,
			Title:            art.Title,
			Status:           ExerciseVerified,
			ReferenceRows:    measured.ReferenceRows,
			CrossCheckRows:   measured.CrossCheckRows,
			FixturesRejected: measured.FixturesRejected,
			FixturesTotal:    measured.FixturesTotal,
			MS:               engine.RoundMS(time.Since(exStart)),
		}
		if verr != nil {
			res.Status = ExerciseFailed
			res.Error = verr
			cr.Exercises = append(cr.Exercises, res)
			return cr, verr
		}
		if p.lastExpected != nil {
			ch.Exercises[ei].Expected = &schema.ExpectedResult{
				Columns: append([]string(nil), p.lastExpected.Columns...),
				Rows:    p.lastExpected.Rows,
			}
		}
		cr.Exercises = append(cr.Exercises, res)
	}
	return cr, nil
}

// activateCourse stamps provenance, stores the plan as active and returns it.
// Storage failure is reported as a failure rather than as a success with a
// warning: a course the gateway cannot keep is a course the learner will lose.
func (p *pipeline) activateCourse(ctx context.Context, plan *schema.CoursePlan, report []schema.ChapterResult, build *courseBuild) schema.Outcome {
	start := time.Now()
	p.begin(schema.StageActivate)
	desc := p.svc.cfg.Provider.Descriptor()
	plan.Chapters = build.verifiedChapters()
	if plan.ID == "" {
		plan.ID = courseID(build.outline)
	}
	if plan.Version < 1 {
		plan.Version = 1
	}
	if plan.Dialect == "" {
		plan.Dialect = "postgresql"
	}
	datasetVersion := p.svc.cfg.DatasetVersion
	if datasetVersion < 1 {
		datasetVersion = plan.Setup.SchemaVersion
	}
	plan.Provenance = provenance(desc, datasetVersion, build.repaired)
	p.evidence.Repairs = build.repaired

	out := p.outcome(schema.StatusActive, nil, nil)
	out.Plan = plan
	out.Chapters = report
	if p.svc.cfg.Depot == nil {
		e := NewError(CodeUnavailable, schema.StageActivate,
			"The course passed every check but there is nowhere to store it.", "")
		p.timed(schema.StageActivate, schema.StateFailed, e.Message, start)
		return p.failedOutcome(schema.StatusFailed, e)
	}
	if err := p.svc.cfg.Depot.SaveCourse(ctx, schema.CourseRecord{
		Plan:      *plan,
		Outcome:   out,
		Status:    schema.StatusActive,
		CreatedAt: plan.Provenance.CreatedAt,
		Request:   &build.request,
	}); err != nil {
		e := NewError(CodeUnavailable, schema.StageActivate,
			"The course passed every check but could not be stored.", engine.RedactSecrets(err.Error()))
		p.timed(schema.StageActivate, schema.StateFailed, e.Message, start)
		return p.failedOutcome(schema.StatusFailed, e)
	}
	p.timed(schema.StageActivate, schema.StateDone,
		fmt.Sprintf("stored as active: %d of %d chapters, %d verified exercises",
			build.verifiedCount(), len(build.outline.Chapters), build.exerciseCount()), start)
	return out
}

// courseSystemPrompt is the challenge system prompt plus the one fact that is
// different about a course: it is more than one query.
//
// The last sentences are a writing rule, not a grading rule. A chapter body, the
// course objectives and the chapter summaries are the learner's reading
// material, and a model left to itself writes them in the register of a product
// page: an opening line that restates the heading, three items where one would
// do, and a closing line that promises more. The shipped chapters
// (web/src/data/content/*.md) do not read that way. One sentence here reaches
// every chapter of every generated course, because the whole-plan path and the
// one-chapter-at-a-time agent path share this prompt.
func courseSystemPrompt() string {
	return "You design SQL courses over a fixed PostgreSQL commerce dataset. Reply with one JSON object and no prose. Every solution must be a single read-only SELECT or WITH statement that runs against the described dataset. The strings inside that object are what the learner reads, so write them for a beginner: short sentences, active voice, present tense, one idea each. Name the table and the column. Do not open a section with a slogan, and do not restate its heading."
}

// courseScratchName names the environment a course is verified in. It is
// derived from the course's own identity — the title and the first chapter's id
// — rather than from a chapter's content, because the environment is opened
// before any chapter exists and has to keep the same name for all of them.
func courseScratchName(plan *schema.CoursePlan) string {
	seed := plan.Title
	if len(plan.Chapters) > 0 {
		seed += "\x00" + plan.Chapters[0].ID
	}
	return fmt.Sprintf("ai_course_%016x", hashPrompt(seed))
}
