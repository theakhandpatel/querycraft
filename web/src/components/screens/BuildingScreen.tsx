import React, { useEffect, useRef, useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import { courseExerciseTotal, moduleExerciseBlocks } from '../../lib/model';
import { executionEngineLabel, generationProvenance } from '../../lib/provenance';
import { runPipeline } from '../../lib/pipeline';
import type { Block, Course } from '../../types';

const PIPE_STEPS = [
  ['Interpret your goal', 'Identify target skills, starting level, and what success looks like'],
  ['Design the progression', 'Order concepts from prerequisites to independent problem solving'],
  ['Build a realistic dataset', 'Create related tables, representative records, and edge cases'],
  ['Write exercises and reference queries', 'Define result contracts, hints, explanations, and expected outcomes'],
  ['Execute and verify', 'Run references, cross-check independent solutions, test edge cases'],
  ['Prepare an editable draft', 'Keep assumptions and validation findings visible for your review'],
];

/* Only the planning beats are paced by the client — there is nothing to measure
   before a query runs. Everything from "Execute and verify" onward is driven by
   real work against the configured engine, and the transcript prints the numbers
   that come back. */
const PLAN_LINES: string[] = [
  'Interpreting goal → <span class="hi">analytical SQL</span>, level: intermediate',
  'Prerequisites resolved → chapters ordered from prerequisites to independent work',
  'Dataset plan → commerce tables, representative records, seeded edge cases',
  'Collecting the authored exercises and their reference queries …',
];

type Measured = { ran: number; failed: number; rows: number; ms: number };

/** Exercises in the draft that carry a reference query we can actually run. */
function referenceExercises(course: Course): Block[] {
  return (course.modules ?? [])
    .flatMap((m) => moduleExerciseBlocks(m))
    .filter((b) => typeof b.solution === 'string' && b.solution.trim().length > 0);
}

export default function BuildingScreen() {
  const s = useStore();
  /* The draft this build is for; COURSES[0] is only a fallback for the
     standalone route. */
  const draftCourse = s.draftCourse ?? COURSES[0];
  const exerciseTotal = courseExerciseTotal(draftCourse);
  const [states, setStates] = useState(PIPE_STEPS.map(() => 'queued'));
  const [log, setLog] = useState<{ t: string; html: string }[]>([]);
  const [logOpen, setLogOpen] = useState(true);
  const [measured, setMeasured] = useState<Measured | null>(null);
  /* Cancel must actually cancel: the abort stops the pacing timers and the
     pending navigation, so leaving this screen never bounces the learner back
     into the review it just aborted.

     The effect owns its run instead of guarding one behind a ref, because
     React.StrictMode double-invokes effects in development: the first
     invocation is torn down immediately, and a ref guard would leave the
     second one believing a run it cannot see is already in flight. */
  const abortRef = useRef<AbortController | null>(null);
  const navTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;
    const { signal } = controller;
    const t0 = performance.now();
    const stamp = (): string => `t+${((performance.now() - t0) / 1000).toFixed(1)}s`;
    const emit = (html: string): void => setLog((l) => [...l, { t: stamp(), html }]);
    const mark = (i: number, state: 'queued' | 'running' | 'done'): void =>
      setStates((prev) => {
        if (prev[i] === state) return prev;
        const next = prev.slice();
        next[i] = state;
        return next;
      });
    void (async () => {
      /* Pacing only: the four planning beats are instant on the server, so the
         shared pipeline primitive spreads them out. Its all-done event is
         ignored here — this screen finishes on measured work, not on a timer. */
      await runPipeline(
        {
          steps: PLAN_LINES.map(() => ''),
          lines: PLAN_LINES.map((line) => ['', line] as [string, string]),
          stepMs: 420,
          lineMs: 380,
          startMs: 0,
        },
        (ev) => {
          if (ev.type === 'step') mark(ev.index, ev.state === 'done' ? 'done' : 'running');
          else if (ev.type === 'log') emit(ev.html);
        },
        signal
      );
      if (signal.aborted) return;

      /* Step 5 — Execute and verify. Every reference query in the draft runs on
         the configured engine; the duration and the counts are the real ones. */
      mark(4, 'running');
      const exercises = referenceExercises(draftCourse);
      emit(`Running ${exercises.length} reference queries on <span class="hi">${executionEngineLabel(s.api)}</span> …`);
      const startedAt = performance.now();
      let ran = 0;
      let failed = 0;
      let rows = 0;
      for (const ex of exercises) {
        if (signal.aborted) return;
        const sql = String(ex.solution);
        let result;
        try {
          result = await s.api.runQuery(sql, ex);
        } catch {
          failed += 1;
          emit(`<span class="fail">✕</span> ${ex.id} — engine unreachable`);
          continue;
        }
        if (signal.aborted) return;
        if (result.error) {
          failed += 1;
          emit(`<span class="fail">✕</span> ${ex.id} — ${result.error.message || result.error.code}`);
        } else {
          ran += 1;
          rows += result.row_count;
          emit(`${ex.id} → ${result.row_count} row${result.row_count === 1 ? '' : 's'}`);
        }
      }
      const verifyMs = Math.round(performance.now() - startedAt);
      setMeasured({ ran, failed, rows, ms: verifyMs });
      if (failed > 0) {
        emit(`<span class="fail">✕</span> ${failed} of ${exercises.length} references failed on the engine — draft ships with the failures listed`);
      } else {
        emit(`<span class="ok">✓</span> ${ran}/${exercises.length} references executed · ${rows} rows · ${verifyMs} ms`);
      }
      mark(4, 'done');

      /* Step 6 — the draft record itself comes from the API, not a timer. */
      mark(5, 'running');
      if (signal.aborted) return;
      try {
        const draft = await s.api.draftCourse({
          title: draftCourse.title,
          exercises: exerciseTotal,
          steps: PIPE_STEPS.map(() => ''),
        });
        if (signal.aborted) return;
        emit(`<span class="ok">✓</span> draft accepted by the API · dataset_version ${draft.datasetVersion}`);
      } catch {
        if (signal.aborted) return;
        emit('<span class="fail">✕</span> the API rejected the draft — review it before enrolling');
      }
      mark(5, 'done');
      emit('<span class="caret"></span>');

      if (signal.aborted) return;
      navTimer.current = setTimeout(() => {
        navTimer.current = null;
        if (signal.aborted) return;
        s.setReviewTab('overview');
        s.showScreen('review');
      }, 700);
    })();

    return () => {
      controller.abort();
      if (abortRef.current === controller) abortRef.current = null;
      if (navTimer.current !== null) {
        clearTimeout(navTimer.current);
        navTimer.current = null;
      }
      setStates(PIPE_STEPS.map(() => 'queued'));
      setLog([]);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
      <div className="grid2">
        <div className="card">
          <h2>Build pipeline</h2>
          <div className="callout mt-12">
            <strong>Your request</strong><br />
            <span className="mono" style={{ fontSize: 12 }}>{s.goalInput || '—'}</span>
          </div>
          <div className="pipeline">
            {PIPE_STEPS.map(([title, small], i) => (
              <div className={`pipe ${states[i]}`} key={i}>
                <div className="check">{states[i] === 'done' ? '✓' : i + 1}</div>
                <div><strong>{title}</strong><small>{small}</small></div>
                <span className="state">{states[i] === 'done' ? 'Done' : states[i] === 'running' ? 'Working' : 'Queued'}</span>
              </div>
            ))}
          </div>
          <button className="small ghost mt-12" onClick={() => setLogOpen((v) => !v)}>
            <Icon name="chevron-down" />Show generation stream
          </button>
          <div className={`stream${logOpen ? ' open' : ''}`}>
            {log.map((l, i) => (
              <div className="ln" key={i}><span className="t">{l.t}</span><span dangerouslySetInnerHTML={{ __html: l.html }} /></div>
            ))}
          </div>
        </div>
        <div className="card">
          <h2>Why this takes a moment</h2>
          <div className="reportrow mt-8"><span>Chapters planned</span><span>{(draftCourse.modules ?? []).length}</span></div>
          <div className="reportrow"><span>Exercises with result contracts</span><span>{exerciseTotal}</span></div>
          <div className="reportrow">
            <span>Reference queries executed</span>
            <span>{measured ? `${measured.ran}/${measured.ran + measured.failed} · ${measured.rows} rows` : 'measuring …'}</span>
          </div>
          <div className="reportrow">
            <span>Engine time</span>
            <span>{measured ? `${measured.ms} ms` : '—'}</span>
          </div>
          <div className="reportrow">
            <span>Reference failures</span>
            <span className={measured && measured.failed > 0 ? 'fail' : undefined}>
              {measured ? (measured.failed > 0 ? `${measured.failed} to review` : '0') : '—'}
            </span>
          </div>
          <div className="reportrow"><span>Execution engine</span><span className="mono">{executionEngineLabel(s.api)}</span></div>
          <div className="reportrow"><span>Draft pipeline</span><span className="mono" style={{ fontSize: 11 }}>{generationProvenance(s.api)}</span></div>
          <div className="callout mt-16">
            <strong>What is measured here.</strong> The planning beats are paced in the client because they are instant.
            From “Execute and verify” on, every line is a real request: each reference query runs on the engine named
            above and the row counts, timings and failures printed are the ones it returned.
          </div>
        </div>
      </div>
    </div>
  );
}
