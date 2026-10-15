package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
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
func (p *pipeline) openScratch(ctx context.Context, plan *CoursePlan) (engine.Executor, func(), *Error) {
	if p.svc.cfg.Runner == nil {
		e := newError(CodeEnvironmentDown, StageScratchEnvironment, "No database is available to verify against.", "")
		p.mark(StageScratchEnvironment, StateFailed, e.Message)
		return nil, nil, e
	}
	start := time.Now()
	p.begin(StageScratchEnvironment)
	scratch := courseScratchName(plan)
	ex, release, err := p.svc.cfg.Runner.Scratch(ctx, scratch)
	if err != nil {
		e := newError(CodeEnvironmentDown, StageScratchEnvironment,
			"A throwaway environment could not be created.", engine.RedactSecrets(err.Error()))
		p.timed(StageScratchEnvironment, StateFailed, e.Detail, start)
		return nil, nil, e
	}
	p.evidence.ScratchDatabase = scratch
	if serr := p.prepare(ctx, ex, Artifact{Setup: plan.Setup}); serr != nil {
		release()
		p.timed(StageScratchEnvironment, StateFailed, serr.Detail, start)
		return nil, nil, serr
	}
	p.timed(StageScratchEnvironment, StateDone,
		fmt.Sprintf("%d setup statement(s) applied; dataset %q readable", len(plan.Setup.Statements), plan.Setup.Dataset), start)
	return ex, release, nil
}

// verifyChapterInEnv executes and cross-checks one chapter's exercises in the
// course's scratch environment, and writes each exercise's measured rows into
// the plan. The first failure ends the chapter: the report still names the
// exercise that failed, because "which one refused and what the engine said" is
// the fact a repair — or a drop — is decided on.
func (p *pipeline) verifyChapterInEnv(ctx context.Context, ex engine.Executor, plan *CoursePlan, ci int) (ChapterResult, *Error) {
	ch := &plan.Chapters[ci]
	scratch := p.evidence.ScratchDatabase
	cr := ChapterResult{
		ID:        ch.ID,
		Title:     ch.Title,
		Status:    ChapterVerified,
		Exercises: make([]ExerciseResult, 0, len(ch.Exercises)),
	}
	for ei := range ch.Exercises {
		art := plan.exerciseArtifact(*ch, ch.Exercises[ei])
		/* Each exercise gets its own evidence: the aggregate belongs to the
		   course, and one exercise's row count must not be read as another's. */
		p.evidence = Evidence{ScratchDatabase: scratch}
		p.lastExpected = nil
		exStart := time.Now()
		verr := p.verifyInEnv(ctx, ex, art)
		measured := p.evidence
		res := ExerciseResult{
			ID:             art.ID,
			Title:          art.Title,
			Status:         "verified",
			ReferenceRows:  measured.ReferenceRows,
			CrossCheckRows: measured.CrossCheckRows,
			FixturesPassed: measured.FixturesRejected,
			FixturesTotal:  measured.FixturesTotal,
			MS:             engine.RoundMS(time.Since(exStart)),
		}
		if verr != nil {
			res.Status = "failed"
			res.Error = verr
			cr.Exercises = append(cr.Exercises, res)
			return cr, verr
		}
		if p.lastExpected != nil {
			ch.Exercises[ei].Expected = &ExpectedResult{
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
func (p *pipeline) activateCourse(ctx context.Context, plan *CoursePlan, report []ChapterResult, build *courseBuild) Outcome {
	start := time.Now()
	p.begin(StageActivate)
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
	plan.Provenance = Provenance{
		Provider:         desc.Name,
		Model:            desc.Model,
		ProviderVersion:  desc.Version,
		PipelineVersion:  PipelineVersion,
		EvaluatorVersion: eval.EvaluatorVersion,
		DatasetVersion:   datasetVersion,
		CreatedAt:        time.Now().UTC(),
		Repairs:          build.repaired,
	}
	p.evidence.Repairs = build.repaired

	out := p.outcome(StatusActive, nil, nil)
	out.Plan = plan
	out.Chapters = report
	if p.svc.cfg.Depot == nil {
		e := newError(CodeUnavailable, StageActivate,
			"The course passed every check but there is nowhere to store it.", "")
		p.timed(StageActivate, StateFailed, e.Message, start)
		return p.failedOutcome(StatusFailed, e)
	}
	if err := p.svc.cfg.Depot.SaveCourse(ctx, CourseRecord{
		Plan:      *plan,
		Outcome:   out,
		Status:    StatusActive,
		CreatedAt: plan.Provenance.CreatedAt,
		Request:   &build.request,
	}); err != nil {
		e := newError(CodeUnavailable, StageActivate,
			"The course passed every check but could not be stored.", engine.RedactSecrets(err.Error()))
		p.timed(StageActivate, StateFailed, e.Message, start)
		return p.failedOutcome(StatusFailed, e)
	}
	p.timed(StageActivate, StateDone,
		fmt.Sprintf("stored as active: %d of %d chapters, %d verified exercises",
			build.verifiedCount(), len(build.outline.Chapters), build.exerciseCount()), start)
	return out
}

// courseSystemPrompt is the challenge system prompt plus the one fact that is
// different about a course: it is more than one query.
func courseSystemPrompt() string {
	return "You design SQL courses over a fixed PostgreSQL commerce dataset. Reply with one JSON object and no prose. Every solution must be a single read-only SELECT or WITH statement that runs against the described dataset."
}

// courseScratchName names the environment a course is verified in. It is
// derived from the course's own identity — the title and the first chapter's id
// — rather than from a chapter's content, because the environment is opened
// before any chapter exists and has to keep the same name for all of them.
func courseScratchName(plan *CoursePlan) string {
	seed := plan.Title
	if len(plan.Chapters) > 0 {
		seed += "\x00" + plan.Chapters[0].ID
	}
	return fmt.Sprintf("ai_course_%016x", hashPrompt(seed))
}
