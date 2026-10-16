import React, { useState } from 'react';
import Icon from '../ui/Icon';
import Field from '../ui/Field';
import { useStore } from '../../state/store';

const GOAL_HEADING_ID = 'create-goal-heading';
const OUTLINE_CHECKBOX_ID = 'create-approve-outline';

const EXAMPLES = [
  ['SQL joins for interviews', 'Teach me SQL joins for backend engineering interviews, from basics to multi-table edge cases.'],
  ['Window functions', 'Get me fluent with window functions: rankings, running totals, moving averages, and gaps-and-islands problems.'],
  ['Query plans & indexes', 'Show me how to read EXPLAIN ANALYZE and reason about indexes and query plans on realistic data.'],
];

export default function CreateScreen() {
  const s = useStore();
  const [goal, setGoal] = useState(s.goalInput);
  const [showOptional, setShowOptional] = useState(false);
  const [level, setLevel] = useState('');
  const [source, setSource] = useState('');
  /* The outline pause is opt-in. Off is today's flow: one run, outline into
     chapters. On stops the run after the outline so the index can be approved
     before the expensive chapter phase is paid for. */
  const [approveOutline, setApproveOutline] = useState(false);
  const goalRef = React.useRef<HTMLTextAreaElement>(null);

  function design() {
    const g = goal.trim();
    if (!g) {
      goalRef.current?.focus();
      s.toast('Tell me the skill you want to build.');
      return;
    }
    /* The learner's own words about their level and their source material travel
       with the request, so the generation is shaped by them rather than by a
       boolean that only says some context exists. */
    s.submitBuild(g, { level, source, requireApproval: approveOutline });
    s.showScreen('building');
  }

  return (
    <div className="home">
      <div className="eyebrow">Create · a course that runs real SQL</div>
      <h1 id={GOAL_HEADING_ID}>What do you want to get good at?</h1>
      <p className="lede">
        Describe the skill or the problem. Querycraft drafts the path, builds a realistic PostgreSQL practice environment, and runs every exercise before you see it — so the answers you get are the engine's answers, not a guess.
      </p>
      <div className="goalbox">
        <textarea
          ref={goalRef}
          aria-labelledby={GOAL_HEADING_ID}
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          placeholder="For example: I know basic SQL but I struggle with analytical interview questions. Teach me to combine CTEs, window functions, and conditional aggregation using realistic commerce data."
        />
        <div className="goalbottom">
          <div className="actions">
            <button className="small ghost" onClick={() => setShowOptional((v) => !v)}><Icon name="plus" />Add context or source material</button>
            <button className="small ghost" onClick={() => setGoal('I know basic SQL. Build me a challenging PostgreSQL course on analytics: multi-table joins, CTEs, window functions, cohort retention, and query optimization. Use a realistic e-commerce dataset and interview-style problems.')}>Use a detailed example</button>
            <label htmlFor={OUTLINE_CHECKBOX_ID} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 12, color: 'var(--text-2)' }}>
              <input id={OUTLINE_CHECKBOX_ID} type="checkbox" checked={approveOutline} onChange={(e) => setApproveOutline(e.target.checked)} />
              Show me the outline before the chapters are written
            </label>
          </div>
          <button className="primary" onClick={design}>Design my course<Icon name="arrow-right" /></button>
        </div>
        {showOptional && (
          <div className="optionalctx">
            <Field label="What should the AI know about your level?">
              {(id) => <input id={id} value={level} onChange={(e) => setLevel(e.target.value)} placeholder="e.g. Comfortable with SELECT, WHERE, GROUP BY; weak at window functions" />}
            </Field>
            <Field label="Paste notes, a syllabus, or a reference (optional)">
              {(id) => <textarea id={id} value={source} onChange={(e) => setSource(e.target.value)} placeholder="Paste notes or describe a resource to incorporate. This context stays attached to the course." />}
            </Field>
          </div>
        )}
      </div>
      <div className="examples">
        {EXAMPLES.map(([label, text]) => (
          <button className="example" key={label} onClick={() => setGoal(text)}>{label}<Icon name="arrow-right" /></button>
        ))}
      </div>
      <div className="homefoot">
        <strong style={{ color: 'var(--text)' }}>Every exercise is executed before it ships.</strong> Reference queries run against the same engine you use, result contracts are checked for order, duplicates and NULLs, and each chapter mixes a short idea with a query you write yourself. Nothing is a static example.
      </div>
    </div>
  );
}
