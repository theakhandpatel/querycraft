/**
 * The generation-run reducer and its one entry effect.
 *
 * The run has one state object (`RunState`) and one phase tag. The reducer is
 * pure: it owns the steps, the stream and the cards the screen renders, and it
 * never touches a timer, a clock or the network. The effect owns every side
 * effect: the abort controller, the pacing, the submit/approve/delete calls,
 * the job poll and the 700 ms navigation. The component dispatches actions and
 * renders, and calls `approve` / `discard` when the learner decides.
 */

import { useCallback, useEffect, useLayoutEffect, useReducer, useRef, useState } from 'react';
import { useStore } from '../../state/store';
import type {
  AiCourseBuildAnswer,
  AiCourseRecord,
  AiJob,
  AiJobHandle,
  AiJobStage,
  AiOutcome,
} from '../../api/aiContract';
import type { Course } from '../../types';
import { authoredReport, courseFromPlan, reportFromOutcome, type DraftReport } from '../../lib/draft';
import { courseExerciseTotal, exercisesWithReference } from '../../lib/model';
import { executionEngineLabel } from '../../lib/provenance';
import { poll, settlePhase, type LogLine, type LogLineDraft } from '../../lib/generationPoll';
import { runPipeline } from '../../lib/pipeline';
import { sleep } from '../../lib/sleep';
import { jobIsRunning, outcomeFromJob } from '../../api/ai';

/* The checklist is the pipeline's report, not a script.
 *
 * Each step names the gateway stages whose success turns it "Done", so a step
 * is never Done before something said so. The previous version advanced a step
 * every 420 ms and had four of the six Done before the request had even
 * answered, which is the same "it is still a mock" problem as a fabricated row
 * count: the screen claimed work that nobody had reported. Step 0 is the one
 * step the client performs itself, and it says so. */
export const PIPE_STEPS: { title: string; small: string; stages: readonly string[] }[] = [
  {
    title: 'Read your request',
    small: 'The goal and the context you gave, taken as written — nothing is inferred here',
    stages: [],
  },
  {
    title: 'Design the progression',
    small: 'Order concepts from prerequisites to independent problem solving',
    stages: ['validate_request', 'request_bundle', 'validate_artifact'],
  },
  {
    title: 'Build the practice environment',
    small: 'A real scratch database holding the seeded dataset',
    stages: ['scratch_environment'],
  },
  {
    title: 'Execute and verify every exercise',
    small: 'References, independent cross-checks, adversarial fixtures',
    stages: ['reference_contract', 'cross_check', 'adversarial_fixtures'],
  },
  {
    title: 'Store the course you can inspect',
    small: 'With the assumptions and the validation findings kept beside it',
    stages: ['activate'],
  },
];

type StepState = 'idle' | 'running' | 'done';

export type Phase =
  | 'queued'
  | 'pacing'
  | 'submitting'
  | 'generating'
  | 'watching'
  | 'awaiting'
  | 'approving'
  | 'discarding'
  | 'verifying'
  | 'drafting'
  | 'finishing'
  | 'failed';

export type Failure = {
  title: string;
  detail: string;
  exercises: { id: string; title: string; stage: string; message: string; detail?: string | null }[];
};

export interface RunState {
  phase: Phase;
  log: LogLine[];
  steps: StepState[];
  built: Course | null;
  report: DraftReport | null;
  generated: AiOutcome | null;
  job: AiJob | null;
  recordStatus: string | null;
  awaiting: AiOutcome | null;
  failure: Failure | null;
}

type Action =
  | { type: 'start'; resuming: boolean }
  | { type: 'paced'; t: string; draft: LogLineDraft }
  | { type: 'pacingDone'; available: boolean }
  | { type: 'stageReport'; stages: readonly AiJobStage[] }
  | { type: 'jobView'; t: string; view: AiJob; drafts: readonly LogLineDraft[] }
  | { type: 'adopt'; course: Course; status: string }
  | { type: 'submitted'; handle: AiJobHandle | null }
  | { type: 'settled'; t: string; outcome: AiOutcome; report: DraftReport; adopted: boolean; goal: string }
  | { type: 'approving'; t: string; courseId: string }
  | { type: 'approvalFailed'; t: string; message: string }
  | { type: 'discarding' }
  | { type: 'discarded'; t: string }
  | { type: 'authored'; phase: 'verifying'; t: string; course: Course; draft: LogLineDraft }
  | { type: 'authored'; phase: 'drafting' }
  | { type: 'authoredDone'; t: string; report: DraftReport }
  | { type: 'log'; t: string; draft: LogLineDraft }
  | { type: 'markStep'; index: number; step: StepState }
  | { type: 'failed'; t: string; failure: Failure; drafts?: readonly LogLineDraft[] }
  | { type: 'pollMissed'; t: string; failure: Failure; draft: LogLineDraft }
  | { type: 'pollTimedOut'; t: string; failure: Failure; draft: LogLineDraft }
  | { type: 'finish' }
  | { type: 'reset' };

/* How often a running job is read. The gateway's own stage list is what makes
    the checklist move, so this is a read of a small document, not a poll for a
    verdict. */
const JOB_POLL_MS = 1200;

/* The job a resumed build is running under, keyed by course id. A resume is a
   POST, and StrictMode mounts the run effect twice: the first mount sends the
   POST and is torn down, so the handle is kept here and the second mount
   watches the same build instead of starting a second one. */
const resumedBuildJobs = new Map<string, AiJobHandle>();

/** Marks a step Done only when every stage it names reported done, and Working
    once one of them has. A step whose stage failed is left where it was: the
    failure itself is printed in the transcript and on the failure card. */
function applyStages(steps: StepState[], stages: readonly { name: string; state: string }[]): StepState[] {
  const byName = new Map(stages.map((stage) => [stage.name, stage.state]));
  let next = steps;
  PIPE_STEPS.forEach((step, i) => {
    if (step.stages.length === 0) return;
    const reported = step.stages.map((name) => byName.get(name));
    if (reported.some((state) => state === 'failed')) return;
    if (reported.every((state) => state === 'done' || state === 'skipped')) next = setStep(next, i, 'done');
    else if (reported.some((state) => state === 'done')) next = setStep(next, i, 'running');
  });
  return next;
}

function setStep(steps: StepState[], i: number, state: StepState): StepState[] {
  if (steps[i] === state) return steps;
  const next = steps.slice();
  next[i] = state;
  return next;
}

function append(log: LogLine[], t: string, drafts: readonly LogLineDraft[]): LogLine[] {
  if (drafts.length === 0) return log;
  return [...log, ...drafts.map((draft) => ({ t, ...draft }))];
}

function stageLines(
  stages: readonly { name: string; state: string; detail?: string; ms?: number }[]
): LogLineDraft[] {
  return stages.map((stage) => {
    const glyph = stage.state === 'done' ? '✓' : stage.state === 'failed' ? '✕' : '–';
    const tone = stage.state === 'done' ? 'ok' : stage.state === 'failed' ? 'fail' : 'hi';
    const ms = stage.ms !== undefined ? ` · ${stage.ms} ms` : '';
    return { tone, glyph, text: [`${stage.name}${ms}${stage.detail ? ` — ${stage.detail}` : ''}`] };
  });
}

function initialRunState(): RunState {
  return {
    phase: 'queued',
    log: [],
    steps: PIPE_STEPS.map(() => 'idle'),
    built: null,
    report: null,
    generated: null,
    job: null,
    recordStatus: null,
    awaiting: null,
    failure: null,
  };
}

function reducer(state: RunState, action: Action): RunState {
  switch (action.type) {
    case 'start':
      return action.resuming
        ? { ...state, phase: 'submitting', steps: setStep(setStep(state.steps, 0, 'done'), 1, 'running') }
        : { ...state, phase: 'pacing' };

    case 'paced':
      return { ...state, log: append(state.log, action.t, [action.draft]) };

    case 'pacingDone':
      return {
        ...state,
        phase: 'submitting',
        steps: action.available ? setStep(setStep(state.steps, 0, 'done'), 1, 'running') : setStep(state.steps, 0, 'done'),
      };

    case 'stageReport':
      return { ...state, steps: applyStages(state.steps, action.stages) };

    case 'jobView':
      return { ...state, job: action.view, log: append(state.log, action.t, action.drafts) };

    case 'adopt':
      return { ...state, built: action.course, recordStatus: action.status };

    case 'submitted':
      return { ...state, phase: action.handle ? 'watching' : 'generating' };

    case 'settled': {
      const { outcome, report, adopted, goal } = action;
      let steps = applyStages(state.steps, outcome.stages);
      const drafts: LogLineDraft[] = stageLines(outcome.stages);
      for (const attempt of outcome.attempts) {
        drafts.push({ tone: 'hi', glyph: `attempt ${attempt.number}`, text: [`${attempt.stage} — ${attempt.detail}`] });
      }
      if (outcome.error) {
        drafts.push({
          tone: 'fail',
          glyph: '✕',
          text: [`generation ${outcome.status} · ${outcome.error.code} — ${outcome.error.message}`],
        });
      }

      const chosen = settlePhase(outcome);
      let phase: Phase;
      let failure: Failure | null = null;
      let awaiting: AiOutcome | null = null;
      let built = state.built;
      let recordStatus = state.recordStatus;

      if (chosen === 'awaiting') {
        const plan = outcome.plan;
        if (!plan) {
          phase = 'failed';
          failure = {
            title: 'The outline could not be read',
            detail: 'The gateway paused the run but the stored outline did not come back. Nothing was written.',
            exercises: [],
          };
        } else {
          awaiting = outcome;
          recordStatus = 'building';
          built = state.built ?? courseFromPlan(plan, goal);
          steps = setStep(setStep(steps, 1, 'done'), 2, 'idle');
          const count = plan.chapters.length;
          drafts.push({
            tone: 'hi',
            glyph: '→',
            text: [
              'the outline is stored: ',
              { text: String(count), tone: 'hi' },
              ` chapter${count === 1 ? '' : 's'} planned — approve it to write them, or discard it`,
            ],
          });
          for (const ch of plan.chapters) drafts.push({ text: [`\u00a0\u00a0${ch.id} — ${ch.title}`] });
          drafts.push({ caret: true, text: [] });
          phase = 'awaiting';
        }
      } else if (chosen === 'finishing' && adopted && outcome.plan) {
        steps = setStep(steps, 4, 'running');
        const plan = outcome.plan;
        const chapterCount = plan.chapters.length;
        const exerciseCount = plan.chapters.reduce((n, c) => n + c.exercises.length, 0);
        drafts.push({
          tone: 'ok',
          glyph: '✓',
          text: [
            `${chapterCount} chapter${chapterCount === 1 ? '' : 's'} · ${exerciseCount} exercise${
              exerciseCount === 1 ? '' : 's'
            }, each executed and cross-checked on the engine · ${report.totals.rows} rows measured`,
          ],
        });
        drafts.push({
          text: ['Draft ', { text: plan.title, tone: 'hi' }, ' ready to inspect — every exercise carries the rows the engine returned.'],
        });
        steps = setStep(steps, 4, 'done');
        drafts.push({ caret: true, text: [] });
        phase = 'finishing';
      } else if (chosen === 'finishing') {
        /* An active plan the app could not adopt: the gateway activated the
           course, the app did not add it. */
        phase = 'failed';
        failure = {
          title: 'The course could not be opened',
          detail: 'The gateway activated the course but the app could not add it. Nothing was saved to your learning.',
          exercises: [],
        };
      } else if (outcome.status === 'awaiting_approval') {
        phase = 'failed';
        failure = {
          title: 'The outline could not be read',
          detail: 'The gateway paused the run but the stored outline did not come back. Nothing was written.',
          exercises: [],
        };
      } else {
        const failedEx = report.chapters.flatMap((c) => c.failures.map((f) => ({ ...f })));
        const refusedAt = outcome.error?.stage ?? 'verification';
        for (const f of failedEx) {
          drafts.push({ tone: 'fail', glyph: '✕', text: [`${f.id} · ${f.stage} — ${f.message}`] });
        }
        steps = setStep(steps, 4, 'idle');
        drafts.push({ caret: true, text: [] });
        failure = {
          title:
            outcome.status === 'unavailable'
              ? 'Generation is not available'
              : outcome.error?.code === 'AI_UNAVAILABLE'
                ? 'The generation service did not answer'
                : `The course was rejected at the ${refusedAt} stage`,
          detail: outcome.error
            ? `${outcome.error.message}${outcome.error.detail ? ` — ${outcome.error.detail}` : ''}`
            : 'The gateway refused this course; the exercises below are the ones it could not verify.',
          exercises: failedEx,
        };
        phase = 'failed';
      }

      return {
        ...state,
        phase,
        steps,
        awaiting,
        generated: outcome,
        report,
        built,
        recordStatus,
        failure,
        log: append(state.log, action.t, drafts),
      };
    }

    case 'approving':
      return {
        ...state,
        phase: 'approving',
        awaiting: null,
        steps: setStep(setStep(state.steps, 1, 'done'), 2, 'running'),
        log: append(state.log, action.t, [
          {
            tone: 'hi',
            glyph: '→',
            text: ['approval sent — writing every chapter of ', { text: action.courseId, tone: 'mono' }],
          },
        ]),
      };

    case 'approvalFailed':
      return {
        ...state,
        phase: 'failed',
        failure: {
          title: 'The course could not be built',
          detail:
            'The gateway did not accept the approval. The outline is still stored, so you can approve it again or discard it.',
          exercises: [],
        },
        log: append(state.log, action.t, [
          { tone: 'fail', glyph: '✕', text: [`the approval could not be sent — ${action.message}`] },
        ]),
      };

    case 'discarding':
      return { ...state, phase: 'discarding' };

    case 'discarded':
      return {
        ...state,
        awaiting: null,
        built: null,
        log: append(state.log, action.t, [
          { tone: 'hi', glyph: 'discarded', text: ['the outline and the course were removed'] },
        ]),
      };

    case 'authored':
      if (action.phase === 'verifying') {
        return {
          ...state,
          phase: 'verifying',
          built: action.course,
          steps: setStep(setStep(setStep(state.steps, 1, 'done'), 2, 'done'), 3, 'running'),
          log: append(state.log, action.t, [action.draft]),
        };
      }
      return { ...state, phase: 'drafting', steps: setStep(state.steps, 4, 'running') };

    case 'authoredDone':
      return {
        ...state,
        phase: 'finishing',
        report: action.report,
        steps: setStep(state.steps, 4, 'done'),
        log: append(state.log, action.t, [{ caret: true, text: [] }]),
      };

    case 'log':
      return { ...state, log: append(state.log, action.t, [action.draft]) };

    case 'markStep':
      return { ...state, steps: setStep(state.steps, action.index, action.step) };

    case 'failed':
      return {
        ...state,
        phase: 'failed',
        failure: action.failure,
        log: append(state.log, action.t, action.drafts ?? []),
      };

    case 'pollMissed':
      return {
        ...state,
        phase: 'failed',
        failure: action.failure,
        log: append(state.log, action.t, [action.draft]),
      };

    case 'pollTimedOut':
      return {
        ...state,
        phase: 'failed',
        failure: action.failure,
        log: append(state.log, action.t, [action.draft]),
      };

    case 'finish':
      return { ...state, phase: 'finishing' };

    case 'reset':
      return initialRunState();
  }
}

/* The only lines paced by the client, because they describe the client's own
   two acts: quoting the request it was given, and asking. Everything after them
   is the gateway's transcript, printed with the durations it measured. */
function pacedLines(goal: string, level: string, ask: string): LogLineDraft[] {
  const excerpt = (text: string): string => {
    const flat = text.replace(/\s+/g, ' ').trim();
    if (!flat) return '';
    return flat.length > 72 ? `${flat.slice(0, 71)}…` : flat;
  };
  const note = excerpt(level);
  return [
    { text: ['Reading your goal → “', { text: excerpt(goal) || '(no goal given)', tone: 'hi' }, '”'] },
    note
      ? { text: ['Your note on level and context → “', { text: note, tone: 'hi' }, '”'] }
      : { text: ['No note on level and context was given, so the draft is asked to justify its own starting point'] },
    { text: ['Asking ', { text: ask, tone: 'hi' }, ' — a whole course is one large answer, so this takes a minute or more'] },
  ];
}

export function useGenerationRun() {
  const s = useStore();
  const [state, dispatch] = useReducer(reducer, undefined, initialRunState);
  const [logOpen, setLogOpen] = useState(true);
  const [runKey, setRunKey] = useState(0);

  /* The approval action and the discard action belong to one run (their abort
     signal, log and settle path), so the effect installs them here and the
     buttons call them. */
  const actionsRef = useRef<{ approve: (courseId: string) => void; discard: (courseId: string) => void } | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const navTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  /* The run effect owns its own lifecycle, so it sees the store as it was on
     the first frame. Availability arrives from the gateway a moment later, so it
     is read through a ref rather than captured. The write is a layout effect, not
     a render-body assignment: a discarded render must not move the ref, and a
     layout effect still runs before the passive effect below, so the timing the
     run depends on is unchanged. */
  const availabilityRef = useRef(s.aiAvailability);
  useLayoutEffect(() => {
    availabilityRef.current = s.aiAvailability;
  }, [s.aiAvailability]);

  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;
    const { signal } = controller;
    const t0 = performance.now();
    const stamp = (): string => `t+${((performance.now() - t0) / 1000).toFixed(1)}s`;
    const emit = (draft: LogLineDraft): void => dispatch({ type: 'log', t: stamp(), draft });
    const finish = (): void => {
      dispatch({ type: 'finish' });
      if (signal.aborted) return;
      navTimer.current = setTimeout(() => {
        navTimer.current = null;
        if (signal.aborted) return;
        s.setReviewTab('overview');
        s.showScreen('review');
      }, 700);
    };

    void (async () => {
      /* Pacing only: the four planning beats are instant on the server, so the
         shared pipeline primitive spreads them out. Its all-done event is
         ignored here — this screen finishes on measured work, not on a timer. */
      const availability = availabilityRef.current;
      /* A run this tab was already watching when the page went away. The
         gateway never stopped building it, so there is nothing to submit: the
         run is picked up where it got to, because starting it again is the one
         thing a resumed tab must never do. */
      const resuming = s.resumeRun;
      /* A stored course the library asked to resume. The gateway owns the
         outline and whatever chapters verified; this screen continues it the
         way an approval does, so nothing new is submitted. */
      const resumingBuild = s.resumeBuild;
      const goal = resuming ? resuming.goal : resumingBuild ? resumingBuild.goal : s.goalInput;
      dispatch({ type: 'start', resuming: resuming !== null });

      if (resuming) {
        dispatch({
          type: 'log',
          t: stamp(),
          draft: {
            tone: 'hi',
            glyph: '↻',
            text: ['reattached to the run already going on the gateway', { text: ` ${resuming.jobId}`, tone: 'mono' }],
          },
        });
      } else {
        const asking = availability?.available
          ? `${availability.provider}${availability.model ? ` · ${availability.model}` : ''} to design the course, then executing every exercise it proposes`
          : 'the built-in curriculum';
        const beats = pacedLines(goal, s.buildContext.level ?? '', asking);
        await runPipeline<LogLineDraft>(
          {
            steps: beats.map(() => ''),
            lines: beats.map((draft): [string, LogLineDraft] => ['', draft]),
            stepMs: 420,
            lineMs: 380,
            startMs: 0,
          },
          /* Only the transcript is written here. Advancing the checklist is the
             job of applyStages() below, once something has actually reported. */
          (ev) => {
            if (ev.type === 'log') dispatch({ type: 'paced', t: stamp(), draft: ev.line });
          },
          signal
        );
        if (signal.aborted) return;
        dispatch({ type: 'pacingDone', available: availability?.available === true });
      }

      /* ---------- generated: the gateway designs and verifies the course ----
       *
       * The references, the cross-checks and the adversarial fixtures all run
       * inside the pipeline, so this asks once and reports what came back. A
       * refusal is a real outcome: it is printed in full and no draft is
       * offered, because a course whose exercises did not run is not a course. */
      /* A resumed run is a gateway run by construction — it was submitted to a
         gateway that answered with a job — so it takes this branch even on the
         first frame, before the availability probe has come back. */
      if (availability?.available || resuming) {
        /* One adoption path, whoever obtained the outcome: a request that was
           held open to the end, or a job that was polled to its end. The
           reducer decides the phase; the store records the report and adopts
           the course, both of which are side effects it cannot do itself. */
        const settle = (outcome: AiOutcome): void => {
          const nextReport = reportFromOutcome(outcome, goal);
          s.setDraftReport(nextReport);
          const adopted = outcome.status === 'active' && outcome.plan ? s.adoptGeneratedCourse(outcome, goal) !== null : false;
          dispatch({ type: 'settled', t: stamp(), outcome, report: nextReport, adopted, goal });
          /* A settled, adopted course is the one case that carries the learner
             on to the review screen after a beat; the reducer marks the phase,
             the effect owns the timer. */
          if (settlePhase(outcome) === 'finishing' && adopted) finish();
        };

        /* Build it. The approval is a second run — the stored outline continued
           into its chapter phase — and it is watched exactly as the first one
           was, so the checklist and the stream keep reporting the gateway's own
           work. A gateway that answers the blocking ask is settled directly. */
        const runApproval = async (courseId: string): Promise<void> => {
          if (signal.aborted) return;
          dispatch({ type: 'approving', t: stamp(), courseId });
          let answer: AiCourseBuildAnswer;
          const carried = resumedBuildJobs.get(courseId);
          if (carried) {
            /* StrictMode mounts this effect twice: the first mount sent the
               POST and was torn down. The build it started is real, so this
               mount watches it rather than starting a second one. */
            answer = { job: carried, outcome: null };
          } else {
            try {
              answer = await s.api.approveCourse(courseId);
            } catch (e) {
              dispatch({ type: 'approvalFailed', t: stamp(), message: e instanceof Error ? e.message : String(e) });
              s.buildSettled();
              return;
            }
            if (answer.job) resumedBuildJobs.set(courseId, answer.job);
          }
          if (signal.aborted) return;
          let built: AiOutcome | null = null;
          if (answer.job) {
            built = await watchJob(answer.job);
            resumedBuildJobs.delete(courseId);
          } else if (answer.outcome) {
            built = answer.outcome;
          }
          s.buildSettled();
          if (signal.aborted || !built) return;
          settle(built);
          if (answer.job) s.runSettled(answer.job.id);
        };

        /* Discard. It is the existing course delete: the row the outline wrote
           goes, and the run that was waiting on it is over. */
        const discardOutline = async (courseId: string): Promise<void> => {
          dispatch({ type: 'discarding' });
          try {
            await s.api.deleteCourse(courseId);
          } catch {
            /* A course the gateway has already forgotten is gone, which is what
               was asked for; anything else keeps it there to try again. */
          }
          if (signal.aborted) return;
          dispatch({ type: 'discarded', t: stamp() });
          s.toast('Outline discarded.');
          s.showScreen('create');
        };
        actionsRef.current = {
          approve: (courseId) => void runApproval(courseId),
          discard: (courseId) => void discardOutline(courseId),
        };

        /* Follow a background run to its end, drawing the gateway's own live
           view as it goes. Two things happen here that a blocking request
           cannot do: the checklist advances on stages that are still
           `running`/`queued`, and the course the run is writing is read as it
           grows, so the chapters that already verified are on screen before the
           run is over. Resolves null when this tab stopped watching; the run
           itself continues on the gateway either way. */
        const watchJob = async (handle: AiJobHandle): Promise<AiOutcome | null> => {
          let seen = new Map<string, string>();
          let readCourseId = '';
          let misses = 0;
          /* The gateway advertises how long a run may take. Waiting a little
             past that is patience; waiting forever is a hang. */
          const deadline = Date.now() + (availability?.timeout_ms ?? 0) + 60_000;
          for (;;) {
            if (signal.aborted) return null;
            let view: AiJob | null = null;
            try {
              view = await s.api.generationJob(handle.id);
            } catch (e) {
              misses += 1;
              if (misses >= 3) {
                dispatch({
                  type: 'pollMissed',
                  t: stamp(),
                  failure: {
                    title: 'The run could not be read',
                    detail:
                      'The generation may still be going on the gateway. Every chapter it has already stored stays in your library.',
                    exercises: [],
                  },
                  draft: {
                    tone: 'fail',
                    glyph: '✕',
                    text: [`the run could not be read — ${e instanceof Error ? e.message : String(e)}`],
                  },
                });
                return null;
              }
            }
            if (signal.aborted) return null;
            if (view) {
              misses = 0;
              const viewed = poll(view, seen);
              seen = viewed.seen;
              dispatch({ type: 'stageReport', stages: view.stages });
              dispatch({ type: 'jobView', t: stamp(), view, drafts: viewed.lines });
              let record: AiCourseRecord | null = null;
              /* Mid-run: read the course the gateway is still writing, so the
                 chapters that already verified are on screen while it works. */
              if (jobIsRunning(view) && view.course_id && view.course_id !== readCourseId) {
                readCourseId = view.course_id;
                try {
                  record = await s.api.getCourse(view.course_id);
                } catch {
                  record = null;
                }
                if (signal.aborted) return null;
                if (record) dispatch({ type: 'adopt', course: s.adoptStoredCourse(record), status: record.status });
              }
              if (!jobIsRunning(view)) {
                /* A job is a view and carries no plan, so a finished run is
                   assembled from the record it stored — read once more,
                   after the last chapter, so the plan is the final one. */
                if (view.course_id) {
                  try {
                    record = await s.api.getCourse(view.course_id);
                  } catch {
                    record = null;
                  }
                  if (signal.aborted) return null;
                  if (record) dispatch({ type: 'adopt', course: s.adoptStoredCourse(record), status: record.status });
                }
                return outcomeFromJob(view, record);
              }
            }
            if (Date.now() > deadline) {
              dispatch({
                type: 'pollTimedOut',
                t: stamp(),
                failure: {
                  title: 'Still building on the gateway',
                  detail:
                    'This run outlived the wait, but it continues on the gateway. Every chapter it verifies is added to the course in your library.',
                  exercises: [],
                },
                draft: { tone: 'fail', glyph: '✕', text: ['the run is taking longer than the gateway budgeted'] },
              });
              return null;
            }
            await sleep(JOB_POLL_MS, signal);
          }
        };

        /* A stored course the library asked to resume: the gateway already
           holds the outline and whatever chapters verified, so the build route
           continues it and nothing new is submitted. */
        if (resumingBuild) {
          await runApproval(resumingBuild.courseId);
          return;
        }

        const request = {
          kind: 'course' as const,
          title: goal,
          scenario: [s.buildContext.level, s.buildContext.source].filter(Boolean).join('\n') || undefined,
          difficulty: undefined,
          exercise_count: 8,
          /* The learner asked to see the index first. This is the one field that
             separates the paused run from today's: without it the gateway runs
             outline into chapters in one request. */
          require_approval: s.buildContext.requireApproval === true ? true : undefined,
        };

        /* Ask for a job first. A blocking request holds this screen on one
           spinner for the whole run; a job lets the gateway report each chapter
           as it verifies and lets the course be read while it is still
           `building`. A gateway that cannot answer the async ask answers the
           blocking one, so a null handle is a fallback, not a failure.

           A resumed run already holds a handle: it is not submitted again, it
           is followed. */
        let handle: AiJobHandle | null = null;
        if (resuming) {
          handle = { id: resuming.jobId, kind: 'course', status: 'running' };
        } else {
          try {
            handle = await s.api.submitGeneration(request, 'course');
          } catch {
            handle = null;
          }
          if (signal.aborted) return;
          /* Remember the run before watching it, so a reload in the next second
             still finds something to come back to. */
          if (handle) s.runStarted(handle.id, goal);
        }
        dispatch({ type: 'submitted', handle });

        if (handle) {
          const outcome = await watchJob(handle);
          /* The breadcrumb survives a tab that merely stopped watching: the run
             is still going, and the next load can pick it up again. */
          if (signal.aborted || !outcome) return;
          settle(outcome);
          s.runSettled(handle.id);
          return;
        }

        let outcome: AiOutcome;
        try {
          outcome = await s.api.generateCourse(request);
        } catch {
          if (signal.aborted) return;
          dispatch({
            type: 'failed',
            t: stamp(),
            failure: {
              title: 'The gateway could not be reached',
              detail:
                'Nothing was generated and no draft was created. Check that the gateway is running, then try again.',
              exercises: [],
            },
            drafts: [{ tone: 'fail', glyph: '✕', text: ['the gateway could not be reached, so no course was generated.'] }],
          });
          return;
        }
        if (signal.aborted) return;
        settle(outcome);
        return;
      }

      /* ---------- authored: no provider, so the built-in course runs here ---
       *
       * This path does real work too: it opens the curriculum that ships with
       * the app and runs every reference query through the ExecutionService, so
       * the row counts and durations printed are the engine's. What it is not is
       * generated, and the report it produces says so. */
      const id = s.useAuthoredDraft(goal);
      const course = (id && s.COURSES.find((c) => c.id === id)) || null;
      if (!course) {
        dispatch({
          type: 'failed',
          t: stamp(),
          failure: {
            title: 'The built-in course could not be opened',
            detail:
              'No provider is configured and the bundled curriculum could not be opened as a draft. Nothing was created.',
            exercises: [],
          },
        });
        return;
      }
      const exercises = exercisesWithReference(course);
      dispatch({
        type: 'authored',
        phase: 'verifying',
        t: stamp(),
        course,
        draft: {
          text: [
            `No provider is configured: opening the built-in curriculum and running its ${exercises.length} reference queries on `,
            { text: executionEngineLabel(s.api), tone: 'hi' },
            ' …',
          ],
        },
      });
      const startedAt = performance.now();
      let ran = 0;
      let failedCount = 0;
      let rows = 0;
      for (const ex of exercises) {
        if (signal.aborted) return;
        const sql = String(ex.solution);
        let result;
        try {
          /* The reference queries are the draft course's own, so they run where
             that course runs; the draft declares the commerce dataset.
             Sequential on purpose: the transcript reports each reference in
             course order, and the engine serialises queries anyway. */
          result = await s.api.runQuery(sql, { block: ex, course });
        } catch {
          failedCount += 1;
          emit({ tone: 'fail', glyph: '✕', text: [`${ex.id} — engine unreachable`] });
          continue;
        }
        if (signal.aborted) return;
        if (result.error) {
          failedCount += 1;
          emit({ tone: 'fail', glyph: '✕', text: [`${ex.id} — ${result.error.message || result.error.code}`] });
        } else {
          ran += 1;
          rows += result.row_count;
          emit({ text: [`${ex.id} → ${result.row_count} row${result.row_count === 1 ? '' : 's'}`] });
        }
      }
      const verifyMs = Math.round(performance.now() - startedAt);
      if (failedCount > 0) {
        emit({
          tone: 'fail',
          glyph: '✕',
          text: [`${failedCount} of ${exercises.length} references failed on the engine — the draft ships with the failures listed`],
        });
      } else {
        emit({
          tone: 'ok',
          glyph: '✓',
          text: [`${ran}/${exercises.length} references executed · ${rows} rows · ${verifyMs} ms`],
        });
      }
      dispatch({ type: 'markStep', index: 3, step: 'done' });

      dispatch({ type: 'authored', phase: 'drafting' });
      if (signal.aborted) return;
      try {
        const draft = await s.api.draftCourse({
          title: course.title,
          exercises: courseExerciseTotal(course),
          steps: PIPE_STEPS.map((step) => step.title),
        });
        if (signal.aborted) return;
        emit({ tone: 'ok', glyph: '✓', text: [`draft accepted by the API · dataset_version ${draft.datasetVersion}`] });
      } catch {
        if (signal.aborted) return;
        emit({ tone: 'fail', glyph: '✕', text: ['the API rejected the draft — review it before enrolling'] });
      }
      const authored = authoredReport(goal, { ran, failed: failedCount, rows, ms: verifyMs }, course);
      s.setDraftReport(authored);
      dispatch({ type: 'authoredDone', t: stamp(), report: authored });
      finish();
    })();

    return () => {
      controller.abort();
      if (abortRef.current === controller) abortRef.current = null;
      if (navTimer.current !== null) {
        clearTimeout(navTimer.current);
        navTimer.current = null;
      }
      actionsRef.current = null;
      dispatch({ type: 'reset' });
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runKey]);

  const approve = useCallback((courseId: string): void => {
    actionsRef.current?.approve(courseId);
  }, []);

  const discard = useCallback((courseId: string): void => {
    actionsRef.current?.discard(courseId);
  }, []);

  const cancel = (): void => {
    abortRef.current?.abort();
    s.showScreen('create');
  };

  const retry = (): void => {
    dispatch({ type: 'reset' });
    setRunKey((k) => k + 1);
  };

  return { state, logOpen, setLogOpen, approve, discard, cancel, retry };
}
