import { useEffect, useRef, useState } from 'react';
import Icon from '../ui/Icon';
import GenerationLog from '../ui/GenerationLog';
import { useChecklist } from '../ui/useChecklist';
import { useStore } from '../../state/store';

import { courseExerciseTotal, exercisesWithReference } from '../../lib/model';
import { executionEngineLabel, generationProvenance, provenanceLabel } from '../../lib/provenance';
import { runPipeline } from '../../lib/pipeline';
/* Stage names and details arrive from the gateway (and, behind it, a model), so
   they are untrusted text. The log renders innerHTML for the paced lines, so
   anything interpolated from the response is escaped first. */
import { escapeHtmlAttribute as esc } from '../../lib/markdown';
import { sleep } from '../../lib/sleep';
import { authoredReport, courseFromPlan, reportFromOutcome, type DraftReport } from '../../lib/draft';
import { jobIsRunning, outcomeFromJob } from '../../api/ai';
import type { AiCourseRecord, AiJob, AiJobHandle, AiOutcome } from '../../api/aiContract';
import type { Course } from '../../types';

/* The checklist is the pipeline's report, not a script.
 *
 * Each step names the gateway stages whose success turns it "Done", so a step
 * is never Done before something said so. The previous version advanced a step
 * every 420 ms and had four of the six Done before the request had even
 * answered, which is the same "it is still a mock" problem as a fabricated row
 * count: the screen claimed work that nobody had reported. Step 0 is the one
 * step the client performs itself, and it says so. */
const PIPE_STEPS: { title: string; small: string; stages: readonly string[] }[] = [
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

/* The only lines paced by the client, because they describe the client's own
   two acts: quoting the request it was given, and asking. Everything after them
   is the gateway's transcript, printed with the durations it measured. */
function pacedLines(goal: string, level: string, ask: string): string[] {
  const excerpt = (text: string): string => {
    const flat = text.replace(/\s+/g, ' ').trim();
    if (!flat) return '';
    return esc(flat.length > 72 ? `${flat.slice(0, 71)}…` : flat);
  };
  const note = excerpt(level);
  return [
    `Reading your goal → “<span class="hi">${excerpt(goal) || '(no goal given)'}</span>”`,
    note
      ? `Your note on level and context → “<span class="hi">${note}</span>”`
      : 'No note on level and context was given, so the draft is asked to justify its own starting point',
    `Asking <span class="hi">${ask}</span> — a whole course is one large answer, so this takes a minute or more`,
  ];
}

/** Marks a step Done only when every stage it names reported done, and Working
    once one of them has. A step whose stage failed is left where it was: the
    failure itself is printed in the transcript and on the failure card. */
function applyStages(
  stages: readonly { name: string; state: string }[],
  mark: (i: number, state: 'idle' | 'running' | 'done') => void
): void {
  const byName = new Map(stages.map((stage) => [stage.name, stage.state]));
  PIPE_STEPS.forEach((step, i) => {
    if (step.stages.length === 0) return;
    const reported = step.stages.map((name) => byName.get(name));
    if (reported.some((state) => state === 'failed')) return;
    if (reported.every((state) => state === 'done' || state === 'skipped')) mark(i, 'done');
    else if (reported.some((state) => state === 'done')) mark(i, 'running');
  });
}

type Failure = {
  title: string;
  detail: string;
  exercises: { id: string; title: string; stage: string; message: string; detail?: string | null }[];
};

/** How often a running job is read. The gateway's own stage list is what makes
    the checklist move, so this is a read of a small document, not a poll for a
    verdict. */
const JOB_POLL_MS = 1200;

/* The job a resumed build is running under, keyed by course id. A resume is a
   POST, and StrictMode mounts the run effect twice: the first mount sends the
   POST and is torn down, so the handle is kept here and the second mount
   watches the same build instead of starting a second one. */
const resumedBuildJobs = new Map<string, AiJobHandle>();

export default function BuildingScreen() {
  const s = useStore();
  /* The draft this build is for. Null until the build has produced one, which is
     the honest state for the opening seconds: there is no course yet. */
  const draftCourse = s.draftCourse;
  const [runKey, setRunKey] = useState(0);
  const { states, mark, reset: resetChecklist, idleWord } = useChecklist(PIPE_STEPS, 'Queued');
  const [log, setLog] = useState<{ t: string; html: string }[]>([]);
  const [logOpen, setLogOpen] = useState(true);
  /* The draft the run produced, held locally so the right-hand card can show
     what was planned even before the store's render catches up. */
  const [built, setBuilt] = useState<Course | null>(null);
  const [report, setReport] = useState<DraftReport | null>(null);
  const [failure, setFailure] = useState<Failure | null>(null);
  /* Present only when this run was a real gateway generation. */
  const [generated, setGenerated] = useState<AiOutcome | null>(null);
  /* The live view of a background run, and the lifecycle the stored course
     reports while chapters are still being written. Both are absent for a
     blocking run, which reports only when it is over. */
  const [job, setJob] = useState<AiJob | null>(null);
  const [recordStatus, setRecordStatus] = useState<string | null>(null);
  /* The paused run's outcome: the outline is stored and the learner must
     approve it (Build it) or discard it. Null for every other run. */
  const [awaiting, setAwaiting] = useState<AiOutcome | null>(null);
  const [approving, setApproving] = useState(false);
  const [discarding, setDiscarding] = useState(false);
  /* The approval action and the discard action belong to one run (their abort
     signal, log and settle path), so the effect installs them here and the
     buttons call them. */
  const approveRef = useRef<(() => void) | null>(null);
  const discardRef = useRef<((courseId: string) => void) | null>(null);
  /* Cancel must actually cancel: the abort stops the pacing timers and the
     pending navigation, so leaving this screen never bounces the learner back
     into the review it just aborted.

     The effect owns its run instead of guarding one behind a ref, because
     React.StrictMode double-invokes effects in development: the first
     invocation is torn down immediately, and a ref guard would leave the
     second one believing a run it cannot see is already in flight. */
  const abortRef = useRef<AbortController | null>(null);
  const navTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  /* The run effect owns its own lifecycle, so it sees the store as it was on
     the first frame. Availability arrives from the gateway a moment later, so it
     is read through a ref rather than captured. */
  const availabilityRef = useRef(s.aiAvailability);
  availabilityRef.current = s.aiAvailability;

  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;
    const { signal } = controller;
    const t0 = performance.now();
    const stamp = (): string => `t+${((performance.now() - t0) / 1000).toFixed(1)}s`;
    const emit = (html: string): void => setLog((l) => [...l, { t: stamp(), html }]);
    const finish = (): void => {
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
      if (resuming) {
        emit(`<span class="hi">↻</span> reattached to the run already going on the gateway<span class="mono"> ${esc(resuming.jobId)}</span>`);
        mark(0, 'done');
        mark(1, 'running');
      } else {
        const asking = availability?.available
          ? `${esc(availability.provider)}${availability.model ? ` · ${esc(availability.model)}` : ''} to design the course, then executing every exercise it proposes`
          : 'the built-in curriculum';
        const beats = pacedLines(goal, s.buildContext.level ?? '', asking);
        await runPipeline(
          {
            steps: beats.map(() => ''),
            lines: beats.map((line) => ['', line] as [string, string]),
            stepMs: 420,
            lineMs: 380,
            startMs: 0,
          },
          /* Only the transcript is written here. Advancing the checklist is the
             job of applyStages() below, once something has actually reported. */
          (ev) => {
            if (ev.type === 'log') emit(ev.html);
          },
          signal
        );
        if (signal.aborted) return;
        mark(0, 'done');
        if (availability?.available) mark(1, 'running');
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
           held open to the end, or a job that was polled to its end. */
        const settle = (outcome: AiOutcome): void => {
          /* Any settled run supersedes the pause: if this is the approval's
             outcome, the decision point is over. The awaiting branch below
             re-arms it when the outcome is itself a pause. */
          setAwaiting(null);
          setGenerated(outcome);

          /* The checklist follows the gateway's own stage report. */
          applyStages(outcome.stages, mark);

          /* The gateway's own stages, verbatim, with the durations it measured. */
          for (const stage of outcome.stages) {
            const glyph = stage.state === 'done' ? '✓' : stage.state === 'failed' ? '✕' : '–';
            const cls = stage.state === 'done' ? 'ok' : stage.state === 'failed' ? 'fail' : 'hi';
            const ms = typeof stage.ms === 'number' ? ` · ${stage.ms} ms` : '';
            emit(`<span class="${cls}">${glyph}</span> ${esc(stage.name)}${ms}${stage.detail ? ` — ${esc(stage.detail)}` : ''}`);
          }
          for (const attempt of outcome.attempts) {
            emit(`<span class="hi">attempt ${attempt.number}</span> ${esc(attempt.stage)} — ${esc(attempt.detail)}`);
          }

          const nextReport = reportFromOutcome(outcome, goal);
          setReport(nextReport);
          s.setDraftReport(nextReport);

          if (outcome.error) {
            emit(`<span class="fail">✕</span> generation ${outcome.status} · ${esc(outcome.error.code)} — ${esc(outcome.error.message)}`);
          }

          /* The pause. The run stopped after storing the outline, and the
             index on the right is what the learner decides on — so instead of
             a failure card this becomes a decision point: build the chapters,
             or discard the course. */
          if (outcome.status === 'awaiting_approval') {
            const plan = outcome.plan;
            if (!plan) {
              setFailure({
                title: 'The outline could not be read',
                detail: 'The gateway paused the run but the stored outline did not come back. Nothing was written.',
                exercises: [],
              });
              return;
            }
            setAwaiting(outcome);
            setRecordStatus('building');
            setBuilt((prev) => prev ?? courseFromPlan(plan, goal));
            mark(1, 'done');
            mark(2, 'idle');
            const count = plan.chapters.length;
            emit(`<span class="hi">→</span> the outline is stored: <span class="hi">${count}</span> chapter${count === 1 ? '' : 's'} planned — approve it to write them, or discard it`);
            for (const ch of plan.chapters) {
              emit(`&nbsp;&nbsp;${esc(ch.id)} — ${esc(ch.title)}`);
            }
            emit('<span class="caret"></span>');
            approveRef.current = () => {
              void runApproval(plan.id);
            };
            return;
          }

          if (outcome.status !== 'active' || !outcome.plan) {
            const failed = nextReport.chapters.flatMap((c) => c.failures.map((f) => ({ ...f })));
            const refusedAt = outcome.error?.stage ?? 'verification';
            for (const f of failed) {
              emit(`<span class="fail">✕</span> ${esc(f.id)} · ${esc(f.stage)} — ${esc(f.message)}`);
            }
            mark(4, 'idle');
            emit('<span class="caret"></span>');
            setFailure({
              title:
                outcome.status === 'unavailable'
                  ? 'Generation is not available'
                  : outcome.error?.code === 'AI_UNAVAILABLE'
                    ? 'The generation service did not answer'
                    : `The course was rejected at the ${refusedAt} stage`,
              detail: outcome.error
                ? `${outcome.error.message}${outcome.error.detail ? ` — ${outcome.error.detail}` : ''}`
                : 'The gateway refused this course; the exercises below are the ones it could not verify.',
              exercises: failed,
            });
            return;
          }

          const adopted = s.adoptGeneratedCourse(outcome, goal);
          if (!adopted) {
            setFailure({
              title: 'The course could not be opened',
              detail: 'The gateway activated the course but the app could not add it. Nothing was saved to your learning.',
              exercises: [],
            });
            return;
          }
          mark(4, 'running');
          const plan = outcome.plan;
          const chapterCount = plan.chapters.length;
          const exerciseCount = plan.chapters.reduce((n, c) => n + c.exercises.length, 0);
          emit(`<span class="ok">✓</span> ${chapterCount} chapter${chapterCount === 1 ? '' : 's'} · ${exerciseCount} exercise${exerciseCount === 1 ? '' : 's'}, each executed and cross-checked on the engine · ${nextReport.totals.rows} rows measured`);
          emit(`Draft <span class="hi">${esc(plan.title)}</span> ready to inspect — every exercise carries the rows the engine returned.`);
          mark(4, 'done');
          emit('<span class="caret"></span>');
          finish();
        };

        /* Build it. The approval is a second run — the stored outline continued
           into its chapter phase — and it is watched exactly as the first one
           was, so the checklist and the stream keep reporting the gateway's own
           work. A gateway that answers the blocking ask is settled directly. */
        const runApproval = async (courseId: string): Promise<void> => {
          if (signal.aborted) return;
          setApproving(true);
          setAwaiting(null);
          approveRef.current = null;
          mark(1, 'done');
          mark(2, 'running');
          emit(`<span class="hi">→</span> approval sent — writing every chapter of <span class="mono">${esc(courseId)}</span>`);
          let answer;
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
              setApproving(false);
              s.buildSettled();
              emit(`<span class="fail">✕</span> the approval could not be sent — ${esc(e instanceof Error ? e.message : String(e))}`);
              setFailure({
                title: 'The course could not be built',
                detail: 'The gateway did not accept the approval. The outline is still stored, so you can approve it again or discard it.',
                exercises: [],
              });
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
          setApproving(false);
          s.buildSettled();
          if (signal.aborted || !built) return;
          settle(built);
          if (answer.job) s.runSettled(answer.job.id);
        };

        /* Discard. It is the existing course delete: the row the outline wrote
           goes, and the run that was waiting on it is over. */
        const discardOutline = async (courseId: string): Promise<void> => {
          setDiscarding(true);
          try {
            await s.api.deleteCourse(courseId);
          } catch {
            /* A course the gateway has already forgotten is gone, which is what
               was asked for; anything else keeps it there to try again. */
          }
          if (signal.aborted) return;
          setDiscarding(false);
          setAwaiting(null);
          setBuilt(null);
          emit('<span class="hi">discarded</span> the outline and the course were removed');
          s.toast('Outline discarded.');
          s.showScreen('create');
        };
        discardRef.current = discardOutline;

        /* Follow a background run to its end, drawing the gateway's own live
           view as it goes. Two things happen here that a blocking request
           cannot do: the checklist advances on stages that are still
           `running`/`queued`, and the course the run is writing is read as it
           grows, so the chapters that already verified are on screen before the
           run is over. Resolves null when this tab stopped watching; the run
           itself continues on the gateway either way. */
        const watchJob = async (handle: AiJobHandle): Promise<AiOutcome | null> => {
          const seen = new Map<string, string>();
          let lastPlanned = -1;
          let lastVerified = -1;
          let lastChapter = '';
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
                emit(`<span class="fail">✕</span> the run could not be read — ${esc(e instanceof Error ? e.message : String(e))}`);
                setFailure({
                  title: 'The run could not be read',
                  detail: 'The generation may still be going on the gateway. Every chapter it has already stored stays in your library.',
                  exercises: [],
                });
                return null;
              }
            }
            if (signal.aborted) return null;
            if (view) {
              misses = 0;
              setJob(view);
              applyStages(view.stages, mark);
              for (const stage of view.stages) {
                if (seen.get(stage.name) === stage.state) continue;
                seen.set(stage.name, stage.state);
                const glyph = stage.state === 'done' ? '✓' : stage.state === 'failed' ? '✕' : stage.state === 'running' ? '→' : '–';
                const cls = stage.state === 'done' ? 'ok' : stage.state === 'failed' ? 'fail' : 'hi';
                const ms = typeof stage.ms === 'number' ? ` · ${stage.ms} ms` : '';
                emit(`<span class="${cls}">${glyph}</span> ${esc(stage.name)}${ms}${stage.detail ? ` — ${esc(stage.detail)}` : ''}`);
              }
              if (typeof view.chapters_planned === 'number' && view.chapters_planned !== lastPlanned) {
                lastPlanned = view.chapters_planned;
                emit(`the outline plans <span class="hi">${view.chapters_planned}</span> chapter${view.chapters_planned === 1 ? '' : 's'}`);
              }
              if (typeof view.chapters_verified === 'number' && view.chapters_verified !== lastVerified) {
                lastVerified = view.chapters_verified;
                const of = typeof view.chapters_planned === 'number' ? ` of ${view.chapters_planned}` : '';
                emit(`<span class="ok">✓</span> ${view.chapters_verified}${of} chapter${view.chapters_verified === 1 ? '' : 's'} verified and stored`);
              }
              if (view.current_chapter && view.current_chapter !== lastChapter) {
                lastChapter = view.current_chapter;
                emit(`writing <span class="hi">${esc(view.current_chapter)}</span>`);
              }
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
                if (record) {
                  setBuilt(s.adoptStoredCourse(record));
                  setRecordStatus(record.status);
                }
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
                  if (record) {
                    setBuilt(s.adoptStoredCourse(record));
                    setRecordStatus(record.status);
                  }
                }
                return outcomeFromJob(view, record);
              }
            }
            if (Date.now() > deadline) {
              emit('<span class="fail">✕</span> the run is taking longer than the gateway budgeted');
              setFailure({
                title: 'Still building on the gateway',
                detail: 'This run outlived the wait, but it continues on the gateway. Every chapter it verifies is added to the course in your library.',
                exercises: [],
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
          emit('<span class="fail">✕</span> the gateway could not be reached, so no course was generated.');
          setFailure({
            title: 'The gateway could not be reached',
            detail: 'Nothing was generated and no draft was created. Check that the gateway is running, then try again.',
            exercises: [],
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
        setFailure({
          title: 'The built-in course could not be opened',
          detail: 'No provider is configured and the bundled curriculum could not be opened as a draft. Nothing was created.',
          exercises: [],
        });
        return;
      }
      setBuilt(course);
      /* These three are true here and now: the built-in curriculum is chosen,
         the environment is the seeded dataset the app ships, and its exercises
         carry their references. The next step is the one that runs them. */
      mark(1, 'done');
      mark(2, 'done');
      mark(3, 'running');
      const exercises = exercisesWithReference(course);
      emit(`No provider is configured: opening the built-in curriculum and running its ${exercises.length} reference queries on <span class="hi">${executionEngineLabel(s.api)}</span> …`);
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
             that course runs; the draft declares the commerce dataset. */
          result = await s.api.runQuery(sql, { block: ex, course });
        } catch {
          failedCount += 1;
          emit(`<span class="fail">✕</span> ${ex.id} — engine unreachable`);
          continue;
        }
        if (signal.aborted) return;
        if (result.error) {
          failedCount += 1;
          emit(`<span class="fail">✕</span> ${ex.id} — ${result.error.message || result.error.code}`);
        } else {
          ran += 1;
          rows += result.row_count;
          emit(`${ex.id} → ${result.row_count} row${result.row_count === 1 ? '' : 's'}`);
        }
      }
      const verifyMs = Math.round(performance.now() - startedAt);
      if (failedCount > 0) {
        emit(`<span class="fail">✕</span> ${failedCount} of ${exercises.length} references failed on the engine — the draft ships with the failures listed`);
      } else {
        emit(`<span class="ok">✓</span> ${ran}/${exercises.length} references executed · ${rows} rows · ${verifyMs} ms`);
      }
      mark(3, 'done');

      mark(4, 'running');
      if (signal.aborted) return;
      try {
        const draft = await s.api.draftCourse({
          title: course.title,
          exercises: courseExerciseTotal(course),
          steps: PIPE_STEPS.map((step) => step.title),
        });
        if (signal.aborted) return;
        emit(`<span class="ok">✓</span> draft accepted by the API · dataset_version ${draft.datasetVersion}`);
      } catch {
        if (signal.aborted) return;
        emit('<span class="fail">✕</span> the API rejected the draft — review it before enrolling');
      }
      const authored = authoredReport(goal, { ran, failed: failedCount, rows, ms: verifyMs }, course);
      setReport(authored);
      s.setDraftReport(authored);
      mark(4, 'done');
      emit('<span class="caret"></span>');
      finish();
    })();

    return () => {
      controller.abort();
      if (abortRef.current === controller) abortRef.current = null;
      if (navTimer.current !== null) {
        clearTimeout(navTimer.current);
        navTimer.current = null;
      }
      approveRef.current = null;
      discardRef.current = null;
      resetChecklist();
      setLog([]);
      setJob(null);
      setRecordStatus(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runKey]);

  const shown = built ?? draftCourse;
  const exerciseTotal = shown ? courseExerciseTotal(shown) : null;
  /* The report is the one measured record this run produced; the card reads it
     rather than keeping a second copy of the same numbers. `totals.verified`
     counts executed references, so the rest of the total are the failures. */
  const totals = report?.totals ?? null;
  const failed = totals ? Math.max(0, totals.exercises - totals.verified) : 0;

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">Course generation</div>
          <h1>Building a course you can inspect</h1>
          <p>Drafting the path is only one step. Exercises are generated, executed, and checked before the course is presented for review.</p>
        </div>
        <button onClick={() => { abortRef.current?.abort(); s.showScreen('create'); }}>Cancel</button>
      </div>

      {awaiting?.plan && (
        <div className="card" style={{ borderColor: 'var(--green)' }}>
          <h2>Your outline is ready to approve</h2>
          <p className="mt-8" style={{ fontSize: 13 }}>
            The gateway designed the index below and stored it. Nothing is written to your learning yet — approve it to
            build the chapters, or discard it and the course is removed.
          </p>
          <div className="mt-12">
            <div className="reportrow"><span><strong>{awaiting.plan.title}</strong></span><span className="muted">{awaiting.plan.chapters.length} chapter{awaiting.plan.chapters.length === 1 ? '' : 's'}</span></div>
            {awaiting.plan.subtitle && <div className="reportrow"><span className="muted">{awaiting.plan.subtitle}</span><span /></div>}
            {awaiting.plan.chapters.map((ch) => (
              <div className="reportrow" key={ch.id}>
                <span>
                  <span className="mono" style={{ fontSize: 12 }}>{ch.id}</span>
                  &nbsp; {ch.title}
                  {ch.summary ? <><br /><span className="muted" style={{ fontSize: 12 }}>{ch.summary}</span></> : null}
                </span>
                <span className="muted">{ch.objectives.length} objective{ch.objectives.length === 1 ? '' : 's'}</span>
              </div>
            ))}
          </div>
          <div className="actions mt-12">
            <button className="primary" disabled={approving} onClick={() => approveRef.current?.()}>
              {approving ? 'Building the chapters …' : 'Build it'}
            </button>
            <button className="ghost" disabled={discarding || approving} onClick={() => { if (awaiting.plan) discardRef.current?.(awaiting.plan.id); }}>
              {discarding ? 'Discarding …' : 'Discard'}
            </button>
          </div>
        </div>
      )}

      {failure && (
        <div className="card" style={{ borderColor: 'var(--amber)' }}>
          <h2>Nothing was created</h2>
          <p className="mt-8" style={{ fontSize: 13 }}><strong>{failure.title}.</strong> {failure.detail}</p>
          {failure.exercises.length > 0 && (
            <div className="mt-12">
              {failure.exercises.map((f) => (
                <div className="reportrow" key={f.id}>
                  <span><span className="mono" style={{ fontSize: 12 }}>{f.id}</span> &nbsp;{f.title}</span>
                  <span className="fail">{f.stage}</span>
                </div>
              ))}
            </div>
          )}
          <div className="actions mt-12">
            <button className="primary" onClick={() => { setFailure(null); setLog([]); setReport(null); setGenerated(null); setBuilt(null); setJob(null); setRecordStatus(null); setRunKey((k) => k + 1); }}>Try again</button>
            <button onClick={() => s.showScreen('create')}>Change the goal</button>
            {report && <button className="ghost" onClick={() => { s.setDraftReport(report); s.setReviewTab('validation'); s.showScreen('review'); }}>See what was measured</button>}
          </div>
        </div>
      )}

      <div className="grid2">
        <div className="card">
          <h2>Build pipeline</h2>
          <div className="callout mt-12">
            <strong>Your request</strong><br />
            {/* A resumed run has no form behind it, so the goal comes from the
                breadcrumb rather than the input the learner had typed. */}
            <span className="mono" style={{ fontSize: 12 }}>{s.goalInput || s.resumeRun?.goal || '—'}</span>
          </div>
          <div className="pipeline">
            {PIPE_STEPS.map(({ title, small }, i) => (
              <div className={`pipe ${states[i]}`} key={i}>
                <div className="check">{states[i] === 'done' ? '✓' : i + 1}</div>
                <div><strong>{title}</strong><small>{small}</small></div>
                <span className="state">{states[i] === 'done' ? 'Done' : states[i] === 'running' ? 'Working' : idleWord}</span>
              </div>
            ))}
          </div>
          <button className="small ghost mt-12" onClick={() => setLogOpen((v) => !v)}>
            <Icon name="chevron-down" />Show generation stream
          </button>
          <GenerationLog log={log} open={logOpen} />
        </div>
        <div className="card">
          <h2>Why this takes a moment</h2>
          <div className="reportrow mt-8"><span>Chapters planned</span><span>{shown ? (shown.modules ?? []).length : '—'}</span></div>
          {/* A background run reports its own chapter count as it goes, so the
              row is the gateway's, not this tab's guess — and it is absent for a
              blocking run, which has no live view to read. */}
          {job && typeof job.chapters_planned === 'number' && (
            <div className="reportrow">
              <span>Chapters verified and stored</span>
              <span>{`${typeof job.chapters_verified === 'number' ? job.chapters_verified : 0} of ${job.chapters_planned}`}</span>
            </div>
          )}
          {job?.current_chapter && (
            <div className="reportrow"><span>Chapter in progress</span><span>{job.current_chapter}</span></div>
          )}
          {!generated && recordStatus && (
            <div className="reportrow"><span>Stored course</span><span>{recordStatus}</span></div>
          )}
          <div className="reportrow"><span>Exercises with result contracts</span><span>{exerciseTotal ?? '—'}</span></div>
          <div className="reportrow">
            <span>Exercises executed and cross-checked</span>
            <span>{totals ? `${totals.verified}/${totals.verified + failed} · ${totals.rows} rows` : 'measuring …'}</span>
          </div>
          <div className="reportrow">
            <span>Engine time</span>
            <span>{totals ? `${totals.ms} ms` : '—'}</span>
          </div>
          <div className="reportrow">
            <span>Reference failures</span>
            <span className={totals && failed > 0 ? 'fail' : undefined}>
              {totals ? (failed > 0 ? `${failed} to review` : '0') : '—'}
            </span>
          </div>
          <div className="reportrow"><span>Execution engine</span><span className="mono">{executionEngineLabel(s.api)}</span></div>
          <div className="reportrow"><span>Draft pipeline</span><span className="mono" style={{ fontSize: 11 }}>{report?.source === 'authored' ? 'built-in curriculum (no AI)' : generationProvenance(s.api)}</span></div>
          {generated?.plan && (
            <div className="reportrow">
              <span>Provider</span>
              <span className="mono" style={{ fontSize: 11 }}>{provenanceLabel(generated.plan.provenance)}</span>
            </div>
          )}
          {generated && (
            <div className="reportrow">
              <span>Generation</span>
              <span className={generated.status === 'active' || generated.status === 'unavailable' || generated.status === 'awaiting_approval' ? undefined : 'fail'}>
                {generated.status}{generated.error ? ` · ${generated.error.code}` : ''}
              </span>
            </div>
          )}
          <div className="callout mt-16">
            <strong>What is measured here.</strong> A step turns Done when the pipeline reports that stage complete —
            never on a timer. The three opening lines are this tab quoting the request you gave it and asking; every
            line after them is the gateway's own transcript. Each reference query runs on the engine named above, so
            the row counts, timings and failures printed are the ones it returned.
            {availabilityRef.current?.available
              ? ' Because a generation service is configured, the design and the verification both happen on the gateway and the stages and durations shown are its own.'
              : ' No generation service is configured, so the course is the built-in curriculum and its references are executed here, in this tab.'}
          </div>
        </div>
      </div>
    </div>
  );
}
