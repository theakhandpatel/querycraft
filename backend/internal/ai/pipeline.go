package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
)

// The §11.1 pipeline. Nothing becomes visible to a learner before activate, and
// activate is only reached after every stage passed against a real engine.
//
// Stage reporting policy: the outcome always lists all nine stages in order,
// whatever happened, and every one carries a non-empty Detail. A stage the run
// never reached is reported as "skipped" with the reason, never omitted, so a
// reader can tell "this check did not run" from "this check ran and passed" —
// the two look identical in an empty list.

// stageOrder is the pipeline's fixed sequence. The repair stage sits at its
// declared position but runs whenever a stage fails, which is why the reported
// slice is a fixed layout rather than an append log.
var stageOrder = []string{
	StageValidateRequest,
	StageRequestBundle,
	StageValidateArtifact,
	StageScratchEnvironment,
	StageReferenceContract,
	StageCrossCheck,
	StageAdversarialFixtures,
	StageRepair,
	StageActivate,
}

// pipeline carries the run's mutable state. One pipeline serves one generation
// request; it is not shared and needs no locking.
type pipeline struct {
	svc      *Service
	req      GenerationRequest
	limits   Limits
	stages   []Stage
	index    map[string]int
	attempts []Attempt
	evidence Evidence
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
	// The course agent's live facts. A chapter is written over minutes, and a
	// poller has to be able to say which chapter is being written and how many
	// have already verified; none of that is derivable from the stage list,
	// which is why it travels separately. planned is 0 for a challenge run.
	planned        int
	verified       int
	currentChapter string
	courseID       string
	// report, when set, receives a live snapshot on every transition. It is the
	// only reason a run can be watched at all; a pipeline with no report is the
	// pipeline that existed before jobs did.
	report func(Progress)
	// chapters is the course agent's resolved chapter report so far, in the
	// course's order. A challenge run never sets it and publishes an empty list.
	chapters []ChapterResult
}

func newPipeline(svc *Service, req GenerationRequest, limits Limits) *pipeline {
	p := &pipeline{svc: svc, req: req, limits: limits, index: make(map[string]int, len(stageOrder))}
	for _, name := range stageOrder {
		p.index[name] = len(p.stages)
		p.stages = append(p.stages, Stage{Name: name, State: StateSkipped})
	}
	return p
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
// "running" forever — a claim about a stage that never finished.
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
func (p *pipeline) setChapter(id string) {
	p.currentChapter = id
	p.notify()
}

// setVerified records how many chapters have verified so far.
func (p *pipeline) setVerified(n int) {
	p.verified = n
	p.notify()
}

// snapshotStages copies the stage list without finalizing it. A building course
// stored mid-run carries the live picture; finalize() would write "the run
// stopped before this stage" into stages that simply have not happened yet,
// which is a claim about a run that is still going.
func (p *pipeline) snapshotStages() []Stage {
	out := make([]Stage, len(p.stages))
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
	stages := make([]Stage, len(p.stages))
	copy(stages, p.stages)
	for i := range stages {
		switch {
		case stages[i].Name != "" && stages[i].Name == p.current:
			stages[i].State = StateRunning
			stages[i].Detail = "running"
			stages[i].MS = engine.RoundMS(time.Since(p.currentStart))
		case stages[i].State == StateSkipped && stages[i].Detail == "":
			// Not reached yet: the run has not stopped before this stage, it
			// simply has not got there. "skipped" would be a lie a poller would
			// read as a final verdict.
			stages[i].State = StateQueued
			stages[i].Detail = "queued"
		}
	}
	attempts := make([]Attempt, len(p.attempts))
	copy(attempts, p.attempts)
	chapters := make([]ChapterResult, len(p.chapters))
	copy(chapters, p.chapters)
	p.report(Progress{
		Stages:         stages,
		Attempts:       attempts,
		Planned:        p.planned,
		Verified:       p.verified,
		CurrentChapter: p.currentChapter,
		CourseID:       p.courseID,
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

func (p *pipeline) outcome(status string, artifact *Artifact, err *Error) Outcome {
	p.finalize()
	// An empty slice, never nil: contracts/ai.schema.json requires `stages` and
	// `attempts` to be arrays, and encoding/json writes a nil slice as `null`.
	stages := p.stages
	if stages == nil {
		stages = []Stage{}
	}
	attempts := p.attempts
	if attempts == nil {
		attempts = []Attempt{}
	}
	return Outcome{
		Status:   status,
		Kind:     p.req.Kind,
		Artifact: artifact,
		Stages:   stages,
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
// it ran and refused, and "skipped" when the run stopped before reaching it —
// either because an earlier stage failed or because a repair was never needed.
func (p *pipeline) finalize() {
	stopped := ""
	for _, st := range p.stages {
		if st.State == StateFailed && stopped == "" {
			stopped = st.Name
		}
	}
	for i := range p.stages {
		if p.stages[i].Detail != "" {
			continue
		}
		switch {
		case p.stages[i].State == StateSkipped && stopped != "":
			p.stages[i].Detail = "skipped: " + stopped + " failed"
		case p.stages[i].Name == StageRepair && p.stages[i].State == StateSkipped:
			p.stages[i].Detail = "not needed: no stage failed"
		case p.stages[i].State == StateSkipped:
			p.stages[i].Detail = "skipped: the run stopped before this stage"
		case p.stages[i].State == StateDone:
			p.stages[i].Detail = "ran"
		default:
			p.stages[i].Detail = "failed"
		}
	}
}

// failedOutcome is the shape of every status that is not "active": an artifact
// is never returned, and an error is always present.
func (p *pipeline) failedOutcome(status string, err *Error) Outcome {
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
func (s *Service) generate(ctx context.Context, req GenerationRequest) Outcome {
	avail := s.Available()
	if !avail.Available {
		return s.unavailable(req.Kind, avail)
	}
	rec := newTranscriptRecorder(kindOf(req), runIDFrom(ctx), providerRedactor(s.cfg.Provider))
	ctx = WithTranscript(ctx, rec)
	out := s.generateRecorded(ctx, req)
	s.saveTranscript(ctx, rec, out)
	return out
}

// kindOf names the run's kind from the request, defaulting to challenge, the
// same default the stored records use.
func kindOf(req GenerationRequest) string {
	if req.Kind == RunKindCourse {
		return RunKindCourse
	}
	return RunKindChallenge
}

// saveTranscript stores the run's record. A failure here is logged and dropped:
// the transcript is evidence about a run, never the thing the run produces, so
// a store that cannot keep it must not turn a generated course into a failure.
func (s *Service) saveTranscript(ctx context.Context, rec *transcriptRecorder, out Outcome) {
	if rec == nil || s == nil || s.cfg.Depot == nil {
		return
	}
	if err := s.cfg.Depot.SaveRun(context.WithoutCancel(ctx), rec.finish(out.Status, out.Error)); err != nil && s.cfg.Logger != nil {
		s.cfg.Logger.Warn("the generation transcript could not be stored", "err", engine.RedactSecrets(err.Error()), "run", rec.id)
	}
}

// generateRecorded is the pipeline proper, run with the transcript recorder
// already attached.
func (s *Service) generateRecorded(ctx context.Context, req GenerationRequest) Outcome {
	// A course is a different sequence of work over the same stages: one plan,
	// one environment, every exercise verified in order. It shares the
	// pipeline's state, its repair budget and its evidence rather than
	// duplicating any of it.
	if req.Kind == "course" {
		return s.generateCourse(ctx, req)
	}
	limits := s.cfg.Limits.Merge(DefaultLimits())
	runCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	p := newPipeline(s, req, limits)
	p.watch(ctx)

	start := time.Now()
	p.begin(StageValidateRequest)
	if verr := validateRequest(req, limits); verr != nil {
		p.timed(StageValidateRequest, StateFailed, verr.Detail, start)
		return p.failedOutcome(StatusFailed, verr)
	}
	req = normalizeRequest(req, limits)
	p.timed(StageValidateRequest, StateDone, requestSummary(req), start)

	prompt := requestPrompt(req)
	p.begin(StageRequestBundle)
	raw, perr := s.complete(runCtx, ProviderRequest{
		Kind:           kindOrChallenge(req.Kind),
		System:         systemPrompt(),
		Prompt:         prompt,
		Schema:         []byte(challengeSchema),
		MaxOutputBytes: limits.MaxOutputBytes,
	})
	if perr != nil {
		return p.failedOutcome(StatusFailed, perr)
	}
	p.timed(StageRequestBundle, StateDone, "candidate bundle received", start)

	var (
		artifact Artifact
		lastErr  *Error
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
				return p.failedOutcome(StatusFailed, lastErr)
			}
			if p.repairs >= limits.MaxRepairs {
				return p.exhausted(lastErr)
			}
			p.attempts = append(p.attempts, Attempt{
				Number: p.repairs + 1,
				Stage:  lastErr.Stage,
				Detail: strings.TrimSpace(lastErr.Message + " " + lastErr.Detail),
			})
			p.begin(StageRepair)
			repaired, rerr := s.complete(runCtx, ProviderRequest{
				Kind:           "repair",
				System:         systemPrompt(),
				Prompt:         prompt + "\n\nA previous bundle failed at the " + lastErr.Stage + " stage.",
				Schema:         []byte(challengeSchema),
				MaxOutputBytes: limits.MaxOutputBytes,
				Previous:       raw,
				Failure:        lastErr.Stage + ": " + lastErr.Message + "; " + lastErr.Detail,
			})
			if rerr != nil {
				return p.failedOutcome(StatusFailed, rerr)
			}
			p.repairs++
			raw = repaired
			p.mark(StageRepair, StateDone, fmt.Sprintf("attempt %d requested after %s failed", p.repairs, lastErr.Stage))
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

// complete performs one provider call. A provider that is configured but
// unreachable is AI_UNAVAILABLE: that is not a content problem and no repair
// can fix it. The detail is redacted because a provider error can quote a URL
// or a credential.
// completeRaw performs one provider call and maps a transport failure onto the
// envelope. It is the tool-calling counterpart of complete: the reply is
// returned whole, because a tool-calling turn carries its answer in ToolCalls
// and has no text to return.
func (s *Service) completeRaw(ctx context.Context, req ProviderRequest) (ProviderResponse, *Error) {
	rec := transcriptFrom(ctx)
	rec.recordRequestMessages(req)
	resp, err := s.cfg.Provider.Complete(ctx, req)
	if err != nil {
		rec.recordError(err)
		return ProviderResponse{}, newError(CodeUnavailable, "",
			"The AI provider could not produce a bundle.", engine.RedactSecrets(err.Error()))
	}
	rec.recordResponse(resp)
	if req.MaxOutputBytes > 0 && len(resp.Text) > req.MaxOutputBytes {
		return ProviderResponse{}, artifactError("bundle", fmt.Sprintf("reply of %d bytes exceeds the %d-byte limit", len(resp.Text), req.MaxOutputBytes))
	}
	return resp, nil
}

func (s *Service) complete(ctx context.Context, req ProviderRequest) (string, *Error) {
	rec := transcriptFrom(ctx)
	rec.recordRequestMessages(req)
	resp, err := s.cfg.Provider.Complete(ctx, req)
	if err != nil {
		rec.recordError(err)
		return "", newError(CodeUnavailable, "",
			"The AI provider could not produce a bundle.", engine.RedactSecrets(err.Error()))
	}
	rec.recordResponse(resp)
	if req.MaxOutputBytes > 0 && len(resp.Text) > req.MaxOutputBytes {
		return "", artifactError("bundle", fmt.Sprintf("reply of %d bytes exceeds the %d-byte limit", len(resp.Text), req.MaxOutputBytes))
	}
	return resp.Text, nil
}

// agentTimeout is the run-level timeout as an error, so the course agent can
// hand it back up and the run publishes it through the same failedOutcome the
// challenge path uses. It says the same thing timeout() says.
func (p *pipeline) agentTimeout(last *Error) *Error {
	stage := StageRequestBundle
	if last != nil {
		stage = last.Stage
	}
	return newError(CodeUnavailable, stage,
		fmt.Sprintf("Generation exceeded the %s time limit.", p.limits.Timeout),
		"the run was stopped before it could finish; nothing was activated")
}

func (p *pipeline) timeout(last *Error) Outcome {
	e := newError(CodeUnavailable, last.Stage,
		fmt.Sprintf("Generation exceeded the %s time limit.", p.limits.Timeout),
		"the run was stopped before it could finish; nothing was activated")
	return p.failedOutcome(StatusFailed, e)
}

// exhausted turns the last content failure into the rejected outcome. The code
// is the most specific one available: a disagreement between two independently
// produced solutions is reported as a cross-check failure because that is the
// actionable fact, and only a bundle that never became runnable is reported as
// an exhausted repair budget. Either way the message names the stage that
// refused and why, so a human can act on it.
func (p *pipeline) exhausted(last *Error) Outcome {
	code := CodeRepairExhausted
	switch last.Stage {
	case StageCrossCheck:
		code = CodeCrossCheckFailed
	case StageReferenceContract:
		code = CodeReferenceFailed
	}
	e := newError(code, last.Stage,
		fmt.Sprintf("Repairs exhausted after %d attempt(s); the challenge was rejected at the %s stage: %s",
			p.repairs, last.Stage, last.Message),
		last.Detail)
	p.evidence.Repairs = p.repairs
	return p.failedOutcome(StatusRejected, e)
}

// decodeBundle parses one provider reply. Provider output is untrusted input,
// so a malformed reply is a policy failure with the field named, never a panic.
func decodeBundle(raw string, a *Artifact) *Error {
	if strings.TrimSpace(raw) == "" {
		return artifactError("bundle", "the provider returned an empty reply")
	}
	if err := decodeJSON(raw, a); err != nil {
		return newError(CodeInvalidArtifact, StageRequestBundle,
			"The provider's reply is not a usable bundle.", "bundle: "+err.Error())
	}
	return nil
}

// verify runs every stage that executes SQL and returns the first failure. It
// creates one throwaway environment for the attempt and always releases it.
func (p *pipeline) verify(ctx context.Context, art Artifact) *Error {
	start := time.Now()
	p.begin(StageValidateArtifact)
	if verr := validateArtifact(art, p.limits); verr != nil {
		p.timed(StageValidateArtifact, StateFailed, verr.Detail, start)
		return verr
	}
	p.timed(StageValidateArtifact, StateDone, fmt.Sprintf("%d hints, %d setup statement(s)", len(art.Hints), len(art.Setup.Statements)), start)

	if p.svc.cfg.Runner == nil {
		e := newError(CodeEnvironmentDown, StageScratchEnvironment, "No database is available to verify against.", "")
		p.mark(StageScratchEnvironment, StateFailed, e.Message)
		return e
	}
	start = time.Now()
	p.begin(StageScratchEnvironment)
	scratch := scratchName(art)
	ex, release, err := p.svc.cfg.Runner.Scratch(ctx, scratch)
	if err != nil {
		e := newError(CodeEnvironmentDown, StageScratchEnvironment,
			"A throwaway environment could not be created.", engine.RedactSecrets(err.Error()))
		p.timed(StageScratchEnvironment, StateFailed, e.Detail, start)
		return e
	}
	defer release()
	// The Runner seam exposes the environment *name*, not the physical
	// database, so this is the name the pipeline asked for. It is recorded
	// because "which environment did this run in" is the first question a
	// rejected run raises.
	p.evidence.ScratchDatabase = scratch

	if serr := p.prepare(ctx, ex, art); serr != nil {
		p.timed(StageScratchEnvironment, StateFailed, serr.Detail, start)
		return serr
	}
	p.timed(StageScratchEnvironment, StateDone, fmt.Sprintf("%d setup statement(s) applied; dataset %q readable", len(art.Setup.Statements), art.Setup.Dataset), start)

	return p.verifyInEnv(ctx, ex, art)
}

// verifyInEnv runs the three stages that execute SQL — reference contract,
// cross-check, adversarial fixtures — against an environment the caller owns,
// and records what they measured in p.evidence. It is split out of verify
// because a course verifies many exercises in one environment: the challenge
// path creates that environment, and the course path creates it once.
func (p *pipeline) verifyInEnv(ctx context.Context, ex engine.Executor, art Artifact) *Error {
	start := time.Now()
	contract := evalContract(art.ResultContract)
	start = time.Now()
	p.begin(StageReferenceContract)
	refQR, refErr := p.runReference(ctx, ex, art)
	if refErr != nil {
		p.timed(StageReferenceContract, StateFailed, refErr.Detail, start)
		return refErr
	}
	expected := eval.ExpectedResult{Columns: contract.Columns, Rows: refQR.Rows}
	p.lastExpected = &expected
	cmp := eval.CompareDetailed(expected, contract, refQR)
	if !cmp.OK || !cmp.ColumnsOK {
		e := newError(CodeReferenceFailed, StageReferenceContract,
			"The reference solution does not satisfy its own result contract.",
			"columns or rows did not match: "+strings.Join(cmp.Codes, ", "))
		p.timed(StageReferenceContract, StateFailed, e.Detail, start)
		return e
	}
	p.evidence.ReferenceRows = refQR.RowCount
	p.timed(StageReferenceContract, StateDone, fmt.Sprintf("reference returned %d row(s)", refQR.RowCount), start)

	// Cross-check: a second, independently produced solution must agree with
	// the first under the same contract. This catches some contract errors. It
	// is not proof of correctness, because both solutions can share a mistake.
	start = time.Now()
	p.begin(StageCrossCheck)
	crossQR, crossErr := p.runStatement(ctx, ex, art.CrossCheckSolution, StageCrossCheck, CodeCrossCheckFailed, "cross-check solution")
	if crossErr != nil {
		p.timed(StageCrossCheck, StateFailed, crossErr.Detail, start)
		return crossErr
	}
	if crossCmp := eval.CompareDetailed(expected, contract, crossQR); !crossCmp.OK {
		e := newError(CodeCrossCheckFailed, StageCrossCheck,
			"The two solutions disagree under the result contract.",
			"codes: "+strings.Join(crossCmp.Codes, ", "))
		p.timed(StageCrossCheck, StateFailed, e.Detail, start)
		return e
	}
	p.evidence.CrossCheckRows = crossQR.RowCount
	p.timed(StageCrossCheck, StateDone, fmt.Sprintf("cross-check agreed on %d row(s)", crossQR.RowCount), start)

	start = time.Now()
	p.begin(StageAdversarialFixtures)
	if ferr := p.fixtures(ctx, ex, art, expected, contract); ferr != nil {
		p.timed(StageAdversarialFixtures, StateFailed, ferr.Detail, start)
		return ferr
	}
	p.timed(StageAdversarialFixtures, StateDone,
		fmt.Sprintf("%d/%d wrong queries were rejected", p.evidence.FixturesRejected, p.evidence.FixturesTotal), start)
	return nil
}

// prepare applies the setup plan and checks the dataset it refers to. For a
// challenge over the sealed dataset the statement list is empty, and the check
// is the point: a bundle that names a dataset this build does not ship must
// fail before anything runs.
func (p *pipeline) prepare(ctx context.Context, ex engine.Executor, art Artifact) *Error {
	for i, stmt := range art.Setup.Statements {
		res := engine.Execute(ctx, ex, stmt, engine.DefaultLimits())
		if res.Status != "succeeded" || res.Truncated {
			return newError(CodeInvalidArtifact, StageScratchEnvironment,
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
func (p *pipeline) checkDataset(ctx context.Context, ex engine.Executor, setup SetupPlan) *Error {
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

func datasetError(dataset, detail string) *Error {
	return newError(CodeInvalidArtifact, StageScratchEnvironment,
		"The dataset the challenge expects is not available.",
		fmt.Sprintf("setup.dataset: %s: %s", dataset, detail))
}

// runReference executes the reference solution and enforces the execution
// properties the rest of the pipeline relies on: it ran, it finished, and it
// returned something.
func (p *pipeline) runReference(ctx context.Context, ex engine.Executor, art Artifact) (engine.QueryResult, *Error) {
	res, qerr := p.runStatement(ctx, ex, art.ReferenceSolution, StageReferenceContract, CodeReferenceFailed, "reference solution")
	if qerr != nil {
		return res, qerr
	}
	if res.RowCount == 0 {
		return res, newError(CodeReferenceFailed, StageReferenceContract,
			"The reference solution returned no rows.", "an empty reference is not a usable challenge")
	}
	return res, nil
}

// runStatement runs one candidate statement for a named stage. The stage and
// code are parameters because the same execution failure means different things
// depending on which solution produced it: a reference that does not run is a
// contract failure, a cross-check that does not run is a disagreement.
func (p *pipeline) runStatement(ctx context.Context, ex engine.Executor, sql, stage, code, what string) (engine.QueryResult, *Error) {
	res := engine.Execute(ctx, ex, sql, engine.DefaultLimits())
	if res.Status != "succeeded" {
		return res, newError(code, stage, "The "+what+" did not run.", queryErrorText(res))
	}
	if res.Truncated {
		return res, newError(code, stage, "The "+what+" returned a truncated result.",
			fmt.Sprintf("truncated at %d row(s)", res.RowCount))
	}
	return res, nil
}

// fixtures runs adversarial queries through the real evaluator. Each one must
// fail; a fixture that passes means the evaluator would accept an answer that
// is not the reference, which is a reason to reject the bundle rather than a
// reason to keep it.
func (p *pipeline) fixtures(ctx context.Context, ex engine.Executor, art Artifact, expected eval.ExpectedResult, contract eval.Contract) *Error {
	fixtures := p.defaultFixtures(art, expected)
	if p.svc.cfg.AdversarialFixtures != nil {
		if custom := p.svc.cfg.AdversarialFixtures(art); len(custom) > 0 {
			fixtures = custom
		}
	}
	p.evidence.FixturesTotal = len(fixtures)
	p.evidence.FixturesRejected = 0
	for _, fx := range fixtures {
		resp := eval.Evaluate(ctx, ex, eval.Request{
			SQL: fx.SQL,
			Exercise: eval.Exercise{
				Prompt:   art.Prompt,
				Contract: contract,
				Result:   &expected,
			},
		})
		if resp.Passed {
			return newError(CodeInvalidArtifact, StageAdversarialFixtures,
				"A wrong query was accepted by the evaluator.",
				fmt.Sprintf("adversarial_fixtures: the query %q passed", fx.Name))
		}
		p.evidence.FixturesRejected++
	}
	if len(fixtures) == 0 {
		return newError(CodeInvalidArtifact, StageAdversarialFixtures,
			"No adversarial fixtures were available to run.", "adversarial_fixtures: none")
	}
	return nil
}

// defaultFixtures derives the wrong queries every generated artifact must
// reject. They are composed here, from the contract and the reference text,
// because a wrong query that depends on the bundle's own wording would not be a
// property of the evaluator.
func (p *pipeline) defaultFixtures(art Artifact, expected eval.ExpectedResult) []Fixture {
	fixtures := []Fixture{
		{Name: "wrong_columns", SQL: `SELECT 1 AS "ai_fixture_wrong_column"`},
		{Name: "constant_answer", SQL: constantFixture(art.ResultContract.Columns)},
	}
	// A LIMIT 1 variant only fails when there is more than one row to lose; on a
	// single-row reference it would be correct, and rejecting a correct bundle
	// for it would be the fixture's bug, not the bundle's.
	if len(expected.Rows) > 1 {
		if body := trimTrailingSemicolon(art.ReferenceSolution); body != "" {
			fixtures = append(fixtures, Fixture{
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

// activate is step 9: stamp provenance, store the artifact as active, and only
// then tell the caller. A bundle that never reaches this function is never
// written to the depot as active.
func (p *pipeline) activate(ctx context.Context, art Artifact) Outcome {
	start := time.Now()
	p.begin(StageActivate)
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
	datasetVersion := p.svc.cfg.DatasetVersion
	if datasetVersion < 1 {
		// The config field exists so this package need not import the dataset;
		// when the caller leaves it zero the artifact's own claim is the most
		// honest number available, and validateArtifact has already bounded it.
		datasetVersion = art.Setup.SchemaVersion
	}
	art.Provenance = Provenance{
		Provider:         desc.Name,
		Model:            desc.Model,
		ProviderVersion:  desc.Version,
		PipelineVersion:  PipelineVersion,
		EvaluatorVersion: eval.EvaluatorVersion,
		DatasetVersion:   datasetVersion,
		CreatedAt:        time.Now().UTC(),
		Repairs:          p.repairs,
	}
	p.evidence.Repairs = p.repairs

	out := p.outcome(StatusActive, &art, nil)
	if err := p.svc.cfg.Depot.SaveChallenge(ctx, Record{
		Artifact:  art,
		Outcome:   out,
		Status:    StatusActive,
		CreatedAt: art.Provenance.CreatedAt,
	}); err != nil {
		e := newError(CodeUnavailable, StageActivate,
			"The challenge passed every check but could not be stored.", engine.RedactSecrets(err.Error()))
		p.timed(StageActivate, StateFailed, e.Message, start)
		return p.failedOutcome(StatusFailed, e)
	}
	p.timed(StageActivate, StateDone, "stored as active", start)
	return out
}

/* ---------- request rendering ---------- */

// requestPrompt renders the validated request for a provider. It is plain text
// on purpose: the provider interface has no structured request, and a
// human-readable brief is the same thing a person would be handed.
func requestPrompt(req GenerationRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kind: %s\n", kindOrChallenge(req.Kind))
	if req.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", req.Title)
	}
	if len(req.Topics) > 0 {
		fmt.Fprintf(&b, "Topics: %s\n", strings.Join(nonEmpty(req.Topics), ", "))
	}
	fmt.Fprintf(&b, "Difficulty: %s\n", req.Difficulty)
	if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario: %s\n", req.Scenario)
	}
	for _, c := range req.Constraints {
		fmt.Fprintf(&b, "Constraint: %s\n", c)
	}
	fmt.Fprintf(&b, "Exercises: %d\n", req.ExerciseCount)
	b.WriteString("Design one exercise with a reference solution, an independent cross-check solution, and a result contract.")
	/* What the exercise will actually run against. The environment loads the
	   dataset before a single setup statement is applied, so a model that is not
	   told this writes `CREATE TABLE customers` and the run dies on "relation
	   already exists" — measured, after a repair had already been spent. The
	   allowed shapes are exactly what validateSetupStatement accepts, so the
	   model is not being asked to guess at a rule the gate will then apply. */
	b.WriteString(datasetBrief())
	b.WriteString("Do not create, alter or drop these tables.")
	b.WriteString("\nsetup.statements may only contain single CREATE TABLE or INSERT INTO statements that add extra scratch objects your exercise needs. Use an empty list when the dataset alone is enough. Do not use DROP, ALTER, UPDATE, DELETE or SET anywhere in them.")
	return b.String()
}

// datasetBrief names the tables and columns an exercise will run against,
// rendered from the shipped dataset rather than from a hand-written list.
//
// The brief used to say only "the commerce dataset", and a real model then wrote
// `o.order_id` for a column that is `o.id`: the run passed validate_artifact and
// the scratch environment and died at reference_contract, with the repair budget
// already spent. A model asked to write SQL against a schema has to be handed
// the schema; describing it here also keeps the two from drifting, since the
// column list comes from the same source of truth the evaluator runs against.
func datasetBrief() string {
	tables, ok := dataset.Tables("commerce")
	if !ok || len(tables.Tables) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThe exercise runs against the commerce dataset, already loaded. Write the reference and cross-check solutions with exactly these tables and column names:\n")
	for _, t := range tables.Tables {
		fmt.Fprintf(&b, "  %s(", t.Name)
		for i, c := range t.Cols {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strings.Join(nonEmpty(c), " "))
		}
		fmt.Fprintf(&b, ") — %d rows\n", t.Rows)
	}
	return b.String()
}

func requestSummary(req GenerationRequest) string {
	if req.Title != "" {
		return fmt.Sprintf("%s (%s, %d exercises)", req.Title, req.Difficulty, req.ExerciseCount)
	}
	return fmt.Sprintf("%s (%s, %d exercises)", strings.Join(nonEmpty(req.Topics), ", "), req.Difficulty, req.ExerciseCount)
}

func kindOrChallenge(kind string) string {
	if kind == "" {
		return "challenge"
	}
	return kind
}

func systemPrompt() string {
	return "You design SQL exercises over a fixed PostgreSQL commerce dataset. Reply with one JSON object and no prose. Solutions must be single read-only SELECT or WITH statements."
}

func scratchName(art Artifact) string {
	return fmt.Sprintf("ai_%016x", hashPrompt(art.ReferenceSolution))
}

func evalContract(rc ResultContract) eval.Contract {
	return eval.Contract{
		Columns:          rc.Columns,
		ColumnOrder:      rc.ColumnOrder,
		RowOrder:         rc.RowOrder,
		Duplicates:       rc.Duplicates,
		NumericTolerance: rc.NumericTolerance,
	}
}

func queryErrorText(res engine.QueryResult) string {
	if res.Error == nil {
		return res.Status
	}
	text := res.Error.Message
	if res.Error.Detail != nil && *res.Error.Detail != "" {
		text += " (" + *res.Error.Detail + ")"
	}
	return text
}

func trimTrailingSemicolon(sql string) string {
	return strings.TrimRight(strings.TrimSpace(sql), ";")
}

// quoteIdent quotes a PostgreSQL identifier. It is used only for identifiers
// that came from the server's own catalog or from our own contract, never for a
// value; values are always passed as bind parameters.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// probeStrings runs one parameterised read-only probe and returns the first
// column's values as strings. engine.Execute has no argument support, and
// interpolating a provider-supplied dataset name into SQL would be an
// injection, so this is a small dedicated helper. Values are formatted with
// fmt rather than a type switch because the only probes here return integers
// and text.
func probeStrings(ctx context.Context, ex engine.Executor, sql string, args ...any) ([]string, error) {
	rows, err := ex.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		if len(out) >= 1024 {
			break
		}
		vals, verr := rows.Values()
		if verr != nil {
			return nil, verr
		}
		if len(vals) == 0 {
			continue
		}
		out = append(out, valueString(vals[0]))
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, rerr
	}
	return out, nil
}

func valueString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
