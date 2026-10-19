package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/ai/transcript"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* pipeline.go runs the §11.1 generation pipeline: the policy that turns one
 * GenerationRequest into an active artifact, or into an honest refusal.
 *
 * The model is an untrusted content generator. Each stage proves a property of
 * the candidate, or stops the run with an envelope error that names the stage.
 * The stages, in order:
 *
 *   1. validate_request    the learner's ask is usable (validateRequest).
 *   2. request_bundle      one provider call returns a candidate bundle.
 *   3. validate_artifact   the bundle obeys the policy gate (validateArtifact).
 *   4. scratch_environment a throwaway database is created, the setup plan is
 *                          applied, and the named dataset is checked.
 *   5. reference_contract  the reference solution runs and satisfies its own
 *                          result contract. The engine's rows become the
 *                          expected result, so the grader and the verifier
 *                          share one answer.
 *   6. cross_check         a second, independently produced solution returns
 *                          the same result under the same contract.
 *   7. adversarial_fixtures deliberately wrong queries must all be rejected,
 *                          so a passing answer cannot be a false positive.
 *   8. repair              on a content failure, one more provider call rewrites
 *                          the bundle, up to Limits.MaxRepairs.
 *   9. activate            provenance is stamped, the artifact is stored as
 *                          active, and only then is it returned.
 *
 * Nothing becomes visible to a learner before activate, and activate is reached
 * only after every SQL-executing stage passed against a real engine.
 *
 * The run records evidence as it goes in pipeline.evidence: the scratch
 * database name, the reference and cross-check row counts, and the fixture
 * tally. Callers read it to audit a rejected run.
 *
 * Collaborators: Service owns the run and its transcript; provider.Provider
 * supplies candidates; engine.Executor runs SQL; eval grades results;
 * schema.Depot stores the active artifact; Runner hands out scratch
 * environments.
 *
 * Stage reporting policy: a run that starts lists all nine stages in order, and
 * every one carries a non-empty Detail. A stage the run never reached is
 * reported as "skipped" with the reason, never omitted, so a reader can tell
 * "this check did not run" from "this check ran and passed". The two look
 * identical in an empty list.
 */

// stageOrder is the pipeline's fixed sequence. The repair stage sits at its
// declared position but runs whenever a stage fails, which is why the reported
// slice is a fixed layout rather than an append log.
var stageOrder = []string{
	schema.StageValidateRequest,
	schema.StageRequestBundle,
	schema.StageValidateArtifact,
	schema.StageScratchEnvironment,
	schema.StageReferenceContract,
	schema.StageCrossCheck,
	schema.StageAdversarialFixtures,
	schema.StageRepair,
	schema.StageActivate,
}

// QueuedStages returns the pipeline's whole shape with every stage queued, in
// order. Callers that show a run before it starts (the job registry builds a
// view the moment a submission is accepted) carry the plan rather than an empty
// list, which would read as a run that checked nothing.
func QueuedStages() []schema.Stage {
	stages := make([]schema.Stage, 0, len(stageOrder))
	for _, name := range stageOrder {
		stages = append(stages, schema.Stage{Name: name, State: schema.StateQueued, Detail: "queued"})
	}
	return stages
}

// pipeline carries the run's mutable state. One pipeline serves one generation
// request; it is not shared and needs no locking.
type pipeline struct {
	svc      *Service
	req      schema.GenerationRequest
	limits   Limits
	stages   []schema.Stage
	index    map[string]int
	attempts []schema.Attempt
	evidence schema.Evidence
	repairs  int
	// lastExpected is the reference result of the most recent verifyInEnv, in
	// the shape the evaluator compares against. The challenge path ignores it
	// (it re-derives nothing) and the course path copies it into the exercise
	// it just verified, so the rows a learner is graded against are the rows
	// the engine returned here.
	lastExpected *eval.ExpectedResult

	// current is the stage executing right now, or "" between stages. The
	// stages slice only ever holds what has finished, so a live view needs this
	// separate fact to say "this is happening now" instead of reporting a long
	// provider call as an empty stage.
	current      string
	currentStart time.Time
	// build is the course under construction, or nil for a challenge run. It is
	// the one home of the course facts a poller reads: the planned and verified
	// counts, the chapter in flight, the stored row's id and the resolved
	// report all derive from it in notify() rather than being copied beside it.
	build *courseBuild
	// report, when set, receives a live snapshot on every transition. It is the
	// only reason a run can be watched at all. A blocking caller attaches none,
	// and notify() then does nothing.
	report func(Progress)
}

func newPipeline(svc *Service, req schema.GenerationRequest, limits Limits) *pipeline {
	p := &pipeline{svc: svc, req: req, limits: limits, index: make(map[string]int, len(stageOrder))}
	for _, name := range stageOrder {
		p.index[name] = len(p.stages)
		p.stages = append(p.stages, schema.Stage{Name: name, State: schema.StateSkipped})
	}
	return p
}

// Progress is a live snapshot of a run in flight, taken from the pipeline.
//
// The course fields are what makes a long run legible: a course is written one
// chapter at a time, so "which chapter is being written" and "how many have
// verified" are the two facts a poller needs and neither is a stage. A
// challenge run leaves them zero and publishes an empty chapter list.
type Progress struct {
	Stages   []schema.Stage
	Attempts []schema.Attempt
	// Planned is how many chapters the outline planned, Verified how many have
	// passed, and Current the chapter the gateway started verifying most
	// recently. It keeps that name until the next chapter is handed in, so a
	// poller reads which chapter the run is on rather than a blank.
	Planned        int
	Verified       int
	CurrentChapter string
	// CourseID is the stored row's id, set as soon as the outline is stored.
	// A client can open it mid-run and see the chapters that already verified.
	CourseID string
	// Chapters is the resolved per-chapter report so far, in course order.
	Chapters []schema.ChapterResult
}

// progressKey carries the run's reporter to the pipeline. It travels in the
// context rather than in the Service because it belongs to one run, not to the
// configuration that serves every run.
type progressKey struct{}

// WithProgress attaches a reporter for one run. A pipeline with no reporter
// behaves exactly as it did before this existed: the live view is derived from
// the pipeline, never required by it.
func WithProgress(ctx context.Context, report func(Progress)) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, report)
}

// progressFrom returns the run's reporter, or nil when nobody is watching.
func progressFrom(ctx context.Context) func(Progress) {
	if report, ok := ctx.Value(progressKey{}).(func(Progress)); ok {
		return report
	}
	return nil
}

// mark records what actually happened in one stage, updating it in place so a
// retry shows the last attempt's verdict rather than every attempt's.
func (p *pipeline) mark(name, state, detail string) {
	i, ok := p.index[name]
	if !ok {
		return
	}
	p.stages[i].State = state
	p.stages[i].Detail = detail
	if name == p.current {
		p.current = ""
	}
	p.notify()
}

// watch attaches the run's progress reporter, if its context carries one, and
// publishes the first snapshot: a poller that arrives before the first stage
// does should see the pipeline's whole shape rather than an empty list, which
// would read as a run that checked nothing.
func (p *pipeline) watch(ctx context.Context) {
	p.report = progressFrom(ctx)
	p.notify()
}

// begin records the stage that is executing right now. It deliberately leaves
// p.stages alone: a stage that is merely in flight has not passed or failed
// anything, and finalize() reads that slice to write the run's final prose. If
// begin wrote into it, a run that died mid-stage would report a stage as
// "running" forever, which is a claim about a stage that never finished.
func (p *pipeline) begin(name string) {
	if _, ok := p.index[name]; !ok {
		return
	}
	p.current = name
	p.currentStart = time.Now()
	p.notify()
}

// setChapter records which chapter the agent is writing right now, so a poller
// can name it rather than showing a stage called "request_bundle" for minutes.
// The fact lives on the build, which is the course run's own state.
func (p *pipeline) setChapter(id string) {
	if p.build == nil {
		return
	}
	p.build.current = id
	p.notify()
}

// snapshotStages copies the stage list without finalizing it. A building course
// stored mid-run carries the live picture; finalize() would write "the run
// stopped before this stage" into stages that simply have not happened yet,
// which is a claim about a run that is still going.
func (p *pipeline) snapshotStages() []schema.Stage {
	out := make([]schema.Stage, len(p.stages))
	copy(out, p.stages)
	return out
}

// notify publishes a live copy of the run's progress. It is called from the
// run's own goroutine on every transition and costs nothing when nobody is
// watching, which is the case for every blocking caller.
func (p *pipeline) notify() {
	if p.report == nil {
		return
	}
	stages := make([]schema.Stage, len(p.stages))
	copy(stages, p.stages)
	for i := range stages {
		switch {
		case stages[i].Name != "" && stages[i].Name == p.current:
			stages[i].State = schema.StateRunning
			stages[i].Detail = "running"
			stages[i].MS = engine.RoundMS(time.Since(p.currentStart))
		case stages[i].State == schema.StateSkipped && stages[i].Detail == "":
			// Not reached yet: the run has not stopped before this stage, it
			// simply has not got there. A poller would read "skipped" as a
			// final verdict, so report the stage as queued instead.
			stages[i].State = schema.StateQueued
			stages[i].Detail = "queued"
		}
	}
	attempts := make([]schema.Attempt, len(p.attempts))
	copy(attempts, p.attempts)
	// The course facts all derive from the one build, so there is no second
	// copy to keep in step. A challenge run has no build and reports the zero
	// value.
	planned, verified, currentChapter, courseID := 0, 0, "", ""
	chapters := []schema.ChapterResult{}
	if p.build != nil {
		planned = len(p.build.outline.Chapters)
		verified = p.build.verifiedCount()
		currentChapter = p.build.current
		courseID = p.build.plan.ID
		chapters = append(chapters, p.build.report...)
	}
	p.report(Progress{
		Stages:         stages,
		Attempts:       attempts,
		Planned:        planned,
		Verified:       verified,
		CurrentChapter: currentChapter,
		CourseID:       courseID,
		Chapters:       chapters,
	})
}

// timed marks a stage and records how long it took.
func (p *pipeline) timed(name, state, detail string, start time.Time) {
	p.mark(name, state, detail)
	if i, ok := p.index[name]; ok {
		p.stages[i].MS = engine.RoundMS(time.Since(start))
	}
}

func (p *pipeline) outcome(status string, artifact *schema.Artifact, err *schema.Error) schema.Outcome {
	p.finalize()
	// An empty slice, never nil: contracts/ai.schema.json requires `attempts` to
	// be an array, and encoding/json writes a nil slice as `null`. `stages` is
	// always built by newPipeline, so it needs no guard.
	attempts := p.attempts
	if attempts == nil {
		attempts = []schema.Attempt{}
	}
	return schema.Outcome{
		Status:   status,
		Kind:     p.req.Kind,
		Artifact: artifact,
		Stages:   p.stages,
		Attempts: attempts,
		Evidence: p.evidence,
		Error:    err,
	}
}

// finalize fills in the prose for every reported stage. A stage is never a
// blank row: the client renders the detail verbatim, and "failures are
// understandable" is an M4 exit-gate clause, so "this did not run" has to say
// so out loud rather than looking like a check that found nothing.
//
// Policy, stated once: a stage is "done" when it ran and passed, "failed" when
// it ran and refused, and "skipped" when the run stopped before reaching it,
// either because an earlier stage failed or because a repair was never needed.
func (p *pipeline) finalize() {
	stopped := ""
	for _, st := range p.stages {
		if st.State == schema.StateFailed && stopped == "" {
			stopped = st.Name
		}
	}
	for i := range p.stages {
		if p.stages[i].Detail != "" {
			continue
		}
		switch {
		case p.stages[i].State == schema.StateSkipped && stopped != "":
			p.stages[i].Detail = "skipped: " + stopped + " failed"
		case p.stages[i].Name == schema.StageRepair && p.stages[i].State == schema.StateSkipped:
			p.stages[i].Detail = "not needed: no stage failed"
		case p.stages[i].State == schema.StateSkipped:
			p.stages[i].Detail = "skipped: the run stopped before this stage"
		case p.stages[i].State == schema.StateDone:
			p.stages[i].Detail = "ran"
		default:
			p.stages[i].Detail = "failed"
		}
	}
}

// failedOutcome is the shape of every status that is not "active": an artifact
// is never returned, and an error is always present.
func (p *pipeline) failedOutcome(status string, err *schema.Error) schema.Outcome {
	return p.outcome(status, nil, err)
}

// generate runs the pipeline with a bounded repair loop.
//
// It owns the run's transcript: the recorder is attached before any provider
// call and stored once, whatever the run's outcome. A run that was refused up
// front (AI off) makes no provider call and is not a transcript; every other
// run leaves a record, including one that failed validation before it reached
// the model, because "the request was refused and here is why" is the same
// class of fact.
func (s *Service) generate(ctx context.Context, req schema.GenerationRequest) schema.Outcome {
	avail := s.Available()
	if !avail.Available {
		return s.unavailable(req.Kind, avail)
	}
	rec := transcript.NewRecorder(kindOf(req), transcript.RunIDFrom(ctx), transcript.Redactor(s.cfg.Provider))
	ctx = transcript.WithRecorder(ctx, rec)
	out := s.generateRecorded(ctx, req)
	s.saveTranscript(ctx, rec, out)
	return out
}

// kindOf names the run's kind from the request, defaulting to challenge, the
// same default the stored records use.
func kindOf(req schema.GenerationRequest) string {
	if req.Kind == schema.RunKindCourse {
		return schema.RunKindCourse
	}
	return schema.RunKindChallenge
}

// saveTranscript stores the run's record. A failure here is logged and dropped:
// the transcript is evidence about a run, never the thing the run produces, so
// a store that cannot keep it must not turn a generated course into a failure.
func (s *Service) saveTranscript(ctx context.Context, rec *transcript.Recorder, out schema.Outcome) {
	if rec == nil || s == nil || s.cfg.Depot == nil {
		return
	}
	if err := s.cfg.Depot.SaveRun(context.WithoutCancel(ctx), rec.Finish(out.Status, out.Error)); err != nil && s.cfg.Logger != nil {
		s.cfg.Logger.Warn("the generation transcript could not be stored", "err", engine.RedactSecrets(err.Error()), "run", rec.ID())
	}
}

// generateRecorded is the pipeline proper, run with the transcript recorder
// already attached.
func (s *Service) generateRecorded(ctx context.Context, req schema.GenerationRequest) schema.Outcome {
	// A course is a different sequence of work over the same stages: one plan,
	// one environment, every exercise verified in order. It shares the
	// pipeline's state, its repair budget and its evidence rather than
	// duplicating any of it.
	if req.Kind == "course" {
		return s.generateCourse(ctx, req)
	}
	limits := s.cfg.Limits
	runCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	p := newPipeline(s, req, limits)
	p.watch(ctx)

	start := time.Now()
	p.begin(schema.StageValidateRequest)
	if verr := validateRequest(req, limits); verr != nil {
		p.timed(schema.StageValidateRequest, schema.StateFailed, verr.Detail, start)
		return p.failedOutcome(schema.StatusFailed, verr)
	}
	req = normalizeRequest(req, limits)
	p.timed(schema.StageValidateRequest, schema.StateDone, requestSummary(req), start)

	prompt := requestPrompt(req)
	p.begin(schema.StageRequestBundle)
	raw, perr := s.complete(runCtx, provider.ProviderRequest{
		Kind:           kindOrChallenge(req.Kind),
		System:         systemPrompt(),
		Prompt:         prompt,
		Schema:         []byte(challengeSchema),
		MaxOutputBytes: limits.MaxOutputBytes,
	})
	if perr != nil {
		return p.failedOutcome(schema.StatusFailed, perr)
	}
	p.timed(schema.StageRequestBundle, schema.StateDone, "candidate bundle received", start)

	var (
		artifact schema.Artifact
		lastErr  *schema.Error
	)
	for {
		if lastErr != nil {
			if runCtx.Err() != nil {
				return p.timeout(lastErr)
			}
			if lastErr.Code == CodeEnvironmentDown {
				// The engine, not the content, is the problem. Spending a
				// repair on it would be a claim that the model can fix a
				// database, which is not true.
				return p.failedOutcome(schema.StatusFailed, lastErr)
			}
			if p.repairs >= limits.MaxRepairs {
				return p.exhausted(lastErr)
			}
			p.attempts = append(p.attempts, schema.Attempt{
				Number: p.repairs + 1,
				Stage:  lastErr.Stage,
				Detail: strings.TrimSpace(lastErr.Message + " " + lastErr.Detail),
			})
			p.begin(schema.StageRepair)
			repaired, rerr := s.complete(runCtx, provider.ProviderRequest{
				Kind:           "repair",
				System:         systemPrompt(),
				Prompt:         prompt + "\n\nA previous bundle failed at the " + lastErr.Stage + " stage.",
				Schema:         []byte(challengeSchema),
				MaxOutputBytes: limits.MaxOutputBytes,
				Previous:       raw,
				Failure:        lastErr.Stage + ": " + lastErr.Message + "; " + lastErr.Detail,
			})
			if rerr != nil {
				return p.failedOutcome(schema.StatusFailed, rerr)
			}
			p.repairs++
			raw = repaired
			p.mark(schema.StageRepair, schema.StateDone, fmt.Sprintf("attempt %d requested after %s failed", p.repairs, lastErr.Stage))
			lastErr = nil
		}

		if derr := decodeBundle(raw, &artifact); derr != nil {
			lastErr = derr
			continue
		}
		if verr := p.verify(runCtx, artifact); verr != nil {
			lastErr = verr
			continue
		}
		return p.activate(runCtx, artifact)
	}
}

// completeRaw performs one provider call and maps a transport failure onto the
// envelope. It is the tool-calling counterpart of complete: the reply is
// returned whole, because a tool-calling turn carries its answer in ToolCalls
// and has no text to return.
//
// A provider that is configured but unreachable is AI_UNAVAILABLE: that is not a
// content problem and no repair can fix it. The detail is redacted because a
// provider error can quote a URL or a credential.
func (s *Service) completeRaw(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, *schema.Error) {
	rec := transcript.From(ctx)
	rec.RecordRequestMessages(req)
	resp, err := s.cfg.Provider.Complete(ctx, req)
	if err != nil {
		rec.RecordError(err)
		return provider.ProviderResponse{}, NewError(CodeUnavailable, "",
			"The AI provider could not produce a bundle.", engine.RedactSecrets(err.Error()))
	}
	rec.RecordResponse(resp)
	if req.MaxOutputBytes > 0 && len(resp.Text) > req.MaxOutputBytes {
		return provider.ProviderResponse{}, artifactError("bundle", fmt.Sprintf("reply of %d bytes exceeds the %d-byte limit", len(resp.Text), req.MaxOutputBytes))
	}
	return resp, nil
}

// complete performs one provider call and returns the reply text.
func (s *Service) complete(ctx context.Context, req provider.ProviderRequest) (string, *schema.Error) {
	resp, err := s.completeRaw(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// timeout is the run-level timeout as an error, so the course agent can hand it
// back up and the run publishes it through the same failedOutcome the challenge
// path uses.
func (p *pipeline) timeout(last *schema.Error) schema.Outcome {
	stage := schema.StageRequestBundle
	if last != nil {
		stage = last.Stage
	}
	return p.failedOutcome(schema.StatusFailed, NewError(CodeUnavailable, stage,
		fmt.Sprintf("Generation exceeded the %s time limit.", p.limits.Timeout),
		"the run was stopped before it could finish; nothing was activated"))
}

// exhausted turns the last content failure into the rejected outcome. The code
// is the most specific one available: a disagreement between two independently
// produced solutions is reported as a cross-check failure because that is the
// actionable fact, and only a bundle that never became runnable is reported as
// an exhausted repair budget. Either way the message names the stage that
// refused and why, so a human can act on it.
func (p *pipeline) exhausted(last *schema.Error) schema.Outcome {
	code := CodeRepairExhausted
	if mapped, ok := codeForStage(last.Stage); ok {
		code = mapped
	}
	e := NewError(code, last.Stage,
		fmt.Sprintf("Repairs exhausted after %d attempt(s); the challenge was rejected at the %s stage: %s",
			p.repairs, last.Stage, last.Message),
		last.Detail)
	p.evidence.Repairs = p.repairs
	return p.failedOutcome(schema.StatusRejected, e)
}

// codeForStage names the envelope code for the two stages whose failure means
// the same thing on both the challenge and the course path. A caller that maps
// a stage differently keeps its own arm and falls back to its own code.
func codeForStage(stage string) (string, bool) {
	switch stage {
	case schema.StageCrossCheck:
		return CodeCrossCheckFailed, true
	case schema.StageReferenceContract:
		return CodeReferenceFailed, true
	}
	return "", false
}

// decodeBundle parses one provider reply. Provider output is untrusted input,
// so a malformed reply is a policy failure with the field named, never a panic.
func decodeBundle(raw string, a *schema.Artifact) *schema.Error {
	if strings.TrimSpace(raw) == "" {
		return artifactError("bundle", "the provider returned an empty reply")
	}
	if err := decodeJSON(raw, a); err != nil {
		return NewError(CodeInvalidArtifact, schema.StageRequestBundle,
			"The provider's reply is not a usable bundle.", "bundle: "+err.Error())
	}
	return nil
}

// verify runs every stage that executes SQL and returns the first failure. It
// creates one throwaway environment for the attempt and always releases it.
func (p *pipeline) verify(ctx context.Context, art schema.Artifact) *schema.Error {
	start := time.Now()
	p.begin(schema.StageValidateArtifact)
	if verr := validateArtifact(art, p.limits); verr != nil {
		p.timed(schema.StageValidateArtifact, schema.StateFailed, verr.Detail, start)
		return verr
	}
	p.timed(schema.StageValidateArtifact, schema.StateDone, fmt.Sprintf("%d hints, %d setup statement(s)", len(art.Hints), len(art.Setup.Statements)), start)

	if p.svc.cfg.Runner == nil {
		e := NewError(CodeEnvironmentDown, schema.StageScratchEnvironment, "No database is available to verify against.", "")
		p.mark(schema.StageScratchEnvironment, schema.StateFailed, e.Message)
		return e
	}
	start = time.Now()
	p.begin(schema.StageScratchEnvironment)
	scratch := scratchName(art)
	ex, release, err := p.svc.cfg.Runner.Scratch(ctx, scratch)
	if err != nil {
		e := NewError(CodeEnvironmentDown, schema.StageScratchEnvironment,
			"A throwaway environment could not be created.", engine.RedactSecrets(err.Error()))
		p.timed(schema.StageScratchEnvironment, schema.StateFailed, e.Detail, start)
		return e
	}
	defer release()
	// The Runner seam exposes the environment *name*, not the physical
	// database, so this is the name the pipeline asked for. It is recorded
	// because "which environment did this run in" is the first question a
	// rejected run raises.
	p.evidence.ScratchDatabase = scratch

	if serr := p.prepare(ctx, ex, art); serr != nil {
		p.timed(schema.StageScratchEnvironment, schema.StateFailed, serr.Detail, start)
		return serr
	}
	p.timed(schema.StageScratchEnvironment, schema.StateDone, fmt.Sprintf("%d setup statement(s) applied; dataset %q readable", len(art.Setup.Statements), art.Setup.Dataset), start)

	return p.verifyInEnv(ctx, ex, art)
}

// verifyInEnv runs the three stages that execute SQL (reference contract,
// cross-check, adversarial fixtures) against an environment the caller owns,
// and records what they measured in p.evidence. It is split out of verify
// because a course verifies many exercises in one environment: the challenge
// path creates that environment, and the course path creates it once.
func (p *pipeline) verifyInEnv(ctx context.Context, ex engine.Executor, art schema.Artifact) *schema.Error {
	contract := evalContract(art.ResultContract)
	start := time.Now()
	p.begin(schema.StageReferenceContract)
	refQR, refErr := p.runReference(ctx, ex, art)
	if refErr != nil {
		p.timed(schema.StageReferenceContract, schema.StateFailed, refErr.Detail, start)
		return refErr
	}
	expected := eval.ExpectedResult{Columns: contract.Columns, Rows: refQR.Rows}
	p.lastExpected = &expected
	cmp := eval.CompareDetailed(expected, contract, refQR)
	if !cmp.OK || !cmp.ColumnsOK {
		e := NewError(CodeReferenceFailed, schema.StageReferenceContract,
			"The reference solution does not satisfy its own result contract.",
			"columns or rows did not match: "+strings.Join(cmp.Codes, ", "))
		p.timed(schema.StageReferenceContract, schema.StateFailed, e.Detail, start)
		return e
	}
	p.evidence.ReferenceRows = refQR.RowCount
	p.timed(schema.StageReferenceContract, schema.StateDone, fmt.Sprintf("reference returned %d row(s)", refQR.RowCount), start)

	// Cross-check: a second, independently produced solution must agree with
	// the first under the same contract. This catches some contract errors. It
	// is not proof of correctness, because both solutions can share a mistake.
	start = time.Now()
	p.begin(schema.StageCrossCheck)
	crossQR, crossErr := p.runStatement(ctx, ex, art.CrossCheckSolution, schema.StageCrossCheck, CodeCrossCheckFailed, "cross-check solution")
	if crossErr != nil {
		p.timed(schema.StageCrossCheck, schema.StateFailed, crossErr.Detail, start)
		return crossErr
	}
	if crossCmp := eval.CompareDetailed(expected, contract, crossQR); !crossCmp.OK {
		e := NewError(CodeCrossCheckFailed, schema.StageCrossCheck,
			"The two solutions disagree under the result contract.",
			"codes: "+strings.Join(crossCmp.Codes, ", "))
		p.timed(schema.StageCrossCheck, schema.StateFailed, e.Detail, start)
		return e
	}
	p.evidence.CrossCheckRows = crossQR.RowCount
	p.timed(schema.StageCrossCheck, schema.StateDone, fmt.Sprintf("cross-check agreed on %d row(s)", crossQR.RowCount), start)

	start = time.Now()
	p.begin(schema.StageAdversarialFixtures)
	if ferr := p.fixtures(ctx, ex, art, expected, contract); ferr != nil {
		p.timed(schema.StageAdversarialFixtures, schema.StateFailed, ferr.Detail, start)
		return ferr
	}
	p.timed(schema.StageAdversarialFixtures, schema.StateDone,
		fmt.Sprintf("%d/%d wrong queries were rejected", p.evidence.FixturesRejected, p.evidence.FixturesTotal), start)
	return nil
}

// prepare applies the setup plan and checks the dataset it refers to. For a
// challenge over the sealed dataset the statement list is empty, and the check
// is the point: a bundle that names a dataset this build does not ship must
// fail before anything runs.
func (p *pipeline) prepare(ctx context.Context, ex engine.Executor, art schema.Artifact) *schema.Error {
	for i, stmt := range art.Setup.Statements {
		res := engine.Execute(ctx, ex, stmt, engine.DefaultLimits())
		if res.Status != "succeeded" || res.Truncated {
			return NewError(CodeInvalidArtifact, schema.StageScratchEnvironment,
				"The setup plan could not be applied.",
				fmt.Sprintf("setup.statements: statement %d failed: %s", i, queryErrorText(res)))
		}
	}
	return p.checkDataset(ctx, ex, art.Setup)
}

// checkDataset is the pipeline's own probe, not generated content, so it may
// read the catalog: it verifies that the named dataset exists, that its version
// matches what the bundle claims, and that every table in the dataset schema is
// readable. The table list comes from the server rather than a hardcoded list,
// so this package does not need to know the dataset's contents.
func (p *pipeline) checkDataset(ctx context.Context, ex engine.Executor, setup schema.SetupPlan) *schema.Error {
	versions, err := probeStrings(ctx, ex,
		`SELECT "version" FROM "dataset"."dataset_meta" WHERE "dataset" = $1`, setup.Dataset)
	if err != nil {
		return datasetError(setup.Dataset, "the dataset metadata could not be read: "+engine.RedactSecrets(err.Error()))
	}
	if len(versions) != 1 {
		return datasetError(setup.Dataset, fmt.Sprintf("expected 1 metadata row, found %d", len(versions)))
	}
	if versions[0] != fmt.Sprintf("%d", setup.SchemaVersion) {
		return datasetError(setup.Dataset, fmt.Sprintf("the dataset is version %s but the bundle claims %d", versions[0], setup.SchemaVersion))
	}

	tables, err := probeStrings(ctx, ex,
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'dataset' ORDER BY table_name`)
	if err != nil {
		return datasetError(setup.Dataset, "the table list could not be read: "+engine.RedactSecrets(err.Error()))
	}
	if len(tables) == 0 {
		return datasetError(setup.Dataset, "the dataset schema has no tables")
	}
	for _, table := range tables {
		res := engine.Execute(ctx, ex, `SELECT * FROM "dataset".`+quoteIdent(table)+` LIMIT 0`, engine.DefaultLimits())
		if res.Status != "succeeded" {
			return datasetError(setup.Dataset, fmt.Sprintf("table %q is not readable: %s", table, queryErrorText(res)))
		}
	}
	return nil
}

func datasetError(dataset, detail string) *schema.Error {
	return NewError(CodeInvalidArtifact, schema.StageScratchEnvironment,
		"The dataset the challenge expects is not available.",
		fmt.Sprintf("setup.dataset: %s: %s", dataset, detail))
}

// runReference executes the reference solution and enforces the execution
// properties the rest of the pipeline relies on: it ran, it finished, and it
// returned something.
func (p *pipeline) runReference(ctx context.Context, ex engine.Executor, art schema.Artifact) (engine.QueryResult, *schema.Error) {
	res, qerr := p.runStatement(ctx, ex, art.ReferenceSolution, schema.StageReferenceContract, CodeReferenceFailed, "reference solution")
	if qerr != nil {
		return res, qerr
	}
	if res.RowCount == 0 {
		return res, NewError(CodeReferenceFailed, schema.StageReferenceContract,
			"The reference solution returned no rows.", "an empty reference is not a usable challenge")
	}
	return res, nil
}

// runStatement runs one candidate statement for a named stage. The stage and
// code are parameters because the same execution failure means different things
// depending on which solution produced it: a reference that does not run is a
// contract failure, a cross-check that does not run is a disagreement.
func (p *pipeline) runStatement(ctx context.Context, ex engine.Executor, sql, stage, code, what string) (engine.QueryResult, *schema.Error) {
	res := engine.Execute(ctx, ex, sql, engine.DefaultLimits())
	if res.Status != "succeeded" {
		return res, NewError(code, stage, "The "+what+" did not run.", queryErrorText(res))
	}
	if res.Truncated {
		return res, NewError(code, stage, "The "+what+" returned a truncated result.",
			fmt.Sprintf("truncated at %d row(s)", res.RowCount))
	}
	return res, nil
}

// fixtures runs adversarial queries through the real evaluator. Each one must
// fail; a fixture that passes means the evaluator would accept an answer that
// is not the reference, which is a reason to reject the bundle rather than a
// reason to keep it.
func (p *pipeline) fixtures(ctx context.Context, ex engine.Executor, art schema.Artifact, expected eval.ExpectedResult, contract eval.Contract) *schema.Error {
	fixtures := p.defaultFixtures(art, expected)
	p.evidence.FixturesTotal = len(fixtures)
	p.evidence.FixturesRejected = 0
	for _, fx := range fixtures {
		resp := eval.Evaluate(ctx, ex, eval.Request{
			SQL: fx.SQL,
			Exercise: eval.Exercise{
				Contract: contract,
				Result:   &expected,
			},
		})
		if resp.Passed {
			return NewError(CodeInvalidArtifact, schema.StageAdversarialFixtures,
				"A wrong query was accepted by the evaluator.",
				fmt.Sprintf("adversarial_fixtures: the query %q passed", fx.Name))
		}
		p.evidence.FixturesRejected++
	}
	if len(fixtures) == 0 {
		return NewError(CodeInvalidArtifact, schema.StageAdversarialFixtures,
			"No adversarial fixtures were available to run.", "adversarial_fixtures: none")
	}
	return nil
}

// defaultFixtures derives the wrong queries every generated artifact must
// reject. They are composed here, from the contract and the reference text,
// because a wrong query that depends on the bundle's own wording would not be a
// property of the evaluator.
func (p *pipeline) defaultFixtures(art schema.Artifact, expected eval.ExpectedResult) []fixture {
	fixtures := []fixture{
		{Name: "wrong_columns", SQL: `SELECT 1 AS "ai_fixture_wrong_column"`},
		{Name: "constant_answer", SQL: constantFixture(art.ResultContract.Columns)},
	}
	// A LIMIT 1 variant only fails when there is more than one row to lose; on a
	// single-row reference it would be correct, and rejecting a correct bundle
	// for it would be the fixture's bug, not the bundle's.
	if len(expected.Rows) > 1 {
		if body := trimTrailingSemicolon(art.ReferenceSolution); body != "" {
			fixtures = append(fixtures, fixture{
				Name: "limit_one",
				SQL:  "SELECT * FROM (" + body + ") AS ai_fixture LIMIT 1",
			})
		}
	}
	return fixtures
}

func constantFixture(columns []string) string {
	parts := make([]string, 0, len(columns))
	for _, c := range columns {
		parts = append(parts, `'ai-fixture-constant' AS `+quoteIdent(c))
	}
	return "SELECT " + strings.Join(parts, ", ")
}

// provenance attributes an artifact or a reply to the provider that produced it.
// Every stamp site shares the constructor, so a new field lands in one place.
func provenance(desc provider.ProviderDescriptor, datasetVersion, repairs int) schema.Provenance {
	return schema.Provenance{
		Provider:         desc.Name,
		Model:            desc.Model,
		ProviderVersion:  desc.Version,
		PipelineVersion:  schema.PipelineVersion,
		EvaluatorVersion: eval.EvaluatorVersion,
		DatasetVersion:   datasetVersion,
		CreatedAt:        time.Now().UTC(),
		Repairs:          repairs,
	}
}

// activate is step 9: stamp provenance, store the artifact as active, and only
// then tell the caller. A bundle that never reaches this function is never
// written to the depot as active.
func (p *pipeline) activate(ctx context.Context, art schema.Artifact) schema.Outcome {
	start := time.Now()
	p.begin(schema.StageActivate)
	desc := p.svc.cfg.Provider.Descriptor()
	if art.ID == "" {
		art.ID = fmt.Sprintf("gen-%016x", hashPrompt(art.ReferenceSolution))
	}
	if art.Version < 1 {
		art.Version = 1
	}
	if art.Dialect == "" {
		art.Dialect = "postgresql"
	}
	if art.Type == "" {
		art.Type = "write"
	}
	art.Provenance = provenance(desc, p.svc.cfg.DatasetVersion, p.repairs)
	p.evidence.Repairs = p.repairs

	out := p.outcome(schema.StatusActive, &art, nil)
	if err := p.svc.cfg.Depot.SaveChallenge(ctx, schema.Record{
		Artifact:  art,
		Outcome:   out,
		Status:    schema.StatusActive,
		CreatedAt: art.Provenance.CreatedAt,
	}); err != nil {
		e := NewError(CodeUnavailable, schema.StageActivate,
			"The challenge passed every check but could not be stored.", engine.RedactSecrets(err.Error()))
		p.timed(schema.StageActivate, schema.StateFailed, e.Message, start)
		return p.failedOutcome(schema.StatusFailed, e)
	}
	p.timed(schema.StageActivate, schema.StateDone, "stored as active", start)
	return out
}
