import { useCallback, useEffect, useRef, useState } from 'react';
import Icon from './ui/Icon';
import ResultGrid from './ResultGrid';
import PlanView from './PlanView';
import SqlEditor from './editor/SqlEditor';
import { DialectRefusal, useChapterDialect, useDialectSupport } from './ui/DialectBadge';
import { useStore } from '../state/store';
import { gradePlan, type PlanBundle, type PlanCheck } from '../api/planContract';
import { datasetFor } from '../lib/dataset';
import { dialectLabel } from '../lib/dialect';
import { servedDialects } from '../lib/dialectChoice';
import { SIMPLE_MODE } from '../lib/mode';
import { cachedSqlSchema, loadSqlSchema } from '../lib/sqlSchema';
import type { SqlSchema } from '../lib/sqlSchema';
import type { Block, CheckResult, Course, CourseModule, QueryResult, TutorRunState } from '../types';

type Feedback =
  | { kind: 'pending' }
  | { kind: 'error'; code: string }
  | { kind: 'hint'; hints: string[] }
  | { kind: 'tutor'; message: string; hintLevel: number; revealsSolution: boolean }
  | { kind: 'tutorPending' }
  | { kind: 'tutorUnavailable'; reason: string }
  | { kind: 'solution' }
  | { kind: 'accepted' }
  | { kind: 'passed'; checks: CheckResult[] }
  | { kind: 'failed'; checks: CheckResult[]; cause: CheckResult | { label: string } };

interface FeedbackCtx {
  block: Block;
  hint: () => void;
  solution: () => void;
  accept: () => void;
  run: () => void;
  keep: (sql: string) => void;
  nextChapter: () => void;
  explain: () => void;
}

/* One inline SQL cell. Holds its own draft + result + feedback state; execution
   and grading always go through the API boundary, never computed here.
   `onPassed` lets a host that derives its own next step (the workbench on Home)
   hold the page still for the verdict instead of advancing under the learner. */
export default function SqlCell({ block, module: mod, course, onPassed }: { block: Block; module: CourseModule; course: Course; onPassed?: () => void }) {
  const s = useStore();
  const isExercise = block.type === 'exercise';
  const id = block.id as string;
  /* M5: which fixture this cell runs against. A perf exercise and a `plans`
     chapter both resolve to the larger dataset; every other cell is the
     commerce one, exactly as before. Resolved once here so Run, Check and the
     plan capture cannot disagree about the database. */
  const dataset = datasetFor({ course, block });
  /* M6 step 7: the dialect this chapter runs on, and whether the engine behind
     the seam can serve it. `supported` is `null` until the engine answers, so
     Run is never disabled on a guess — only on the engine's own refusal. */
  const dialect = useChapterDialect(mod, course);
  const dialectSupport = useDialectSupport(dialect);
  const dialectRefused = dialectSupport.supported === false;
  /* Keyed by the cell, not the chapter: a chapter can hold several cells and
     each one points its own Run control at its own refusal row, so the id has
     to be unique in the document. */
  const refusalId = `dialrefusal-${id}`;
  /* M5: a perf exercise opts into the plan viewer. Plan support comes from the
     capability block; an adapter that reports none disables the action with the
     reason shown, the same honest pattern the AI lab uses when AI is off.
     Until capabilities have been reported we do not *assume* support, but we do
     not refuse either: the call itself answers truthfully if the engine cannot
     plan. */
  const perf = isExercise ? block.perf : undefined;
  const capsKnown = s.engineCaps !== null;
  /* M6 step 6: the answer is per dialect, not per adapter — the browser adapter
     hosts two engines and only one of them plans. An adapter that reports no
     `explainDialects` means its `explain` answers for every dialect it serves. */
  const explainDialects = s.engineCaps
    ? s.engineCaps.explainDialects ?? (s.engineCaps.explain ? servedDialects(s.engineCaps) ?? [] : [])
    : null;
  const planBlockedReason =
    explainDialects !== null && !explainDialects.includes(dialect)
      ? dialect === 'sqlite'
        ? 'SQLite has no EXPLAIN (ANALYZE), so a plan cannot be captured for this dialect.'
        : 'This environment reports that it cannot produce a plan for this dialect.'
      : null;
  const planWritable = capsKnown ? s.engineCaps!.writable : false;
  const starter = isExercise ? block.starter || 'SELECT ...' : (block.sql as string);

  const [code, setCode] = useState(starter);
  const [schema, setSchema] = useState<SqlSchema | null>(() => cachedSqlSchema(dataset, dialect));
  const schemaRequested = useRef(false);
  const [running, setRunning] = useState(false);
  const [qr, setQr] = useState<QueryResult | null>(null);
  const [fb, setFb] = useState<Feedback | null>(null);
  /* M5: a performance exercise's captured plan and its structural verdict. */
  const [plan, setPlan] = useState<PlanBundle | null>(null);
  const [planChecks, setPlanChecks] = useState<PlanCheck[] | null>(null);
  const [planBusy, setPlanBusy] = useState(false);
  const [tutorBusy, setTutorBusy] = useState(false);
  const feedbackRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let alive = true;
    /* Start from this block's own starter, then let a saved draft win. Without
       the reset, a cell that React reuses across a chapter change kept the
       previous chapter's SQL in the editor — and the previous chapter's result
       and plan in the panels below it. The draft is keyed by the exercise id,
       so "no draft" means "never touched this exercise", not "keep whatever was
       on screen". */
    setCode(starter);
    setQr(null);
    setFb(null);
    setPlan(null);
    setPlanChecks(null);
    s.api.getDraft(id).then((draft) => {
      if (alive && draft != null) setCode(draft);
    });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, starter]);

  /* Completion reads the same workspace the Tables drawer describes, but only
     after the learner touches the editor. A lesson must not boot an engine to
     offer a completion list, and a SQLite chapter must never boot PGlite. */
  const requestSchema = useCallback(() => {
    if (schemaRequested.current) return;
    const ready = cachedSqlSchema(dataset, dialect);
    if (ready) {
      setSchema(ready);
      return;
    }
    schemaRequested.current = true;
    loadSqlSchema(s.api, dataset, dialect)
      .then((next) => setSchema(next))
      .catch(() => {
        schemaRequested.current = false;
      });
  }, [s.api, dataset, dialect]);

  useEffect(() => {
    schemaRequested.current = false;
    setSchema(cachedSqlSchema(dataset, dialect));
  }, [dataset, dialect, id]);

  /* The verdict is the moment. When it lands, bring it into view without
     hiding the result above it; the reduced-motion setting makes the move
     instant. */
  useEffect(() => {
    if (!fb || (fb.kind !== 'passed' && fb.kind !== 'failed' && fb.kind !== 'error')) return;
    feedbackRef.current?.scrollIntoView({ block: 'nearest', behavior: s.motion ? 'auto' : 'smooth' });
  }, [fb, s.motion]);

  function onCode(next: string) {
    setCode(next);
    s.api.saveDraft(id, next);
  }

  async function run() {
    /* Refused by dialect: the button is disabled too, but the keyboard shortcut
       reaches this function directly, so the guard lives here as well. */
    if (dialectRefused) return;
    setRunning(true);
    setFb(null);
    /* The resolved dialect goes to the seam explicitly: with two engines in one
       adapter, a screen that left it out would run the chapter's *authored*
       dialect and the result would not be what the badge above it promised. */
    const out = await s.api.runQuery(code, { block, module: mod, course, dialect });
    setQr(out);
    setRunning(false);
  }

  function nextChapter() {
    const idx = (course.modules || []).indexOf(mod);
    s.openLessonIn(course.id, idx + 1);
  }

  async function check() {
    if (dialectRefused) return;
    setFb({ kind: 'pending' });
    const out = await s.api.checkAnswer(code, { item: block, module: mod, course, dialect });
    setQr(out.qr);
    if (out.qr.status !== 'succeeded') {
      s.recordEvent(id, 'error', undefined);
      setFb({ kind: 'error', code: out.qr.error!.code });
      return;
    }
    if (out.passed) {
      s.recordEvent(id, 'passed', undefined);
      setFb({ kind: 'passed', checks: out.checks });
      s.toast('Exercise passed. Nice.');
      onPassed?.();
    } else {
      const cause = out.checks.find((r) => !r.ok) || { label: 'the result contract' };
      s.recordEvent(id, 'failed', undefined);
      setFb({ kind: 'failed', checks: out.checks, cause });
    }
  }

  /* M5: capture the plan through the seam and grade it against the perf
     contract. The same bookkeeping a normal exercise uses — a recorded event
     and the rebuilt progress — so a perf exercise is passed, retaken and
     progressed exactly like any other; only the oracle is structural. */
  async function capturePlan(analyze: boolean) {
    if (!perf) return;
    setPlanBusy(true);
    setFb(null);
    try {
      const bundle = await s.api.explain({
        sql: code,
        analyze,
        dataset,
        exercise: { id, version: block.version ?? 1, lesson_id: mod.id, perf },
      });
      setPlan(bundle);
      if (bundle.status !== 'succeeded') {
        setPlanChecks(null);
        s.recordEvent(id, 'error', undefined);
        setFb({ kind: 'error', code: bundle.error?.code ?? 'ENVIRONMENT_UNAVAILABLE' });
        return;
      }
      const verdict = gradePlan(bundle, perf);
      setPlanChecks(verdict.checks);
      if (verdict.passed) {
        s.recordEvent(id, 'passed', undefined);
        s.toast('Plan passed. Nice.');
        onPassed?.();
      } else {
        const cause = verdict.checks.find((r) => !r.ok) || { label: 'the perf contract' };
        s.recordEvent(id, 'failed', undefined);
        s.toast(`Plan not yet — ${String(cause.label).toLowerCase()}`);
      }
    } finally {
      setPlanBusy(false);
    }
  }

  function hint() {
    /* §6.2: viewing a hint is recorded, like the solution, so the history can
       tell "solved it alone" from "solved it with help". */
    s.recordEvent(id, 'hint', undefined);
    setFb({ kind: 'hint', hints: block.hints || [] });
  }
  function solution() {
    s.recordEvent(id, 'solution', undefined);
    setFb({ kind: 'solution' });
    s.toast('Solution revealed.');
  }
  function accept() {
    s.recordEvent(id, 'accepted', undefined);
    setFb({ kind: 'accepted' });
    s.toast('Accepted. Recorded as accepted_by_user, not passed.');
  }
  function keep(sql: string) {
    s.saveQuery(sql, block);
  }
  /* "Explain my mistake" is a real tutor call, not a stub. The gateway serves
     POST /tutor (M4); the button used to toast "in a live app…" which is the
     classic "it's a mock" tell. The endpoint is dialect-neutral — it takes the
     learner's SQL, the objective and the engine's own error — so this is the
     same call on both dialects. AI off (or the offline engine, where
     there is no tutor) reports honestly instead of inventing an answer.

     The level escalates on each ask. Without it, "ask for a firmer hint" sent
     the same request and the model returned the same hint — the level is the
     whole difference between a nudge and a worked approach, so the previous
     hint's level is the floor for the next ask. */
  async function explain() {
    if (tutorBusy) return;
    const nextLevel = fb && fb.kind === 'tutor' ? fb.hintLevel + 1 : undefined;
    setTutorBusy(true);
    setFb({ kind: 'tutorPending' });
    try {
      const objective = String(block.prompt || block.sql || 'Write a correct query for this exercise.');
      const res = await s.api.tutor({
        sql: code,
        objective,
        error_code: qr?.error?.code,
        error_message: qr?.error?.message,
        hint_level: nextLevel,
        result_summary: qr?.status === 'succeeded'
          ? `${qr.row_count} row${qr.row_count === 1 ? '' : 's'} returned`
          : undefined,
      });
      setFb({ kind: 'tutor', message: res.message, hintLevel: res.hint_level, revealsSolution: res.reveals_solution });
    } catch (e) {
      const reason = e instanceof Error && e.message ? e.message : 'The tutor is unavailable right now.';
      setFb({ kind: 'tutorUnavailable', reason });
    } finally {
      setTutorBusy(false);
    }
  }
  function reset() {
    setCode(starter);
    s.api.saveDraft(id, starter);
    setQr(null);
    setFb(null);
    setPlan(null);
    setPlanChecks(null);
    s.toast('Cell reset.');
  }
  /* Open the Ask tutor drawer for this exercise. The cell hands over its own
     latest SQL and run state — the drawer mirrors them back and sends them as
     the facts of the request, never a recomputed guess. */
  function openTutor() {
    const run: TutorRunState =
      qr === null
        ? { status: 'not_run' }
        : qr.status === 'succeeded'
          ? { status: 'ran', rows: qr.row_count }
          : { status: 'error', code: qr.error?.code ?? 'EVALUATION_ERROR', message: qr.error?.message };
    s.openTutor({ block, module: mod, course, sql: code, run });
  }

  /* One status line for assistive technology, and the only live region in the
     cell — so a run and a verdict are each announced exactly once instead of
     racing the result grid and the feedback panel to be read. The words are the
     same words the screen shows. */
  function liveStatus(): string {
    if (planBusy) return 'Capturing the query plan.';
    if (running) return 'Running your query.';
    if (fb) {
      if (fb.kind === 'pending') return 'Checking your answer against the contract.';
      if (fb.kind === 'passed') return 'Your answer passed.';
      if (fb.kind === 'failed') return `Not yet — ${String(fb.cause.label).toLowerCase()}.`;
      if (fb.kind === 'error') return `Execution error ${fb.code}. This is not a wrong answer. ${executionHelp(fb.code)}`;
      if (fb.kind === 'hint') return `${fb.hints.length} hint${fb.hints.length === 1 ? '' : 's'} shown.`;
      if (fb.kind === 'tutorPending') return 'Asking the tutor for a hint.';
      if (fb.kind === 'tutor') return 'Tutor hint shown.';
      if (fb.kind === 'tutorUnavailable') return 'The tutor is unavailable.';
      if (fb.kind === 'solution') return 'Reference solution shown.';
      if (fb.kind === 'accepted') return 'Recorded as accepted by you, not as a pass.';
    }
    if (qr) {
      return qr.status === 'succeeded'
        ? `Query returned ${qr.row_count} ${qr.row_count === 1 ? 'row' : 'rows'}.`
        : `Query failed with ${qr.error?.code ?? 'an error'}.`;
    }
    return '';
  }

  return (
    <div className={`cell${isExercise ? ' exercise' : ''}`} id={`cell-${id}`}>
      <div className="celltop">
        <span className="kindtag">
          <Icon name={isExercise ? 'target' : 'terminal'} />{' '}
          {isExercise ? `Exercise ${id.replace('ex.', '')}` : 'Runnable example'}
        </span>
        <span>{isExercise && block.contract ? `contract · ${block.contract.columns.length} columns` : ''}</span>
      </div>
      <div className="cellbody">
        {isExercise && block.prompt ? <p className="cellprompt">{block.prompt}</p> : null}
        <div className="visually-hidden" role="status" aria-live="polite">{liveStatus()}</div>
        <div className="editorwrap">
          <SqlEditor
            value={code}
            dialect={dialect}
            schema={schema}
            ariaLabel={isExercise ? `SQL editor for exercise ${id.replace('ex.', '')}` : 'SQL editor for this example'}
            describedBy={dialectRefused ? refusalId : undefined}
            onChange={onCode}
            onRun={run}
            onFirstInteraction={requestSchema}
          />
        </div>
        <div className="runrow">
          {isExercise ? (
            <>
              <button
                /* Once the contract passes, the next move is the primary one.
                   Run stays available but stops competing with it. */
                className={fb?.kind === 'passed' ? 'small' : 'primary small'}
                onClick={run}
                disabled={dialectRefused}
                title={dialectRefused ? dialectSupport.refusal ?? undefined : undefined}
                aria-describedby={dialectRefused ? refusalId : undefined}
              ><Icon name="play" />Run</button>
              <button
                className="small"
                onClick={check}
                disabled={dialectRefused}
                title={dialectRefused ? dialectSupport.refusal ?? undefined : undefined}
                aria-describedby={dialectRefused ? refusalId : undefined}
              ><Icon name="check" />Check answer</button>
              <span className="celldesktop-extra">
                <button className="small ghost" onClick={hint}>Hint</button>
                <button className="small ghost" onClick={solution}>Show solution</button>
                {!SIMPLE_MODE && <button className="small ghost" onClick={openTutor}><Icon name="sparkles" />Ask tutor</button>}
                <button className="small ghost" onClick={() => keep(code)}>Keep query</button>
                <button className="small ghost" onClick={reset}><Icon name="refresh" />Reset</button>
              </span>
              {/* On a phone the five secondary controls live behind one menu so
                  the cell keeps two actions and the thumb keeps its target. */}
              <details className="cellmore">
                <summary className="small">More</summary>
                <div className="cellmoremenu">
                  <button className="small ghost" onClick={hint}>Hint</button>
                  <button className="small ghost" onClick={solution}>Show solution</button>
                  {!SIMPLE_MODE && <button className="small ghost" onClick={openTutor}><Icon name="sparkles" />Ask tutor</button>}
                  <button className="small ghost" onClick={() => keep(code)}>Keep query</button>
                  <button className="small ghost" onClick={reset}><Icon name="refresh" />Reset</button>
                </div>
              </details>
            </>
          ) : (
            <>
              <button
                className="small"
                onClick={run}
                disabled={dialectRefused}
                title={dialectRefused ? dialectSupport.refusal ?? undefined : undefined}
                aria-describedby={dialectRefused ? refusalId : undefined}
              ><Icon name="play" />Run</button>
              <span className="celldesktop-extra">
                <button className="small ghost" onClick={() => keep(code)}>Keep query</button>
                <button className="small ghost" onClick={reset}><Icon name="refresh" />Reset</button>
              </span>
              <details className="cellmore">
                <summary className="small">More</summary>
                <div className="cellmoremenu">
                  <button className="small ghost" onClick={() => keep(code)}>Keep query</button>
                  <button className="small ghost" onClick={reset}><Icon name="refresh" />Reset</button>
                </div>
              </details>
            </>
          )}
          <span className="kb"><span className="kbd">Ctrl</span>+<span className="kbd">Enter</span></span>
        </div>
        {/* The reason Run is off, and the one control that fixes it. Rendered
            only when the engine itself said it cannot serve this chapter. */}
        <DialectRefusal
          id={refusalId}
          moduleId={mod.id}
          dialect={dialect}
          support={dialectSupport}
          onSwitched={(to) => s.toast(`This chapter now runs on ${dialectLabel(to)}. Your SQL draft was kept.`)}
        />
        {perf ? (
          <div className="planactions">
            <button
              className="small"
              onClick={() => void capturePlan(false)}
              disabled={planBusy || planBlockedReason !== null}
              title={planBlockedReason ?? 'Ask the planner for a plan without running the statement.'}
            >
              <Icon name="flask" />{planBusy ? 'Capturing…' : 'Show the plan'}
            </button>
            <button
              className="small ghost"
              onClick={() => void capturePlan(true)}
              disabled={planBusy || planBlockedReason !== null || !planWritable}
              title={
                planBlockedReason ??
                (!planWritable
                  ? SIMPLE_MODE
                    ? 'Analyze needs the in-browser workspace to finish opening. Try again in a moment.'
                    : 'Analyze executes the statement, which needs a writable environment (sign in on the gateway).'
                  : 'Run EXPLAIN ANALYZE: the statement is executed so the plan reports measured times.')
              }
            >
              <Icon name="play" />Analyze
            </button>
            {planBlockedReason ? (
              <span className="planunsupported"><Icon name="info" />{planBlockedReason}</span>
            ) : !planWritable ? (
              <span className="muted">
                Analyze runs the statement, so it needs a writable environment{capsKnown ? '.' : ' — checking the engine.'}
              </span>
            ) : (
              <span className="muted">Analyze executes the statement to measure it; Show the plan estimates only.</span>
            )}
          </div>
        ) : null}
        {perf && (planBusy || plan) ? (
          <div className="cellresult" aria-busy={planBusy}>
            {planBusy ? (
              <div className="qrun"><span className="spin"></span>POST /explain → {s.api.config.engine}</div>
            ) : plan ? (
              <PlanView bundle={plan} contract={perf} checks={planChecks ?? undefined} />
            ) : null}
          </div>
        ) : null}
        <div className="cellresult" aria-busy={running}>
          {running ? (
            <div className="qrun"><span className="spin"></span>POST /query → {s.api.config.engine}</div>
          ) : (
            <ResultGrid qr={qr} />
          )}
        </div>
        <div className="cellfeedback" ref={feedbackRef}>{renderFeedback(fb, { block, hint, solution, accept, run, keep, nextChapter, explain })}</div>
        {block.note ? <div className="cellnote">{block.note}</div> : null}
      </div>
    </div>
  );
}

function CheckList({ checks }: { checks: CheckResult[] }) {
  return (
    <ul className="checklist">
      {checks.map((r) => (
        <li key={`${r.code}-${r.label}`}>
          <span className={`mk ${r.ok ? 'ok' : 'no'}`}>{r.ok ? '✓' : '✕'}</span>
          <span>
            {r.label}
            {!r.ok && <span className="fbcode"> {r.code}</span>}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** The recovery a learner can act on, per execution error. The old copy told
    every failure to "fix the statement", which is false for a missing engine,
    a timeout, or a result cap. */
function executionHelp(code: string): string {
  switch (code) {
    case 'QUERY_SYNTAX_ERROR':
      return 'The database rejected the syntax. Fix the statement and run again.';
    case 'QUERY_RUNTIME_ERROR':
      return 'The statement failed at runtime. Check the names and the data, then run again.';
    case 'QUERY_TIMEOUT':
      return 'The statement took too long. Narrow the work or raise the timeout, then run again.';
    case 'RESULT_LIMIT_EXCEEDED':
      return 'The result exceeded the limit. Add a filter or a LIMIT, then run again.';
    case 'ENVIRONMENT_UNAVAILABLE':
      return 'The database is not reachable right now. This is not a problem in your statement. Try again when the workspace is ready.';
    case 'PERSISTENCE_FAILED':
      return 'The workspace could not save the result. Try again.';
    case 'EVALUATION_ERROR':
      return 'The answer could not be checked. Try again.';
    default:
      return 'The statement did not run. Try again, or read the error code above.';
  }
}

function renderFeedback(fb: Feedback | null, ctx: FeedbackCtx) {
  if (!fb) return null;
  const { block, hint, solution, accept, run, keep, nextChapter, explain } = ctx;
  if (fb.kind === 'pending') {
    return <div className="fb pending"><span className="spin"></span> &nbsp;POST /answers/evaluate — checking the result contract…</div>;
  }
  if (fb.kind === 'error') {
    return (
      <div className="fb error">
        <div className="fbhead"><Icon name="alert" />Execution error — not a wrong answer</div>
        This is an execution failure, so it is reported as <span className="fbcode">{fb.code}</span>, not as a failed attempt. {executionHelp(fb.code)}
        <div className="fbactions actions">
          <button className="small ghost" onClick={explain}>
            <Icon name="sparkles" />Explain my mistake
          </button>
        </div>
      </div>
    );
  }
  if (fb.kind === 'hint') {
    return (
      <div className="fb">
        <div className="fbhead"><Icon name="info" />Hints</div>
        {fb.hints.map((h, i) => (
          /* Ordered hints from one evaluation; the number is the identity. */
          <p key={i} style={{ margin: '4px 0', color: 'var(--text-2)', fontSize: 13 }}>
            <strong>Hint {i + 1}.</strong> <span dangerouslySetInnerHTML={{ __html: h }} />
          </p>
        ))}
        <div className="fbactions actions">
          <button className="small ghost" onClick={run}>Back to result</button>
        </div>
      </div>
    );
  }
  if (fb.kind === 'tutorPending') {
    return <div className="fb pending"><span className="spin"></span> &nbsp;POST /tutor — asking for a nudge…</div>;
  }
  if (fb.kind === 'tutorUnavailable') {
    return (
      <div className="fb">
        <div className="fbhead"><Icon name="info" />Tutor unavailable</div>
        {fb.reason} The exercise still grades normally — the tutor is a helper, not a gate.
        <div className="fbactions actions">
          <button className="small ghost" onClick={run}>Back to result</button>
        </div>
      </div>
    );
  }
  if (fb.kind === 'tutor') {
    return (
      <div className="fb">
        <div className="fbhead"><Icon name="sparkles" />Tutor</div>
        <p style={{ margin: '4px 0', color: 'var(--text-1)', fontSize: 13.5, lineHeight: 1.65 }}>{fb.message}</p>
        <p className="muted mt-8" style={{ fontSize: 12 }}>
          {fb.revealsSolution
            ? 'This hint names the approach. Your result is still what passes — any correct formulation counts.'
            : `Guidance, not the answer — a ${fb.hintLevel === 1 ? 'gentle' : 'firmer'} nudge. Ask again for more.`}
        </p>
        <div className="fbactions actions">
          <button className="small ghost" onClick={explain}>
            <Icon name="sparkles" />Ask for a firmer hint
          </button>
          <button className="small ghost" onClick={run}>Back to result</button>
        </div>
      </div>
    );
  }
  if (fb.kind === 'solution') {
    return (
      <div className="fb">
        <div className="fbhead"><Icon name="eye" />Reference solution</div>
        <pre style={{ margin: 0, fontFamily: 'var(--mono)', fontSize: 12.5, lineHeight: 1.7, whiteSpace: 'pre-wrap', color: 'var(--code-text)', background: 'var(--code-bg)', border: '1px solid var(--code-line)', borderRadius: 6, padding: 12 }}>
          {block.solution || ''}
        </pre>
        <p className="muted mt-8" style={{ fontSize: 12 }}>
          Viewing the solution is recorded as an event. Any correct result passes — this is one of several valid formulations.
        </p>
        <div className="fbactions actions">
          <button className="small ghost" onClick={() => keep(String(block.solution || ''))}>Keep this solution</button>
          <button className="small ghost" onClick={run}>Hide</button>
        </div>
      </div>
    );
  }
  if (fb.kind === 'accepted') {
    return (
      <div className="fb" style={{ background: 'var(--accent-soft)' }}>
        <div className="fbhead" style={{ color: 'var(--accent)' }}><Icon name="info" />Accepted by you</div>
        Recorded separately from a pass — never counted as correct. Progress is not blocked, and the original result is kept as history.
      </div>
    );
  }
  if (fb.kind === 'passed') {
    return (
      <div className="fb passed">
        <span className="verdict-stamp" aria-hidden="true">Passed</span>
        <div className="fbhead"><Icon name="check" />Passed</div>
        Your result satisfies the contract on the current exercise version.
        <CheckList checks={fb.checks} />
        <div className="fbactions actions verdict-actions">
          <span className="verdict-record"><span className="dot" aria-hidden="true" />Recorded</span>
          <button className="primary small" onClick={nextChapter}>Next chapter<Icon name="arrow-right" /></button>
        </div>
      </div>
    );
  }
  if (fb.kind === 'failed') {
    return (
      <div className="fb failed">
        <div className="fbhead"><Icon name="alert" />Not yet — {String(fb.cause.label).toLowerCase()}</div>
        Correctness first: the result does not satisfy the contract yet.
        <CheckList checks={fb.checks} />
        <div className="fbactions actions">
          <button className="small" onClick={hint}>Show hint</button>
          <button className="small ghost" onClick={solution}>Show solution</button>
          <button className="small ghost" onClick={accept}>Accept my answer</button>
        </div>
      </div>
    );
  }
  return null;
}
