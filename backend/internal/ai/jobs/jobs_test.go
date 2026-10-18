package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
	piAI "github.com/sky-valley/pi/ai"
)

/* The job registry, from the outside.
 *
 * The promise under test is narrow and load-bearing: a submission is answered
 * before the run is done, and from then on the id it returned describes the run
 * truthfully — its stages while it goes, its outcome when it ends, and never
 * another learner's run. The pipeline's own promises are proved beside it, in
 * backend/internal/ai; nothing here re-tests the gate.
 *
 * The database seam is real: every test boots the embedded PostgreSQL the
 * gateway ships (pgtest) and runs the pipeline over a writable executor. The
 * Depot is the shipped in-process store, because the registry is scoped by job
 * id and the store's records are the durable thing a job names. */

// toolSaveChapter and toolFinishCourse are the names the course agent
// advertises. They are literals here because the wire protocol is the thing a
// scripted provider answers, and a test that read the names from the gateway
// could not catch a rename.
const (
	toolSaveChapter  = "save_chapter"
	toolFinishCourse = "finish_course"
)

// bootPG starts a cluster and returns a writable executor. The pipeline applies
// setup statements and the evaluator runs learner SQL, so the executor must be
// writable; whether the SQL is real PostgreSQL does not depend on the role.
func bootPG(t *testing.T) engine.Executor {
	t.Helper()
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	ex, err := engine.NewPgxExecutorWithConfig(ctx, srv.DSN, engine.PgxConfig{MaxConns: 2, MinConns: 1, Writable: true})
	if err != nil {
		t.Fatalf("open the test executor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close() })
	return ex
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// gatedProvider stops every provider call until the test opens the gate, which
// is what makes "what does a poller see while the run is in flight" a fact
// rather than a race. The gate is released once, for every call.
type gatedProvider struct {
	base    provider.Provider
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedProvider(base provider.Provider) *gatedProvider {
	return &gatedProvider{base: base, entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (p *gatedProvider) Descriptor() provider.ProviderDescriptor { return p.base.Descriptor() }

// Course forwards the wrapped provider's chapter conversation: the gate
// exists to hold the outline call, and the loop runs against the same provider
// the gate wraps.
func (p *gatedProvider) Course() provider.CourseCapability {
	capability, ok := provider.CourseCapabilityOf(p.base)
	if !ok {
		return provider.CourseCapability{}
	}
	return capability
}

func (p *gatedProvider) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return provider.ProviderResponse{}, ctx.Err()
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

func (stuckProvider) Descriptor() provider.ProviderDescriptor {
	return provider.ProviderDescriptor{Name: "stuck", Model: "stuck-v1", Version: "1", Configured: true}
}

func (stuckProvider) Complete(ctx context.Context, _ provider.ProviderRequest) (provider.ProviderResponse, error) {
	<-ctx.Done()
	return provider.ProviderResponse{}, ctx.Err()
}

// jobService builds the service factory a registry needs. The course agent's
// model resolves from the raw config the same way it does at boot, and the
// conversation comes from the provider's own stream when it has one — the same
// wiring the ai package's testService uses.
func jobService(p provider.Provider, d schema.Depot, ex engine.Executor, limits ai.Limits) ServiceFor {
	return func(string) *ai.Service {
		return ai.NewService(ai.Config{
			Provider:       p,
			Depot:          d,
			Runner:         providedRunner{exec: ex},
			DatasetVersion: 1,
			Limits:         limits,
		})
	}
}

// providedRunner hands the pipeline an executor directly, so a test can run the
// pipeline over the cluster it booted. A nil executor is no environment.
type providedRunner struct{ exec engine.Executor }

func (r providedRunner) Scratch(context.Context, string) (engine.Executor, func(), error) {
	if r.exec == nil {
		return nil, nil, engine.ErrEnvironmentUnavailable
	}
	return r.exec, func() {}, nil
}

// waitTerminal polls until the job reports an outcome, and fails the test if it
// never does. It asserts on the job's own status, never on a timing field.
func waitTerminal(t *testing.T, jobs *Jobs, ownerID, id string) JobView {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		view, ok, err := jobs.Job(ownerID, id)
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
func stageByName(stages []schema.Stage, name string) (schema.Stage, bool) {
	for _, st := range stages {
		if st.Name == name {
			return st, true
		}
	}
	return schema.Stage{}, false
}

func courseRequest() schema.GenerationRequest {
	return schema.GenerationRequest{
		Topics:      []string{"joins", "aggregation", "window functions"},
		Difficulty:  schema.Beginner,
		Scenario:    "Count what each customer ordered, then rank the results.",
		Constraints: []string{"one statement per exercise"},
	}
}

/* ---------- the scripted course ---------- */

// brokenCrossCheck is a cross-check that keeps one row instead of the whole
// answer: it runs and returns rows, so only the cross-check stage — the
// evaluator's own agreement gate — can refuse it.
const brokenCrossCheck = `SELECT c.name AS customer_name, 1 AS order_count FROM "dataset"."customers" c ORDER BY customer_name`

/* ---------- the tests ---------- */

// TestAJobReportsProgressWhileItRuns is the reason the registry exists: a
// caller that could not wait any longer can see which stage the run is in,
// which stage it has passed, and which it has not reached, rather than a
// spinner that says nothing.
func TestAJobReportsProgressWhileItRuns(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := store.NewMemory()
	provider := newGatedProvider(ai.NewFakeProvider())
	jobs := NewJobs(jobService(provider, depot, ex, ai.Limits{}), ai.Limits{})

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if job.Status != JobRunning || job.ID == "" || job.Kind != "course" {
		t.Fatalf("a submission must be answered with a running job: %+v", job)
	}
	provider.waitEntered(t)

	view, ok, err := jobs.Job("learner-1", job.ID)
	if err != nil || !ok {
		t.Fatalf("poll the running job: ok = %v, err = %v", ok, err)
	}
	if view.Status != JobRunning {
		t.Fatalf("status = %q while the provider is still answering, want %q", view.Status, JobRunning)
	}
	if len(view.Stages) != len(ai.QueuedStages()) {
		t.Fatalf("a live view reports %d stages, want the pipeline's %d", len(view.Stages), len(ai.QueuedStages()))
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

	// The registry's promise is the live view's honesty. Which stage is where,
	// and whether the stored exercises carry measured rows, are the ai
	// pipeline's own promises, tested in its package.

	close(provider.release)
	done := waitTerminal(t, jobs, "learner-1", job.ID)

	if done.Status != schema.StatusActive {
		t.Fatalf("terminal status = %q, want %q (error = %v)", done.Status, schema.StatusActive, done.Error)
	}
	if done.FinishedAt == nil || done.FinishedAt.IsZero() {
		t.Fatal("a finished job must say when it finished")
	}
	if done.CourseID == "" {
		t.Fatalf("an active course job must name the record it stored: %+v", done)
	}

	// The id the job named is the id the depot holds.
	rec, found, err := depot.Course(ctx, done.CourseID)
	if err != nil || !found {
		t.Fatalf("the job's course_id must name a stored course: found = %v, err = %v", found, err)
	}
	if rec.Status != schema.StatusActive {
		t.Fatalf("stored record = %q, want the active plan %s", rec.Status, done.CourseID)
	}
}

// TestJobIsOwnerScoped is the privacy rule the record routes already follow: a
// job id guesses nothing, and another learner is told the job does not exist
// rather than that it is not theirs.
func TestJobIsOwnerScoped(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := store.NewMemory()
	provider := newGatedProvider(ai.NewFakeProvider())
	jobs := NewJobs(jobService(provider, depot, ex, ai.Limits{}), ai.Limits{})

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	t.Cleanup(func() { close(provider.release) })

	if _, ok, err := jobs.Job("learner-2", job.ID); ok || err != nil {
		t.Fatalf("another learner read the job: ok = %v, err = %v", ok, err)
	}
	if _, ok, err := jobs.Job("", job.ID); ok || err != nil {
		t.Fatalf("an anonymous caller read the job: ok = %v, err = %v", ok, err)
	}
	if _, ok, err := jobs.Job("learner-1", "gen-does-not-exist"); ok || err != nil {
		t.Fatalf("an unknown id answered: ok = %v, err = %v", ok, err)
	}
	if _, ok, err := jobs.Job("learner-1", job.ID); !ok || err != nil {
		t.Fatalf("the owner could not read their own job: ok = %v, err = %v", ok, err)
	}
}

// TestARunThatExceedsItsBudgetEnds is the bound: a provider that never answers
// must not leave a job running forever, because "still running" is what a
// client waits on and nothing would ever release it.
func TestARunThatExceedsItsBudgetEnds(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := store.NewMemory()
	limits := ai.Limits{Timeout: 250 * time.Millisecond}
	jobs := NewJobs(jobService(stuckProvider{}, depot, ex, limits), limits)

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	view := waitTerminal(t, jobs, "learner-1", job.ID)

	if view.Status != schema.StatusFailed {
		t.Fatalf("status = %q, want %q", view.Status, schema.StatusFailed)
	}
	if view.Error == nil || view.Error.Code != ai.CodeUnavailable {
		t.Fatalf("a stopped run must name the reason: %+v", view.Error)
	}
	if view.CourseID != "" || view.ChallengeID != "" {
		t.Fatalf("a run that stored nothing named a record: %+v", view)
	}
}

// TestDrainWaitsForTheLastRun is the shutdown contract: the store and the
// environment manager close as soon as run returns, and a detached job cannot
// be allowed to write its transcript after them. Drain must not return while a
// run is still executing, and the run it waited for must have reached a
// terminal state when it does.
func TestDrainWaitsForTheLastRun(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := store.NewMemory()
	provider := newGatedProvider(ai.NewFakeProvider())
	jobs := NewJobs(jobService(provider, depot, ex, ai.Limits{}), ai.Limits{})

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	provider.waitEntered(t)

	drained := make(chan error, 1)
	go func() { drained <- jobs.Drain(ctx) }()

	// The run is parked inside the provider call, so a Drain that returns now
	// would return while the run is still executing.
	select {
	case err := <-drained:
		t.Fatalf("Drain returned with a run still executing: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(provider.release)
	if err := <-drained; err != nil {
		t.Fatalf("drain: %v", err)
	}

	view, ok, err := jobs.Job("learner-1", job.ID)
	if err != nil || !ok {
		t.Fatalf("poll after drain: ok = %v, err = %v", ok, err)
	}
	if view.Status == JobRunning {
		t.Fatalf("Drain returned before the run reached a terminal state: %+v", view)
	}
	if view.FinishedAt == nil {
		t.Fatalf("the run Drain waited for reports no finish time: %+v", view)
	}
}

// TestACourseThatLosesAChapterStillReportsWhy: "which chapter refused and why"
// is the useful part of a partial course, and a poller must get it from the job
// rather than only from a log line. The chapters that verified are still
// stored, so the job names a record that answers.
func TestACourseThatLosesAChapterStillReportsWhy(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := store.NewMemory()
	jobs := NewJobs(jobService(&chapterBreaker{}, depot, ex, ai.Limits{}), ai.Limits{})

	job, err := jobs.Submit(ctx, Submission{OwnerID: "learner-1", Kind: "course", Request: courseRequest()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	view := waitTerminal(t, jobs, "learner-1", job.ID)

	if view.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %v)", view.Status, schema.StatusActive, view.Error)
	}
	if view.CourseID == "" {
		t.Fatal("an activated course must name the record it stored")
	}
	if view.ChaptersVerified < 1 {
		t.Fatalf("job progress = %d of %d verified, want at least one verified chapter", view.ChaptersVerified, view.ChaptersPlanned)
	}
	dropped, verified := 0, 0
	for _, ch := range view.Chapters {
		switch ch.Status {
		case ai.ChapterVerified:
			verified++
		case ai.ChapterDropped:
			dropped++
			if ch.Error == nil || ch.Error.Stage != schema.StageCrossCheck {
				t.Fatalf("the dropped chapter does not name the stage that refused it: %+v", ch.Error)
			}
		}
	}
	if verified == 0 || dropped == 0 {
		t.Fatalf("the report counts %d verified and %d dropped, want a partial course", verified, dropped)
	}
	if verified+dropped != len(view.Chapters) || view.ChaptersPlanned != len(view.Chapters) {
		t.Fatalf("the report has %d planned, %d entries, %d verified and %d dropped: the job's numbers must agree", view.ChaptersPlanned, len(view.Chapters), verified, dropped)
	}
}

// chapterBreaker serves the built-in provider's chapter conversation and
// corrupts every save_chapter after the first: the first chapter verifies, the
// rest cannot agree with their references, so the course keeps what ran and
// reports the engine's own verdict for what did not.
type chapterBreaker struct{}

func (chapterBreaker) Descriptor() provider.ProviderDescriptor {
	return ai.NewFakeProvider().Descriptor()
}

func (chapterBreaker) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	return ai.NewFakeProvider().Complete(ctx, req)
}

func (chapterBreaker) Course() provider.CourseCapability {
	capability, _ := provider.CourseCapabilityOf(ai.NewFakeProvider())
	inner := capability.StreamFn
	firstID := ""
	capability.StreamFn = func(ctx context.Context, model *piAI.Model, req piAI.TranscriptContext, opts *piAI.SimpleStreamOptions) *piAI.AssistantMessageEventStream {
		src := inner(ctx, model, req, opts)
		out := piAI.NewAssistantMessageEventStream()
		go func() {
			defer out.End()
			for ev, ok := src.Next(); ok; ev, ok = src.Next() {
				if ev.Type == piAI.EventDone && ev.Message != nil {
					for i, c := range ev.Message.Content {
						tc, ok := c.(piAI.ToolCall)
						if !ok || tc.Name != "save_chapter" {
							continue
						}
						raw, merr := json.Marshal(tc.Arguments)
						if merr != nil {
							continue
						}
						var ch schema.CourseChapter
						if json.Unmarshal(raw, &ch) != nil {
							continue
						}
						if firstID == "" {
							firstID = ch.ID
							continue
						}
						for j := range ch.Exercises {
							ch.Exercises[j].CrossCheckSolution = brokenCrossCheck
						}
						fixed, merr := json.Marshal(ch)
						if merr != nil {
							continue
						}
						var args map[string]any
						if json.Unmarshal(fixed, &args) == nil {
							ev.Message.Content[i] = piAI.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: args}
						}
					}
				}
				out.Push(ev)
			}
		}()
		return out
	}
	return capability
}
