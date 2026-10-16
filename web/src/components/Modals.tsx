import React, { useEffect, useState } from 'react';
import Icon from './ui/Icon';
import { useStore } from '../state/store';
import { MODULES } from '../data/curriculum';
import { activeModules } from '../lib/model';

function Backdrop({ id, children }: { id: string; children: React.ReactNode }) {
  const s = useStore();
  return (
    <div
      className={`modalback${s.modal === id ? ' open' : ''}`}
      onClick={(e) => { if (e.target === e.currentTarget) s.closeModal(); }}
    >
      <div className="modal">{children}</div>
    </div>
  );
}

function Head({ eyebrow, title, sub }: { eyebrow: string; title: string; sub: string; id?: string }) {
  const s = useStore();
  return (
    <div className="modalhead">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h2>{title}</h2>
        <p className="muted">{sub}</p>
      </div>
      <button className="small" aria-label="Close" onClick={() => s.closeModal()}><Icon name="x" /></button>
    </div>
  );
}

function EditCourseModal() {
  const s = useStore();
  const [title, setTitle] = useState(s.course.title);
  const [goal, setGoal] = useState(s.reviewGoal);
  const [level, setLevel] = useState('Comfortable with SELECT, filtering, joins, and basic GROUP BY');
  const [context, setContext] = useState('Commerce data. Revenue excludes cancelled orders and accounts for returned quantities. Use PostgreSQL syntax. Keep examples realistic and progressively harder.');
  const [dialect, setDialect] = useState('PostgreSQL');

  useEffect(() => {
    if (s.modal === ('editCourse')) {
      setTitle(s.course.title);
      setGoal(s.reviewGoal);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.modal]);

  return (
    <Backdrop id="editCourse">
      <Head eyebrow="Course settings" title="Edit course context" sub="Change the course without losing the existing draft." />
      <div className="field"><label>Course title</label><input value={title} onChange={(e) => setTitle(e.target.value)} /></div>
      <div className="field"><label>Learning goal</label><textarea value={goal} onChange={(e) => setGoal(e.target.value)} /></div>
      <div className="field"><label>Assumed starting point</label><input value={level} onChange={(e) => setLevel(e.target.value)} /></div>
      <div className="field"><label>Context / business rules</label><textarea value={context} onChange={(e) => setContext(e.target.value)} /></div>
      <div className="field"><label>Dialect</label>
        <select value={dialect} onChange={(e) => setDialect(e.target.value)}>
          <option>PostgreSQL</option><option>SQLite</option><option>MySQL</option><option>SQL Server</option>
        </select>
      </div>
      <div className="modalfoot">
        <button onClick={() => s.closeModal()}>Cancel</button>
        <button className="primary" onClick={() => { s.setReviewGoal(goal); s.saveCourseEdits({ title, goal, context, dialect }); s.closeModal(); }}>Save changes</button>
      </div>
    </Backdrop>
  );
}

function EditLessonModal() {
  const s = useStore();
  const m = activeModules(s.course)[s.activeLesson] || MODULES[0];
  const [title, setTitle] = useState(m && m.title);
  const [body, setBody] = useState(m && m.summary);
  const [prompt, setPrompt] = useState('');

  useEffect(() => {
    if (s.modal === ('editLesson') && m) {
      setTitle(m.title);
      setBody(m.summary);
      setPrompt((m.blocks || []).filter((b) => b.type === 'exercise').map((b) => b.prompt).join('\n'));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.modal]);

  return (
    <Backdrop id="editLesson">
      <Head eyebrow="Chapter editor" title="Edit this chapter" sub="Edit this chapter independently from the rest of the course." />
      <div className="field"><label>Chapter title</label><input value={title || ''} onChange={(e) => setTitle(e.target.value)} /></div>
      <div className="field"><label>Chapter page / explanation</label><textarea value={body || ''} onChange={(e) => setBody(e.target.value)} /></div>
      <div className="field"><label>Exercise prompt</label><textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} /></div>
      <div className="modalfoot">
        <button onClick={() => s.closeModal()}>Cancel</button>
        <button className="primary" onClick={() => { s.saveLessonEdits({ title, summary: body }); s.closeModal(); }}>Save chapter</button>
      </div>
    </Backdrop>
  );
}

const EXT_CHOICES = [
  ['deeper', 'Go deeper on a topic', 'Harder exercises and tricky edge cases'],
  ['advanced', 'Add advanced concepts', 'New chapters that build on current prerequisites'],
  ['project', 'Add a capstone project', 'One open-ended task combining several patterns'],
  ['branch', 'Branch into a related topic', 'Explore query plans, indexes, or data modeling'],
  ['practice', 'Add a practice set', 'More interview questions without new theory'],
  ['context', 'Adapt to my work', 'Use a domain, schema, or source material I provide'],
];

function ExtendModal() {
  const s = useStore();
  const [kind, setKind] = useState('deeper');
  const [prompt, setPrompt] = useState('');
  return (
    <Backdrop id="extend">
      <Head eyebrow="Course evolution" title="How do you want to extend this course?" sub="Add depth where you need it. Existing chapters stay intact unless you revise them." />
      <div className="choicegrid">
        {EXT_CHOICES.map(([k, title, sub]) => (
          <button key={k} className={`choice${kind === k ? ' selected' : ''}`} onClick={() => setKind(k)}>
            <strong>{title}</strong><span>{sub}</span>
          </button>
        ))}
      </div>
      <div className="field">
        <label>What should change? (optional)</label>
        <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="e.g. Make the window-function section much harder. Include date gaps, ties, NULLs, and performance trade-offs." />
      </div>
      <div className="modalfoot">
        <button onClick={() => s.closeModal()}>Cancel</button>
        <button className="primary" onClick={() => { s.applyExtension(kind, prompt); s.closeModal(); }}>Generate extension<Icon name="sparkles" /></button>
      </div>
    </Backdrop>
  );
}

function AddLessonModal() {
  const s = useStore();
  const [title, setTitle] = useState('');
  const [goal, setGoal] = useState('');
  const [position, setPosition] = useState('end');
  return (
    <Backdrop id="addLesson">
      <Head eyebrow="Chapter editor" title="Add a chapter" sub="Describe a topic or choose a useful next step." />
      <div className="field"><label>Chapter title</label><input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. Debugging row multiplication in multi-join queries" /></div>
      <div className="field"><label>What should the learner be able to do?</label><textarea value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="Describe the skill, challenge, or concept this chapter should cover." /></div>
      <div className="field"><label>Placement</label>
        <select value={position} onChange={(e) => setPosition(e.target.value)}>
          <option value="end">At the end of the course</option>
          <option value="before">Before the final challenge</option>
          <option value="after">After the current chapter</option>
        </select>
      </div>
      <div className="modalfoot">
        <button onClick={() => s.closeModal()}>Cancel</button>
        <button className="primary" onClick={() => { s.addLesson({ title, goal, position }); setTitle(''); setGoal(''); s.closeModal(); }}>Add to draft</button>
      </div>
    </Backdrop>
  );
}

export default function Modals() {
  return (
    <>
      <EditCourseModal />
      <EditLessonModal />
      <ExtendModal />
      <AddLessonModal />
    </>
  );
}
