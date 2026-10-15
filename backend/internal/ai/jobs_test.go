package ai

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* The job registry, from the outside.
 *
 * The promise under test is narrow and load-bearing: a submission is answered
 * before the run is done, and from then on the id it returned describes the run
 * truthfully — its stages while it goes, its outcome when it ends, and never
 * another learner's run. The pipeline's own promises are proved in
 * course_test.go and pipeline_test.go; nothing here re-tests the gate. */

// gatedProvider stops every provider call until the test opens the gate, which
// is what makes "what does a poller see while the run is in flight" a fact
// rather than a race. The gate is released once, for every call.
type gatedProvider struct {
	base    Provider
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedProvider(base Provider) *gatedProvider {
	return &gatedProvider{base: base, entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (p *gatedProvider) Descriptor() ProviderDescriptor { return p.base.Descriptor() }

func (p *gatedProvider) Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return ProviderResponse{}, ctx.Err()
	}
	return p.base.Complete(ctx, req)
}

// waitEntered blocks until the provider has been asked for something, so a test
// can be certain the run is inside the provider call.
func (p *gatedProvider) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-p.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the provider was never called")
	}
}

// stuckProvider never answers until its context ends. It is a provider that
// hangs, which is what a run budget exists for.
type stuckProvider struct{}

func (stuckProvider) Descriptor() ProviderDescriptor {
	return ProviderDescriptor{Name: "stuck", Model: "stuck-v1", Version: "1", Configured: true}
}

func (stuckProvider) Complete(ctx context.Context, _ ProviderRequest) (ProviderResponse, error) {
	<-ctx.Done()
	return ProviderResponse{}, ctx.Err()
}

// jobService builds the service factory a registry needs. The depot is the
// in-process one: the record's owner is the transport's business (see
// httpapi's ownerScopedDepot) and the registry scopes jobs, not records.
func jobService(p Provider, d Depot, ex engine.Executor, limits Limits) ServiceFor {
	return func(string) *Service {
		return NewService(Config{
			Provider:       p,
			Depot:          d,
			Runner:         ProvidedRunner{Exec: ex},
			DatasetVersion: 1,
			Limits:         limits,
		})
	}
}

// waitTerminal polls until the job reports an outcome, and fails the test if it
// never does. It asserts on the job's own status, never on a timing field.
func waitTerminal(t *testing.T, jobs *Jobs, ownerID, id string) JobView {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		view, ok, err := jobs.Job(context.Background(), ownerID, id)
		if err != nil {
			t.Fatalf("poll job: %v", err)
		}
		if !ok {
			t.Fatalf("job %s disappeared while it was still running", id)
		}
		if view.Status != JobRunning {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is still running after the test's own deadline", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stageByName reads one stage out of a reported list.
func stageByName(stages []Stage, name string) (Stage, bool) {
	for _, st := range stages {
		if st.Name == name {
			return st, true
		}
	}
	return Stage{}, false
}

func courseRequest() GenerationRequest {
	return GenerationRequest{
		Topics:      []string{"joins", "aggregation", "window functions"},
		Difficulty:  Beginner,
		Scenario:    "Count what each customer ordered, then rank the results.",
		Constraints: []string{"one statement per exercise"},
	}
}

// TestAJobReportsProgressWhileItRuns is the reason the registry exists: a
// caller that could not wait any longer can see which stage the run is in,
// which stage it has passed, and which it has not reached, rather than a
// spinner that says nothing.
func TestAJobReportsProgressWhileItRuns(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	provider := newGatedProvider(NewFakeProvider())
	jobs := NewJobs(jobService(provider, depot, ex, Limits{}), Limits{})

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if job.Status != JobRunning || job.ID == "" || job.Kind != "course" {
		t.Fatalf("a submission must be answered with a running job: %+v", job)
	}
	provider.waitEntered(t)

	view, ok, err := jobs.Job(ctx, "learner-1", job.ID)
	if err != nil || !ok {
		t.Fatalf("poll the running job: ok = %v, err = %v", ok, err)
	}
	if view.Status != JobRunning {
		t.Fatalf("status = %q while the provider is still answering, want %q", view.Status, JobRunning)
	}
	if len(view.Stages) != len(stageOrder) {
		t.Fatalf("a live view reports %d stages, want the pipeline's %d", len(view.Stages), len(stageOrder))
	}
	if view.Error != nil {
		t.Fatalf("a running job must not carry an error: %v", view.Error)
	}
	if view.CourseID != "" || view.ChallengeID != "" {
		t.Fatalf("a running job names no record: %+v", view)
	}
	if len(view.Attempts) != 0 {
		t.Fatalf("attempts = %d before anything failed, want 0", len(view.Attempts))
	}

	// The run is stalled inside the provider call, so this picture is exact:
	// the request was validated, the plan is being written, and nothing has
	// been executed yet.
	validated, _ := stageByName(view.Stages, StageValidateRequest)
	if validated.State != StateDone {
		t.Fatalf("validate_request = %q, want %q", validated.State, StateDone)
	}
	bundle, _ := stageByName(view.Stages, StageRequestBundle)
	if bundle.State != StateRunning {
		t.Fatalf("request_bundle = %q while the provider is answering, want %q", bundle.State, StateRunning)
	}
	scratch, _ := stageByName(view.Stages, StageScratchEnvironment)
	if scratch.State != StateQueued {
		t.Fatalf("scratch_environment = %q before the plan exists, want %q", scratch.State, StateQueued)
	}

	close(provider.release)
	done := waitTerminal(t, jobs, "learner-1", job.ID)

	if done.Status != StatusActive {
		t.Fatalf("terminal status = %q, want %q (error = %v)", done.Status, StatusActive, done.Error)
	}
	if done.FinishedAt == nil || done.FinishedAt.IsZero() {
		t.Fatal("a finished job must say when it finished")
	}
	if done.CourseID == "" {
		t.Fatalf("an active course job must name the record it stored: %+v", done)
	}
	for _, st := range done.Stages {
		switch st.State {
		case StateDone, StateFailed, StateSkipped:
		default:
			t.Fatalf("stage %q is %q in a finished job; queued and running are live-only states", st.Name, st.State)
		}
		if st.Detail == "" {
			t.Fatalf("stage %q reports no detail", st.Name)
		}
	}

	// The id the job named is the id the depot holds, and the stored plan is
	// the one the pipeline verified rather than a second guess.
	rec, found, err := depot.Course(ctx, done.CourseID)
	if err != nil || !found {
		t.Fatalf("the job's course_id must name a stored course: found = %v, err = %v", found, err)
	}
	if rec.Status != StatusActive || rec.Plan.ID != done.CourseID {
		t.Fatalf("stored record = %+v, want the active plan %s", rec.Status, done.CourseID)
	}
	for ci, ch := range rec.Plan.Chapters {
		for ei, exercise := range ch.Exercises {
			if exercise.Expected == nil || len(exercise.Expected.Rows) == 0 {
				t.Fatalf("chapter %d exercise %d reached the store with no measured rows", ci, ei)
			}
		}
	}
}

// TestJobIsOwnerScoped is the privacy rule the record routes already follow: a
// job id guesses nothing, and another learner is told the job does not exist
// rather than that it is not theirs.
func TestJobIsOwnerScoped(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	provider := newGatedProvider(NewFakeProvider())
	jobs := NewJobs(jobService(provider, depot, ex, Limits{}), Limits{})

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	t.Cleanup(func() { close(provider.release) })

	if _, ok, err := jobs.Job(ctx, "learner-2", job.ID); ok || err != nil {
		t.Fatalf("another learner read the job: ok = %v, err = %v", ok, err)
	}
	if _, ok, err := jobs.Job(ctx, "", job.ID); ok || err != nil {
		t.Fatalf("an anonymous caller read the job: ok = %v, err = %v", ok, err)
	}
	if _, ok, err := jobs.Job(ctx, "learner-1", "gen-does-not-exist"); ok || err != nil {
		t.Fatalf("an unknown id answered: ok = %v, err = %v", ok, err)
	}
	if _, ok, err := jobs.Job(ctx, "learner-1", job.ID); !ok || err != nil {
		t.Fatalf("the owner could not read their own job: ok = %v, err = %v", ok, err)
	}
}

// TestARunThatExceedsItsBudgetEnds is the bound: a provider that never answers
// must not leave a job running forever, because "still running" is what a
// client waits on and nothing would ever release it.
func TestARunThatExceedsItsBudgetEnds(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	limits := Limits{Timeout: 250 * time.Millisecond}
	jobs := NewJobs(jobService(stuckProvider{}, depot, ex, limits), limits)

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	view := waitTerminal(t, jobs, "learner-1", job.ID)

	if view.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", view.Status, StatusFailed)
	}
	if view.Error == nil || view.Error.Code != CodeUnavailable {
		t.Fatalf("a stopped run must name the reason: %+v", view.Error)
	}
	if view.CourseID != "" || view.ChallengeID != "" {
		t.Fatalf("a run that stored nothing named a record: %+v", view)
	}
	if n := depot.courseCount(); n != 0 {
		t.Fatalf("a failed run stored %d course(s)", n)
	}
}

// TestACourseThatLosesAChapterStillReportsWhy: "which chapter refused and why"
// is the useful part of a partial course, and a poller must get it from the job
// rather than only from a log line. The chapters that verified are still
// stored, so the job names a record that answers.
func TestACourseThatLosesAChapterStillReportsWhy(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()
	broken := plan.Chapters[1]
	// A cross-check that cannot agree with its reference: the exercise fails at
	// the cross-check stage, which is where a chapter is refused for its
	// content rather than for its shape.
	broken.Exercises[0].CrossCheckSolution = brokenCrossCheck
	agent := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps: []ProviderResponse{
			saveChapterCall(t, plan.Chapters[0]),
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			finishCourseCall(),
		},
	}
	jobs := NewJobs(jobService(agent, depot, ex, Limits{}), Limits{})

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	view := waitTerminal(t, jobs, "learner-1", job.ID)

	if view.Status != StatusActive {
		t.Fatalf("status = %q, want %q (error = %v)", view.Status, StatusActive, view.Error)
	}
	if view.CourseID == "" {
		t.Fatal("an activated course must name the record it stored")
	}
	if view.ChaptersPlanned != len(plan.Chapters) || view.ChaptersVerified != 1 {
		t.Fatalf("job progress = %d of %d verified, want 1 of %d", view.ChaptersVerified, view.ChaptersPlanned, len(plan.Chapters))
	}
	if len(view.Chapters) != 2 {
		t.Fatalf("the job report has %d chapters, want one entry per planned chapter", len(view.Chapters))
	}
	dropped, verified := 0, 0
	for _, ch := range view.Chapters {
		switch ch.Status {
		case ChapterVerified:
			verified++
		case ChapterDropped:
			dropped++
			if ch.Error == nil || ch.Error.Stage != StageCrossCheck {
				t.Fatalf("the dropped chapter does not name the stage that refused it: %+v", ch.Error)
			}
		}
	}
	if verified != 1 || dropped != 1 {
		t.Fatalf("the report counts %d verified and %d dropped, want 1 and 1", verified, dropped)
	}
	stored, found, err := depot.Course(ctx, view.CourseID)
	if err != nil || !found {
		t.Fatalf("the job named a course that is not stored: found = %v err = %v", found, err)
	}
	if len(stored.Plan.Chapters) != 1 {
		t.Fatalf("the stored course has %d chapters, want only the one that verified", len(stored.Plan.Chapters))
	}
	if got := len(agent.agentCalls()); got != 5 {
		t.Fatalf("tool-calling turns = %d, want 5 (one write, three refusals, the close)", got)
	}
}
