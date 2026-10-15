import React, { useEffect, useRef, useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import { courseShort } from '../../lib/model';
import { artifactProvenanceLabel, executionEngineLabel, generationProvenance } from '../../lib/provenance';
import type { AiArtifact, AiOutcome } from '../../api/aiContract';
import type { Block } from '../../types';

const CL_STEPS = [
  ['Validate the request', 'Topics, difficulty, scenario, constraints'],
  ['Run reference vs contract', 'The reference must satisfy its own result contract'],
  ['Adversarial fixtures', 'Deliberately wrong queries must be rejected by the evaluator'],
  ['Register the challenge', 'Save the artifact with the versions it was checked against'],
];

/** The authored challenge whose reference query this lab validates against. */
function referenceBlock(): Block | null {
  const exercises: Block[] = [];
  for (const c of COURSES) {
    for (const m of c.modules ?? []) {
      for (const b of m.blocks ?? []) {
        if (b.type === 'exercise' && typeof b.solution === 'string' && b.solution.trim()) exercises.push(b);
      }
    }
  }
  return exercises.find((b) => b.id === 'ex.5.1') ?? exercises[0] ?? null;
}

/* Fixtures that must fail the same evaluator the learner is graded by. Each one
   is wrong in a different way, so a green run is real evidence that a wrong
   answer cannot slip through. */
function adversarialFixtures(reference: string): { name: string; sql: string }[] {
  const base = reference.trim().replace(/;\s*$/, '');
  return [
    { name: 'drops a required column', sql: 'SELECT name, category, price\nFROM products\nORDER BY category, price DESC;' },
    { name: 'returns one row instead of the full set', sql: `${base}\nLIMIT 1;` },
    { name: 'returns a constant instead of the query', sql: 'SELECT 1 AS price_rank;' },
  ];
}

type Evidence = {
  referencePassed: boolean;
  fixturesRejected: number;
  fixturesTotal: number;
  referenceRows: number;
  ms: number;
  version: number;
};

/** What "Try it" got back from the same grader the learner is graded by. */
type TryOutcome = {
  passed: boolean;
  rows: number;
  ms: number | string;
  error?: string;
};

/* The form speaks in learner words; the contract speaks in levels. */
const DIFFICULTY: Record<string, 'beginner' | 'intermediate' | 'advanced'> = {
  'Warm-up': 'beginner',
  Challenging: 'intermediate',
  Brutal: 'advanced',
};

function commaList(value: string): string[] {
  return value.split(',').map((v) => v.trim()).filter(Boolean);
}

function sentenceList(value: string): string[] {
  return value.split(/[\n.;]+/).map((v) => v.trim()).filter(Boolean);
}

export default function ChallengeLabScreen() {
  const s = useStore();
  const [topics, setTopics] = useState('window functions, ranking within groups, tie handling');
  const [difficulty, setDifficulty] = useState('Challenging');
  const [scenario, setScenario] = useState('A subscription business wants to find the top plan per region each month');
  const [constraints, setConstraints] = useState('Answer with one statement. Return no more than 50 rows. Must include a deterministic tie-break.');
  const [states, setStates] = useState(CL_STEPS.map(() => 'idle'));
  const [log, setLog] = useState<{ t: string; html: string }[]>([]);
  const [logOpen, setLogOpen] = useState(true);
  const [result, setResult] = useState<Evidence | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const running = useRef(false);
  /* The real gateway generation, separate from the authored evidence run. Once
     it has produced a response the pipeline card renders the service's own
     stages instead of the local CL_STEPS list. */
  const [aiOutcome, setAiOutcome] = useState<AiOutcome | null>(null);
  const [generating, setGenerating] = useState(false);
  const [trying, setTrying] = useState(false);
  const [tried, setTried] = useState<TryOutcome | null>(null);

  /* A fragment can name a registered challenge (`#/lab/<id>`). When it does,
     the lab reads that challenge back through the API and shows it in the same
     card a fresh generation uses, instead of opening on an empty request form.
     An id the service does not know — or one this adapter cannot reach — changes
     nothing: the lab keeps its own default state. */
  const linkedChallenge = s.challengeId;
  useEffect(() => {
    if (!linkedChallenge) return;
    let alive = true;
    s.api
      .getChallenge(linkedChallenge)
      .then((record) => {
        if (!alive || !record) return;
        setAiOutcome(record.outcome.artifact ? record.outcome : { ...record.outcome, artifact: record.artifact });
      })
      .catch(() => {
        /* an unreadable challenge must not break the lab */
      });
    return () => {
      alive = false;
    };
  }, [linkedChallenge, s.api]);

  function stop(): void {
    abortRef.current?.abort();
    abortRef.current = null;
    running.current = false;
  }

  function reset(): void {
    stop();
    setStates(CL_STEPS.map(() => 'idle'));
    setLog([]);
    setLogOpen(true);
    setResult(null);
    setAiOutcome(null);
    setTried(null);
  }

  /* Leaving the lab mid-run used to leave the stream ticking and then flip the
     result card on from off-screen. Abort it with the component. */
  useEffect(() => stop, []);

  function run(): void {
    if (running.current) return;
    running.current = true;
    const controller = new AbortController();
    abortRef.current = controller;
    const { signal } = controller;

    setResult(null);
    setStates(CL_STEPS.map(() => 'idle'));
    setLog([]);
    setLogOpen(true);
    /* Back to the authored pipeline view: this run is the offline evidence
       path, so the card must show its own stages, not a previous gateway run. */
    setAiOutcome(null);
    setTried(null);

    const t0 = performance.now();
    const stamp = (): string => `t+${((performance.now() - t0) / 1000).toFixed(1)}s`;
    const emit = (html: string): void => setLog((l) => [...l, { t: stamp(), html }]);
    const mark = (i: number, state: 'idle' | 'running' | 'done'): void =>
      setStates((prev) => {
        if (prev[i] === state) return prev;
        const next = prev.slice();
        next[i] = state;
        return next;
      });
    /* The real work finishes in well under a second, which is too fast to read.
       The pause is legibility only — it is never reported as engine time. */
    const pause = (ms: number): Promise<void> =>
      new Promise((resolve) => {
        if (signal.aborted) return resolve();
        const onAbort = (): void => {
          clearTimeout(id);
          resolve();
        };
        const id = setTimeout(() => {
          signal.removeEventListener('abort', onAbort);
          resolve();
        }, ms);
        signal.addEventListener('abort', onAbort, { once: true });
      });

    void (async () => {
      try {
        /* 1. Validate the request — this genuinely can fail. */
        mark(0, 'running');
        emit('Validating request → topics, difficulty, scenario, constraints');
        const missing = [
          !topics.trim() ? 'topics' : '',
          !scenario.trim() ? 'scenario' : '',
          !constraints.trim() ? 'constraints' : '',
        ].filter(Boolean);
        if (missing.length) {
          emit(`<span class="fail">✕</span> request rejected — ${missing.join(', ')} required`);
          mark(0, 'idle');
          running.current = false;
          abortRef.current = null;
          return;
        }
        const block = referenceBlock();
        if (!block) {
          emit('<span class="fail">✕</span> no authored reference is available in this build');
          mark(0, 'idle');
          running.current = false;
          abortRef.current = null;
          return;
        }
        mark(0, 'done');
        if (signal.aborted) return;
        await pause(240);
        if (signal.aborted) return;

        /* 2. The reference must pass its own contract on the real engine. */
        mark(1, 'running');
        emit(`Grading <span class="hi">${block.id}</span> reference on ${executionEngineLabel(s.api)} …`);
        const reference = String(block.solution);
        const refEval = await s.api.checkAnswer(reference, { item: block });
        if (signal.aborted) return;
        const referenceRows = refEval.qr.row_count;
        if (refEval.passed) {
          emit(`<span class="ok">✓</span> reference satisfies its contract · ${referenceRows} rows · ${refEval.duration_ms} ms`);
        } else {
          const bad = refEval.checks.filter((c) => !c.ok).map((c) => c.label);
          emit(`<span class="fail">✕</span> reference failed its own contract${bad.length ? ` — ${bad.join('; ')}` : ''}`);
        }
        mark(1, 'done');
        if (signal.aborted) return;
        await pause(240);
        if (signal.aborted) return;

        /* 3. Wrong queries must not pass. Measure it instead of asserting it. */
        mark(2, 'running');
        const fixtures = adversarialFixtures(reference);
        emit(`Running ${fixtures.length} adversarial fixtures against the same evaluator …`);
        let rejected = 0;
        for (const fixture of fixtures) {
          if (signal.aborted) return;
          const verdict = await s.api.checkAnswer(fixture.sql, { item: block });
          if (signal.aborted) return;
          if (verdict.passed) {
            emit(`<span class="fail">✕</span> fixture “${fixture.name}” passed — the contract is too loose`);
          } else {
            rejected += 1;
            emit(`<span class="ok">✓</span> rejected: ${fixture.name}`);
          }
        }
        emit(
          rejected === fixtures.length
            ? `<span class="ok">✓</span> ${rejected}/${fixtures.length} wrong queries rejected`
            : `<span class="fail">✕</span> only ${rejected}/${fixtures.length} wrong queries rejected`
        );
        if (s.api.mode === 'mock' && rejected < fixtures.length) {
          emit('<span class="hi">note</span> the canned dev engine replays the reference result for any SQL, so it cannot tell these fixtures apart — switch to the gateway or the in-browser engine for a real verdict');
        }
        mark(2, 'done');
        if (signal.aborted) return;
        await pause(240);
        if (signal.aborted) return;

        /* 4. Register the artifact through the API. */
        mark(3, 'running');
        const draft = await s.api.draftChallenge({
          title: `Challenge — ${topics.split(',')[0].trim() || 'practice'}`,
          exercises: fixtures.length + 1,
          steps: CL_STEPS.map(() => ''),
        });
        if (signal.aborted) return;
        if (draft.ok) {
          emit(`<span class="ok">✓</span> challenge registered · version 1`);
        } else {
          emit(`<span class="fail">✕</span> the API did not accept the challenge`);
        }
        mark(3, 'done');
        emit('<span class="caret"></span>');

        setResult({
          referencePassed: refEval.passed,
          fixturesRejected: rejected,
          fixturesTotal: fixtures.length,
          referenceRows,
          ms: Math.round(performance.now() - t0),
          version: draft.ok ? 1 : 0,
        });
        running.current = false;
        abortRef.current = null;
      } catch {
        if (signal.aborted) return;
        emit('<span class="fail">✕</span> the run could not finish — see the engine badge above');
        running.current = false;
        abortRef.current = null;
      }
    })();
  }

  /* The real generation. The gateway owns the pipeline, so this sends the
     request and renders whatever came back — an active artifact, a rejection,
     a failure or an unavailable lab — without adding a local stage. */
  async function generateOnGateway(): Promise<void> {
    const availability = s.aiAvailability;
    if (!availability || !availability.available) return;
    setGenerating(true);
    setResult(null);
    setTried(null);
    try {
      const outcome = await s.api.generateChallenge({
        kind: 'challenge',
        title: topics.trim() ? `Challenge — ${topics.split(',')[0].trim()}` : undefined,
        topics: commaList(topics),
        scenario: scenario.trim() || undefined,
        constraints: sentenceList(constraints),
        difficulty: DIFFICULTY[difficulty] ?? 'intermediate',
      });
      setAiOutcome(outcome);
    } catch (e) {
      /* generateChallenge resolves for every adapter; a rejection here means the
         layer itself failed. Report that as a failed outcome, not an artifact. */
      setAiOutcome({
        status: 'failed',
        kind: 'challenge',
        artifact: null,
        stages: [],
        attempts: [],
        error: {
          code: 'ENVIRONMENT_UNAVAILABLE',
          message: e instanceof Error ? e.message : 'The generation call could not be completed.',
        },
      });
    } finally {
      setGenerating(false);
    }
  }

  /* Send the generated challenge's own reference through the grader the learner
     uses, so "it is graded by the same evaluator" is something they can see. */
  async function tryIt(artifact: AiArtifact): Promise<void> {
    setTrying(true);
    const block: Block = {
      type: 'exercise',
      id: artifact.id,
      prompt: artifact.prompt,
      solution: artifact.reference_solution,
      contract: artifact.result_contract,
      hints: artifact.hints,
    };
    try {
      const verdict = await s.api.checkAnswer(artifact.reference_solution, { item: block });
      setTried({ passed: verdict.passed, rows: verdict.qr.row_count, ms: verdict.duration_ms });
    } catch (e) {
      setTried({
        passed: false,
        rows: 0,
        ms: '—',
        error: e instanceof Error ? e.message : 'The grader could not be reached.',
      });
    } finally {
      setTrying(false);
    }
  }

  const verified = result ? result.referencePassed && result.fixturesRejected === result.fixturesTotal : false;
  const artifact = aiOutcome?.artifact ?? null;
  const ai = s.aiAvailability;
  const aiReady = ai !== null && ai.available;

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">AI Challenge Lab · {courseShort(s.course)}</div>
          <h1>Generate practice that passes the same bar</h1>
          <p>Describe what you want to drill. The challenge is checked the only way that means anything: its reference query and a set of deliberately wrong queries are graded by the same evaluator you are.</p>
        </div>
        <span className="pill" style={{ background: 'var(--accent-soft)', color: 'var(--accent)' }}>AI is optional · core works without it</span>
      </div>

      <div className="grid2">
        <div className="card">
          <h2>Request</h2>
          <div className="field"><label>Topics</label><input value={topics} onChange={(e) => setTopics(e.target.value)} /></div>
          <div className="field">
            <label>Difficulty</label>
            <select value={difficulty} onChange={(e) => setDifficulty(e.target.value)}>
              <option>Warm-up</option>
              <option>Challenging</option>
              <option>Brutal</option>
            </select>
          </div>
          <div className="field"><label>Scenario</label><input value={scenario} onChange={(e) => setScenario(e.target.value)} /></div>
          <div className="field"><label>Constraints</label><textarea rows={2} value={constraints} onChange={(e) => setConstraints(e.target.value)} /></div>
          <div className="actions mt-14">
            <button className="primary" onClick={run}><Icon name="sparkles" />Generate challenge</button>
            <button onClick={() => void generateOnGateway()} disabled={!aiReady || generating}>
              <Icon name="sparkles" />{generating ? 'Generating…' : 'Generate with AI'}
            </button>
            <button onClick={reset}>Reset</button>
          </div>
          <div className="mt-8" style={{ fontSize: 12, color: 'var(--text-3)' }}>
            {ai === null
              ? 'Checking AI availability …'
              : ai.available
                ? `Gateway generation is available on ${ai.provider}${ai.model ? ` · ${ai.model}` : ''}.`
                : ai.reason}
          </div>
        </div>

        <div className="card">
          <h2>Validation pipeline</h2>
          <div className="pipeline">
            {aiOutcome
              ? aiOutcome.stages.length > 0
                ? aiOutcome.stages.map((stage, i) => (
                    <div
                      className={`pipe ${stage.state === 'done' ? 'done' : stage.state === 'failed' ? 'failed' : 'idle'}`}
                      key={`${stage.name}-${i}`}
                    >
                      <div className="check">{stage.state === 'done' ? '✓' : stage.state === 'failed' ? '✕' : '–'}</div>
                      <div><strong>{stage.name}</strong><small>{stage.detail ?? ''}</small></div>
                      <span className="state">
                        {stage.state}
                        {typeof stage.ms === 'number' ? ` · ${stage.ms} ms` : ''}
                      </span>
                    </div>
                  ))
                : (
                    <div className="pipe idle">
                      <div className="check">–</div>
                      <div>
                        <strong>Nothing ran</strong>
                        <small>{aiOutcome.error?.message ?? 'The service returned no pipeline stages.'}</small>
                      </div>
                      <span className="state">{aiOutcome.status}</span>
                    </div>
                  )
              : CL_STEPS.map(([title, small], i) => (
                  <div className={`pipe ${states[i]}`} key={i}>
                    <div className="check">{states[i] === 'done' ? '✓' : i + 1}</div>
                    <div><strong>{title}</strong><small>{small}</small></div>
                    <span className="state">{states[i] === 'done' ? 'Done' : states[i] === 'running' ? 'Working' : 'Idle'}</span>
                  </div>
                ))}
          </div>
          {aiOutcome && (
            <div className="mt-12">
              {aiOutcome.attempts.map((attempt) => (
                <div className="reportrow" key={`${attempt.number}-${attempt.stage}`}>
                  <span>Attempt {attempt.number} · {attempt.stage}</span>
                  <span style={{ maxWidth: '62%' }}>{attempt.detail}</span>
                </div>
              ))}
              {aiOutcome.evidence && (
                <>
                  {typeof aiOutcome.evidence.reference_rows === 'number' && (
                    <div className="reportrow"><span>Reference rows</span><span>{aiOutcome.evidence.reference_rows}</span></div>
                  )}
                  {typeof aiOutcome.evidence.cross_check_rows === 'number' && (
                    <div className="reportrow"><span>Cross-check rows</span><span>{aiOutcome.evidence.cross_check_rows}</span></div>
                  )}
                  {typeof aiOutcome.evidence.fixtures_total === 'number' && (
                    <div className="reportrow">
                      <span>Adversarial fixtures</span>
                      <span>{aiOutcome.evidence.fixtures_rejected ?? 0}/{aiOutcome.evidence.fixtures_total} rejected</span>
                    </div>
                  )}
                  {typeof aiOutcome.evidence.repairs === 'number' && (
                    <div className="reportrow"><span>Repairs</span><span>{aiOutcome.evidence.repairs}</span></div>
                  )}
                  {aiOutcome.evidence.scratch_database && (
                    <div className="reportrow">
                      <span>Scratch database</span>
                      <span className="mono" style={{ fontSize: 11 }}>{aiOutcome.evidence.scratch_database}</span>
                    </div>
                  )}
                </>
              )}
              <div className="reportrow">
                <span>Generation status</span>
                <span className={aiOutcome.status === 'active' ? 'pass' : aiOutcome.status === 'unavailable' ? undefined : 'fail'}>
                  {aiOutcome.status}
                </span>
              </div>
              {aiOutcome.error && (
                <div className="reportrow">
                  <span>Service error</span>
                  <span className="fail">
                    {aiOutcome.error.code}{aiOutcome.error.stage ? ` · ${aiOutcome.error.stage}` : ''}
                  </span>
                </div>
              )}
            </div>
          )}
          {aiOutcome?.error && (
            <div className="callout mt-12">
              <strong>{aiOutcome.error.code}.</strong> {aiOutcome.error.message}
              {aiOutcome.error.detail ? <span className="mono" style={{ fontSize: 11 }}> — {aiOutcome.error.detail}</span> : null}
            </div>
          )}
          <button className="small ghost mt-12" onClick={() => setLogOpen((v) => !v)}><Icon name="chevron-down" />Show stream</button>
          <div className={`stream${logOpen ? ' open' : ''}`}>
            {log.map((l, i) => (
              <div className="ln" key={i}><span className="t">{l.t}</span><span dangerouslySetInnerHTML={{ __html: l.html }} /></div>
            ))}
          </div>
        </div>
      </div>

      {artifact && (
        <div className="mt-18">
          <div className="card">
            <div className="sectiontitle">
              <h2>{artifact.title}</h2>
              <span className="pill" style={{ background: 'var(--green-soft)', color: 'var(--green)' }}>
                {artifact.difficulty} · v{artifact.version}
              </span>
            </div>
            <div className="grid2 mt-12">
              <div>
                <h3>Prompt</h3>
                <p className="mt-8" style={{ fontSize: 13.5 }}>{artifact.prompt}</p>
                {artifact.objectives.length > 0 && (
                  <>
                    <h3 className="mt-12">Objectives</h3>
                    <ul className="mt-8" style={{ fontSize: 13 }}>
                      {artifact.objectives.map((objective) => <li key={objective}>{objective}</li>)}
                    </ul>
                  </>
                )}
                {artifact.hints.length > 0 && (
                  <>
                    <h3 className="mt-12">Hints</h3>
                    <ul className="mt-8" style={{ fontSize: 13 }}>
                      {artifact.hints.map((hint) => <li key={hint}>{hint}</li>)}
                    </ul>
                  </>
                )}
              </div>
              <div>
                <h3>Reference</h3>
                <pre className="syntaxblock">{artifact.reference_solution}</pre>
                <div className="reportrow">
                  <span>Result contract</span>
                  <span className="mono" style={{ fontSize: 11 }}>{artifact.result_contract.columns.join(', ') || '—'}</span>
                </div>
                <div className="reportrow">
                  <span>Provider</span>
                  <span className="mono" style={{ fontSize: 11 }}>{artifactProvenanceLabel(artifact)}</span>
                </div>
                <div className="reportrow">
                  <span>Dataset version</span>
                  <span className="mono" style={{ fontSize: 11 }}>{artifact.provenance.dataset_version}</span>
                </div>
                <div className="actions mt-14">
                  <button className="primary" onClick={() => void tryIt(artifact)} disabled={trying}>
                    <Icon name="play" />{trying ? 'Grading…' : 'Try it'}
                  </button>
                  <button onClick={() => s.toast('Saved to your challenge set.')}>Save challenge</button>
                </div>
                {tried && (
                  <div className="mt-12">
                    <div className="reportrow">
                      <span>Reference vs contract</span>
                      <span className={tried.error ? 'fail' : tried.passed ? 'pass' : 'fail'}>
                        {tried.error ? 'could not grade' : tried.passed ? '✓ pass' : '✕ failed'}
                      </span>
                    </div>
                    <div className="reportrow"><span>Rows</span><span>{tried.rows}</span></div>
                    <div className="reportrow"><span>Engine time</span><span>{tried.ms} ms</span></div>
                    <div className="reportrow">
                      <span>Execution engine</span>
                      <span className="mono" style={{ fontSize: 11 }}>{executionEngineLabel(s.api)}</span>
                    </div>
                    {tried.error && (
                      <div className="callout mt-12"><strong>Not graded.</strong> {tried.error}</div>
                    )}
                  </div>
                )}
              </div>
            </div>
            <div className="callout mt-14">
              <strong>How to read this.</strong> This artifact came from the gateway's generation pipeline; the stage list above is the
              pipeline's own report. “Try it” sends the reference solution through the same grader you are, so a green result is that
              evaluator's verdict, not a claim.
            </div>
          </div>
        </div>
      )}

      {result && (
        <div className="mt-18">
          <div className="card">
            <div className="sectiontitle">
              <h2>{verified ? 'Challenge validated' : 'Challenge needs attention'}</h2>
              <span className="pill" style={verified
                ? { background: 'var(--green-soft)', color: 'var(--green)' }
                : { background: 'var(--red-soft)', color: 'var(--red)' }}>
                {verified ? 'Verified on the engine' : 'Not verified'}
              </span>
            </div>
            <div className="grid2 mt-12">
              <div>
                <h3>Prompt</h3>
                <p className="mt-8" style={{ fontSize: 13.5 }}>{scenario}. Return the top plan per region each month, including a deterministic tie-break, using window functions.</p>
                <div className="reportrow mt-8"><span>Difficulty</span><span>{difficulty}</span></div>
                <div className="reportrow"><span>Constraints</span><span className="mono" style={{ fontSize: 11 }}>{constraints}</span></div>
                <div className="reportrow"><span>Reference rows</span><span>{result.referenceRows}</span></div>
              </div>
              <div>
                <h3>Validation evidence</h3>
                <div className="reportrow">
                  <span>Reference vs contract</span>
                  <span className={result.referencePassed ? 'pass' : 'fail'}>{result.referencePassed ? '✓ pass' : '✕ failed'}</span>
                </div>
                <div className="reportrow">
                  <span>Adversarial fixtures</span>
                  <span className={result.fixturesRejected === result.fixturesTotal ? 'pass' : 'fail'}>
                    {result.fixturesRejected}/{result.fixturesTotal} rejected
                  </span>
                </div>
                <div className="reportrow"><span>Wall time</span><span>{result.ms} ms</span></div>
                <div className="reportrow"><span>Execution engine</span><span className="mono" style={{ fontSize: 11 }}>{executionEngineLabel(s.api)}</span></div>
                <div className="reportrow"><span>Evidence source</span><span className="mono" style={{ fontSize: 11 }}>{generationProvenance(s.api)}</span></div>
                <div className="reportrow"><span>Artifact version</span><span className="mono" style={{ fontSize: 11 }}>challenge_version {result.version}</span></div>
              </div>
            </div>
            <div className="callout mt-14">
              <strong>How to read this.</strong> The reference query and the wrong queries above were graded by the same
              evaluator you are, on the engine named above — a green run means the reference satisfies its contract and
              the fixtures did not. The challenge text itself is authored, not model-generated, in this build.
            </div>
            <div className="actions mt-14">
              <button className="primary" onClick={s.resumeCourse}><Icon name="play" />Practise this now</button>
              <button onClick={() => s.toast('Saved to your challenge set.')}>Save challenge</button>
              <button className="ghost" onClick={reset}>Discard</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
