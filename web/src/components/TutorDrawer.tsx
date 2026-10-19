import { useEffect, useMemo, useRef, useState } from 'react';
import Icon from './ui/Icon';
import Markdown from './ui/Markdown';
import DialectBadge, { useChapterDialect } from './ui/DialectBadge';
import { useDrawerFocus } from './ui/useDrawerFocus';
import { useTutorChat, tutorTitle } from './ui/useTutorChat';
import { useStore } from '../state/store';
import { datasetFor } from '../lib/dataset';
import { dialectLabel } from '../lib/dialect';
import { DEFAULT_DATASET } from '../types';
import type { Dialect, TutorRunState, TutorTarget } from '../types';
import type { AiTutorContext } from '../api/aiContract';

/*
 * The Ask tutor drawer (§6.5, §11.2; design of record
 * docs/reviews/2026-10-18/tutor-drawer-design.md).
 *
 * A sibling of the Tables drawer that shares its slot, motion and focus
 * contract. It holds a session conversation about one exercise; a reload starts
 * empty and the foot says so. Everything the learner reads about what the tutor
 * saw comes from the response `context`, never a client guess.
 */

const CHIPS = [
  'Why is my result wrong?',
  'Which tables should I use?',
  'Walk me through the approach',
  'What does this error mean?',
];

const INTRO =
  'Ask about this exercise. The tutor sees your query, the tables it runs on, and the result once you run it. It explains the problem, it does not hand over the answer.';
const GUIDANCE_NOTE = 'Guidance, not the answer, a gentle nudge. Ask a follow-up for more.';
const REVEAL_NOTE = 'This reply names the approach. Your result still decides the pass, and any correct formulation counts.';
const NOT_SAVED = 'This chat is not saved. Reloading starts a new one. Your SQL draft is kept.';

type ExpectedState = 'included' | 'columns_only' | 'unavailable';
type LearnerResult = 'ran' | 'error' | 'not_run' | 'unavailable';

/** The facts the drawer renders. Every field comes from the gateway's
    `context` once a turn has answered; before that, only the facts the client
    will send, and no expected-result claim, because the client does not know
    whether the gateway included one. */
interface SeenFacts {
  known: boolean;
  exerciseId: string;
  exerciseTitle: string;
  dataset: string;
  dialect: Dialect;
  tables: string[];
  tablesTruncated: boolean;
  expected: ExpectedState;
  expectedColumns: number;
  expectedRows: number;
  learnerResult: LearnerResult;
}

function seenFacts(shown: TutorTarget | null, tables: string[], dialect: Dialect, ctx: AiTutorContext | undefined): SeenFacts {
  if (!shown) {
    return {
      known: false,
      exerciseId: '',
      exerciseTitle: '',
      dataset: DEFAULT_DATASET,
      dialect,
      tables,
      tablesTruncated: false,
      expected: 'unavailable',
      expectedColumns: 0,
      expectedRows: 0,
      learnerResult: 'not_run',
    };
  }
  if (ctx) {
    return {
      known: true,
      exerciseId: ctx.exercise_id,
      exerciseTitle: tutorTitle(shown),
      dataset: ctx.dataset,
      dialect,
      tables: ctx.tables,
      tablesTruncated: ctx.tables_truncated,
      expected: ctx.expected,
      expectedColumns: ctx.expected_columns,
      expectedRows: ctx.expected_rows,
      learnerResult: ctx.learner_result,
    };
  }
  return {
    known: false,
    exerciseId: String(shown.block.id ?? ''),
    exerciseTitle: tutorTitle(shown),
    dataset: datasetFor({ course: shown.course, block: shown.block }),
    dialect,
    tables,
    tablesTruncated: false,
    expected: 'unavailable',
    expectedColumns: shown.block.contract?.columns.length ?? 0,
    expectedRows: 0,
    learnerResult: shown.run.status,
  };
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** The collapsed sentence. It answers one question: what did the tutor see? */
function summarySentence(f: SeenFacts): string {
  const head = `This exercise, the ${f.dataset} tables (${f.tables.length})`;
  if (f.known && f.expected === 'included') return `${head}, your last run, and the expected result.`;
  if (f.known && f.expected === 'columns_only') {
    return `${head}, your SQL, and the expected columns. You have not attempted yet, so the tutor cannot see the expected rows.`;
  }
  if (f.known && f.expected === 'unavailable') {
    return `${head}, and your SQL. This exercise carries no expected result for the tutor.`;
  }
  if (f.learnerResult === 'not_run') return `${head}, and your SQL. You have not run it, so there is no result yet.`;
  if (f.learnerResult === 'unavailable') {
    return `${head}, and your SQL. The gateway could not run it, so the tutor sees no result.`;
  }
  if (f.learnerResult === 'error') return `${head}, and your SQL. Your last run failed, so the tutor sees the error.`;
  return `${head}, and your SQL, and your last run.`;
}

/** The run state under the SQL block, in the engine's words. The learner's own
    numbers are kept for a run; the gateway's `context` is the source when it
    reports that it could not run the statement, or that none was attempted. */
function runLine(run: TutorRunState): string {
  if (run.status === 'error') return `Error · ${run.code}`;
  if (run.status === 'ran') return `Ran · ${plural(run.rows, 'row', 'rows')} returned`;
  return 'Not run yet. The tutor sees your SQL but no result.';
}

/** One line under the SQL block, with an optional detail the drawer prints
    beneath it. */
interface SqlRunLine {
  line: string;
  detail?: string;
}

function sqlRun(f: SeenFacts, run: TutorRunState): SqlRunLine {
  if (f.known && f.learnerResult === 'unavailable') {
    return { line: 'The gateway could not run this SQL, so the tutor sees it without a result.' };
  }
  if (f.known && f.learnerResult === 'not_run') {
    return { line: 'Not run yet. The tutor sees your SQL but no result.' };
  }
  if (run.status === 'error') return { line: runLine(run), detail: run.message };
  return { line: runLine(run) };
}

function expectedText(f: SeenFacts): string {
  if (f.expected === 'included') {
    const rows = f.expectedRows ? `, ${plural(f.expectedRows, 'row', 'rows')}` : '';
    return `${plural(f.expectedColumns, 'column', 'columns')}${rows}`;
  }
  if (f.expected === 'columns_only') return `${plural(f.expectedColumns, 'column', 'columns')}; rows hidden until you attempt`;
  return 'This exercise carries no expected result.';
}

export default function TutorDrawer() {
  const s = useStore();
  const target = s.tutorTarget;
  const open = target !== null;
  const asideRef = useRef<HTMLElement | null>(null);
  const composerRef = useRef<HTMLTextAreaElement | null>(null);
  useDrawerFocus(open, asideRef, composerRef);

  /* Keep the last exercise on screen while the panel slides out, so a close
     does not blank the content mid-transition. */
  const [shown, setShown] = useState<TutorTarget | null>(null);
  useEffect(() => {
    if (target) setShown(target);
  }, [target]);

  const dialect = useChapterDialect(shown?.module ?? null, shown?.course ?? null);
  const dataset = shown ? datasetFor({ course: shown.course, block: shown.block }) : DEFAULT_DATASET;
  const chat = useTutorChat(target, s.api);
  const [text, setText] = useState('');
  const [tables, setTables] = useState<string[]>([]);

  useEffect(() => {
    if (!shown) return;
    let alive = true;
    setTables([]);
    /* The strip lists the dataset the exercise runs on, read from the same
       gateway the Tables drawer reads, so it names real relations. */
    s.api
      .getTables(dataset, dialect)
      .then((res) => {
        if (alive) setTables(res.tables.map((t) => t.name));
      })
      .catch(() => {
        if (alive) setTables([]);
      });
    return () => {
      alive = false;
    };
  }, [s.api, dataset, dialect, shown]);

  const facts = useMemo(() => seenFacts(shown, tables, dialect, chat.state.context ?? undefined), [shown, tables, dialect, chat.state.context]);
  const thinking = chat.state.phase === 'thinking';
  const lastTurn = chat.state.turns[chat.state.turns.length - 1];

  function openTables(table?: string): void {
    s.openTables(table);
  }

  function send(): void {
    if (thinking) return;
    const next = text.trim();
    if (!next) return;
    setText('');
    chat.ask(next);
  }

  function fillChip(question: string): void {
    setText(question);
    composerRef.current?.focus();
  }

  function liveStatus(): string {
    if (thinking) return 'Asking the tutor.';
    if (chat.state.phase === 'rate_limited') return 'Tutor limit reached. Try again in a few minutes.';
    if (chat.state.phase === 'error') {
      return chat.state.errorCode === 'AI_UNAVAILABLE' || chat.state.errorCode === 'ENVIRONMENT_UNAVAILABLE'
        ? 'The tutor is unavailable. The exercise still grades normally.'
        : 'The tutor could not answer. Your exercise is unchanged.';
    }
    if (lastTurn?.role === 'tutor') return 'Tutor reply shown.';
    return '';
  }

  const headerId = String(shown?.block.id ?? '').replace('ex.', '');
  const title = tutorTitle(shown);
  const runSql = shown ? sqlRun(facts, shown.run) : { line: '' };

  return (
    <>
      <div className={`drawer-back${open ? ' open' : ''}`} onClick={() => s.closeTutor()} aria-hidden="true"></div>
      <aside
        ref={asideRef}
        className={`drawer tutor-drawer${open ? ' open' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label="Ask tutor"
        tabIndex={-1}
      >
        <div className="drawerhead">
          <div style={{ minWidth: 0 }}>
            <div className="eyebrow">Ask tutor</div>
            <h2>
              {headerId ? `Exercise ${headerId}` : 'Ask tutor'}
              {title ? ` · ${title}` : ''}
            </h2>
            <div className="tutor-badges">
              <span className="pill"><Icon name="database" />{facts.dataset} · {plural(facts.tables.length, 'table', 'tables')}</span>
              <DialectBadge dialect={dialect} />
            </div>
          </div>
          <button className="small ghost" aria-label="Close" onClick={() => s.closeTutor()}><Icon name="x" /></button>
        </div>

        <div className="drawerbody">
          <div className="visually-hidden" role="status" aria-live="polite">{liveStatus()}</div>

          {shown ? (
            <>
              <div className="tutor-context">
                <div className="tutor-context-head"><Icon name="sparkles" />The tutor can see</div>
                <p className="tutor-context-line">{summarySentence(facts)}</p>
                <details className="tutor-sees" open>
                  <summary><span>What the tutor sees</span><Icon name="chevron-down" className="ico chev" /></summary>
                  <ul>
                    <li><span className="k">Exercise</span><span className="v">{facts.exerciseId}{facts.exerciseTitle ? ` · ${facts.exerciseTitle}` : ''}</span></li>
                    <li><span className="k">Dataset</span><span className="v">{facts.dataset} on {dialectLabel(facts.dialect)}</span></li>
                    <li><span className="k">Tables</span><span className="v">{facts.tables.join(', ') || 'none'}</span></li>
                    <li><span className="k">Your query</span><span className="v">{runSql.line}{runSql.detail ? ` — ${runSql.detail}` : ''}</span></li>
                    {facts.known ? <li><span className="k">Expected</span><span className="v">{expectedText(facts)}</span></li> : null}
                    {facts.tablesTruncated ? <li><span className="k">Tables shown</span><span className="v">first {facts.tables.length} of more</span></li> : null}
                  </ul>
                </details>
              </div>

              <div className="tutor-strip">
                <div className="tutor-strip-head">
                  <span className="eyebrow">Tables in this dataset</span>
                  <button className="tutor-link" onClick={() => openTables()}>Open tables</button>
                </div>
                <div className="tutor-chips">
                  {facts.tables.map((name) => (
                    <button key={name} className="tutor-chip" onClick={() => openTables(name)}>{name}</button>
                  ))}
                </div>
              </div>

              <div className="tutor-sql">
                <div className="eyebrow">Your latest query</div>
                <pre>{shown.sql}</pre>
                <div className="tutor-sqlrun">{runSql.line}{runSql.detail ? <span className="tutor-sqldetail"> {runSql.detail}</span> : null}</div>
              </div>

              {chat.state.turns.length === 0 && chat.state.phase === 'idle' ? <p className="tutor-intro">{INTRO}</p> : null}

              {chat.state.turns.length > 0 ? (
                <div className="tutor-turns">
                  {/* Append-only conversation for this exercise; turns are never
                      reordered or deleted, so the position is stable. */}
                  {chat.state.turns.map((turn, i) => (
                    <div className="tutor-turn" key={i}>
                      <div className={`tutor-role${turn.role === 'tutor' ? ' tutor' : ''}`}>
                        {turn.role === 'tutor' ? <Icon name="sparkles" /> : null}
                        {turn.role === 'tutor' ? 'Tutor' : 'You'}
                      </div>
                      <div className={`tutor-bubble ${turn.role}`}>
                        {turn.role === 'tutor' ? <Markdown source={turn.text} /> : turn.text}
                      </div>
                      {turn.role === 'tutor' ? (
                        <div className="tutor-turnnote">{turn.revealsSolution ? REVEAL_NOTE : GUIDANCE_NOTE}</div>
                      ) : null}
                    </div>
                  ))}
                  {thinking ? (
                    <div className="fb pending tutor-pending"><span className="spin"></span> &nbsp;Asking the tutor…</div>
                  ) : null}
                </div>
              ) : null}

              {chat.state.phase === 'error' || chat.state.phase === 'rate_limited' ? (
                <div className="tutor-error">
                  {chat.state.phase === 'rate_limited' ? (
                    <>
                      <div className="fbhead"><Icon name="clock" />Tutor limit reached</div>
                      <p>You have sent the tutor its hourly limit of questions. Try again in a few minutes. Your exercise still grades normally.</p>
                      <div className="tutor-actions">
                        <button className="small" disabled>Retry</button>
                        <button className="small ghost" onClick={chat.dismiss}>Dismiss</button>
                      </div>
                    </>
                  ) : chat.state.errorCode === 'AI_UNAVAILABLE' || chat.state.errorCode === 'ENVIRONMENT_UNAVAILABLE' ? (
                    <>
                      <div className="fbhead"><Icon name="info" />Tutor unavailable</div>
                      <p>{chat.state.errorMessage} The tutor needs the gateway. This exercise still grades normally. The tutor is a helper, not a gate.</p>
                      <div className="tutor-actions">
                        <button className="small" onClick={chat.retry}>Retry</button>
                        <button className="small ghost" onClick={chat.dismiss}>Dismiss</button>
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="fbhead"><Icon name="alert" />The tutor could not answer{chat.state.errorCode ? ` · ${chat.state.errorCode}` : ''}</div>
                      {chat.state.errorMessage ? <p>{chat.state.errorMessage}</p> : null}
                      <p>The tutor call failed. Nothing here changed your exercise. Ask again in a moment.</p>
                      <div className="tutor-actions">
                        <button className="small" onClick={chat.retry}>Retry</button>
                        <button className="small ghost" onClick={chat.dismiss}>Dismiss</button>
                      </div>
                    </>
                  )}
                </div>
              ) : null}
            </>
          ) : null}
        </div>

        <footer className="drawerfoot">
          <div className="tutor-chips tutor-suggest">
            {CHIPS.map((question) => (
              <button key={question} className="tutor-sug" disabled={thinking} onClick={() => fillChip(question)}>{question}</button>
            ))}
          </div>
          <div className="tutor-composer">
            <textarea
              ref={composerRef}
              rows={2}
              aria-label="Ask about this exercise"
              placeholder="Ask about this exercise…"
              value={text}
              disabled={thinking}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault();
                  send();
                }
              }}
            />
            <button className="primary" disabled={thinking || text.trim() === ''} onClick={send}>Send</button>
          </div>
          <div className="tutor-footrow">
            <span className="tutor-kbd"><span className="kbd">Enter</span> to send · <span className="kbd">Shift</span>+<span className="kbd">Enter</span> for a new line</span>
            <span className="tutor-notsaved"><Icon name="info" />{NOT_SAVED}</span>
          </div>
        </footer>
      </aside>
    </>
  );
}
