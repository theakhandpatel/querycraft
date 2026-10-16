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

// courseCount is how many courses the store holds.
func courseCount(t *testing.T, m *store.Memory) int {
	t.Helper()
	recs, err := m.Courses(context.Background())
	if err != nil {
		t.Fatalf("list courses: %v", err)
	}
	return len(recs)
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

// jobService builds the service factory a registry needs.
func jobService(p provider.Provider, d schema.Depot, ex engine.Executor, limits ai.Limits) ServiceFor {
	return func(string) *ai.Service {
		return ai.NewService(ai.Config{
			Provider:       p,
			Depot:          d,
			Runner:         ai.ProvidedRunner{Exec: ex},
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

// coursePlanFixture is a valid two-chapter course over the commerce dataset: it
// is the course the scripted agent below writes, so the registry can be given a
// real course with one chapter that verifies and one that is refused.
func coursePlanFixture() schema.CoursePlan {
	return schema.CoursePlan{
		Title:      "Trusting a query",
		Subtitle:   "Two chapters on reading the answer instead of the SQL.",
		Level:      "intermediate",
		Dialect:    "postgresql",
		Objectives: []string{"Answer a question with one statement.", "Check the answer against the question."},
		Setup:      schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1, Statements: []string{}},
		Chapters: []schema.CourseChapter{
			chapterFixture("gen.one", 1, "Customers and how many orders they placed",
				"How many orders did each customer place? Return one row per customer who has ordered, with the customer name and the order count, highest count first, breaking ties by name.",
				`SELECT c.name AS customer_name, count(o.id) AS order_count
FROM "dataset"."customers" c
JOIN "dataset"."orders" o ON o.customer_id = c.id
GROUP BY c.name
ORDER BY order_count DESC, customer_name ASC`,
				`SELECT c.name AS customer_name,
       (SELECT count(*) FROM "dataset"."orders" o WHERE o.customer_id = c.id) AS order_count
FROM "dataset"."customers" c
WHERE EXISTS (SELECT 1 FROM "dataset"."orders" o WHERE o.customer_id = c.id)
ORDER BY order_count DESC, customer_name ASC`),
			chapterFixture("gen.two", 2, "Products per category and their average price",
				"For each product category, return the category, how many products it contains, and the average price rounded to two decimals, highest average price first.",
				`SELECT p.category AS category, count(*) AS product_count, round(avg(p.price), 2) AS avg_price
FROM "dataset"."products" p
GROUP BY p.category
ORDER BY avg_price DESC, category ASC`,
				`SELECT DISTINCT p.category AS category,
       count(*) OVER (PARTITION BY p.category) AS product_count,
       round(avg(p.price) OVER (PARTITION BY p.category), 2) AS avg_price
FROM "dataset"."products" p
ORDER BY avg_price DESC, category ASC`),
		},
	}
}

// chapterFixture is one chapter with one exercise, carrying the SQL the engine
// really runs.
func chapterFixture(id string, n int, title, prompt, reference, crossCheck string) schema.CourseChapter {
	return schema.CourseChapter{
		ID:         id,
		N:          n,
		Title:      title,
		Summary:    prompt,
		Kind:       "Practice",
		Minutes:    10,
		Objectives: []string{"Read the commerce schema.", "Answer the question with one statement."},
		Sections: []schema.CourseSection{{
			Title: "The shape of the question",
			Body:  "Read the question twice and decide the grain of the answer: one row per what? Every clause after that exists to produce exactly those rows, and nothing else.\n\n" + prompt,
		}},
		Exercises: []schema.CourseExercise{{
			ID:                 id + ".1",
			Title:              title,
			Prompt:             prompt,
			Difficulty:         schema.Intermediate,
			Type:               "write",
			Objectives:         []string{"Read the commerce schema.", "Answer the question with one statement."},
			Hints:              []string{"Start from the table that owns the fact you are reporting on."},
			ReferenceSolution:  reference,
			CrossCheckSolution: crossCheck,
			ResultContract: schema.ResultContract{
				Columns:          exerciseColumns(id),
				ColumnOrder:      "exact",
				RowOrder:         "exact",
				Duplicates:       "preserved",
				NumericTolerance: 0,
			},
			Checks: []schema.Check{},
		}},
	}
}

// exerciseColumns names the columns each fixture exercise returns, in order.
func exerciseColumns(id string) []string {
	if id == "gen.one" {
		return []string{"customer_name", "order_count"}
	}
	return []string{"category", "product_count", "avg_price"}
}

// brokenCrossCheck is a cross-check that keeps one row instead of the whole
// answer, so it cannot agree with its reference under the contract.
const brokenCrossCheck = `SELECT c.name AS customer_name, 1 AS order_count FROM "dataset"."customers" c ORDER BY customer_name`

// scriptedAgent answers the outline call and then replays a queue of scripted
// turns, one per tool-calling request. It records every request, which is how a
// test reads the conversation the gateway actually had.
type scriptedAgent struct {
	outline string
	steps   []provider.ProviderResponse
	calls   []provider.ProviderRequest
}

func (a *scriptedAgent) Descriptor() provider.ProviderDescriptor {
	return provider.ProviderDescriptor{Name: "scripted", Model: "scripted-agent-v1", Version: "1", Configured: true}
}

func (a *scriptedAgent) Complete(_ context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	a.calls = append(a.calls, req)
	if len(req.Tools) == 0 {
		return provider.ProviderResponse{Text: a.outline, Provider: "scripted", Model: "scripted-agent-v1", FinishReason: "stop"}, nil
	}
	if len(a.steps) == 0 {
		return provider.ProviderResponse{Text: "I am done.", Provider: "scripted", Model: "scripted-agent-v1", FinishReason: "stop"}, nil
	}
	step := a.steps[0]
	a.steps = a.steps[1:]
	step.Provider, step.Model = "scripted", "scripted-agent-v1"
	return step, nil
}

// agentCalls is every tool-calling request the provider saw, in order.
func (a *scriptedAgent) agentCalls() []provider.ProviderRequest {
	var out []provider.ProviderRequest
	for _, req := range a.calls {
		if len(req.Tools) > 0 {
			out = append(out, req)
		}
	}
	return out
}

// saveChapterCall scripts one save_chapter turn.
func saveChapterCall(t *testing.T, ch schema.CourseChapter) provider.ProviderResponse {
	t.Helper()
	args, err := json.Marshal(ch)
	if err != nil {
		t.Fatalf("encode chapter: %v", err)
	}
	return provider.ProviderResponse{
		ToolCalls: []provider.ToolCall{{
			ID:       "call_" + ch.ID,
			Type:     "function",
			Function: provider.ToolCallFunction{Name: toolSaveChapter, Arguments: string(args)},
		}},
		FinishReason: "tool_calls",
	}
}

func finishCourseCall() provider.ProviderResponse {
	return provider.ProviderResponse{
		ToolCalls: []provider.ToolCall{{
			ID:       "call_finish_course",
			Type:     "function",
			Function: provider.ToolCallFunction{Name: toolFinishCourse, Arguments: `{"summary":"done"}`},
		}},
		FinishReason: "tool_calls",
	}
}

// outlineFor renders the outline of a plan fixture: exactly what the agent's
// first call asks for.
func outlineFor(t *testing.T, plan schema.CoursePlan) string {
	t.Helper()
	outline := ai.CourseOutline{
		Title:      plan.Title,
		Subtitle:   plan.Subtitle,
		Level:      plan.Level,
		Objectives: plan.Objectives,
		Setup:      plan.Setup,
	}
	for i, ch := range plan.Chapters {
		outline.Chapters = append(outline.Chapters, ai.OutlineChapter{
			ID:         ch.ID,
			N:          i + 1,
			Title:      ch.Title,
			Summary:    ch.Summary,
			Kind:       ch.Kind,
			Minutes:    ch.Minutes,
			Objectives: ch.Objectives,
			Exercises:  len(ch.Exercises),
		})
	}
	raw, err := json.Marshal(outline)
	if err != nil {
		t.Fatalf("encode outline: %v", err)
	}
	return string(raw)
}

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

	view, ok, err := jobs.Job(ctx, "learner-1", job.ID)
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

	// The run is stalled inside the provider call, so this picture is exact:
	// the request was validated, the plan is being written, and nothing has
	// been executed yet.
	validated, _ := stageByName(view.Stages, schema.StageValidateRequest)
	if validated.State != schema.StateDone {
		t.Fatalf("validate_request = %q, want %q", validated.State, schema.StateDone)
	}
	bundle, _ := stageByName(view.Stages, schema.StageRequestBundle)
	if bundle.State != schema.StateRunning {
		t.Fatalf("request_bundle = %q while the provider is answering, want %q", bundle.State, schema.StateRunning)
	}
	scratch, _ := stageByName(view.Stages, schema.StageScratchEnvironment)
	if scratch.State != schema.StateQueued {
		t.Fatalf("scratch_environment = %q before the plan exists, want %q", scratch.State, schema.StateQueued)
	}

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
	for _, st := range done.Stages {
		switch st.State {
		case schema.StateDone, schema.StateFailed, schema.StateSkipped:
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
	if rec.Status != schema.StatusActive || rec.Plan.ID != done.CourseID {
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
	depot := store.NewMemory()
	provider := newGatedProvider(ai.NewFakeProvider())
	jobs := NewJobs(jobService(provider, depot, ex, ai.Limits{}), ai.Limits{})

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
	if n := courseCount(t, depot); n != 0 {
		t.Fatalf("a failed run stored %d course(s)", n)
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

	view, ok, err := jobs.Job(context.Background(), "learner-1", job.ID)
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
	plan := coursePlanFixture()
	broken := plan.Chapters[1]
	// A cross-check that cannot agree with its reference: the exercise fails at
	// the cross-check stage, which is where a chapter is refused for its
	// content rather than for its shape.
	broken.Exercises[0].CrossCheckSolution = brokenCrossCheck
	agent := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps: []provider.ProviderResponse{
			saveChapterCall(t, plan.Chapters[0]),
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			finishCourseCall(),
		},
	}
	jobs := NewJobs(jobService(agent, depot, ex, ai.Limits{}), ai.Limits{})

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
	if view.ChaptersPlanned != len(plan.Chapters) || view.ChaptersVerified != 1 {
		t.Fatalf("job progress = %d of %d verified, want 1 of %d", view.ChaptersVerified, view.ChaptersPlanned, len(plan.Chapters))
	}
	if len(view.Chapters) != 2 {
		t.Fatalf("the job report has %d chapters, want one entry per planned chapter", len(view.Chapters))
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
