import React, { useEffect, useState } from 'react';
import Icon from './ui/Icon';
import Field from './ui/Field';
import { useStore } from '../state/store';
import { MODULES } from '../data/curriculum';
import { activeModules } from '../lib/model';

function Backdrop({ id, children }: { id: string; children: React.ReactNode }) {
  const s = useStore();
  return (
    <div
      className={`modalback${s.modal === id ? ' open' : ''}`}
      onClick={(e) => { if (e.target === e.currentTarget) s.closeModal(); }}
      /* The click outside is a pointer convenience. Escape is the keyboard
         path, and it reaches the panel from any field inside it. */
      onKeyDown={(e) => { if (e.key === 'Escape') s.closeModal(); }}
    >
      <div className="modal">{children}</div>
    </div>
  );
}

function Head({ eyebrow, title, sub }: { eyebrow: string; title: string; sub: string }) {
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
  /* The learning goal is the one field the store reads back (`setReviewGoal`),
     so it keeps its state; the rest of the form only wrote to itself. */
  const [goal, setGoal] = useState(s.reviewGoal);

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
      <Field label="Course title">{(id) => <input id={id} value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
      <Field label="Learning goal">{(id) => <textarea id={id} value={goal} onChange={(e) => setGoal(e.target.value)} />}</Field>
      <div className="modalfoot">
        <button onClick={() => s.closeModal()}>Cancel</button>
        <button className="primary" onClick={() => { s.setReviewGoal(goal); s.saveCourseEdits({ title }); s.closeModal(); }}>Save changes</button>
      </div>
    </Backdrop>
  );
}

function EditLessonModal() {
  const s = useStore();
  const m = activeModules(s.course)[s.activeLesson] || MODULES[0];
  const [title, setTitle] = useState(m && m.title);
  const [body, setBody] = useState(m && m.summary);

  useEffect(() => {
    if (s.modal === ('editLesson') && m) {
      setTitle(m.title);
      setBody(m.summary);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.modal]);

  return (
    <Backdrop id="editLesson">
      <Head eyebrow="Chapter editor" title="Edit this chapter" sub="Edit this chapter independently from the rest of the course." />
      <Field label="Chapter title">{(id) => <input id={id} value={title || ''} onChange={(e) => setTitle(e.target.value)} />}</Field>
      <Field label="Chapter page / explanation">{(id) => <textarea id={id} value={body || ''} onChange={(e) => setBody(e.target.value)} />}</Field>
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
      <Field label="What should change? (optional)">
        {(id) => <textarea id={id} value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="e.g. Make the window-function section much harder. Include date gaps, ties, NULLs, and performance trade-offs." />}
      </Field>
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
  return (
    <Backdrop id="addLesson">
      <Head eyebrow="Chapter editor" title="Add a chapter" sub="Describe a topic or choose a useful next step." />
      <Field label="Chapter title">{(id) => <input id={id} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. Debugging row multiplication in multi-join queries" />}</Field>
      <Field label="What should the learner be able to do?">{(id) => <textarea id={id} value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="Describe the skill, challenge, or concept this chapter should cover." />}</Field>
      <div className="modalfoot">
        <button onClick={() => s.closeModal()}>Cancel</button>
        <button className="primary" onClick={() => { s.addLesson({ title, goal }); setTitle(''); setGoal(''); s.closeModal(); }}>Add to draft</button>
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
