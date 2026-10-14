import React from 'react';
import Icon from '../ui/Icon';
import Schema from '../Schema';
import { useStore } from '../../state/store';
import { COURSES, MODULES } from '../../data/curriculum';
import { chapterOutline, courseExerciseTotal, moduleExerciseBlocks } from '../../lib/model';
import type { Course, ModalName } from '../../types';

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
  const [tieRule, setTieRule] = React.useState('include');

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">Draft course · ready to explore</div>
          <h1>{course.title}</h1>
          <p>{s.courseSubtitle}</p>
          <div className="actions mt-12">
            <span className="pill"><span className="dot"></span> Validation passed with notes</span>
            <span className="pill subtle">PostgreSQL · {modules.length} chapters · {exerciseTotal} exercises</span>
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
        <div className="stat"><strong>4</strong><span>Dataset tables</span></div>
        <div className="stat"><strong>3</strong><span>Validation dimensions</span></div>
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
            <div className="reportrow"><span>Starting assumption</span><span>Basic SELECT / GROUP BY</span></div>
            <div className="reportrow"><span>Dialect</span><span>PostgreSQL</span></div>
            <div className="reportrow"><span>Learning style</span><span>Short concept + exercise</span></div>
            <div className="reportrow"><span>Grading</span><span>Result contract, not SQL text</span></div>
            <div className="reportrow"><span>Difficulty curve</span><span>Steady → challenging</span></div>
            <div className="reportrow"><span>Source context</span><span>{s.contextState}</span></div>
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

      {s.reviewTab === 'validation' && (
        <div className="grid2">
          <div className="card">
            <h2>Validation summary</h2>
            <div className="reportrow"><span>Reference queries executed</span><span className="pass">✓ Passed</span></div>
            <div className="reportrow"><span>Column names, types, and ordering</span><span className="pass">✓ Checked</span></div>
            <div className="reportrow"><span>Duplicates, NULLs, and empty results</span><span className="pass">✓ Checked</span></div>
            <div className="reportrow"><span>Independent query cross-check</span><span className="pass">✓ Passed with notes</span></div>
            <div className="reportrow"><span>Boundary and tie cases</span><span className="warn">Review 1 note</span></div>
            <div className="reportrow"><span>Exercises repaired and re-run</span><span>3</span></div>
            <p className="muted mt-12" style={{ fontSize: 11 }}>A second query agreeing with the reference is not proof. The validator checks the result contract and targeted edge cases on the engine.</p>
          </div>
          <div className="card">
            <h2>Open validation note</h2>
            <span className="pill" style={{ background: 'var(--amber-soft)', color: 'var(--amber)' }}>Needs human decision</span>
            <h3 className="mt-12">Top-N ranking when categories tie</h3>
            <p className="mt-8" style={{ fontSize: 12 }}>The exercise says “top two categories” but does not say whether ties return more than two rows or are broken deterministically.</p>
            <div className="field">
              <label>Choose the intended rule</label>
              <select value={tieRule} onChange={(e) => setTieRule(e.target.value)}>
                <option value="include">Include all ties at the cutoff (RANK)</option>
                <option value="exact">Return exactly two rows (ROW_NUMBER with stable tie-break)</option>
              </select>
            </div>
            <button className="primary mt-12" onClick={() => s.toast('Rule applied and exercises re-checked.')}>Apply rule and re-check</button>
          </div>
        </div>
      )}

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
