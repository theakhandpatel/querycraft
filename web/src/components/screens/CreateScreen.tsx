import React, { useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';

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
  const goalRef = React.useRef<HTMLTextAreaElement>(null);

  function design() {
    const g = goal.trim();
    if (!g) {
      goalRef.current && goalRef.current.focus();
      s.toast('Tell me the skill you want to build.');
      return;
    }
    s.submitBuild(g, !!(level.trim() || source.trim()));
    s.showScreen('building');
  }

  return (
    <div className="home">
      <div className="eyebrow">Create · a course that runs real SQL</div>
      <h1>What do you want to get good at?</h1>
      <p className="lede">
        Describe the skill or the problem. Querycraft drafts the path, builds a realistic PostgreSQL practice environment, and runs every exercise before you see it — so the answers you get are the engine's answers, not a guess.
      </p>
      <div className="goalbox">
        <textarea
          ref={goalRef}
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          placeholder="For example: I know basic SQL but I struggle with analytical interview questions. Teach me to combine CTEs, window functions, and conditional aggregation using realistic commerce data."
        />
        <div className="goalbottom">
          <div className="actions">
            <button className="small ghost" onClick={() => setShowOptional((v) => !v)}><Icon name="plus" />Add context or source material</button>
            <button className="small ghost" onClick={() => setGoal('I know basic SQL. Build me a challenging PostgreSQL course on analytics: multi-table joins, CTEs, window functions, cohort retention, and query optimization. Use a realistic e-commerce dataset and interview-style problems.')}>Use a detailed example</button>
          </div>
          <button className="primary" onClick={design}>Design my course<Icon name="arrow-right" /></button>
        </div>
        {showOptional && (
          <div className="optionalctx">
            <div className="field">
              <label>What should the AI know about your level?</label>
              <input value={level} onChange={(e) => setLevel(e.target.value)} placeholder="e.g. Comfortable with SELECT, WHERE, GROUP BY; weak at window functions" />
            </div>
            <div className="field">
              <label>Paste notes, a syllabus, or a reference (optional)</label>
              <textarea value={source} onChange={(e) => setSource(e.target.value)} placeholder="Paste notes or describe a resource to incorporate. This context stays attached to the course." />
            </div>
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
