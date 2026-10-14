import React, { useEffect, useRef, useState } from 'react';
import Icon from './ui/Icon';
import ResultGrid from './ResultGrid';
import { useStore } from '../state/store';
import type { Block, CheckResult, Course, CourseModule, EventKind, QueryResult } from '../types';

type Feedback =
  | { kind: 'pending' }
  | { kind: 'error'; code: string }
  | { kind: 'hint'; hints: string[] }
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
  reportProblem: () => void;
}

/* One inline SQL cell. Holds its own draft + result + feedback state; execution
   and grading always go through the API boundary, never computed here. */
export default function SqlCell({ block, module: mod, course }: { block: Block; module: CourseModule; course: Course }) {
  const s = useStore();
  const isExercise = block.type === 'exercise';
  const id = block.id as string;
  const starter = isExercise ? block.starter || 'SELECT ...' : (block.sql as string);

  const [code, setCode] = useState(starter);
  const [running, setRunning] = useState(false);
  const [qr, setQr] = useState<QueryResult | null>(null);
  const [fb, setFb] = useState<Feedback | null>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    let alive = true;
    s.api.getDraft(id).then((draft) => {
      if (alive && draft != null) setCode(draft);
    });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  function autogrow() {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = 'auto';
    ta.style.height = Math.max(96, ta.scrollHeight) + 'px';
  }
  useEffect(autogrow, [code]);

  function onChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    setCode(e.target.value);
    s.api.saveDraft(id, e.target.value);
  }

  async function run() {
    setRunning(true);
    setFb(null);
    const out = await s.api.runQuery(code, block);
    setQr(out);
    setRunning(false);
  }

  function nextChapter() {
    const idx = (course.modules || []).indexOf(mod);
    s.openLessonIn(course.id, idx + 1);
  }

  async function check() {
    setFb({ kind: 'pending' });
    const out = await s.api.checkAnswer(code, { item: block, module: mod, course });
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
    } else {
      const cause = out.checks.find((r) => !r.ok) || { label: 'the result contract' };
      s.recordEvent(id, 'failed', undefined);
      setFb({ kind: 'failed', checks: out.checks, cause });
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
  function explain() {
    s.toast('In a live app, a tutor call would use your query, the error and the objective.');
  }
  function reportProblem() {
    s.toast('This would open the on-demand critique flow: critic → verifier → fixer, with your approval before anything applies.');
  }
  function reset() {
    setCode(starter);
    s.api.saveDraft(id, starter);
    setQr(null);
    setFb(null);
    s.toast('Cell reset.');
  }

  const lineCount = Math.max(String(starter).split('\n').length, 6);
  const gutter = Array.from({ length: lineCount }, (_, i) => i + 1).join('\n');

  /* One status line for assistive technology, and the only live region in the
     cell — so a run and a verdict are each announced exactly once instead of
     racing the result grid and the feedback panel to be read. The words are the
     same words the screen shows. */
  function liveStatus(): string {
    if (running) return 'Running your query.';
    if (fb) {
      if (fb.kind === 'pending') return 'Checking your answer against the contract.';
      if (fb.kind === 'passed') return 'Your answer passed.';
      if (fb.kind === 'failed') return `Not yet — ${String(fb.cause.label).toLowerCase()}.`;
      if (fb.kind === 'error') return `Execution error ${fb.code}. This is not a wrong answer; fix the statement and run again.`;
      if (fb.kind === 'hint') return `${fb.hints.length} hint${fb.hints.length === 1 ? '' : 's'} shown.`;
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
        <div className="visually-hidden" role="status" aria-live="polite">{liveStatus()}</div>
        <div className="editorwrap">
          <div className="gutter" aria-hidden="true">{gutter}</div>
          <textarea
            ref={taRef}
            className="editor"
            spellCheck="false"
            aria-label={isExercise ? `SQL editor for exercise ${id.replace('ex.', '')}` : 'SQL editor for this example'}
            value={code}
            onChange={onChange}
            onKeyDown={(e) => {
              if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                e.preventDefault();
                run();
              }
            }}
          />
        </div>
        <div className="runrow">
          {isExercise ? (
            <>
              <button className="primary small" onClick={run}><Icon name="play" />Run</button>
              <button className="small" onClick={check}><Icon name="check" />Check answer</button>
              <button className="small ghost" onClick={hint}>Hint</button>
              <button className="small ghost" onClick={solution}>Show solution</button>
              <button className="small ghost" onClick={() => keep(code)}>Keep query</button>
              <button className="small ghost" onClick={reset}><Icon name="refresh" />Reset</button>
            </>
          ) : (
            <>
              <button className="small" onClick={run}><Icon name="play" />Run</button>
              <button className="small ghost" onClick={() => keep(code)}>Keep query</button>
              <button className="small ghost" onClick={reset}><Icon name="refresh" />Reset</button>
            </>
          )}
          <span className="kb"><span className="kbd">Ctrl</span>+<span className="kbd">Enter</span></span>
        </div>
        <div className="cellresult" aria-busy={running}>
          {running ? (
            <div className="qrun"><span className="spin"></span>POST /query → {s.api.config.engine}</div>
          ) : (
            <ResultGrid qr={qr} />
          )}
        </div>
        <div className="cellfeedback">{renderFeedback(fb, { block, hint, solution, accept, run, keep, nextChapter, explain, reportProblem })}</div>
        {block.note ? <div className="cellnote">{block.note}</div> : null}
      </div>
    </div>
  );
}

function CheckList({ checks }: { checks: CheckResult[] }) {
  return (
    <ul className="checklist">
      {checks.map((r, i) => (
        <li key={i}>
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

function renderFeedback(fb: Feedback | null, ctx: FeedbackCtx) {
  if (!fb) return null;
  const { block, hint, solution, accept, run, keep, nextChapter, explain, reportProblem } = ctx;
  if (fb.kind === 'pending') {
    return <div className="fb pending"><span className="spin"></span> &nbsp;POST /answers/evaluate — checking the result contract…</div>;
  }
  if (fb.kind === 'error') {
    return (
      <div className="fb error">
        <div className="fbhead"><Icon name="alert" />Execution error — not a wrong answer</div>
        This is an execution failure, so it is reported as <span className="fbcode">{fb.code}</span>, not as a failed attempt. Fix the statement and run again.
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
        <div className="fbhead"><Icon name="check" />Passed</div>
        Your result satisfies the contract on the current exercise version.
        <CheckList checks={fb.checks} />
        <div className="fbactions actions">
          <span className="pill subtle"><span className="dot"></span> Recorded</span>
          <button className="small ghost" onClick={nextChapter}>Next chapter<Icon name="arrow-right" /></button>
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
          <button className="small ghost" onClick={reportProblem}>
            <Icon name="sparkles" />Report a problem
          </button>
        </div>
      </div>
    );
  }
  return null;
}
