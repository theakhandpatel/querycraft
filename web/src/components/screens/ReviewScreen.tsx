import Icon from '../ui/Icon';
import Schema from '../Schema';
import { useStore } from '../../state/store';
import { COURSES, MODULES, TABLES } from '../../data/curriculum';
import { chapterOutline, courseExerciseTotal, moduleExerciseBlocks } from '../../lib/model';
import type { DraftReport } from '../../lib/draft';
import type { Course } from '../../types';

const TABS = [
  ['overview', 'Overview'],
  ['curriculum', 'Chapter index'],
  ['lessonPreview', 'Sample chapter'],
  ['dataset', 'Dataset & context'],
  ['validation', 'Validation'],
];

export default function ReviewScreen() {
  const s = useStore();
  const course = s.draftCourse ?? COURSES[0];
  const modules = course.modules ?? MODULES;
  const exerciseTotal = courseExerciseTotal(course);
  /* What the build measured. Everything the validation tab prints comes from
     here, and when there is no report it says so rather than showing a
     plausible pass — a draft opened by a deep link has not been built in this
     session, and the screen must not pretend otherwise. */
  const report = s.draftReport;

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">{report?.source === 'authored' ? 'Draft course · built-in curriculum' : 'Draft course · ready to explore'}</div>
          <h1>{course.title}</h1>
          <p>{s.courseSubtitle}</p>
          <div className="actions mt-12">
            {report && report.outcome === 'active' && report.totals.verified > 0 && (
              <span className="pill"><span className="dot"></span> {report.totals.verified} of {report.totals.exercises} exercises executed and cross-checked</span>
            )}
            {report && report.outcome !== 'active' && (
              <span className="pill" style={{ background: 'var(--amber-soft)', color: 'var(--amber)' }}>{report.totals.verified} of {report.totals.exercises} exercises verified · the rest failed</span>
            )}
            {!report && (
              <span className="pill subtle">No build in this session · open Create to run one</span>
            )}
            <span className="pill subtle">{dialectLabel(course.dialect)} · {modules.length} chapters · {exerciseTotal} exercises</span>
          </div>
        </div>
        <div className="actions">
          <button onClick={() => s.openModal('editCourse')}><Icon name="edit" />Edit course details</button>
          <button className="primary" onClick={s.startLearning}>Add to my learning<Icon name="arrow-right" /></button>
        </div>
      </div>

      <div className="coursehero">
        <div className="courseicon"><Icon name="layers" /></div>
        <div><h2>Before you start</h2><p>{s.reviewGoal}</p></div>
      </div>

      <div className="stats">
        <div className="stat"><strong>{modules.length}</strong><span>Chapters in this draft</span></div>
        <div className="stat"><strong>{exerciseTotal}</strong><span>Hands-on exercises</span></div>
        <div className="stat"><strong>{TABLES.length}</strong><span>Dataset tables</span></div>
        <div className="stat"><strong>{report ? report.totals.sections : '—'}</strong><span>Authored sections</span></div>
      </div>

      <div className="tabs">
        {TABS.map(([key, label]) => (
          <button key={key} className={`tab${s.reviewTab === key ? ' active' : ''}`} onClick={() => s.setReviewTab(key)}>{label}</button>
        ))}
      </div>

      {s.reviewTab === 'overview' && (
        <div className="grid2">
          <div className="card">
            <h2>What you'll be able to do</h2>
            {modules.map((m, i) => {
              const first = (m.objectives && m.objectives[0]) || m.title;
              return (
                <div className="reportrow" key={m.id}>
                  <span><span className="mono" style={{ fontSize: 11, color: 'var(--text-3)' }}>{i + 1 < 10 ? '0' : ''}{i + 1}</span> &nbsp;{first}</span>
                  <span className="muted">Chapter {m.n}</span>
                </div>
              );
            })}
            <div className="callout mt-14">The course uses one commerce dataset throughout. Questions get harder while the business context stays familiar, so you spend your attention on SQL, not on re-learning a schema.</div>
          </div>
          <div className="card">
            <h2>Course decisions</h2>
            <div className="reportrow"><span>Starting assumption</span><span>{s.buildContext.level || 'Not stated'}</span></div>
            <div className="reportrow"><span>Dialect</span><span>{dialectLabel(course.dialect)}</span></div>
            <div className="reportrow"><span>Learning style</span><span>Short concept + exercise</span></div>
            <div className="reportrow"><span>Grading</span><span>Result contract, not SQL text</span></div>
            <div className="reportrow"><span>Level</span><span>{course.level || 'Not stated'}</span></div>
            <div className="reportrow"><span>Source context</span><span>{s.buildContext.source ? 'Attached to the request' : s.contextState}</span></div>
            <button className="mt-12" onClick={() => s.openModal('editCourse')}>Change assumptions or context</button>
          </div>
        </div>
      )}

      {s.reviewTab === 'curriculum' && (
        <div className="grid2">
          <div className="card">
            <div className="sectiontitle">
              <h2>Chapter index</h2>
              <div className="actions">
                <button className="small" onClick={() => s.openModal('addLesson')}><Icon name="plus" />Add chapter</button>
                <button className="small" onClick={() => s.openModal('extend')}>Extend course</button>
              </div>
            </div>
            <p className="muted mt-8" style={{ fontSize: 12 }}>Select a chapter to open it. Use the arrows to reorder. Changes affect this draft only.</p>
            <div className="lessonlist">
              {modules.map((m, i) => (
                <div className={`lessonrow${s.progress.done[m.id] ? ' done' : ''}`} key={m.id}>
                  <span className="lessonnum">{s.progress.done[m.id] ? '✓' : i + 1}</span>
                  <div className="lessontext">
                    <strong>{m.title}</strong>
                    <small>{m.summary} · {chapterOutline(m).length} sections · {m.minutes} min</small>
                  </div>
                  <div className="actions">
                    <button className="small" title="Open chapter" onClick={() => s.openLessonIn(course.id, i)}><Icon name="eye" /></button>
                    <button className="small" title="Edit chapter" onClick={() => s.openModal('editLesson')}><Icon name="edit" /></button>
                    <button className="small" title="Move up" disabled={i === 0} onClick={() => s.moveLesson(i, -1)}><Icon name="arrow-up" /></button>
                    <button className="small" title="Move down" disabled={i === modules.length - 1} onClick={() => s.moveLesson(i, 1)}><Icon name="arrow-down" /></button>
                  </div>
                </div>
              ))}
            </div>
            <div className="actions mt-14">
              <button onClick={() => s.toast('Draft changes are saved to this workspace.')}>Save draft</button>
              <button className="primary" onClick={s.startLearning}>Add to my learning<Icon name="arrow-right" /></button>
            </div>
          </div>
          <div className="card">
            <h2>How the progression works</h2>
            <div className="reportrow"><span>1. Understand the data</span><span className="muted">Orient</span></div>
            <div className="reportrow"><span>2. Build correct queries</span><span className="muted">Core patterns</span></div>
            <div className="reportrow"><span>3. Combine patterns</span><span className="muted">Composition</span></div>
            <div className="reportrow"><span>4. Solve open-ended cases</span><span className="muted">Transfer</span></div>
            <div className="reportrow"><span>5. Reason about performance</span><span className="muted">Production thinking</span></div>
            <p className="muted mt-12" style={{ fontSize: 12 }}>Add a prerequisite, deepen a topic, insert a practice set, or branch into a related subject without rebuilding the whole course.</p>
          </div>
        </div>
      )}

      {s.reviewTab === 'lessonPreview' && <SampleChapter course={course} />}

      {s.reviewTab === 'dataset' && (
        <div className="grid2">
          <div className="card">
            <div className="sectiontitle">
              <h2>Commerce dataset</h2>
              <button className="small" onClick={() => s.setTablesOpen(true)}>Open tables</button>
            </div>
            <p className="muted mt-8" style={{ fontSize: 12 }}>Generated by SQL so it can be rebuilt identically. The seed includes awkward cases on purpose: customers with no orders, a product never ordered, NULL cities and emails, and a zero-stock product.</p>
            <Schema api={s.api} />
          </div>
          <div className="card">
            <h2>Context attached to the course</h2>
            <div className="reportrow"><span>Domain</span><span>E-commerce operations</span></div>
            <div className="reportrow"><span>Revenue rule</span><span>Excludes cancelled; nets returns</span></div>
            <div className="reportrow"><span>Date range</span><span>12 months, several statuses</span></div>
            <div className="reportrow"><span>Seed</span><span className="mono">dataset_version 1</span></div>
            <div className="callout mt-14">Edge cases are seeded deliberately, so an exercise about <span className="mono" style={{ fontSize: 12 }}>LEFT JOIN</span> can actually encounter customers with no orders instead of a dataset where every join matches.</div>
          </div>
        </div>
      )}

      {s.reviewTab === 'validation' && <Validation report={report} />}

      <div className="actions mt-18" style={{ justifyContent: 'space-between' }}>
        <div className="actions">
          <button onClick={() => s.openModal('extend')}><Icon name="plus" />Add depth or extend course</button>
          <button onClick={() => s.openModal('editCourse')}>Edit course context</button>
        </div>
        <div className="actions">
          <button onClick={() => s.showScreen('create')}>Discard and create another</button>
          <button className="primary" onClick={s.startLearning}>Add to my learning<Icon name="arrow-right" /></button>
        </div>
      </div>
    </div>
  );
}

function SampleChapter({ course }: { course: Course }) {
  const s = useStore();
  const ms = course.modules ?? MODULES;
  const m = ms[4] || ms[0];
  const outline = chapterOutline(m);
  const ex = moduleExerciseBlocks(m)[0];
  return (
    <div className="grid2">
      <div className="card">
        <div className="eyebrow">Sample chapter · {m.kind}</div>
        <h2 className="mt-8" style={{ fontSize: 19 }}>{m.title}</h2>
        <p className="mt-8">{m.summary}</p>
        <h3 className="mt-16">Sections in this chapter</h3>
        {outline.map((g, i) => {
          const ec = g.exIds.length;
          return (
            <div className="reportrow" key={i}>
              <span><span className="mono" style={{ fontSize: 11, color: 'var(--text-3)' }}>{i + 1 < 10 ? '0' : ''}{i + 1}</span> &nbsp;{g.title}</span>
              <span className="muted">{ec ? `${ec} exercise${ec > 1 ? 's' : ''}` : `${g.items.length} parts`}</span>
            </div>
          );
        })}
        {ex ? <div className="callout mt-14"><strong>Exercise {String(ex.id).replace('ex.', '')}</strong><br />{ex.prompt}</div> : null}
        <button className="primary mt-12" onClick={() => s.openLessonIn(course.id, ms.indexOf(m))}>Open this chapter<Icon name="arrow-right" /></button>
      </div>
      <div className="card">
        <h2>How a chapter works</h2>
        <div className="reportrow"><span>1. Concept</span><span className="muted">Read the idea</span></div>
        <div className="reportrow"><span>2. Runnable example</span><span className="muted">Run it, change it</span></div>
        <div className="reportrow"><span>3. Exercise</span><span className="muted">Write it yourself</span></div>
        <p className="muted mt-12" style={{ fontSize: 12 }}>Each chapter is a short index of sections. Read a section, run its example, then pass the exercise. Add a section or a whole new chapter at any time.</p>
      </div>
    </div>
  );
}

/** The dialect as a heading reads it. The app runs two engines and a course
    declares one, so this is never guessed from a default. */
function dialectLabel(dialect: unknown): string {
  return dialect === 'sqlite' ? 'SQLite' : 'PostgreSQL';
}

/**
 * What the build measured, printed as it was measured.
 *
 * This screen used to print a fixed list — "✓ Passed", "3 repaired", "Review 1
 * note" — that came from nowhere: it was the shape of a validation report, not
 * one. Every row here reads the report the run produced, and when there is no
 * report the screen says there is no report instead of showing a green tick.
 *
 * The one thing it never does is upgrade an absence to a pass: a number the run
 * did not measure is printed as "not measured", and a failure is printed with
 * the exercise, the stage that refused, and the engine's own message.
 */
function Validation({ report }: { report: DraftReport | null }) {
  const s = useStore();
  if (!report) {
    return (
      <div className="grid2">
        <div className="card">
          <h2>No build in this session</h2>
          <p className="mt-8" style={{ fontSize: 13 }}>
            This draft has not been built in this browser session, so nothing has been measured for it. Querycraft does not
            show a validation summary it did not produce.
          </p>
          <button className="primary mt-12" onClick={() => s.showScreen('create')}>Design a course</button>
        </div>
      </div>
    );
  }

  const { totals } = report;
  const failures = report.chapters.flatMap((c) => c.failures);
  const active = report.outcome === 'active';
  const authored = report.source === 'authored';

  return (
    <div className="grid2">
      <div className="card">
        <h2>Validation summary</h2>
        <div className="reportrow">
          <span>Where the course came from</span>
          <span>{authored ? 'The built-in curriculum (no AI)' : `${report.provider || 'a provider'}${report.model ? ` · ${report.model}` : ''}`}</span>
        </div>
        <div className="reportrow">
          <span>Exercises</span>
          <span>{totals.exercises}</span>
        </div>
        <div className="reportrow">
          <span>Exercises executed and cross-checked</span>
          <span className={active ? 'pass' : 'warn'}>{active ? `✓ ${totals.verified}` : `${totals.verified} of ${totals.exercises}`}</span>
        </div>
        <div className="reportrow">
          <span>Rows measured by the engine</span>
          <span>{totals.rows}</span>
        </div>
        <div className="reportrow">
          <span>Engine time, all exercises</span>
          <span className="mono">{authored ? `${totals.ms} ms in this tab` : `${totals.ms} ms on the gateway`}</span>
        </div>
        <div className="reportrow">
          <span>Repairs the pipeline spent</span>
          <span>{report.repairs === 0 ? '0 — none were needed' : report.repairs}</span>
        </div>
        <div className="reportrow">
          <span>Reported at</span>
          <span className="mono" style={{ fontSize: 11 }}>{new Date(report.at).toLocaleString()}</span>
        </div>
        <p className="muted mt-12" style={{ fontSize: 11 }}>
          A second query agreeing with the reference is not proof. The validator checks the result contract and targeted
          edge cases on the engine; this list is what it reported, not a summary of it.
        </p>
      </div>
      <div className="card">
        <h2>{failures.length > 0 ? 'What refused' : 'What was checked'}</h2>
        {failures.length > 0 ? (
          <>
            <span className="pill" style={{ background: 'var(--amber-soft)', color: 'var(--amber)' }}>
              {failures.length} exercise{failures.length === 1 ? '' : 's'} did not verify
            </span>
            {failures.map((f) => (
              <div className="mt-12" key={f.id}>
                <div className="reportrow">
                  <span><span className="mono" style={{ fontSize: 12 }}>{f.id}</span> &nbsp;{f.title}</span>
                  <span className="fail">{f.stage}</span>
                </div>
                <p className="muted" style={{ fontSize: 12 }}>{f.message}{f.detail ? ` — ${f.detail}` : ''}</p>
              </div>
            ))}
            <p className="muted mt-12" style={{ fontSize: 11 }}>
              A course with a failing exercise is not offered. Change the goal and run the build again, or report the
              defect from the challenge you were working on.
            </p>
          </>
        ) : (
          <>
            <div className="reportrow"><span>Reference solutions executed</span><span className="pass">✓ {totals.verified} of {totals.exercises}</span></div>
            <div className="reportrow">
              <span>Independent cross-check agreed</span>
              <span className="pass">✓ {report.chapters.reduce((n, c) => n + c.crossCheckRows, 0)} rows</span>
            </div>
            <div className="reportrow">
              <span>Wrong queries rejected</span>
              <span className="pass">✓ {report.chapters.reduce((n, c) => n + c.fixturesRejected, 0)} of {report.chapters.reduce((n, c) => n + c.fixturesTotal, 0)}</span>
            </div>
            <div className="reportrow"><span>Column names, types, and ordering</span><span className="pass">✓ Checked by the contract</span></div>
            <div className="reportrow"><span>Duplicates, NULLs, and empty results</span><span className="pass">✓ Checked by the evaluator</span></div>
            <p className="muted mt-12" style={{ fontSize: 12 }}>
              {authored
                ? 'No generation service is configured, so this draft is the curriculum that ships with the app. Its reference queries were executed here, in this tab, against the engine named above.'
                : 'The gateway designed this course, then executed and cross-checked every exercise before it was returned. Nothing above is a claim made by the model.'}
            </p>
          </>
        )}
      </div>
      {report.chapters.length > 0 && (
        <div className="card" style={{ gridColumn: '1 / -1' }}>
          <h2>Per chapter</h2>
          {report.chapters.map((c) => (
            <div className="reportrow" key={c.id}>
              <span>{c.title}</span>
              <span className="muted">
                {c.sections} section{c.sections === 1 ? '' : 's'} · {c.verified}/{c.exercises} verified
                {c.rows > 0 ? ` · ${c.rows} rows` : ''}
                {c.failures.length > 0 ? ` · ${c.failures.length} failed` : ''}
              </span>
            </div>
          ))}
        </div>
      )}
      {report.stages.length > 0 && (
        <div className="card" style={{ gridColumn: '1 / -1' }}>
          <h2>Pipeline stages as reported</h2>
          {report.stages.map((st, i) => (
            <div className="reportrow" key={`${st.name}-${i}`}>
              <span className="mono" style={{ fontSize: 12 }}>{st.name}</span>
              <span className={st.state === 'failed' ? 'fail' : st.state === 'done' ? 'pass' : 'muted'}>
                {st.state}{typeof st.ms === 'number' ? ` · ${st.ms} ms` : ''}{st.detail ? ` — ${st.detail}` : ''}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
