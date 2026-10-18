// Package jobs owns the asynchronous generation runs. A submission is answered
// with an id, the run continues on a detached context, and a poller reads the
// pipeline's own stages and outcome through JobView.
package jobs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/transcript"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* Generation as a job.
 *
 * A run is minutes of work, and the whole time it is a black box: the request
 * is open, the learner sees a spinner, and a browser that reloads, navigates or
 * loses the tab takes the answer with it. A submit can instead be answered with
 * an id, and the run continues on a context that is not the request's.
 * Detaching matters for a reason beyond the transport: the provider calls are
 * already paid for, so cancelling them because a client went away throws away
 * work the learner asked for and waits for nothing.
 *
 * The registry is deliberately modest. It is not a queue: a submission is
 * refused when the gateway is already running its maximum number of
 * generations, because a run holds a scratch PostgreSQL database and accepting
 * work that cannot start is how a gateway stops answering anything. A finished
 * job is dropped once an owner has more than a handful, because the stored
 * record is the durable thing and the job is only where the run's stages were
 * readable while it ran.
 *
 * Nothing here decides what is true about a generation. The pipeline does, and
 * a job view is a live copy of the pipeline's own report, so a poller and a
 * blocking caller can never be told different things about the same run.
 */

// JobRunning is the status of a job that has not reached a terminal outcome.
// Every other status a job reports is one of the pipeline's own terminal
// statuses, so a client branches on the same strings whichever route it used.
const JobRunning = "running"

// Job is the handle a submission receives: what to poll and what it is.
type Job struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// JobView is one job as a poller sees it. While the run is in flight the stages
// are a live picture: a stage that has not started reads "queued", the one
// executing reads "running", and the rest carry what they measured. Once the
// run ends, the stages are the pipeline's own final report, the same slice the
// blocking envelope carries, so the two routes cannot disagree about what ran.
type JobView struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// Stages and Attempts are never null: a client renders them, and an empty
	// list and a missing one are not the same statement.
	Stages   []schema.Stage   `json:"stages"`
	Attempts []schema.Attempt `json:"attempts"`
	// Chapters travels with a rejected course only, for the same reason the
	// blocking envelope carries it: the useful part of a rejection is which
	// exercise refused and what the engine said about it.
	Chapters []schema.ChapterResult `json:"chapters,omitempty"`
	// ChaptersPlanned, ChaptersVerified and CurrentChapter are the course's
	// live progress. A poller reads them to draw "2 of 4 chapters written" and
	// to name the chapter the gateway is working on while the run is still
	// going.
	ChaptersPlanned  int           `json:"chapters_planned,omitempty"`
	ChaptersVerified int           `json:"chapters_verified,omitempty"`
	CurrentChapter   string        `json:"current_chapter,omitempty"`
	Error            *schema.Error `json:"error"`
	// CourseID (or ChallengeID) names the stored record once the run created
	// one. An id that is present is a promise that GET /courses/{id} answers;
	// a run that failed or was rejected names neither, because it stored
	// nothing.
	CourseID    string     `json:"course_id,omitempty"`
	ChallengeID string     `json:"challenge_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// ServiceFor builds the service a job runs as. It is a function rather than a
// service value because a service carries an owner, and the owner of a job is
// the session that submitted it: the registry has no way to name an owner
// itself, so it cannot build a service for the wrong one.
type ServiceFor func(ownerID string) *ai.Service

// Submission is one ask for a generation run.
type Submission struct {
	// OwnerID is the account the job belongs to. It comes from the session and
	// never from the request body.
	OwnerID string
	Kind    string
	Request schema.GenerationRequest
	// BuildCourseID, when set, makes the job a *continuation* rather than a new
	// generation: the stored course with this id is resumed into its chapter
	// phase (BuildCourse). Request is then unused, because the request that
	// produced the outline is stored beside the course, and kind is always
	// course.
	BuildCourseID string
	// OnFinish, when set, is called once with the terminal view, after the job
	// is readable. It is the metrics hook: a run that answered a request that
	// is already over still has to be counted where the blocking route counts
	// it, or an operator would see a quiet gateway and a busy database.
	OnFinish func(JobView)
}

// jobHeadroom is how much longer than the pipeline's own budget a job's context
// lives. The pipeline stops itself and says why; the extra time is what makes
// that the answer, instead of a context cancellation with no report at all.
const jobHeadroom = 15 * time.Second

// maxFinishedJobsPerOwner bounds what a busy owner leaves behind. The record is
// the durable thing, so keeping every job ever run would only grow the heap.
const maxFinishedJobsPerOwner = 20

// Jobs owns the asynchronous runs.
type Jobs struct {
	serviceFor ServiceFor

	// maxRunning is a bound, not a preference: a generation spends a scratch
	// database, and an unbounded registry is a resource leak that looks like a
	// feature.
	maxRunning int

	mu      sync.Mutex
	jobs    map[string]*job
	running int
	// idle closes when running falls back to zero, so Drain can wait for the
	// last run to end without polling. Submit replaces it each time a run
	// starts from idle.
	idle chan struct{}
}

// NewJobs builds the registry. The limits are the pipeline's, so the number of
// simultaneous generations a gateway admits is the capacity it advertises
// rather than a second, private guess.
func NewJobs(serviceFor ServiceFor, limits ai.Limits) *Jobs {
	l := limits.Merge(ai.DefaultLimits())
	return &Jobs{
		serviceFor: serviceFor,
		maxRunning: l.MaxConcurrent,
		jobs:       map[string]*job{},
	}
}

// Running reports how many jobs are executing right now.
func (j *Jobs) Running() int {
	if j == nil {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running
}

// Drain waits until no job is executing, or until the context ends, and
// returns the context's error when the wait is cut short. Shutdown calls it
// before the store and the database close: a run is detached from the request
// that asked for it, so it outlives the server, and a transcript written after
// the store closed would be lost.
func (j *Jobs) Drain(ctx context.Context) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	if j.running == 0 {
		j.mu.Unlock()
		return nil
	}
	idle := j.idle
	j.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Submit starts a run and returns immediately with the id to poll. The run gets
// its own context: the request that asked for it is about to end, and
// context.WithoutCancel keeps the request's values (the id a log line carries)
// while detaching its cancellation.
func (j *Jobs) Submit(ctx context.Context, sub Submission) (Job, error) {
	if j == nil {
		return Job{}, ai.NewError(ai.CodeUnavailable, "", "AI generation is not available.", "")
	}
	kind := strings.TrimSpace(sub.Kind)
	if kind == "" {
		kind = "challenge"
	}
	svc := j.serviceFor(sub.OwnerID)
	if svc == nil {
		return Job{}, ai.NewError(ai.CodeUnavailable, "", "AI is off: no generation service is configured.", "")
	}
	// The same answer a blocking submit gives when generation cannot run, so a
	// caller that never learns the difference is not told a different story.
	if av := svc.Available(); !av.Available {
		return Job{}, svc.UnavailableOutcome(kind, av).Error
	}

	jb := newJob(kind, sub.OwnerID)
	j.mu.Lock()
	if j.running >= j.maxRunning {
		j.mu.Unlock()
		return Job{}, ai.NewError(ai.CodeEnvironmentDown, "",
			fmt.Sprintf("The gateway is already running its maximum of %d generation(s); try again when one finishes.", j.maxRunning), "")
	}
	if j.running == 0 {
		j.idle = make(chan struct{})
	}
	j.jobs[jb.id] = jb
	j.running++
	j.mu.Unlock()

	req := sub.Request
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), svc.Limits().Timeout+jobHeadroom)
	go func() {
		defer cancel()
		out := j.run(runCtx, svc, kind, req, sub.BuildCourseID, jb)
		j.complete(jb, out, sub.OnFinish)
	}()
	return Job{ID: jb.id, Kind: kind, Status: JobRunning}, nil
}

// Job returns the view of one job. A job that belongs to another learner is
// reported as unknown, never as forbidden, because that is the rule the stored
// records already follow: an id must not be probeable for existence.
func (j *Jobs) Job(ownerID, id string) (JobView, bool, error) {
	if j == nil || id == "" {
		return JobView{}, false, nil
	}
	j.mu.Lock()
	jb, ok := j.jobs[id]
	j.mu.Unlock()
	if !ok || jb.ownerID != ownerID {
		return JobView{}, false, nil
	}
	return jb.view(), true, nil
}

// DiscardCourse ends the job that was waiting on a course, so a poller is told
// why the run it was watching will never continue. It is called when the
// learner discards the stored course: the run itself stopped at
// awaiting_approval (that is what its transcript records), and this is the
// later fact that the course it named is gone. Only a paused job is touched. A
// running job is never rewritten, and a job that already reached a terminal
// status keeps it.
func (j *Jobs) DiscardCourse(ownerID, courseID string) {
	if j == nil || courseID == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, jb := range j.jobs {
		if jb.ownerID != ownerID {
			continue
		}
		jb.mu.Lock()
		if jb.courseID == courseID && jb.status == schema.StatusAwaitingApproval {
			jb.status = schema.StatusDiscarded
		}
		jb.mu.Unlock()
	}
}

// run executes the pipeline for one job, publishing every stage as it lands.
//
// A panic here is recovered rather than left to the runtime: this goroutine has
// no net/http recovery above it, so an unrecovered panic in a provider would
// take the whole gateway down and every other learner's session with it. The
// job reports the bug as its own failure instead, invents no stage evidence for
// it, and keeps the process up to serve the next request.
func (j *Jobs) run(ctx context.Context, svc *ai.Service, kind string, req schema.GenerationRequest, buildCourseID string, jb *job) (out schema.Outcome) {
	defer func() {
		if r := recover(); r != nil {
			out = schema.Outcome{
				Status:   schema.StatusFailed,
				Kind:     kind,
				Stages:   []schema.Stage{},
				Attempts: []schema.Attempt{},
				Error: ai.NewError(ai.CodeInvalidArtifact, "",
					"The generation run stopped unexpectedly.", engine.RedactSecrets(fmt.Sprint(r))),
			}
		}
	}()
	// The transcript is named after the job id a client already polls, so the
	// run a learner watched and the run they can later audit share one name.
	runCtx := ai.WithProgress(transcript.WithRunID(ctx, jb.id), jb.observe)
	// A continuation is not a new generation: the course and its request are
	// already stored, so only the id travels.
	if buildCourseID != "" {
		return svc.BuildCourse(runCtx, buildCourseID)
	}
	if kind == "course" {
		return svc.GenerateCourse(runCtx, req)
	}
	return svc.GenerateChallenge(runCtx, req)
}

// complete publishes the terminal view, frees the run slot and then hands the
// view to the caller's hook. The slot is freed before the hook runs so a hook
// that submits another job cannot deadlock against this one.
func (j *Jobs) complete(jb *job, out schema.Outcome, onFinish func(JobView)) {
	jb.finish(out)
	j.mu.Lock()
	j.running--
	if j.running == 0 {
		close(j.idle)
	}
	j.evictLocked(jb.ownerID)
	j.mu.Unlock()
	if onFinish != nil {
		onFinish(jb.view())
	}
}

// evictLocked drops the oldest finished jobs of one owner until the owner is
// within the bound. Running jobs are never evicted: an id a client holds must
// keep answering while the run it names is still going.
func (j *Jobs) evictLocked(ownerID string) {
	finished := 0
	for _, jb := range j.jobs {
		if jb.ownerID == ownerID && jb.done() {
			finished++
		}
	}
	for finished > maxFinishedJobsPerOwner {
		oldest := ""
		var oldestAt time.Time
		for id, jb := range j.jobs {
			if jb.ownerID != ownerID || !jb.done() {
				continue
			}
			if oldest == "" || jb.finishedAt().Before(oldestAt) {
				oldest, oldestAt = id, jb.finishedAt()
			}
		}
		if oldest == "" {
			return
		}
		delete(j.jobs, oldest)
		finished--
	}
}

/* ---------- one job ---------- */

// job is one run's mutable state. The pipeline writes it from the run's
// goroutine and a poller reads it from an HTTP handler, so every field below
// the id is guarded by mu.
type job struct {
	id      string
	ownerID string
	kind    string
	created time.Time

	mu               sync.Mutex
	status           string
	stages           []schema.Stage
	attempts         []schema.Attempt
	chapters         []schema.ChapterResult
	chaptersPlanned  int
	chaptersVerified int
	currentChapter   string
	err              *schema.Error
	courseID         string
	challengeID      string
	finishedAt0      time.Time
}

// newJob starts a view that already carries the pipeline's whole shape: every
// stage queued, in order. A poller that arrives before the first stage does
// should see the plan, not an empty list that looks like a run that checked
// nothing.
func newJob(kind, ownerID string) *job {
	stages := ai.QueuedStages()
	return &job{
		id:       transcript.NewID("gen-"),
		ownerID:  ownerID,
		kind:     kind,
		created:  time.Now().UTC(),
		status:   JobRunning,
		stages:   stages,
		attempts: []schema.Attempt{},
	}
}

// observe takes one live snapshot from the pipeline. A snapshot that arrives
// after the run ended is dropped: the terminal view is the pipeline's own final
// report, and a late copy must not overwrite it with a worse one.
func (jb *job) observe(p ai.Progress) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	if jb.status != JobRunning {
		return
	}
	jb.stages = p.Stages
	jb.attempts = p.Attempts
	if p.Planned > 0 {
		jb.chaptersPlanned = p.Planned
	}
	if p.Chapters != nil {
		jb.chapters = p.Chapters
	}
	jb.chaptersVerified = p.Verified
	jb.currentChapter = p.CurrentChapter
	if p.CourseID != "" {
		jb.courseID = p.CourseID
	}
}

// finish records the terminal outcome: the status a client branches on, the
// evidence that justified it, and the id of the record it stored.
func (jb *job) finish(out schema.Outcome) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.status = out.Status
	if out.Kind != "" {
		jb.kind = out.Kind
	}
	if out.Stages != nil {
		jb.stages = out.Stages
	}
	if out.Attempts != nil {
		jb.attempts = out.Attempts
	}
	jb.chapters = out.Chapters
	jb.currentChapter = ""
	jb.chaptersVerified = 0
	jb.chaptersPlanned = len(out.Chapters)
	for _, ch := range out.Chapters {
		if ch.Status == ai.ChapterVerified {
			jb.chaptersVerified++
		}
	}
	jb.err = out.Error
	// A terminal view names a record only when one exists. A refused course
	// deleted the building row it had written, so the live id a poller saw
	// mid-run must not survive: an id that does not answer is worse than no id
	// at all. A run that failed after the outline left its building row behind,
	// and the id it was already reporting still answers.
	switch {
	case out.Plan != nil:
		jb.courseID = out.Plan.ID
	case out.Status == schema.StatusRejected:
		jb.courseID = ""
	}
	if out.Artifact != nil {
		jb.challengeID = out.Artifact.ID
	}
	jb.finishedAt0 = time.Now().UTC()
}

func (jb *job) done() bool {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.status != JobRunning
}

func (jb *job) finishedAt() time.Time {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.finishedAt0
}

// view copies what a poller needs. The copies are the point: the pipeline owns
// the slices it reports, and a reader on another goroutine must not be handed
// one it can race with.
func (jb *job) view() JobView {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	v := JobView{
		ID:               jb.id,
		Kind:             jb.kind,
		Status:           jb.status,
		Stages:           append(make([]schema.Stage, 0, len(jb.stages)), jb.stages...),
		Attempts:         append(make([]schema.Attempt, 0, len(jb.attempts)), jb.attempts...),
		Chapters:         jb.chapters,
		ChaptersPlanned:  jb.chaptersPlanned,
		ChaptersVerified: jb.chaptersVerified,
		CurrentChapter:   jb.currentChapter,
		Error:            jb.err,
		CourseID:         jb.courseID,
		ChallengeID:      jb.challengeID,
		CreatedAt:        jb.created,
	}
	if !jb.finishedAt0.IsZero() {
		at := jb.finishedAt0
		v.FinishedAt = &at
	}
	return v
}
