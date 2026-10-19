package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/jobs"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
	piai "github.com/sky-valley/pi/ai"
)

/* The asynchronous generation surface, from the transport's point of view.
 *
 * Two things belong to this package and are proved here: the routes keep the
 * owner rule the record routes already follow (a job id guesses nothing), and
 * the job a client polls and the record it reads afterwards are the same run —
 * the id in the job view is the id GET /courses/{id} answers, with the same
 * measured rows inside it.
 *
 * A real generated course is verified against a real PostgreSQL (pgtest), and
 * the provider takes a deliberate pause so the "while it runs" half of the
 * contract is observable rather than raced. Nothing below asserts on a timing
 * field. */

type jobStage struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// jobView is the wire shape of one job. Decoding into the test's own struct
// rather than the service's means a field renamed on the server without a
// matching wire change is caught here instead of silently following along.
type jobView struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Status   string     `json:"status"`
	Stages   []jobStage `json:"stages"`
	Attempts []struct {
		Number int    `json:"number"`
		Stage  string `json:"stage"`
	} `json:"attempts"`
	Chapters []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"chapters"`
	// The M8 progress fields: a course is written one chapter at a time, so a
	// poller draws "2 of 4 written" from these and not from the stage list.
	ChaptersPlanned  int    `json:"chapters_planned"`
	ChaptersVerified int    `json:"chapters_verified"`
	CurrentChapter   string `json:"current_chapter"`
	Error            *struct {
		Code    string `json:"code"`
		Stage   string `json:"stage"`
		Message string `json:"message"`
	} `json:"error"`
	CourseID    string `json:"course_id"`
	ChallengeID string `json:"challenge_id"`
	FinishedAt  string `json:"finished_at"`
}

// courseEnvelope is the wire shape of GET /courses/{id}, decoded into the
// test's own struct for the same reason jobView is.
type courseEnvelope struct {
	OK     bool `json:"ok"`
	Course struct {
		Status string `json:"status"`
		Plan   struct {
			ID       string              `json:"id"`
			Chapters []courseChapterView `json:"chapters"`
		} `json:"plan"`
	} `json:"course"`
}

type courseChapterView struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Sections []struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	} `json:"sections"`
	Exercises []struct {
		ID       string `json:"id"`
		Expected *struct {
			Rows [][]any `json:"rows"`
		} `json:"expected"`
	} `json:"exercises"`
}

// written is how many chapters of this view carry content. A building course
// carries exactly the chapters that verified and an outline skeleton for the
// rest, which is what makes "readable mid-run" checkable.
func (c courseEnvelope) written() int {
	n := 0
	for _, ch := range c.Course.Plan.Chapters {
		if len(ch.Sections) > 0 {
			n++
		}
	}
	return n
}

type jobEnvelope struct {
	OK    bool    `json:"ok"`
	Job   jobView `json:"job"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// asyncCourseBody is the request the course audit uses, so the two drive the
// provider down the same path.
const asyncCourseBody = `{"topics":["joins","aggregation","window functions"],"difficulty":"beginner","scenario":"Count what each customer ordered, then rank the results.","constraints":["one statement per exercise"]}`

// pausingProvider holds every completion for a moment, so a submission can be
// polled while the run is still in flight. It forwards the built-in provider's
// chapter conversation unchanged; only the pipeline's own calls wait.
type pausingProvider struct {
	inner *ai.FakeProvider
	delay time.Duration
}

func (p *pausingProvider) Descriptor() provider.ProviderDescriptor { return p.inner.Descriptor() }

func (p *pausingProvider) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return provider.ProviderResponse{}, ctx.Err()
	}
	return p.inner.Complete(ctx, req)
}

func (p *pausingProvider) Course() provider.CourseCapability {
	capability, _ := provider.CourseCapabilityOf(p.inner)
	return capability
}

// jobServer is a gateway with real PostgreSQL behind it and generation wired to
// the deterministic provider, paused long enough that a submission can be
// polled while it is still running.
func jobServer(t *testing.T) *Server {
	t.Helper()
	// The pause is what makes "while it runs" observable rather than raced: it
	// is between turns, so the run stays pollable for a while after each
	// chapter lands.
	return jobServerWith(t, &pausingProvider{inner: &ai.FakeProvider{}, delay: 300 * time.Millisecond})
}

// jobServerWith is jobServer with the provider under test named by the caller,
// which is how a test drives a model that misbehaves.
func jobServerWith(t *testing.T, provider provider.Provider) *Server {
	t.Helper()
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	ex, err := engine.NewPgxExecutorWithConfig(ctx, srv.DSN, engine.PgxConfig{MaxConns: 2, MinConns: 1, Writable: true})
	if err != nil {
		t.Fatalf("open the test executor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close() })

	st, err := store.NewPostgres(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("open the metadata store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s := New(ex, st, Config{MaxRows: 200, TimeoutMS: 10_000, MaxConcurrent: 4}, pgtest.QuietLogger())
	s.UseAI(ai.Config{
		Provider:       provider,
		Depot:          st,
		Runner:         providedRunner{exec: ex},
		DatasetVersion: 1,
	})
	return s
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

// pollJob reads one job view over the real route.
func pollJob(t *testing.T, s *Server, token, id string) jobEnvelope {
	t.Helper()
	w := doAs(t, s, token, http.MethodGet, "/generation/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /generation/%s status = %d body = %s", id, w.Code, w.Body.String())
	}
	var out jobEnvelope
	mustDecode(t, w, &out)
	return out
}

// waitJob polls until the run reaches an outcome, which is what a client does.
// It fails the test rather than hanging if the job never finishes.
func waitJob(t *testing.T, s *Server, token, id string) jobEnvelope {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		out := pollJob(t, s, token, id)
		if out.Job.Status != jobs.JobRunning {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never left status %q", id, jobs.JobRunning)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAGenerationJobIsSubmittedPolledAndReadBack drives the whole asynchronous
// loop over the real HTTP surface: submit, watch it run, read the record it
// named. It also pins the default: a plain POST is still answered with the
// envelope, because that is what every script and audit reads.
func TestAGenerationJobIsSubmittedPolledAndReadBack(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "async-owner@example.test")

	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", asyncCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)
	if !submitted.OK || submitted.Job.ID == "" {
		t.Fatalf("a submission must answer with ok and a job id: %s", w.Body.String())
	}
	if submitted.Job.Kind != "course" || submitted.Job.Status != jobs.JobRunning {
		t.Fatalf("submitted job = %+v, want a running course", submitted.Job)
	}

	// The provider is paused, so this poll happens while the run is in flight.
	// The states have to be one of the live ones, and at least one stage must
	// still be ahead of the runner.
	live := pollJob(t, s, token, submitted.Job.ID)
	if live.Job.Status != jobs.JobRunning {
		t.Fatalf("status = %q while the provider is still answering, want %q", live.Job.Status, jobs.JobRunning)
	}
	if len(live.Job.Stages) == 0 {
		t.Fatal("a running job reports no stages: a poller would have nothing to show")
	}
	ahead := 0
	for _, st := range live.Job.Stages {
		switch st.State {
		case schema.StateQueued, schema.StateRunning:
			ahead++
		case schema.StateDone, schema.StateFailed, schema.StateSkipped:
		default:
			t.Fatalf("stage %q reports %q, which is not a live state", st.Name, st.State)
		}
	}
	if ahead == 0 {
		t.Fatalf("every stage is already final while status is %q: %+v", live.Job.Status, live.Job.Stages)
	}
	// A running job may already name the course it is building: the outline is
	// stored before the first chapter is written, which is the whole point of
	// streaming. What must never happen is a count that runs ahead of the
	// record or an id the course route refuses to answer.
	if live.Job.ChaptersVerified > live.Job.ChaptersPlanned {
		t.Fatalf("a running job reports %d verified of %d planned", live.Job.ChaptersVerified, live.Job.ChaptersPlanned)
	}
	if live.Job.CourseID != "" {
		w := doAs(t, s, token, http.MethodGet, "/courses/"+live.Job.CourseID, "")
		if w.Code != http.StatusOK {
			t.Fatalf("a running job named a course the route will not answer: status = %d body = %s", w.Code, w.Body.String())
		}
		var mid courseEnvelope
		mustDecode(t, w, &mid)
		if mid.Course.Status != schema.StatusBuilding && mid.Course.Status != schema.StatusActive {
			t.Fatalf("a running job's course is %q, want %q or %q", mid.Course.Status, schema.StatusBuilding, schema.StatusActive)
		}
		if mid.Course.Status == schema.StatusBuilding && len(mid.Course.Plan.Chapters) != live.Job.ChaptersPlanned {
			t.Fatalf("a building course carries %d chapter(s), the job planned %d", len(mid.Course.Plan.Chapters), live.Job.ChaptersPlanned)
		}
	}

	done := waitJob(t, s, token, submitted.Job.ID)
	if done.Job.Status != schema.StatusActive {
		t.Fatalf("terminal status = %q, want %q (error = %+v)", done.Job.Status, schema.StatusActive, done.Job.Error)
	}
	if done.Job.CourseID == "" || done.Job.ChallengeID != "" {
		t.Fatalf("an active course job must name exactly one course: %+v", done.Job)
	}
	if done.Job.FinishedAt == "" {
		t.Fatal("a finished job must say when it finished")
	}
	for _, st := range done.Job.Stages {
		switch st.State {
		case schema.StateDone, schema.StateFailed, schema.StateSkipped:
		default:
			t.Fatalf("a finished job reports stage %q as %q", st.Name, st.State)
		}
		if st.Detail == "" {
			t.Fatalf("a finished job reports stage %q with no detail", st.Name)
		}
	}

	// The record the job named is the record the course route answers, with the
	// measured rows inside it.
	w = doAs(t, s, token, http.MethodGet, "/courses/"+done.Job.CourseID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /courses/%s status = %d body = %s", done.Job.CourseID, w.Code, w.Body.String())
	}
	var rec struct {
		OK     bool `json:"ok"`
		Course struct {
			Status string `json:"status"`
			Plan   struct {
				ID       string `json:"id"`
				Chapters []struct {
					Exercises []struct {
						Expected *struct {
							Rows [][]any `json:"rows"`
						} `json:"expected"`
					} `json:"exercises"`
				} `json:"chapters"`
			} `json:"plan"`
		} `json:"course"`
	}
	mustDecode(t, w, &rec)
	if rec.Course.Plan.ID != done.Job.CourseID || rec.Course.Status != schema.StatusActive {
		t.Fatalf("stored course = %+v, want the active plan %s", rec.Course.Status, done.Job.CourseID)
	}
	exercises := 0
	for _, ch := range rec.Course.Plan.Chapters {
		for _, exercise := range ch.Exercises {
			exercises++
			if exercise.Expected == nil || len(exercise.Expected.Rows) == 0 {
				t.Fatal("a stored exercise has no measured rows: the job named a plan the grader cannot use")
			}
		}
	}
	if exercises == 0 {
		t.Fatal("the stored course has no exercises")
	}

	// A finished job stays readable: a client that was away when the run ended
	// still learns what happened.
	again := pollJob(t, s, token, submitted.Job.ID)
	if again.Job.Status != schema.StatusActive || again.Job.CourseID != done.Job.CourseID {
		t.Fatalf("polling a finished job changed the answer: %+v", again.Job)
	}

	// The run is counted where the blocking route counts it, and no generation
	// is left running.
	if got, ok := s.Registry().Value(metricGenerations, metrics.Labels{"kind": "course", "status": schema.StatusActive, "code": ""}); !ok || got < 1 {
		t.Fatalf("querycraft_generations_total{status=active} = %v (present = %v), want at least 1", got, ok)
	}
	if got, ok := s.Registry().Value(metricGenerationJobs, metrics.Labels{"state": "running"}); !ok || got != 0 {
		t.Fatalf("querycraft_generation_jobs{running} = %v (present = %v), want 0", got, ok)
	}

	// Another learner sees neither the job nor the record it made.
	other := signupUser(t, s, "async-other@example.test")
	if w := doAs(t, s, other, http.MethodGet, "/generation/"+submitted.Job.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another learner read the job: status = %d body = %s", w.Code, w.Body.String())
	}
	if w := doAs(t, s, other, http.MethodGet, "/courses/"+done.Job.CourseID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another learner read the course: status = %d body = %s", w.Code, w.Body.String())
	}

	// The default is unchanged: a POST without the async request is answered
	// with the envelope in one round trip, which is what the audits read.
	w = doAs(t, s, token, http.MethodPost, "/courses/generate", asyncCourseBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("blocking submit status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Status string `json:"status"`
		Kind   string `json:"kind"`
	}
	mustDecode(t, w, &out)
	if out.Status != schema.StatusActive || out.Kind != "course" {
		t.Fatalf("blocking submit answered %+v, want an active course", out)
	}
}

// TestAnUnknownJobIsNotFound: an id that was never issued is missing, the same
// answer a job belonging to someone else gets.
func TestAnUnknownJobIsNotFound(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "async-unknown@example.test")
	if w := doAs(t, s, token, http.MethodGet, "/generation/gen-does-not-exist", ""); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

// TestGenerationJobsNeedASession: the poll route is not an oracle for anonymous
// callers, and a job is never readable without the account that owns it.
func TestGenerationJobsNeedASession(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	if w := do(t, s, http.MethodGet, "/generation/gen-anything", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous poll status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
}

// TestGenerationJobPollingIsOffWithAI: with no generation service attached the
// poll route says so the way the other generation routes do — a capability
// answer, not a crash and not an invented job.
func TestGenerationJobPollingIsOffWithAI(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "async-off@example.test")

	w := doAs(t, s, token, http.MethodGet, "/generation/gen-anything", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	var out jobEnvelope
	mustDecode(t, w, &out)
	if out.OK || out.Job.ID != "" {
		t.Fatalf("an AI-off poll must not invent a job: %s", w.Body.String())
	}
	if out.Error == nil || out.Error.Code != ai.CodeUnavailable {
		t.Fatalf("error = %+v, want %s", out.Error, ai.CodeUnavailable)
	}

	// A submission to a gateway with no generation service is refused with the
	// same envelope the blocking route already answers with.
	w = doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", asyncCourseBody)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("AI-off async submit status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	var off struct {
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	mustDecode(t, w, &off)
	if off.Status != schema.StatusUnavailable || off.Error == nil || off.Error.Code != ai.CodeUnavailable {
		t.Fatalf("AI-off async submit answered %+v", off)
	}
}

/* ---------- M8: the course that is written while you watch ---------- */

// TestACourseIsReadableWhileLaterChaptersAreStillBeingWritten is the promise of
// the streaming loop seen from outside the process: the run stores the course's
// shape before it writes a chapter, stores each chapter the moment it verifies,
// and GET /courses/{id} answers with exactly those chapters while the rest of
// the course is still being written. The provider pauses between turns, so the
// window is observed rather than raced; nothing here reads a timing field.
func TestACourseIsReadableWhileLaterChaptersAreStillBeingWritten(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "streaming-reader@example.test")

	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", asyncCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)

	// Poll like a client, and read the record the moment the job says a
	// chapter verified. Two chapters are still unwritten at that moment, and
	// each of them costs the provider another pause, so the poll cannot miss
	// the window in which the course is half written.
	var (
		seen     courseEnvelope
		seenAt   int
		planned  int
		courseID string
	)
	deadline := time.Now().Add(60 * time.Second)
	for {
		view := pollJob(t, s, token, submitted.Job.ID)
		if view.Job.Status != jobs.JobRunning {
			t.Fatalf("the run reached %q before a chapter could be read mid-run", view.Job.Status)
		}
		if view.Job.ChaptersVerified > 0 && view.Job.CourseID != "" {
			seenAt, planned, courseID = view.Job.ChaptersVerified, view.Job.ChaptersPlanned, view.Job.CourseID
			w := doAs(t, s, token, http.MethodGet, "/courses/"+courseID, "")
			if w.Code != http.StatusOK {
				t.Fatalf("GET /courses/%s while the run is writing status = %d body = %s", courseID, w.Code, w.Body.String())
			}
			mustDecode(t, w, &seen)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never reported a verified chapter", submitted.Job.ID)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The stored row is the record the finished run will carry, still being
	// written: same id, the whole planned shape. Which chapters carry content
	// mid-run is the ai pipeline's own promise, tested in its package.
	if seen.Course.Status != schema.StatusBuilding {
		t.Fatalf("the course read mid-run is %q, want %q", seen.Course.Status, schema.StatusBuilding)
	}
	if seen.Course.Plan.ID != courseID {
		t.Fatalf("the mid-run course has id %q, want %q", seen.Course.Plan.ID, courseID)
	}
	if len(seen.Course.Plan.Chapters) != planned {
		t.Fatalf("the building course carries %d chapter(s), the job planned %d", len(seen.Course.Plan.Chapters), planned)
	}
	if seenAt >= planned {
		t.Fatalf("every planned chapter (%d) already verified: this is not a mid-run read", planned)
	}

	// The same id is the active course once the run is over, with every
	// chapter carrying content.
	done := waitJob(t, s, token, submitted.Job.ID)
	if done.Job.Status != schema.StatusActive || done.Job.CourseID != courseID {
		t.Fatalf("the finished job = %+v, want the course it was building (%s)", done.Job, courseID)
	}
	w = doAs(t, s, token, http.MethodGet, "/courses/"+courseID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /courses/%s after the run status = %d body = %s", courseID, w.Code, w.Body.String())
	}
	var final courseEnvelope
	mustDecode(t, w, &final)
	if final.Course.Status != schema.StatusActive {
		t.Fatalf("the finished course is %q, want %q", final.Course.Status, schema.StatusActive)
	}
	if final.written() != len(final.Course.Plan.Chapters) || final.written() == 0 {
		t.Fatalf("the active course carries content for %d of %d chapter(s)", final.written(), len(final.Course.Plan.Chapters))
	}
}

// crossCheckBreaker is a model that will not be told: it rewrites every chapter
// it is asked to save so the cross-check cannot agree with the reference. The
// gateway must refuse the run instead of storing it, because a course with an
// unverified exercise in it is worse than no course at all.
// crossCheckBreaker serves the built-in provider's chapter conversation and
// corrupts every save_chapter's cross-check solutions: they run, return rows,
// and disagree, which is the honest reason a course is refused — the engine's,
// not the transport's.
type crossCheckBreaker struct{ inner *ai.FakeProvider }

func (a *crossCheckBreaker) Descriptor() provider.ProviderDescriptor { return a.inner.Descriptor() }

func (a *crossCheckBreaker) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	return a.inner.Complete(ctx, req)
}

func (a *crossCheckBreaker) Course() provider.CourseCapability {
	capability, _ := provider.CourseCapabilityOf(a.inner)
	inner := capability.StreamFn
	capability.StreamFn = func(ctx context.Context, model *piai.Model, req piai.TranscriptContext, opts *piai.SimpleStreamOptions) *piai.AssistantMessageEventStream {
		src := inner(ctx, model, req, opts)
		out := piai.NewAssistantMessageEventStream()
		go func() {
			defer out.End()
			for ev, ok := src.Next(); ok; ev, ok = src.Next() {
				if ev.Type == piai.EventDone && ev.Message != nil {
					for i, c := range ev.Message.Content {
						tc, ok := c.(piai.ToolCall)
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
						for j := range ch.Exercises {
							ch.Exercises[j].CrossCheckSolution = `SELECT 1 AS n`
						}
						fixed, merr := json.Marshal(ch)
						if merr != nil {
							continue
						}
						var args map[string]any
						if json.Unmarshal(fixed, &args) != nil {
							continue
						}
						ev.Message.Content[i] = piai.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: args}
					}
				}
				out.Push(ev)
			}
		}()
		return out
	}
	return capability
}

// TestARefusedCourseLeavesNoRowBehind: the outline's row is real while the run
// is going, so a run in which nothing verifies has to remove it. The route says
// the run was refused, names no record, and lists nothing — a learner is never
// offered a course with no content.
func TestARefusedCourseLeavesNoRowBehind(t *testing.T) {
	s := jobServerWith(t, &crossCheckBreaker{inner: &ai.FakeProvider{}})
	token := signupUser(t, s, "streaming-refusal@example.test")

	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", asyncCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)

	done := waitJob(t, s, token, submitted.Job.ID)
	if done.Job.Status != schema.StatusRejected {
		t.Fatalf("terminal status = %q, want %q (error = %+v)", done.Job.Status, schema.StatusRejected, done.Job.Error)
	}
	if done.Job.CourseID != "" {
		t.Fatalf("a refused run named a record: %s", done.Job.CourseID)
	}
	if done.Job.Error == nil || done.Job.Error.Code != ai.CodeCrossCheckFailed {
		t.Fatalf("the refusal = %+v, want the engine's own cross-check verdict", done.Job.Error)
	}
	if done.Job.ChaptersVerified != 0 {
		t.Fatalf("a refused run reports %d verified chapter(s)", done.Job.ChaptersVerified)
	}
	// Nothing is stored: the row the outline wrote is gone, not left as a
	// course with no chapters in it.
	w = doAs(t, s, token, http.MethodGet, "/courses", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /courses status = %d body = %s", w.Code, w.Body.String())
	}
	var list struct {
		Courses []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"courses"`
	}
	mustDecode(t, w, &list)
	if len(list.Courses) != 0 {
		t.Fatalf("a refused course left %d record(s) stored: %+v", len(list.Courses), list.Courses)
	}
}

/* ---------- the outline pause and the approval that continues it ---------- */

// approvalCourseBody is asyncCourseBody plus the opt-in pause. It is the only
// difference between a paused run and today's one-request run.
const approvalCourseBody = `{"topics":["joins","aggregation","window functions"],"difficulty":"beginner","scenario":"Count what each customer ordered, then rank the results.","require_approval":true}`

// TestAPausedCourseIsApprovedOverTheRealRoutes drives the whole slice over
// HTTP: submit a run that asks for approval, watch it stop with the outline
// stored, read the index the learner would approve, POST the approval, and
// watch the course activate.
func TestAPausedCourseIsApprovedOverTheRealRoutes(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "approval-owner@example.test")

	// Submit the paused run and let it stop at the outline.
	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", approvalCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)

	paused := waitJob(t, s, token, submitted.Job.ID)
	if paused.Job.Status != schema.StatusAwaitingApproval {
		t.Fatalf("the run stopped at %q, want %q (error = %+v)", paused.Job.Status, schema.StatusAwaitingApproval, paused.Job.Error)
	}
	if paused.Job.CourseID == "" {
		t.Fatal("a paused run must name the course it stored, because that is the index to approve")
	}
	if paused.Job.Error != nil {
		t.Fatalf("a pause is not a failure, but the job carries an error: %+v", paused.Job.Error)
	}

	// The run's transcript records the same non-terminal status, and it is a
	// status the run contract admits — the pause is a real ending, not one the
	// contract would refuse.
	w = doAs(t, s, token, http.MethodGet, "/runs/"+submitted.Job.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs/%s status = %d body = %s", submitted.Job.ID, w.Code, w.Body.String())
	}
	var run struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
	}
	mustDecode(t, w, &run)
	if run.Run.Status != schema.StatusAwaitingApproval {
		t.Fatalf("the paused run's transcript says %q, want %q", run.Run.Status, schema.StatusAwaitingApproval)
	}
	// The transcript validates against the shared run contract, which is where
	// the new status had to be admitted: a response carrying a status the
	// contract refuses is a contract the gateway is not keeping.
	runContract := loadContract(t, "run.schema.json")
	decodedRun := decodeResponseObject(t, w.Body.Bytes())
	if vs := validateSchema(contractDef(t, runContract, "#/$defs/runTranscript"), decodedRun["run"], ""); len(vs) > 0 {
		t.Fatalf("the paused run's transcript violates contracts/run.schema.json:\n%s", formatViolations(vs))
	}

	// The stored record is a paused course carrying the outline's chapters,
	// with no content yet: this is what the learner approves. The row says
	// `awaiting_approval`, not `building`: a pause is its own state.
	w = doAs(t, s, token, http.MethodGet, "/courses/"+paused.Job.CourseID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /courses/%s status = %d body = %s", paused.Job.CourseID, w.Code, w.Body.String())
	}
	var index courseEnvelope
	mustDecode(t, w, &index)
	if index.Course.Status != schema.StatusAwaitingApproval {
		t.Fatalf("the stored course is %q, want %q", index.Course.Status, schema.StatusAwaitingApproval)
	}
	if len(index.Course.Plan.Chapters) == 0 {
		t.Fatal("the stored outline has no chapters to approve")
	}
	if index.written() != 0 {
		t.Fatalf("the stored outline carries content for %d chapter(s) before approval", index.written())
	}
	for _, ch := range index.Course.Plan.Chapters {
		if ch.Title == "" {
			t.Errorf("the stored outline's chapter %q has no title to approve", ch.ID)
		}
	}

	// Approve: continue the stored outline into the chapter phase. It is
	// submitted as a job, so the client can watch it the same way.
	w = doAs(t, s, token, http.MethodPost, "/courses/"+paused.Job.CourseID+"/build?async=1", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("build submit status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var built jobEnvelope
	mustDecode(t, w, &built)
	if built.Job.Kind != "course" || built.Job.Status != jobs.JobRunning {
		t.Fatalf("build job = %+v, want a running course", built.Job)
	}

	done := waitJob(t, s, token, built.Job.ID)
	if done.Job.Status != schema.StatusActive {
		t.Fatalf("the approval ended at %q, want %q (error = %+v)", done.Job.Status, schema.StatusActive, done.Job.Error)
	}
	if done.Job.CourseID != paused.Job.CourseID {
		t.Fatalf("the approval produced course %q, want the paused one %q", done.Job.CourseID, paused.Job.CourseID)
	}

	// The same record is now active, whole, with measured rows everywhere.
	w = doAs(t, s, token, http.MethodGet, "/courses/"+paused.Job.CourseID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /courses/%s after approval status = %d body = %s", paused.Job.CourseID, w.Code, w.Body.String())
	}
	var final courseEnvelope
	mustDecode(t, w, &final)
	if final.Course.Status != schema.StatusActive {
		t.Fatalf("the approved course is %q, want %q", final.Course.Status, schema.StatusActive)
	}
	if final.written() != len(final.Course.Plan.Chapters) || final.written() == 0 {
		t.Fatalf("the approved course carries content for %d of %d chapter(s)", final.written(), len(final.Course.Plan.Chapters))
	}
	for _, ch := range final.Course.Plan.Chapters {
		for _, exercise := range ch.Exercises {
			if exercise.Expected == nil || len(exercise.Expected.Rows) == 0 {
				t.Fatalf("approved chapter %s exercise %s has no measured rows", ch.ID, exercise.ID)
			}
		}
	}

	// Counting happens where the blocking route counts it, and the paused run
	// is counted under its own status.
	if got, ok := s.Registry().Value(metricGenerations, metrics.Labels{"kind": "course", "status": schema.StatusAwaitingApproval, "code": ""}); !ok || got < 1 {
		t.Fatalf("querycraft_generations_total{status=awaiting_approval} = %v (present = %v), want at least 1", got, ok)
	}
}

// TestAPausedCourseCanBeDiscarded: discarding is the existing DELETE, and it
// ends the run the job was paused on rather than leaving a poller on
// awaiting_approval for a course that no longer exists.
func TestAPausedCourseCanBeDiscarded(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "approval-discard@example.test")

	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", approvalCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)
	paused := waitJob(t, s, token, submitted.Job.ID)
	if paused.Job.Status != schema.StatusAwaitingApproval || paused.Job.CourseID == "" {
		t.Fatalf("the run did not pause: %+v", paused.Job)
	}

	// Discard deletes the row.
	w = doAs(t, s, token, http.MethodDelete, "/courses/"+paused.Job.CourseID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("discard status = %d body = %s", w.Code, w.Body.String())
	}
	if w := doAs(t, s, token, http.MethodGet, "/courses/"+paused.Job.CourseID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("the discarded course still reads: status = %d body = %s", w.Code, w.Body.String())
	}
	// And approving it afterwards is a clean 404, never a resurrected course.
	if w := doAs(t, s, token, http.MethodPost, "/courses/"+paused.Job.CourseID+"/build", ""); w.Code != http.StatusNotFound {
		t.Fatalf("building a discarded course status = %d, want 404; body = %s", w.Code, w.Body.String())
	}

	// The run the learner was watching says it was discarded.
	after := pollJob(t, s, token, submitted.Job.ID)
	if after.Job.Status != schema.StatusDiscarded {
		t.Fatalf("the discarded run reports %q, want %q", after.Job.Status, schema.StatusDiscarded)
	}

	// The list is empty: the outline row is gone, not left as a course.
	w = doAs(t, s, token, http.MethodGet, "/courses", "")
	var list struct {
		Courses []schema.CourseRecord `json:"courses"`
	}
	mustDecode(t, w, &list)
	if len(list.Courses) != 0 {
		t.Fatalf("discarding left %d record(s) stored", len(list.Courses))
	}
}

// TestApprovingAnotherAccountsCourseIs404: the build route follows the same
// owner rule the read and delete routes follow — an id must not be probeable
// for existence, so a foreign course is missing, never forbidden.
func TestApprovingAnotherAccountsCourseIs404(t *testing.T) {
	s := jobServer(t)
	aliceToken := signupUser(t, s, "approval-alice@example.test")
	bobToken := signupUser(t, s, "approval-bob@example.test")

	w := doAs(t, s, aliceToken, http.MethodPost, "/courses/generate?async=1", approvalCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)
	paused := waitJob(t, s, aliceToken, submitted.Job.ID)
	if paused.Job.CourseID == "" {
		t.Fatal("alice's paused run named no course")
	}

	if w := doAs(t, s, bobToken, http.MethodPost, "/courses/"+paused.Job.CourseID+"/build", ""); w.Code != http.StatusNotFound {
		t.Fatalf("bob building alice's course status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
	// The course is untouched and still read-only to bob.
	if w := doAs(t, s, bobToken, http.MethodGet, "/courses/"+paused.Job.CourseID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("bob reading alice's course status = %d, want 404", w.Code)
	}
	if w := doAs(t, s, aliceToken, http.MethodGet, "/courses/"+paused.Job.CourseID, ""); w.Code != http.StatusOK {
		t.Fatalf("alice's own course status = %d, want 200", w.Code)
	}
}

// TestBuildingAFinishedCourseIsRefused: the build route continues a course that
// is still being built, and refuses a finished one rather than silently
// re-running its chapters.
func TestBuildingAFinishedCourseIsRefused(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "approval-finished@example.test")

	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", asyncCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)
	done := waitJob(t, s, token, submitted.Job.ID)
	if done.Job.Status != schema.StatusActive || done.Job.CourseID == "" {
		t.Fatalf("the setup run = %+v, want an active course", done.Job)
	}

	if w := doAs(t, s, token, http.MethodPost, "/courses/"+done.Job.CourseID+"/build", ""); w.Code != http.StatusConflict {
		t.Fatalf("building a finished course status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

// TestTheDefaultCourseRunIsUnchanged is the compatibility claim: a run that
// does not ask for approval is one request, outline into chapters, and never
// reports awaiting_approval.
func TestTheDefaultCourseRunIsUnchanged(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "approval-default@example.test")

	w := doAs(t, s, token, http.MethodPost, "/courses/generate", asyncCourseBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("blocking submit status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Status string `json:"status"`
		Plan   *struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	mustDecode(t, w, &out)
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q", out.Status, schema.StatusActive)
	}
	if out.Plan == nil || out.Plan.ID == "" {
		t.Fatal("the default run must return the activated plan")
	}
}

// TestBuildCourseRoutesAreHonestWithAIOff: with no generation service the
// build route says so rather than inventing a continuation.
func TestBuildCourseRoutesAreHonestWithAIOff(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	token := signupUser(t, s, "ai-off-build@example.test")
	w := doAs(t, s, token, http.MethodPost, "/courses/c1/build", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	// And an anonymous caller is refused before any work starts.
	if w := do(t, s, http.MethodPost, "/courses/c1/build", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous build status = %d, want 401", w.Code)
	}
}

// TestAnInterruptedCourseIsResumedOverTheRealRoutes: a build the gateway
// stopped writing (a crash, a dead provider) is not a ghost. The row keeps the
// chapters that verified, the list says `interrupted`, and the build route
// continues from them to an active course.
func TestAnInterruptedCourseIsResumedOverTheRealRoutes(t *testing.T) {
	s := jobServer(t)
	token := signupUser(t, s, "interrupted-owner@example.test")
	ctx := context.Background()

	// A real paused run first, so the row carries a real outline and the
	// request beside it; then the state a crash leaves behind: interrupted.
	w := doAs(t, s, token, http.MethodPost, "/courses/generate?async=1", approvalCourseBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var submitted jobEnvelope
	mustDecode(t, w, &submitted)
	paused := waitJob(t, s, token, submitted.Job.ID)
	if paused.Job.Status != schema.StatusAwaitingApproval || paused.Job.CourseID == "" {
		t.Fatalf("the run stopped at %q with course %q, want a paused outline", paused.Job.Status, paused.Job.CourseID)
	}

	depot := s.aiCourseDepot()
	if depot == nil {
		t.Fatal("the server has no course depot")
	}
	rec, found, err := depot.Course(ctx, paused.Job.CourseID)
	if err != nil || !found {
		t.Fatalf("read the paused course: found = %v err = %v", found, err)
	}
	rec.Status = schema.StatusInterrupted
	rec.Outcome.Status = schema.StatusInterrupted
	if err := depot.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("flip to interrupted: %v", err)
	}

	// The list says interrupted, so the library can offer the resume.
	w = doAs(t, s, token, http.MethodGet, "/courses", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /courses status = %d body = %s", w.Code, w.Body.String())
	}
	var list struct {
		Courses []struct {
			Plan struct {
				ID string `json:"id"`
			} `json:"plan"`
			Status string `json:"status"`
		} `json:"courses"`
	}
	mustDecode(t, w, &list)
	saw := ""
	for _, c := range list.Courses {
		if c.Plan.ID == paused.Job.CourseID {
			saw = c.Status
		}
	}
	if saw != schema.StatusInterrupted {
		t.Fatalf("the interrupted course lists as %q, want %q", saw, schema.StatusInterrupted)
	}

	// Resume: the build route accepts the interrupted row and finishes it.
	w = doAs(t, s, token, http.MethodPost, "/courses/"+paused.Job.CourseID+"/build?async=1", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("resume status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var resumed jobEnvelope
	mustDecode(t, w, &resumed)
	done := waitJob(t, s, token, resumed.Job.ID)
	if done.Job.Status != schema.StatusActive {
		t.Fatalf("the resumed build ended %q, want %q (error = %+v)", done.Job.Status, schema.StatusActive, done.Job.Error)
	}
	if done.Job.CourseID != paused.Job.CourseID {
		t.Fatalf("the resumed build names %q, want the same course %q", done.Job.CourseID, paused.Job.CourseID)
	}

	// The record reads back active, with every planned chapter carrying content.
	w = doAs(t, s, token, http.MethodGet, "/courses/"+paused.Job.CourseID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /courses/%s status = %d body = %s", paused.Job.CourseID, w.Code, w.Body.String())
	}
	var final courseEnvelope
	mustDecode(t, w, &final)
	if final.Course.Status != schema.StatusActive {
		t.Fatalf("the resumed course is %q, want %q", final.Course.Status, schema.StatusActive)
	}
	if final.written() != len(final.Course.Plan.Chapters) || final.written() == 0 {
		t.Fatalf("the resumed course carries content for %d of %d chapter(s)", final.written(), len(final.Course.Plan.Chapters))
	}
}
