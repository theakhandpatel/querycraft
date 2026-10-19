/*
 * AI seam audit (design §11, M4).
 *
 * The honesty rules are the point of this suite. The browser adapters must
 * report the lab unavailable rather than synthesise a challenge; availability
 * must never throw; and the http adapter must surface whatever the gateway
 * reported — an active artifact, a rejection, an unavailable provider, a
 * transport failure — without ever inventing one.
 *
 *   node scripts/ai-adapters-audit.mjs
 *     Requires /tmp/qc-ai.cjs (bundled by `npm run audit-ai` from
 *     src/api/ai.ts), boots a tiny node:http server that answers the fixed
 *     endpoints with canned gateway responses, drives the real
 *     `gatewayAiMethods` against it, and asserts on what came back.
 *
 * The only test-host code is the `perform` shim below: it is the same
 * status-and-body contract `httpLayer` implements, kept minimal so the checks
 * are about the seam, not the shim.
 */
import http from 'node:http';
import { createRequire } from 'node:module';
import { Effect } from 'effect';

const require = createRequire('/tmp/');
const ai = require('/tmp/qc-ai.cjs');
const deadline = require('/tmp/qc-deadline.cjs');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

/* ---------- the fixed endpoints ---------- */

const PATHS = {
  capabilities: 'GET /capabilities',
  challengeGenerate: 'POST /challenges/generate',
  courseGenerate: 'POST /courses/generate',
  list: 'GET /challenges',
  get: 'GET /challenges/ch1',
  reset: 'POST /challenges/ch1/reset',
  tutor: 'POST /tutor',
  critique: 'POST /challenges/ch1/critique',
  report: 'POST /challenges/ch1/report',
  patch: 'POST /challenges/ch1/patches/p1',
  runs: 'GET /runs',
  run: 'GET /runs/run-1',
  runMissing: 'GET /runs/nope',
};

const ARTIFACT = {
  id: 'ch1',
  version: 1,
  title: 'Top plan per region',
  prompt: 'Return the top plan per region each month.',
  difficulty: 'intermediate',
  objectives: ['rank within groups'],
  hints: ['use ROW_NUMBER'],
  setup: { dataset: 'commerce', schema_version: 1, seed_version: 1, statements: [] },
  reference_solution: 'SELECT 1',
  result_contract: { columns: ['plan'], column_order: 'exact', row_order: 'ignored', duplicates: 'preserved', numeric_tolerance: 0 },
  provenance: {
    provider: 'fake',
    model: 'fake-1',
    provider_version: '1',
    pipeline_version: '1',
    evaluator_version: '1',
    dataset_version: 1,
    created_at: '2026-10-15T00:00:00Z',
    repairs: 1,
  },
};

const STAGES = [
  { name: 'validate_request', state: 'done', detail: 'topics ok', ms: 3 },
  { name: 'reference_contract', state: 'done', ms: 12 },
  { name: 'cross_check', state: 'skipped' },
  { name: 'activate', state: 'done', detail: 'saved v1', ms: 1 },
];

const ACTIVE = {
  status: 'active',
  kind: 'challenge',
  artifact: ARTIFACT,
  stages: STAGES,
  attempts: [{ number: 1, stage: 'cross_check', detail: 'second solution agreed' }],
  evidence: { reference_rows: 7, cross_check_rows: 7, fixtures_rejected: 3, fixtures_total: 3, repairs: 1, scratch_database: 'scratch_1' },
  error: null,
};

let canned = {};
const recorded = [];

function serve() {
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      const chunks = [];
      req.on('data', (c) => chunks.push(c));
      req.on('end', () => {
        const key = `${req.method} ${req.url.split('?')[0]}`;
        const body = Buffer.concat(chunks).toString('utf8');
        recorded.push({ key, body });
        const entry = canned[key];
        if (!entry) {
          res.writeHead(404, { 'content-type': 'application/json' });
          res.end(JSON.stringify({ error: { code: 'AI_NOT_FOUND', message: 'no canned route' } }));
          return;
        }
        res.writeHead(entry.status ?? 200, { 'content-type': 'application/json' });
        res.end(entry.json === undefined ? '' : JSON.stringify(entry.json));
      });
    });
    server.listen(0, '127.0.0.1', () => resolve(server));
  });
}

/** The status+body transport `httpLayer` provides; a plain fetch round trip. */
function performFor(base) {
  return (method, path, body) =>
    Effect.tryPromise({
      try: async () => {
        const res = await fetch(base + path, {
          method,
          headers: body === undefined ? undefined : { 'content-type': 'application/json' },
          body: body === undefined ? undefined : JSON.stringify(body),
        });
        const text = await res.text();
        let parsed = null;
        if (text) {
          try {
            parsed = JSON.parse(text);
          } catch {
            parsed = null;
          }
        }
        return { status: res.status, body: parsed };
      },
      catch: (cause) => new Error(cause instanceof Error ? cause.message : String(cause)),
    });
}

const server = await serve();
const base = `http://127.0.0.1:${server.address().port}`;
const methods = ai.gatewayAiMethods({ perform: performFor(base) });
const run = (effect) => Effect.runPromise(effect);
const runFail = (effect) => Effect.runPromise(effect).then(() => null, (e) => e);

/* ---------- browser adapters: unavailable, never invented ---------- */

console.log('browser adapters');
{
  const browser = ai.browserAiMethods();
  const challenge = await run(browser.generateChallenge({ topics: ['joins'] }));
  check('generateChallenge reports unavailable', challenge.status === 'unavailable', challenge.status);
  check('generateChallenge carries AI_UNAVAILABLE', challenge.error?.code === 'AI_UNAVAILABLE', challenge.error);
  check('generateChallenge invents no artifact', challenge.artifact === null, challenge.artifact);
  check('generateChallenge invents no stages', challenge.stages.length === 0 && challenge.attempts.length === 0);

  const course = await run(browser.generateCourse({ title: 'x' }));
  check('generateCourse reports unavailable', course.status === 'unavailable' && course.artifact === null, course.status);

  const availability = await run(browser.aiAvailability());
  check('aiAvailability resolves (never throws)', availability !== null && typeof availability === 'object');
  check('aiAvailability is honestly not available', availability.available === false && availability.configured === false);
  check('aiAvailability gives the learner a reason', typeof availability.reason === 'string' && availability.reason.length > 0, availability.reason);

  const tutorError = await runFail(browser.tutor({ sql: 'SELECT 1', objective: 'x' }));
  check('a browser tutor refuses instead of inventing a hint', tutorError?.code === 'AI_UNAVAILABLE', tutorError);
  const listError = await runFail(browser.listChallenges());
  check('a browser challenge list refuses instead of returning []', listError?.code === 'AI_UNAVAILABLE', listError);
}

/* ---------- how long a provider-backed call may take ---------- */

/*
 * The defect this pins: the transport gave every call the 15 s query deadline,
 * so a course generation that takes ~72 s was aborted mid-run, retried twice,
 * and reported as an unreachable gateway. A provider-backed call must instead
 * inherit the gateway's own run budget, and must not be retried: a second
 * attempt asks the model for a second course.
 */
console.log('the deadline a provider-backed call gets');
{
  const seen = [];
  const spy = (method, path, body, opts) => {
    seen.push({ method, path, opts });
    return performFor(base)(method, path, body, opts);
  };
  const budget = 300000;
  const spied = ai.gatewayAiMethods({ perform: spy, providerDeadlineMs: () => budget });
  const last = () => seen[seen.length - 1];

  await run(spied.generateCourse({ title: 'x' }));
  check('a course generation asks for the advertised budget', last().opts?.timeoutMs === budget, last().opts);
  check('a course generation is not retried', last().opts?.retries === 0, last().opts);

  await run(spied.generateChallenge({ topics: ['joins'] }));
  check('a challenge generation asks for the advertised budget', last().opts?.timeoutMs === budget, last().opts);
  check('a challenge generation is not retried', last().opts?.retries === 0, last().opts);

  await runFail(spied.tutor({ sql: 'SELECT 1', objective: 'x' }));
  check('a tutor call waits on the model too', last().opts?.timeoutMs === budget, last().opts);
  await runFail(spied.critique('ch1', { sql: 'SELECT 1', objective: 'x' }));
  check('a critique waits on the model too', last().opts?.timeoutMs === budget, last().opts);
  await runFail(spied.reportDefect({ challenge_id: 'ch1', sql: 'SELECT 1', objective: 'x', expected_passed: true, actual_passed: false }));
  check('a defect report waits on the model too', last().opts?.timeoutMs === budget, last().opts);

  await runFail(spied.listChallenges());
  check('a plain list keeps the transport deadline, not the run budget', last().opts?.timeoutMs === undefined, last().opts);
  check('a plain list keeps the transport retries', last().opts?.retries === undefined, last().opts);

  const noBudget = ai.gatewayAiMethods({ perform: spy });
  await run(noBudget.generateCourse({ title: 'x' }));
  check(
    'a transport that advertises no budget leaves the deadline to the transport',
    last().opts?.timeoutMs === undefined,
    last().opts,
  );
  check('a transport that advertises no budget still does not retry a generation', last().opts?.retries === 0, last().opts);
}

/* ---------- the arithmetic that keeps "working" ≠ "unreachable" ---------- */

console.log('the advertised run budget');
{
  const body = { ai: { configured: true, available: true, timeout_ms: 120000 } };
  check('the budget is read from /capabilities', deadline.advertisedRunBudgetMs(body) === 120000, deadline.advertisedRunBudgetMs(body));

  for (const [name, value] of [
    ['a body with no ai block', {}],
    ['an ai block with no budget', { ai: { available: true } }],
    ['a zero budget', { ai: { timeout_ms: 0 } }],
    ['a negative budget', { ai: { timeout_ms: -1 } }],
    ['a string budget', { ai: { timeout_ms: '120000' } }],
    ['a null body', null],
    ['a non-object body', 'nope'],
  ]) {
    check(`${name} yields no budget`, deadline.advertisedRunBudgetMs(value) === null, deadline.advertisedRunBudgetMs(value));
  }

  // The load-bearing property: the client outlasts the gateway's own deadline,
  // so a slow run surfaces the pipeline's honest failure, never a transport
  // error the pipeline did not cause.
  for (const advertised of [45000, 120000, 135000, 300000, 600000]) {
    const wait = deadline.providerDeadlineMs(advertised);
    check(`a ${advertised}ms budget is waited out (${wait}ms)`, wait > advertised);
  }
  check(
    'no advertised budget falls back to a generous wait',
    deadline.providerDeadlineMs(null) === deadline.AI_DEADLINE_FALLBACK_MS &&
      deadline.providerDeadlineMs(null) > 60_000,
    deadline.providerDeadlineMs(null),
  );
  check(
    'the fallback is longer than any query deadline the transport uses',
    deadline.AI_DEADLINE_FALLBACK_MS > 15000,
    deadline.AI_DEADLINE_FALLBACK_MS,
  );
}

/* ---------- availability parsing ---------- */

console.log('availability parsing');
{
  const advertised = ai.availabilityFromCapabilities({
    hard_timeout: true,
    ai: { configured: true, available: true, provider: 'fake', model: 'fake-1', deterministic: true, reason: '' },
  });
  check('reads the gateway ai object', advertised.available === true && advertised.provider === 'fake' && advertised.model === 'fake-1', advertised);

  const missing = ai.availabilityFromCapabilities({ hard_timeout: true });
  check('a gateway with no ai object reads as unavailable', missing.available === false, missing);
  check('a gateway with no ai object still explains why', missing.reason.length > 0, missing.reason);
  check('a null body never throws', ai.availabilityFromCapabilities(null).available === false);
}

/* ---------- http adapter: active generation ---------- */

console.log('http adapter · active');
{
  recorded.length = 0;
  canned = {
    [PATHS.challengeGenerate]: { status: 200, json: ACTIVE },
    [PATHS.capabilities]: { status: 200, json: { hard_timeout: true, ai: ai.availabilityFromCapabilities({ ai: { configured: true, available: true, provider: 'fake', reason: '' } }) } },
  };
  const outcome = await run(methods.generateChallenge({ topics: ['window functions'], difficulty: 'intermediate' }));
  check('parses the active outcome', outcome.status === 'active', outcome.status);
  check('keeps the artifact the gateway returned', outcome.artifact?.id === 'ch1', outcome.artifact?.id);
  check('renders the server stages, not a local list', outcome.stages.length === 4 && outcome.stages[0].name === 'validate_request', outcome.stages.map((s) => s.name));
  check('keeps stage state, detail and ms verbatim', outcome.stages[2].state === 'skipped' && outcome.stages[0].detail === 'topics ok' && outcome.stages[1].ms === 12, outcome.stages);
  check('keeps attempts verbatim', outcome.attempts.length === 1 && outcome.attempts[0].detail === 'second solution agreed', outcome.attempts);
  check('keeps evidence verbatim', outcome.evidence?.fixtures_rejected === 3 && outcome.evidence?.scratch_database === 'scratch_1', outcome.evidence);
  check('keeps the artifact provenance (provider/model)', outcome.artifact?.provenance.provider === 'fake' && outcome.artifact?.provenance.model === 'fake-1');

  const sent = recorded.find((r) => r.key === PATHS.challengeGenerate);
  const payload = sent ? JSON.parse(sent.body) : {};
  check('sends the request to the fixed endpoint with kind=challenge', sent !== undefined && payload.kind === 'challenge', payload);
  check('passes the learner topics through', Array.isArray(payload.topics) && payload.topics[0] === 'window functions', payload.topics);

  const availability = await run(methods.aiAvailability());
  check('http aiAvailability reports the gateway ai object', availability.available === true && availability.provider === 'fake', availability);
}

/* ---------- http adapter: an activated course ---------- */

/* The defect this pins: `plan` and `chapters` were added to the outcome type
 * but never read, so a course the gateway had verified, executed and stored
 * arrived at the screen as `status: 'active'` with `plan: undefined`. The screen
 * treats an active outcome with no plan as a refusal, so a real 147-second
 * generation ended with "the course was rejected at the verification stage" and
 * the learner enrolled in nothing. A missing field in a decoder is a
 * silent-feature-death bug, hence a fixture per shape. */

const COURSE_PLAN = {
  id: 'course-abc',
  version: 1,
  title: 'Window functions for interviews',
  subtitle: 'Ranking, partitioning and running totals',
  level: 'intermediate',
  dialect: 'postgresql',
  objectives: ['rank within groups', 'explain the frame clause'],
  setup: { dataset: 'commerce', schema_version: 1, seed_version: 1, statements: [] },
  provenance: {
    provider: 'openai-compatible',
    model: 'deepseek-v4.1-flash',
    provider_version: '1',
    pipeline_version: '1',
    evaluator_version: '1',
    dataset_version: 1,
    created_at: '2026-10-15T00:00:00Z',
    repairs: 1,
  },
  chapters: [
    {
      id: 'cte',
      n: 1,
      title: 'CTEs: readable analytical queries',
      summary: 'Name a step, then build on it.',
      kind: 'lesson',
      minutes: 12,
      objectives: ['factor a query into named steps'],
      sections: [
        { title: 'Why', body: 'A CTE names a step.\n\n```mermaid\ngraph LR; A-->B\n```\n' },
        { title: 'Practice', body: 'Write the revenue per customer first.' },
      ],
      exercises: [
        {
          id: 'ex1',
          title: 'Customers above average revenue',
          prompt: 'Return every customer whose revenue is above the average.',
          difficulty: 'intermediate',
          objectives: ['divide a problem into steps'],
          hints: ['compute the average in a CTE', 'compare in the outer query'],
          reference_solution: 'WITH a AS (SELECT avg(x) AS m FROM t) SELECT * FROM t, a WHERE t.x > a.m',
          cross_check_solution: 'SELECT * FROM t WHERE x > (SELECT avg(x) FROM t)',
          result_contract: { columns: ['id', 'revenue'], column_order: 'exact', row_order: 'ignored', duplicates: 'preserved', numeric_tolerance: 0.01 },
          expected: { columns: ['id', 'revenue'], rows: [[1, '10.00'], [2, '20.00']] },
        },
      ],
    },
    {
      id: 'window',
      n: 2,
      title: 'Window functions',
      summary: 'Rank without collapsing rows.',
      objectives: ['rank within a partition'],
      sections: [{ title: 'Ranking', body: 'Use ROW_NUMBER() OVER (PARTITION BY ...).' }],
      exercises: [
        {
          id: 'ex2',
          title: 'Top plan per region',
          prompt: 'Return the highest plan per region.',
          difficulty: 'advanced',
          objectives: ['partition'],
          hints: ['ROW_NUMBER'],
          reference_solution: 'SELECT * FROM (SELECT *, ROW_NUMBER() OVER (PARTITION BY r ORDER BY p DESC) n FROM t) s WHERE n = 1',
          cross_check_solution: 'SELECT DISTINCT ON (r) * FROM t ORDER BY r, p DESC',
          result_contract: { columns: ['r', 'p'], column_order: 'exact', row_order: 'ignored', duplicates: 'preserved', numeric_tolerance: 0 },
          expected: { columns: ['r', 'p'], rows: [['eu', 'pro']] },
        },
      ],
    },
  ],
};

const COURSE_ACTIVE = {
  status: 'active',
  kind: 'course',
  artifact: null,
  plan: COURSE_PLAN,
  chapters: [
    { id: 'cte', title: 'CTEs: readable analytical queries', exercises: [{ id: 'ex1', title: 'Customers above average revenue', status: 'verified', reference_rows: 2, cross_check_rows: 2, fixtures_rejected: 3, fixtures_total: 3, ms: 6.1 }] },
    { id: 'window', title: 'Window functions', exercises: [{ id: 'ex2', title: 'Top plan per region', status: 'verified', reference_rows: 1, cross_check_rows: 1, fixtures_rejected: 3, fixtures_total: 3, ms: 7.3 }] },
  ],
  stages: [
    { name: 'validate_request', state: 'done', ms: 1 },
    { name: 'request_bundle', state: 'done', ms: 77859 },
    { name: 'validate_artifact', state: 'done', ms: 146928.9, detail: '4 chapters, 8 exercises' },
    { name: 'scratch_environment', state: 'done', ms: 69.1 },
    { name: 'reference_contract', state: 'done', ms: 112.5, detail: '8/8 reference solutions returned rows (124 rows measured)' },
    { name: 'cross_check', state: 'done', ms: 112.5 },
    { name: 'adversarial_fixtures', state: 'done', ms: 112.5 },
    { name: 'activate', state: 'done', ms: 6, detail: 'stored as active: 4 chapters, 8 verified exercises' },
  ],
  attempts: [],
  error: null,
};

console.log('http adapter · an activated course');
{
  canned = { [PATHS.courseGenerate]: { status: 201, json: COURSE_ACTIVE } };
  const outcome = await run(methods.generateCourse({ title: 'window functions', kind: 'course' }));
  check('a course comes back active', outcome.status === 'active', outcome.status);
  check('a course invents no challenge artifact', outcome.artifact === null, outcome.artifact);
  check('the plan survives the decoder at all', outcome.plan != null, outcome.plan);
  check('the plan keeps its title and level', outcome.plan?.title === 'Window functions for interviews' && outcome.plan?.level === 'intermediate', outcome.plan?.title);
  check('the plan keeps every chapter', outcome.plan?.chapters.length === 2, outcome.plan?.chapters.length);
  check('a chapter keeps its markdown sections verbatim', outcome.plan?.chapters[0].sections[0].body.includes('```mermaid'), outcome.plan?.chapters[0].sections[0].body);
  check('a chapter keeps its exercises', outcome.plan?.chapters[1].exercises.length === 1, outcome.plan?.chapters[1].exercises.length);
  check('an exercise keeps its reference and cross-check solutions', typeof outcome.plan?.chapters[0].exercises[0].reference_solution === 'string' && typeof outcome.plan?.chapters[0].exercises[0].cross_check_solution === 'string');
  check('an exercise keeps the engine-measured columns in order', outcome.plan?.chapters[0].exercises[0].expected?.columns[0] === 'id', outcome.plan?.chapters[0].exercises[0].expected);
  check('an exercise keeps the engine-measured rows, cells intact', outcome.plan?.chapters[0].exercises[0].expected?.rows[1][1] === '20.00', outcome.plan?.chapters[0].exercises[0].expected?.rows);
  check('the plan keeps its provenance (provider/model)', outcome.plan?.provenance.provider === 'openai-compatible' && outcome.plan?.provenance.model === 'deepseek-v4.1-flash', outcome.plan?.provenance);
  check('the per-exercise report survives', outcome.chapters?.length === 2 && outcome.chapters?.[0].exercises[0].status === 'verified', outcome.chapters);
  check('the report keeps the measured row counts', outcome.chapters?.[0].exercises[0].reference_rows === 2 && outcome.chapters?.[1].exercises[0].fixtures_total === 3, outcome.chapters?.[0].exercises[0]);
  check('the gateway stages survive alongside the plan', outcome.stages.length === 8 && outcome.stages[7].name === 'activate', outcome.stages.length);

  // A course whose rows were NOT measured must not grade against an invention.
  const unmeasured = { ...COURSE_PLAN, chapters: [{ ...COURSE_PLAN.chapters[0], exercises: [{ ...COURSE_PLAN.chapters[0].exercises[0], expected: undefined }] }] };
  canned = { [PATHS.courseGenerate]: { status: 201, json: { ...COURSE_ACTIVE, plan: unmeasured } } };
  const noRows = await run(methods.generateCourse({ title: 'x', kind: 'course' }));
  check('an exercise with no measured answer keeps none', noRows.plan?.chapters[0].exercises[0].expected === undefined, noRows.plan?.chapters[0].exercises[0].expected);

  // Half a course is not a course: the decoder refuses it rather than letting a
  // chapter render with exercises that never verified.
  const brokenExercises = { ...COURSE_PLAN, chapters: [{ ...COURSE_PLAN.chapters[0], exercises: [{ id: 'ex1' }] }] };
  canned = { [PATHS.courseGenerate]: { status: 201, json: { ...COURSE_ACTIVE, plan: brokenExercises } } };
  const half = await run(methods.generateCourse({ title: 'x', kind: 'course' }));
  check('an unreadable exercise makes the plan unreadable, not half-readable', half.plan == null, half.plan);
  check('an unreadable plan is still reported as active data, not invented', half.status === 'active', half.status);

  canned = { [PATHS.courseGenerate]: { status: 201, json: { ...COURSE_ACTIVE, plan: undefined } } };
  const noPlan = await run(methods.generateCourse({ title: 'x', kind: 'course' }));
  check('an active course with no plan reports no plan', noPlan.plan == null, noPlan.plan);
}

/* ---------- http adapter: honest failures ---------- */

console.log('http adapter · honest failure');
{
  canned = {
    [PATHS.challengeGenerate]: { status: 200, json: { status: 'rejected', artifact: null, stages: [{ name: 'cross_check', state: 'failed', detail: 'solutions disagreed' }], attempts: [], error: { code: 'AI_CROSS_CHECK_FAILED', stage: 'cross_check', message: 'two solutions did not agree' } } },
  };
  const rejected = await run(methods.generateChallenge({}));
  check('a rejected generation stays rejected', rejected.status === 'rejected', rejected.status);
  check('a rejected generation fabricates no artifact', rejected.artifact === null, rejected.artifact);
  check('a rejected generation keeps the error code', rejected.error?.code === 'AI_CROSS_CHECK_FAILED', rejected.error);

  canned = { [PATHS.courseGenerate]: { status: 503, json: { error: { code: 'AI_UNAVAILABLE', message: 'no provider configured' } } } };
  const unavailable = await run(methods.generateCourse({ title: 'x' }));
  check('an unavailable provider reads as unavailable', unavailable.status === 'unavailable', unavailable.status);
  check('an unavailable provider fabricates no artifact', unavailable.artifact === null && unavailable.stages.length === 0);
  check('an unavailable provider keeps the gateway message', unavailable.error?.message === 'no provider configured', unavailable.error);

  canned = { [PATHS.challengeGenerate]: { status: 400, json: { error: { code: 'AI_INVALID_REQUEST', message: 'a topic or title is required', detail: 'topics' } } } };
  const invalid = await run(methods.generateChallenge({}));
  check('a rejected request reads as failed', invalid.status === 'failed' && invalid.error?.code === 'AI_INVALID_REQUEST', invalid);
  check('a rejected request keeps the envelope detail', invalid.error?.detail === 'topics', invalid.error);

  canned = { [PATHS.challengeGenerate]: { status: 200, json: { hello: 'not an outcome' } } };
  const garbage = await run(methods.generateChallenge({}));
  check('an unreadable 2xx body fails rather than passing as an artifact', garbage.status === 'failed' && garbage.artifact === null, garbage.status);
  check('an unreadable 2xx body names AI_INVALID_ARTIFACT', garbage.error?.code === 'AI_INVALID_ARTIFACT', garbage.error);

  canned = { [PATHS.challengeGenerate]: { status: 422, json: { status: 'rejected', artifact: null, stages: [], attempts: [], error: { code: 'AI_INVALID_ARTIFACT', message: 'contract too loose' } } } };
  const outcomeOnError = await run(methods.generateChallenge({}));
  check('a non-2xx that carries an outcome is honoured, not overwritten', outcomeOnError.status === 'rejected' && outcomeOnError.error?.code === 'AI_INVALID_ARTIFACT', outcomeOnError);

  const offline = ai.gatewayAiMethods({ perform: performFor('http://127.0.0.1:1') });
  const unreachable = await run(offline.generateChallenge({}));
  check('a transport failure is a failed, empty outcome', unreachable.status === 'failed' && unreachable.artifact === null, unreachable);
  check('a transport failure names ENVIRONMENT_UNAVAILABLE', unreachable.error?.code === 'ENVIRONMENT_UNAVAILABLE', unreachable.error);
  const offlineAvailability = await run(offline.aiAvailability());
  check('aiAvailability never throws when the gateway is unreachable', offlineAvailability.available === false && offlineAvailability.reason.length > 0, offlineAvailability);
}

/* ---------- http adapter: the non-outcome calls keep the typed error ---------- */

console.log('http adapter · lifecycle, tutor, critique, defects');
{
  canned = {
    [PATHS.list]: { status: 200, json: [{ artifact: ARTIFACT, outcome: ACTIVE, status: 'active', created_at: '2026-10-15T00:00:00Z' }] },
    [PATHS.get]: { status: 200, json: { artifact: ARTIFACT, outcome: ACTIVE, status: 'active', created_at: '2026-10-15T00:00:00Z' } },
    [PATHS.reset]: { status: 200, json: { ok: true } },
    [PATHS.tutor]: { status: 200, json: { message: 'look at the tie-break', hint_level: 1, reveals_solution: false } },
    [PATHS.critique]: { status: 200, json: { message: 'grain is right', notes: ['tie-break is deterministic'] } },
    [PATHS.report]: { status: 200, json: { verified: true, applied: false, summary: 'reproduced', patch: { id: 'p1', challenge_id: 'ch1', summary: 'widen tolerance', changes: [{ field: 'numeric_tolerance', from: '0', to: '0.01' }] }, evidence: { repairs: 0 }, error: null } },
    [PATHS.patch]: { status: 200, json: { applied: true, patch: null, error: null } },
  };

  const list = await run(methods.listChallenges());
  check('listChallenges parses stored records', list.length === 1 && list[0].artifact.id === 'ch1', list.length);
  const one = await run(methods.getChallenge('ch1'));
  check('getChallenge parses a record', one?.artifact.id === 'ch1', one?.artifact.id);
  const reset = await run(methods.resetChallenge('ch1'));
  check('resetChallenge reports ok', reset.ok === true, reset);

  const tutor = await run(methods.tutor({ challenge_id: 'ch1', sql: 'SELECT 1', objective: 'rank' }));
  check('tutor parses the response', tutor.message === 'look at the tie-break' && tutor.hint_level === 1, tutor);
  check('tutor never claims to reveal the solution', tutor.reveals_solution === false, tutor.reveals_solution);

  const critique = await run(methods.critique('ch1', { sql: 'SELECT 1', objective: 'rank' }));
  check('critique parses the notes', critique.notes.length === 1, critique);

  const verdict = await run(methods.reportDefect({ challenge_id: 'ch1', description: 'ties are ambiguous' }));
  check('reportDefect parses the verdict', verdict.verified === true && verdict.applied === false, verdict);
  check('a proposed patch is evidence, not an action', verdict.patch?.id === 'p1' && verdict.applied === false);

  const decision = await run(methods.decidePatch('ch1/p1', true));
  check('decidePatch reports what the gateway applied', decision.applied === true, decision);
  const patchSent = recorded.find((r) => r.key === PATHS.patch);
  const patchBody = patchSent ? JSON.parse(patchSent.body) : {};
  check('decidePatch addresses both route segments', patchSent !== undefined, recorded.map((r) => r.key));
  check('decidePatch sends approved and an approver', patchBody.approved === true && typeof patchBody.approved_by === 'string' && patchBody.approved_by.length > 0, patchBody);

  recorded.length = 0;
  canned = { [PATHS.get]: { status: 404, json: { error: { code: 'AI_NOT_FOUND', message: 'no such challenge' } } } };
  const missing = await run(methods.getChallenge('ch1'));
  check('getChallenge answers null for a missing challenge', missing === null, missing);

  canned = { [PATHS.list]: { status: 500, json: { error: { code: 'ENVIRONMENT_UNAVAILABLE', message: 'depot down' } } } };
  const listError = await runFail(methods.listChallenges());
  check('a failed non-outcome call throws the gateway code', listError?.code === 'ENVIRONMENT_UNAVAILABLE', listError);

  canned = { [PATHS.tutor]: { status: 200, json: { nope: true } } };
  const badTutor = await runFail(methods.tutor({ sql: 'SELECT 1', objective: 'x' }));
  check('an unreadable tutor response throws rather than half-parsing', badTutor?.code === 'AI_INVALID_ARTIFACT', badTutor);
}

/* ---------- the agent-run audit trail ----------
 *
 * The gateway stores a redacted, bounded transcript per generation run and
 * scopes every read to the session. These checks pin the seam that reads it:
 * a summary and a transcript parse whole (or are dropped), tool calls keep
 * their attribution to their results, and a clipped or scrubbed entry says so.
 * The offline page has no run store, so it answers an empty list and null —
 * it never invents a transcript.
 */

const RUN_SUMMARY = {
  id: 'run-1',
  kind: 'course',
  status: 'active',
  entry_count: 4,
  bytes: 80,
  truncated: false,
  started_at: '2026-10-15T00:00:00Z',
  finished_at: '2026-10-15T00:00:02Z',
};

const RUN_TRANSCRIPT = {
  id: 'run-1',
  kind: 'course',
  status: 'active',
  entries: [
    { seq: 0, kind: 'message', role: 'system', content: 'You design SQL courses.', at: '2026-10-15T00:00:00Z' },
    { seq: 1, kind: 'message', role: 'user', content: 'Design one complete course.', at: '2026-10-15T00:00:00Z' },
    { seq: 2, kind: 'tool_call', role: 'assistant', tool_name: 'save_chapter', tool_call_id: 'call_1', content: '{"id":"gen.joins"}', at: '2026-10-15T00:00:01Z' },
    { seq: 3, kind: 'tool_result', role: 'tool', tool_call_id: 'call_1', content: '{"status":"verified"}', at: '2026-10-15T00:00:01Z', truncated: true, redacted: true },
  ],
  entry_count: 4,
  bytes: 80,
  truncated: true,
  started_at: '2026-10-15T00:00:00Z',
  finished_at: '2026-10-15T00:00:02Z',
};

console.log('the agent-run audit trail');
{
  /* The offline page has no run store: an empty list and no run to read, and
     neither invents a transcript. */
  const browser = ai.browserAiMethods();
  const browserList = await run(browser.listRuns());
  check('a browser run list is honestly empty, never fabricated', Array.isArray(browserList) && browserList.length === 0, browserList);
  const browserRun = await run(browser.getRun('run-1'));
  check('a browser run read is not-found, not an invented transcript', browserRun === null, browserRun);

  /* The list route: owner-scoped summaries with the fields a reader scans. */
  canned = { [PATHS.runs]: { status: 200, json: { ok: true, runs: [RUN_SUMMARY] } } };
  const list = await run(methods.listRuns());
  check('the run list route is read', recorded.some((r) => r.key === PATHS.runs));
  check('a stored run summary survives the decoder', list.length === 1 && list[0].id === 'run-1', list);
  check('a summary keeps its kind and status', list[0]?.kind === 'course' && list[0]?.status === 'active', list[0]);
  check('a summary keeps its entry count and bytes', list[0]?.entry_count === 4 && list[0]?.bytes === 80, list[0]);
  check('a summary keeps its start and finish', list[0]?.started_at === '2026-10-15T00:00:00Z' && list[0]?.finished_at === '2026-10-15T00:00:02Z', list[0]);

  /* A malformed row is dropped, so one corrupt run cannot hide the readable
     ones -- and never a half-row. */
  canned = { [PATHS.runs]: { status: 200, json: { ok: true, runs: [RUN_SUMMARY, { id: 'broken' }, { nope: true }] } } };
  const pruned = await run(methods.listRuns());
  check('a malformed run row is dropped, not half-read', pruned.length === 1 && pruned[0].id === 'run-1', pruned);

  /* A failed list read is an error, not the claim "you have no runs". */
  canned = { [PATHS.runs]: { status: 503, json: { ok: false, runs: [], error: { code: 'AI_UNAVAILABLE', message: 'AI is off' } } } };
  const off = await runFail(methods.listRuns());
  check('a run list that failed throws rather than reporting none', off?.code === 'AI_UNAVAILABLE', off);
  check('the AI-off empty list is not smuggled through as data', !Array.isArray(off), off);

  canned = { [PATHS.runs]: { status: 500, json: { error: { code: 'ENVIRONMENT_UNAVAILABLE', message: 'depot down' } } } };
  const down = await runFail(methods.listRuns());
  check('a storage failure is a typed error, not an empty list', down?.code === 'ENVIRONMENT_UNAVAILABLE', down);

  /* One run's whole transcript: entries in order, tool calls attributed to
     their results, and the redacted/truncated flags reported, not hidden. */
  canned = { [PATHS.run]: { status: 200, json: { ok: true, run: RUN_TRANSCRIPT } } };
  const rec = await run(methods.getRun('run-1'));
  check('the transcript is read at all', rec?.id === 'run-1', rec);
  check('the transcript keeps its kind and status', rec?.kind === 'course' && rec?.status === 'active', rec);
  check('the transcript keeps every entry, in order', rec?.entries.length === 4 && rec.entries.map((e) => e.seq).join(',') === '0,1,2,3', rec?.entries);
  check('a message keeps its role and text', rec?.entries[0].role === 'system' && rec?.entries[0].content === 'You design SQL courses.', rec?.entries?.[0]);
  check('a tool call keeps the function it asked for', rec?.entries[2].kind === 'tool_call' && rec?.entries[2].tool_name === 'save_chapter', rec?.entries?.[2]);
  check('a tool result keeps its call attribution', rec?.entries[3].kind === 'tool_result' && rec?.entries[3].tool_call_id === 'call_1', rec?.entries?.[3]);
  check('a clipped entry says so', rec?.entries[3].truncated === true, rec?.entries?.[3]);
  check('a scrubbed entry says so', rec?.entries[3].redacted === true, rec?.entries?.[3]);
  check('a truncated transcript says so', rec?.truncated === true, rec?.truncated);

  /* A run that is gone, or not yours, is a 404 read as null, not an error. */
  canned = { [PATHS.runMissing]: { status: 404, json: { ok: false, error: { code: 'NOT_FOUND', message: 'unknown run' } } } };
  const gone = await run(methods.getRun('nope'));
  check('a run that is not there reads as null', gone === null, gone);

  canned = { [PATHS.run]: { status: 500, json: { error: { code: 'ENVIRONMENT_UNAVAILABLE', message: 'depot down' } } } };
  const readDown = await runFail(methods.getRun('run-1'));
  check('a storage failure on a run is a typed error, not a missing run', readDown?.code === 'ENVIRONMENT_UNAVAILABLE', readDown);

  /* The decoder decides a transcript is whole: a body with no entry list is
     not one this client renders as an empty conversation. */
  check('a body without an entry list is not a transcript', ai.asRunTranscript({ id: 'run-1', kind: 'course', status: 'active' }) === null);
  check('an unreadable summary is dropped by the list decoder', ai.asRunSummaries({ ok: true, runs: [{ id: 'broken' }] }).length === 0);
}

server.close();

if (failed) {
  console.log(`\nai-adapters-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log('\nai-adapters-audit: OK');
