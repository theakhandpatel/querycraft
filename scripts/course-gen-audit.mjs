/*
 * M8 exit-gate audit — a course that builds itself, one chapter at a time.
 *
 * A course is a plan: chapters of markdown and exercises. Under M8 the gateway
 * does not ask for that plan in one reply. It asks for a small outline, stores
 * it as a `building` course, and then holds one conversation in which the model
 * hands in one chapter per `save_chapter` tool call. The gateway verifies each
 * chapter in the course's own throwaway environment — every reference runs,
 * every cross-check is a second independently written solution through the real
 * evaluator, every wrong answer is rejected by the adversarial fixtures — and
 * appends the chapter to the stored plan the moment it passes. The tool result
 * carries the engine's own verdict back into the conversation, so the model
 * repairs against a real error; a chapter that still cannot pass is dropped and
 * reported, and a run in which nothing verified is refused and stores nothing.
 *
 * This audit measures that promise against the real HTTP API of the real binary
 * and its real embedded PostgreSQL:
 *
 *   1. a signed-in caller generates a course and gets `status: "active"` back,
 *      carrying a plan whose chapters have markdown with non-empty bodies and
 *      whose exercises carry the engine's own measured rows (`expected`);
 *   2. every exercise in that plan is reported `verified`, with a non-empty
 *      reference result and a cross-check that measured the same number of rows;
 *   3. the plan can be read back through GET /courses/{id} by the learner who
 *      generated it, while a second signed-in account gets 404 for the same id
 *      and a GET /courses that does not list it;
 *   4. the same request activates twice (the built-in provider is a pure
 *      function of the request). The wall clock of each run is reported because
 *      it is what a person waits; nothing asserts on it;
 *   5. the streaming promise, seen through a stub provider that pauses between
 *      turns: the job reports `chapters_planned` / `chapters_verified` /
 *      `current_chapter` while it runs, and GET /courses/{id} answers with a
 *      `building` course carrying exactly the chapters that already verified
 *      while later chapters are still being written;
 *   6. a chapter whose cross-check disagrees is refused with the engine's own
 *      message, repaired against it and — when the model will not fix it —
 *      dropped and reported. The course still activates with the chapters that
 *      did verify, and never contains the dropped one;
 *   7. a provider whose every chapter disagrees is refused outright: no plan,
 *      no record, and GET /courses lists nothing.
 *
 * Clauses 5–7 are driven by a stub provider: a small HTTP server the gateway is
 * pointed at with QC_AI_PROVIDER=openai-compatible, QC_AI_BASE_URL and a dummy
 * key. The stub speaks the tool-calling protocol — an outline when the gateway
 * asks for one, a `save_chapter` call per chapter, `finish_course` at the end —
 * so the wire the real endpoint uses is the wire this audit exercises. Nothing
 * about a verdict is written in JavaScript: the gateway runs the real pipeline
 * over real SQL and reports its own outcome, and the SQL and the outline are
 * authored here rather than lifted from a response.
 *
 * The two negations are also pinned at unit level in Go, and this audit runs
 * them too — with -count=1, so the PASS it prints is this run rather than a
 * cached verdict:
 *
 *   TestACourseWithNoVerifiedChapterIsRefusedAndNothingIsStored
 *   TestACourseIsStoredWhileItsLaterChaptersAreStillBeingWritten
 *
 *   make audit-course-gen
 *   node scripts/course-gen-audit.mjs
 */
import { spawnSync } from 'node:child_process';
import http from 'node:http';
import { Reporter, backendDir, buildGateway, freePort, getJSON, liveEnv, postJSON, startGateway, stopGateway } from './lib/gateway.mjs';

/* A course run is several statements in a throwaway environment, and the
   negative run adds the bounded repair loop on top. */
const REQUEST_TIMEOUT_MS = 180_000;

/* The stages a run must have completed before anything is stored. `repair` is
   absent from an activation (nothing needed repairing) and present on a
   rejection, so it is asserted where it belongs rather than assumed here. */
const COURSE_STAGES = [
  'validate_request',
  'request_bundle',
  'validate_artifact',
  'scratch_environment',
  'reference_contract',
  'cross_check',
  'adversarial_fixtures',
  'activate',
];

/* One request, used for both determinism runs. The built-in provider answers it
   with a plan built from the prompt, so the same body must activate twice. */
const COURSE_REQUEST = {
  topics: ['joins', 'aggregation', 'window functions'],
  difficulty: 'beginner',
  scenario: 'Count what each customer ordered, then rank the results.',
  constraints: ['one statement per exercise'],
};

const report = new Reporter('course-gen-audit');

/* redactSafe renders an error object for a log line without dumping a body. */
function redactSafe(err) {
  return err ? JSON.stringify({ code: err.code, stage: err.stage, message: err.message }) : 'no error';
}

/* ---------- small readers over the envelope ---------- */

function stageStates(outcome) {
  return new Map((outcome?.stages ?? []).map((s) => [s.name, s.state]));
}

function planExercises(plan) {
  const out = [];
  for (const chapter of plan?.chapters ?? []) {
    for (const exercise of chapter.exercises ?? []) out.push({ chapter, exercise });
  }
  return out;
}

function reportedExercises(outcome) {
  const out = [];
  for (const chapter of outcome?.chapters ?? []) {
    for (const exercise of chapter.exercises ?? []) out.push({ chapter, exercise });
  }
  return out;
}

function ids(items) {
  return items.map(({ exercise }) => exercise.id);
}

function sameSet(a, b) {
  const left = [...a].sort();
  const right = [...b].sort();
  return left.length === right.length && left.every((v, i) => v === right[i]);
}

async function signup(base, email) {
  const res = await postJSON(`${base}/auth/signup`, { email, name: 'Course Gen Audit', password: 'course-gen-audit-passphrase' });
  if (res.status !== 201 || !res.json?.token) throw new Error(`signup failed: HTTP ${res.status} ${res.body}`);
  return res.json.token;
}

function auth(token) {
  return { authorization: `Bearer ${token}` };
}

/**
 * runGoTest runs one Go test in the backend module and returns its output.
 *
 * Go is not on PATH in every environment this repo is exercised in, so the
 * binary comes from QC_GO when the caller set it (the Makefile passes $(GO))
 * and from PATH otherwise, with the standard install appended. The negative
 * case is also proven in Go because a scripted provider there is not something
 * the wire between this script and the gateway can influence.
 */
function runGoTest(name) {
  const go = process.env.QC_GO || 'go';
  /* -count=1 so the verdict is this audit's own run, never a cached one: a
     cached PASS would make "this audit ran it" untrue. */
  const out = spawnSync(go, ['test', './internal/ai/', '-run', name, '-v', '-count=1'], {
    cwd: backendDir(),
    env: { ...process.env, GOTOOLCHAIN: 'local', PATH: `${process.env.PATH ?? ''}:/usr/local/go/bin` },
    encoding: 'utf8',
    timeout: 300_000,
  });
  const text = `${out.stdout ?? ''}${out.stderr ?? ''}`;
  return {
    status: out.status,
    error: out.error,
    text,
    tail: text.trim().split('\n').slice(-8).join('\n'),
  };
}

/* ---------- the stub provider: a model that speaks the tool protocol ---------- */

/* The SQL below is the built-in provider's own authored `joins` topic, pinned
   here on purpose: a stub that read its answers out of an earlier response would
   be testing the pipeline's output instead of the pipeline. Each solution is a
   single read-only statement, which is why a refusal has to come from the engine
   (the cross-check stage) and not from validation. */
const REFERENCE = `SELECT c.name AS customer_name, count(o.id) AS order_count
FROM "dataset"."customers" c
JOIN "dataset"."orders" o ON o.customer_id = c.id
GROUP BY c.name
ORDER BY order_count DESC, customer_name ASC`;

const AGREEING_CROSS_CHECK = `SELECT c.name AS customer_name,
       (SELECT count(*) FROM "dataset"."orders" o WHERE o.customer_id = c.id) AS order_count
FROM "dataset"."customers" c
WHERE EXISTS (SELECT 1 FROM "dataset"."orders" o WHERE o.customer_id = c.id)
ORDER BY order_count DESC, customer_name ASC`;

const DISAGREEING_CROSS_CHECK = `SELECT * FROM (
${REFERENCE}
) AS stub_cross_check LIMIT 1`;

const SECTION_BODY = `Every question in this chapter is answered by one statement, and a single statement always has the same three parts: a row source, a grouping, and an ordering.

- **Row source.** Start from the table that owns the fact you are reporting on, not the one that merely names it.
- **Grouping.** Decide the grain first. GROUP BY is the grain written down.
- **Ordering.** A result set has no order until you ask for one, and ties need a tie-break so two runs cannot disagree.`;

/* The chapters the stub writes: two, one exercise each — the smallest course
   that can show one chapter being dropped while another is kept. */
const STUB_CHAPTERS = [
  {
    id: 'stub.chapter.1',
    title: 'Customers and how many orders they placed',
    summary: 'Count the orders each customer placed, most orders first.',
    objectives: ['Join customers to orders and count them per customer.'],
    exercise: {
      id: 'stub.exercise.1',
      title: 'How many orders did each customer place?',
      prompt: 'List each customer who has placed at least one order, with their name and number of orders, most orders first. Break ties by name.',
      crossCheck: AGREEING_CROSS_CHECK,
    },
  },
  {
    id: 'stub.chapter.2',
    title: 'The same question, answered twice',
    summary: 'This chapter is where the two answers stop agreeing.',
    objectives: ['Tell a reference solution from a solution that only looks like one.'],
    exercise: {
      id: 'stub.exercise.2',
      title: 'How many orders did each customer place? (second answer)',
      prompt: 'List each customer who has placed at least one order, with their name and number of orders, most orders first. Break ties by name.',
      crossCheck: AGREEING_CROSS_CHECK,
    },
  },
];

const BROKEN_CHAPTER_ID = 'stub.chapter.2';
const BROKEN_EXERCISE_ID = 'stub.exercise.2';

/* stubOutline is the small first answer: the same chapters, with no content and
   the exercise count each chapter will actually carry. The title is a parameter
   because it is what names the stored course, and each clause wants its own row. */
function stubOutline(title) {
  return {
    title,
    subtitle: 'Two chapters, one exercise each, written and verified while the run is still going.',
    level: 'beginner',
    objectives: [
      'Count what each customer ordered with one statement.',
      'Keep a course inactive when one of its exercises does not add up.',
    ],
    setup: { dataset: 'commerce', schema_version: 1, seed_version: 1, statements: [] },
    chapters: STUB_CHAPTERS.map((ch, i) => ({
      id: ch.id,
      n: i + 1,
      title: ch.title,
      summary: ch.summary,
      kind: 'Practice',
      minutes: 15,
      objectives: ch.objectives,
      exercises: 1,
    })),
  };
}

/* chapterArguments renders one save_chapter argument object: the outline's own
   chapter with its content filled in. `broken` swaps the exercise's cross-check
   for one that cannot agree with the reference, which is what clause 6 is about. */
function chapterArguments(chapter, broken) {
  return {
    id: chapter.id,
    n: STUB_CHAPTERS.findIndex((ch) => ch.id === chapter.id) + 1,
    title: chapter.title,
    summary: chapter.summary,
    kind: 'Practice',
    minutes: 15,
    objectives: chapter.objectives,
    sections: [{ title: 'The shape of the question', body: SECTION_BODY }],
    exercises: [
      {
        id: chapter.exercise.id,
        title: chapter.exercise.title,
        prompt: chapter.exercise.prompt,
        difficulty: 'beginner',
        type: 'write',
        objectives: ['Answer the prompt with one read-only statement.'],
        hints: ['Start from the table that owns the fact you are counting.'],
        reference_solution: REFERENCE,
        cross_check_solution: broken ? DISAGREEING_CROSS_CHECK : chapter.exercise.crossCheck,
        result_contract: {
          columns: ['customer_name', 'order_count'],
          column_order: 'exact',
          row_order: 'exact',
          duplicates: 'preserved',
          numeric_tolerance: 0,
        },
        checks: [],
      },
    ],
  };
}

/* resolvedChapters reads the tool results the gateway sent back and returns the
   chapters it has resolved (verified or dropped). That is how the stub knows
   which chapter to write next, exactly as a model does. */
function resolvedChapters(parsed) {
  const resolved = new Set();
  for (const m of parsed.messages ?? []) {
    if (m.role !== 'tool') continue;
    let result = null;
    try { result = JSON.parse(m.content); } catch { continue; }
    if (result?.chapter && (result.status === 'verified' || result.status === 'dropped')) resolved.add(result.chapter);
  }
  return resolved;
}

/* conversationTurn is the model's side of the chapter conversation: the next
   chapter that has not been resolved, or finish_course once every one has. */
function conversationTurn(parsed, broken) {
  const resolved = resolvedChapters(parsed);
  const next = STUB_CHAPTERS.find((ch) => !resolved.has(ch.id));
  if (next) {
    return {
      content: null,
      tool_calls: [{
        id: `call_${next.id}`,
        type: 'function',
        function: { name: 'save_chapter', arguments: JSON.stringify(chapterArguments(next, broken.has(next.id))) },
      }],
    };
  }
  return {
    content: null,
    tool_calls: [{
      id: 'call_finish_course',
      type: 'function',
      function: { name: 'finish_course', arguments: JSON.stringify({ summary: 'Every chapter that verified is stored.' }) },
    }],
  };
}

/**
 * startStubProvider is an OpenAI-compatible endpoint that speaks the chapter
 * protocol: an outline when the gateway asks for one, and one save_chapter call
 * per turn once the conversation starts — tools, and never tools together with
 * response_format, because the endpoint rejects that pair.
 *
 * delayMs pauses every reply, which is what lets a poll observe the run
 * mid-flight. breakChapters names the chapters whose cross-check will not agree.
 * Every call is recorded, including the tool results the gateway sent back, so
 * the audit can prove the repair loop really happened and really carried the
 * engine's message.
 */
async function startStubProvider({ title = 'A course written one chapter at a time', delayMs = 0, breakChapters = [] } = {}) {
  const calls = [];
  const broken = new Set(breakChapters);
  const outline = JSON.stringify(stubOutline(title));
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', async () => {
      let parsed = {};
      try { parsed = JSON.parse(body); } catch { /* this is a stub, not a validator */ }
      const hasTools = Array.isArray(parsed.tools) && parsed.tools.length > 0;
      const message = hasTools ? conversationTurn(parsed, broken) : { content: outline };
      const reply = (message.tool_calls ?? [])[0] ?? null;
      let replyChapter = null;
      if (reply?.function?.name === 'save_chapter') {
        try { replyChapter = JSON.parse(reply.function.arguments).id ?? null; } catch { replyChapter = null; }
      }
      calls.push({
        path: req.url ?? '',
        authorized: req.headers.authorization === 'Bearer course-gen-audit-key',
        hasTools,
        hasResponseFormat: parsed.response_format != null,
        toolResults: (parsed.messages ?? []).filter((m) => m.role === 'tool').map((m) => m.content),
        replyName: reply?.function?.name ?? 'content',
        replyChapter,
      });
      if (delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({
        choices: [{ message, finish_reason: message.tool_calls ? 'tool_calls' : 'stop' }],
        usage: { prompt_tokens: 0, completion_tokens: 0 },
      }));
    });
  });
  const port = await freePort();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });
  return {
    base: `http://127.0.0.1:${port}`,
    calls,
    stop: () => new Promise((resolve) => server.close(() => resolve())),
  };
}

/* ---------- the run table ---------- */

function runRow(label, provider, ms, outcome) {
  const exercises = reportedExercises(outcome);
  const refRows = exercises.reduce((n, { exercise }) => n + (exercise.reference_rows ?? 0), 0);
  const crossRows = exercises.reduce((n, { exercise }) => n + (exercise.cross_check_rows ?? 0), 0);
  const fixtures = exercises.reduce((n, { exercise }) => n + (exercise.fixtures_rejected ?? 0), 0);
  return [
    label,
    provider,
    `${ms} ms`,
    String(outcome?.status ?? 'no outcome'),
    String((outcome?.chapters ?? []).length),
    String(exercises.length),
    String(refRows),
    String(crossRows),
    String(fixtures),
  ];
}

/* ---------- the audit ---------- */

async function main() {
  const bin = buildGateway('course-gen');
  const env = await liveEnv('course-gen');
  let gw = null;
  let stub = null;
  const table = [
    ['run', 'provider', 'wall clock', 'status', 'chapters', 'exercises', 'ref rows', 'cross rows', 'fixtures rejected'],
  ];

  try {
    /* ---------- clauses 1, 2, 3, 4: the deterministic provider ---------- */
    gw = await startGateway(bin, { ...env, QC_AI_PROVIDER: 'fake' });
    const headers = auth(await signup(gw.base, `course-gen-audit-${process.pid}@querycraft.local`));

    const caps = await getJSON(`${gw.base}/capabilities`);
    report.section('the lab is on, with the deterministic provider the gateway ships with');
    report.check('capabilities.ai.available is true', caps.json?.ai?.available === true, JSON.stringify(caps.json?.ai));
    report.check('the provider is the built-in deterministic one', caps.json?.ai?.provider === 'fake' && caps.json?.ai?.deterministic === true, JSON.stringify(caps.json?.ai));

    const firstStart = Date.now();
    const first = await postJSON(`${gw.base}/courses/generate`, COURSE_REQUEST, REQUEST_TIMEOUT_MS, headers);
    const firstMS = Date.now() - firstStart;
    console.log(`  ..   HTTP ${first.status} in ${firstMS} ms, status=${first.json?.status}`);
    if (first.json?.error) console.log(`  ..   error: ${first.json.error.code} @ ${first.json.error.stage ?? '-'} — ${first.json.error.message}`);

    report.section('clause 1 — a signed-in caller generates a course that becomes active');
    report.check('the request is answered 201 Created', first.status === 201, `HTTP ${first.status}`);
    report.check('the outcome is active', first.json?.status === 'active', String(first.json?.status));
    report.check('the outcome is a course', first.json?.kind === 'course', String(first.json?.kind));
    const plan = first.json?.plan ?? null;
    report.check('only an active course carries a plan', !!plan, JSON.stringify(first.json?.error ?? null));
    report.check('the plan runs over the commerce dataset', plan?.setup?.dataset === 'commerce', plan?.setup?.dataset);
    report.check('the plan has at least one chapter', (plan?.chapters ?? []).length >= 1, (plan?.chapters ?? []).length);
    report.check('the plan has at least one exercise', planExercises(plan).length >= 1, planExercises(plan).length);

    const thinSections = [];
    for (const chapter of plan?.chapters ?? []) {
      if (!(chapter.sections ?? []).length) thinSections.push(`${chapter.id}: no sections`);
      for (const section of chapter.sections ?? []) {
        if (!String(section.body ?? '').trim()) thinSections.push(`${chapter.id}/${section.title}: empty body`);
      }
    }
    report.check('every chapter carries at least one markdown section with a non-empty body', thinSections.length === 0, thinSections);

    const missingRows = [];
    const columnDrift = [];
    for (const { exercise } of planExercises(plan)) {
      const expected = exercise.expected;
      if (!Array.isArray(expected?.rows) || expected.rows.length === 0) { missingRows.push(exercise.id); continue; }
      const want = exercise.result_contract?.columns ?? [];
      if (JSON.stringify(expected.columns) !== JSON.stringify(want)) columnDrift.push(`${exercise.id}: expected ${JSON.stringify(expected.columns)} contract ${JSON.stringify(want)}`);
      if (expected.rows.some((row) => row.length !== expected.columns.length)) columnDrift.push(`${exercise.id}: a row does not have ${expected.columns.length} columns`);
    }
    report.check('every exercise carries the engine\'s own measured rows (expected.rows is non-empty)', missingRows.length === 0, missingRows);
    report.check('the measured columns are the columns the contract names, in order', columnDrift.length === 0, columnDrift);

    const states = stageStates(first.json);
    const unfinished = COURSE_STAGES.filter((name) => states.get(name) !== 'done');
    report.check(`all ${COURSE_STAGES.length} pipeline stages completed before activation`, unfinished.length === 0, unfinished);
    report.check('the plan is attributed to the provider that wrote it', plan?.provenance?.provider === 'fake' && plan?.provenance?.model === 'fake-v1', plan?.provenance);
    report.check('the provenance names the pipeline and the evaluator', !!plan?.provenance?.pipeline_version && !!plan?.provenance?.evaluator_version, plan?.provenance);
    report.check('the run happened in a throwaway environment of its own', /^ai_course_/.test(String(first.json?.evidence?.scratch_database ?? '')), first.json?.evidence?.scratch_database);

    report.section('clause 2 — every exercise was executed and cross-checked before activation');
    const planIDs = ids(planExercises(plan));
    const reported = reportedExercises(first.json);
    report.check('the per-exercise report covers exactly the plan\'s exercises', sameSet(planIDs, ids(reported)), { plan: planIDs, report: ids(reported) });
    const notVerified = reported.filter(({ exercise }) => exercise.status !== 'verified').map(({ exercise }) => `${exercise.id}:${exercise.status}`);
    report.check('every exercise is reported verified', notVerified.length === 0, notVerified);
    const noRows = reported.filter(({ exercise }) => !(exercise.reference_rows > 0)).map(({ exercise }) => exercise.id);
    report.check('every exercise measured a non-empty reference result (reference_rows > 0)', noRows.length === 0, noRows);
    const disagreed = reported.filter(({ exercise }) => exercise.cross_check_rows !== exercise.reference_rows)
      .map(({ exercise }) => `${exercise.id}: reference ${exercise.reference_rows} vs cross-check ${exercise.cross_check_rows}`);
    report.check('every cross-check measured the same number of rows as its reference', disagreed.length === 0, disagreed);
    const unrunFixtures = reported.filter(({ exercise }) => !(exercise.fixtures_total > 0) || exercise.fixtures_rejected !== exercise.fixtures_total)
      .map(({ exercise }) => `${exercise.id}: ${exercise.fixtures_rejected}/${exercise.fixtures_total}`);
    report.check('the adversarial fixtures ran for every exercise and rejected every wrong query', unrunFixtures.length === 0, unrunFixtures);
    const measured = reported.reduce((n, { exercise }) => n + exercise.reference_rows, 0);
    report.check('the outcome\'s own evidence totals the rows the exercises measured', first.json?.evidence?.reference_rows === measured, { evidence: first.json?.evidence?.reference_rows, measured });

    /* ---------- clause 3: retrieval ---------- */
    const got = await getJSON(`${gw.base}/courses/${encodeURIComponent(plan?.id ?? '')}`, 15_000, headers);
    /* Every JSON route answers JSON whatever it decides (200, 401, 404, 503); a
       path with no registered pattern falls through to the SPA, which answers
       200 text/html. So a body that is not JSON is what says "this build has no
       such route" — checked against a live request rather than assumed from the
       source tree, because the route can land between reading it and running. */
    const html = /^\s*(<!doctype|<html)/i.test(String(got.body ?? ''));
    const routeAbsent = got.status === 405 || got.json == null;
    if (routeAbsent) {
      report.section(`clause 3 — the activated plan must be retrievable, and this build does not serve it (HTTP ${got.status}${html ? ' text/html, the SPA fallback' : ''})`);
      report.check('GET /courses/{id} is served by this build', false,
        `HTTP ${got.status} from GET /courses/<id>, body ${JSON.stringify(String(got.body ?? '').slice(0, 60))} — this audit requires the route, so it fails rather than skipping`);
    } else {
      report.section('clause 3 — the plan is retrievable after the fact');
      const stored = got.json?.course ?? null;
      report.check('GET /courses/{id} answers 200 for the plan just activated', got.status === 200, `HTTP ${got.status}`);
      report.check('the route answers with a stored course', got.json?.ok === true && !!stored, JSON.stringify(got.json).slice(0, 120));
      report.check('the stored course is the plan that was activated', stored?.plan?.id === plan?.id, stored?.plan?.id);
      report.check('the stored record keeps its chapters and exercises', (stored?.plan?.chapters ?? []).length === (plan?.chapters ?? []).length && sameSet(planIDs, ids(planExercises(stored?.plan))), (stored?.plan?.chapters ?? []).length);
      report.check('the stored plan keeps the measured rows', planExercises(stored?.plan).every(({ exercise }) => (exercise.expected?.rows ?? []).length > 0), planExercises(stored?.plan).map(({ exercise }) => `${exercise.id}:${(exercise.expected?.rows ?? []).length}`));
      report.check('the stored record keeps the outcome that admitted it', (stored?.outcome?.stages ?? []).length > 0, stored?.outcome?.stages);

      /* Ownership is enforced per request, not per URL: a second learner who has
         just signed in must be told the course does not exist (404, never 403,
         so its existence is not confirmed), and must not see it listed either. */
      const otherHeaders = auth(await signup(gw.base, `course-gen-audit-other-${process.pid}@querycraft.local`));
      const foreign = await getJSON(`${gw.base}/courses/${encodeURIComponent(plan?.id ?? '')}`, 15_000, otherHeaders);
      report.check('a different signed-in account gets 404 for the same course id', foreign.status === 404 && foreign.json?.error?.code === 'NOT_FOUND',
        `HTTP ${foreign.status} ${JSON.stringify(foreign.json).slice(0, 120)}`);
      report.check('the 404 carries no course for the other account', foreign.json?.course == null, JSON.stringify(foreign.json).slice(0, 120));
      const listed = await getJSON(`${gw.base}/courses`, 15_000, headers);
      report.check('the generator\'s own GET /courses contains the activated plan',
        listed.status === 200 && (listed.json?.courses ?? []).some((rec) => rec?.plan?.id === plan?.id),
        { status: listed.status, ids: (listed.json?.courses ?? []).map((rec) => rec?.plan?.id) });
      const foreignList = await getJSON(`${gw.base}/courses`, 15_000, otherHeaders);
      report.check('the other account\'s GET /courses does not contain it',
        !(foreignList.json?.courses ?? []).some((rec) => rec?.plan?.id === plan?.id),
        { status: foreignList.status, ids: (foreignList.json?.courses ?? []).map((rec) => rec?.plan?.id) });
    }

    /* ---------- clause 4: determinism ---------- */
    report.section('clause 4 — the same request activates again (the provider is a pure function of the request)');
    const secondStart = Date.now();
    const second = await postJSON(`${gw.base}/courses/generate`, COURSE_REQUEST, REQUEST_TIMEOUT_MS, headers);
    const secondMS = Date.now() - secondStart;
    console.log(`  ..   HTTP ${second.status} in ${secondMS} ms, status=${second.json?.status}`);
    report.check('the second run is active too', second.json?.status === 'active', String(second.json?.status));
    report.check('both runs produced the same course id', second.json?.plan?.id === plan?.id, { first: plan?.id, second: second.json?.plan?.id });
    report.check('both runs produced the same exercises', sameSet(planIDs, ids(planExercises(second.json?.plan))), ids(planExercises(second.json?.plan)));
    report.check('both runs completed every pipeline stage', COURSE_STAGES.every((name) => stageStates(second.json).get(name) === 'done'));
    table.push(runRow('run 1 (same request)', 'fake', firstMS, first.json));
    table.push(runRow('run 2 (same request)', 'fake', secondMS, second.json));

    /* ---------- clauses 5, 6 and 7: the stub provider, speaking the tool protocol ---------- */

    /**
     * bootStubGateway starts a fresh stub provider and a gateway pointed at it.
     * The provider is chosen at boot, so each clause gets its own gateway; each
     * clause also gets its own course title, which is what names the stored
     * course, so one clause never reads another clause's record.
     */
    const bootStubGateway = async (tag, options) => {
      const provider = await startStubProvider(options);
      /* A cluster of its own per clause: the previous gateway's embedded
         PostgreSQL is still shutting down when the next one starts, and two
         servers may not share a data directory. */
      const cluster = await liveEnv(tag);
      const gateway = await startGateway(bin, {
        ...cluster,
        QC_AI_PROVIDER: 'openai-compatible',
        QC_AI_BASE_URL: provider.base,
        QC_AI_MODEL: 'stub-v1',
        QC_AI_KEY: 'course-gen-audit-key',
      });
      return { provider, gateway };
    };
    const stopStub = async () => {
      if (!stub) return;
      const closing = stub;
      stub = null;
      await closing.stop();
    };
    const leaveStubGateway = async () => {
      if (gw) { stopGateway(gw.child); gw = null; }
      await stopStub();
      await new Promise((r) => setTimeout(r, 500));
    };

    /* ---------- clause 5: the streaming promise ---------- */
    stopGateway(gw.child);
    gw = null;
    await new Promise((r) => setTimeout(r, 500));
    {
      const boot = await bootStubGateway('course-gen-stream', { title: 'A course written one chapter at a time', delayMs: 300 });
      stub = boot.provider;
      gw = boot.gateway;
      const headers = auth(await signup(gw.base, `course-gen-audit-stream-${process.pid}@querycraft.local`));

      const stubCaps = await getJSON(`${gw.base}/capabilities`);
      report.section('clause 5 — the course is readable while later chapters are still being written');
      report.check('the stubbed provider is the one wired in', stubCaps.json?.ai?.provider === 'openai-compatible' && stubCaps.json?.ai?.model === 'stub-v1', JSON.stringify(stubCaps.json?.ai));

      const streamStart = Date.now();
      const submitted = await postJSON(`${gw.base}/courses/generate?async=1`, COURSE_REQUEST, REQUEST_TIMEOUT_MS, headers);
      report.check('the streaming run is accepted as a job', submitted.status === 202 && !!submitted.json?.job?.id, `HTTP ${submitted.status} ${String(submitted.body).slice(0, 160)}`);

      /* Poll the way a client does, and keep the first view that says a chapter
         verified together with the record it names. That pair is the mid-run
         read: the chapter is on disk while the later ones are not. */
      let midView = null;
      let midCourse = null;
      let sawCurrent = null;
      let lastView = null;
      const deadline = Date.now() + 90_000;
      for (;;) {
        const poll = await getJSON(`${gw.base}/generation/${encodeURIComponent(submitted.json?.job?.id ?? '')}`, 15_000, headers);
        const job = poll.json?.job ?? null;
        if (!job) break;
        lastView = job;
        if (job.status !== 'running') break;
        if (job.current_chapter) sawCurrent = job.current_chapter;
        if ((job.chapters_verified ?? 0) > 0 && job.course_id && !midCourse) {
          midView = job;
          midCourse = await getJSON(`${gw.base}/courses/${encodeURIComponent(job.course_id)}`, 15_000, headers);
        }
        if (Date.now() > deadline) break;
        await new Promise((r) => setTimeout(r, 5));
      }
      const streamMS = Date.now() - streamStart;

      const planned = STUB_CHAPTERS.length;
      report.check('a poll saw the chapter the gateway was working on', STUB_CHAPTERS.some((ch) => ch.id === sawCurrent), sawCurrent);
      report.check('the job reported how many chapters the outline planned', midView?.chapters_planned === planned, midView?.chapters_planned);
      report.check('the job reported the chapters that had already verified', (midView?.chapters_verified ?? 0) > 0, midView?.chapters_verified);
      report.check('the mid-run view named the record it was building', !!midView?.course_id, midView?.course_id);
      report.check('GET /courses/{id} answered while the run was still going', midCourse?.status === 200, `HTTP ${midCourse?.status} ${String(midCourse?.body ?? '').slice(0, 120)}`);
      const building = midCourse?.json?.course ?? null;
      report.check('the record read mid-run is a building course', building?.status === 'building', building?.status);
      report.check('the building course already carries the whole planned shape', (building?.plan?.chapters ?? []).length === planned, (building?.plan?.chapters ?? []).length);
      const written = (building?.plan?.chapters ?? []).filter((ch) => (ch.sections ?? []).length > 0);
      report.check('the building course carries content for exactly the chapters that verified', written.length === midView?.chapters_verified, { written: written.map((ch) => ch.id), verified: midView?.chapters_verified });
      report.check('every verified chapter of the building course carries an exercise with measured rows',
        written.length > 0 && written.every((ch) => (ch.exercises ?? []).length > 0 && ch.exercises.every((ex) => (ex.expected?.rows ?? []).length > 0)),
        written.map((ch) => `${ch.id}:${(ch.exercises ?? []).length}`));
      report.check('a later chapter had not been written when the record was read', (midView?.chapters_verified ?? 0) < (midView?.chapters_planned ?? 0), { verified: midView?.chapters_verified, planned: midView?.chapters_planned });

      report.check('the run finishes active', lastView?.status === 'active', `${lastView?.status} ${redactSafe(lastView?.error)}`);
      report.check('the finished course is the record the poll was reading', lastView?.course_id === midView?.course_id, { mid: midView?.course_id, final: lastView?.course_id });
      const verifiedChapters = (lastView?.chapters ?? []).filter((ch) => ch.status === 'verified');
      report.check('every planned chapter of the finished course verified', verifiedChapters.length === planned, (lastView?.chapters ?? []).map((ch) => `${ch.id}:${ch.status}`));
      const finished = await getJSON(`${gw.base}/courses/${encodeURIComponent(lastView?.course_id ?? '')}`, 15_000, headers);
      report.check('the activated course reads back with every chapter', finished.json?.course?.status === 'active' && (finished.json?.course?.plan?.chapters ?? []).length === planned, finished.json?.course?.status);

      report.check('the first call asked for the outline without tools (the strict schema path)', stub.calls[0]?.hasTools === false, stub.calls[0]?.hasTools);
      report.check('the chapter conversation never sends tools together with response_format', stub.calls.filter((c) => c.hasTools).length > 0 && stub.calls.filter((c) => c.hasTools).every((c) => c.hasResponseFormat === false), stub.calls.map((c) => ({ tools: c.hasTools, schema: c.hasResponseFormat })));
      report.check('the conversation was the outline, one call per chapter and the close', stub.calls.length === planned + 2, stub.calls.map((c) => c.replyName));
      table.push(runRow('run 3 (stub, streaming)', 'stub', streamMS, lastView));

      await leaveStubGateway();
    }

    /* ---------- clause 6: a chapter that cannot be repaired ---------- */
    {
      const boot = await bootStubGateway('course-gen-drop', { title: 'A course with one chapter that will not run', breakChapters: [BROKEN_CHAPTER_ID] });
      stub = boot.provider;
      gw = boot.gateway;
      const headers = auth(await signup(gw.base, `course-gen-audit-drop-${process.pid}@querycraft.local`));

      report.section('clause 6 — a chapter that cannot be repaired is dropped, and the course keeps the ones that ran');

      /* The negative is only meaningful if the two solutions the stub serves
         really do disagree; the audit establishes that against the engine itself
         rather than trusting the plan it wrote. */
      const refRun = await postJSON(`${gw.base}/query`, { sql: REFERENCE, dialect: 'postgresql', dataset: 'commerce' }, 60_000, headers);
      const crossRun = await postJSON(`${gw.base}/query`, { sql: DISAGREEING_CROSS_CHECK, dialect: 'postgresql', dataset: 'commerce' }, 60_000, headers);
      report.check('the stub\'s two solutions really do disagree (the negative is not vacuous)',
        refRun.json?.status === 'succeeded' && crossRun.json?.status === 'succeeded' && refRun.json?.row_count > 0 && refRun.json?.row_count !== crossRun.json?.row_count,
        { reference: { status: refRun.json?.status, rows: refRun.json?.row_count }, crossCheck: { status: crossRun.json?.status, rows: crossRun.json?.row_count } });

      const dropStart = Date.now();
      const bad = await postJSON(`${gw.base}/courses/generate`, COURSE_REQUEST, REQUEST_TIMEOUT_MS, headers);
      const badMS = Date.now() - dropStart;
      console.log(`  ..   HTTP ${bad.status} in ${badMS} ms, status=${bad.json?.status}`);
      if (bad.json?.error) console.log(`  ..   error: ${bad.json.error.code} @ ${bad.json.error.stage ?? '-'} — ${bad.json.error.message}`);

      report.check('the run activates: the chapters that verified are a course', bad.json?.status === 'active', `${bad.json?.status} ${redactSafe(bad.json?.error)}`);
      const kept = bad.json?.plan?.chapters ?? [];
      report.check('the stored plan is only the chapter that verified', kept.length === 1 && kept[0]?.id === STUB_CHAPTERS[0].id, kept.map((ch) => ch.id));
      report.check('the kept chapter carries its exercise and the engine\'s measured rows', (kept[0]?.exercises ?? []).length > 0 && kept[0].exercises.every((ex) => (ex.expected?.rows ?? []).length > 0), kept[0]?.exercises?.map((ex) => ex.id));
      report.check('the stored course contains no unverified exercise', kept.every((ch) => (ch.exercises ?? []).every((ex) => ex.status === undefined || ex.status === 'verified')), kept.map((ch) => ch.id));

      const report6 = bad.json?.chapters ?? [];
      report.check('the report still covers both planned chapters', report6.length === STUB_CHAPTERS.length, report6.map((ch) => `${ch.id}:${ch.status}`));
      report.check('the first chapter is reported verified', report6[0]?.status === 'verified', report6[0]?.status);
      const droppedChapter = report6.find((ch) => ch.id === BROKEN_CHAPTER_ID) ?? null;
      report.check('the chapter that could not be repaired is reported dropped, not hidden', droppedChapter?.status === 'dropped', droppedChapter?.status);
      report.check('the drop names the stage the engine refused at', droppedChapter?.error?.stage === 'cross_check', droppedChapter?.error);
      const failing = (droppedChapter?.exercises ?? []).find((ex) => ex.id === BROKEN_EXERCISE_ID) ?? null;
      report.check('the dropped chapter names the failing exercise', !!failing, (droppedChapter?.exercises ?? []).map((ex) => ex.id));
      report.check('the failing exercise is reported failed at the cross-check stage', failing?.status === 'failed' && failing?.error?.stage === 'cross_check', failing);

      const ch2Writes = stub.calls.filter((c) => c.replyName === 'save_chapter' && c.replyChapter === BROKEN_CHAPTER_ID);
      report.check('the model was asked for that chapter once and to repair it twice, then the loop stopped', ch2Writes.length === 3, ch2Writes.map((c) => c.replyChapter));
      const repairs = ch2Writes.slice(1);
      report.check('every repair pass carried the engine\'s own message about the failing exercise',
        repairs.length === 2 && repairs.every((c) => c.toolResults.some((t) => t.includes(BROKEN_EXERCISE_ID) && t.includes('"stage":"cross_check"') && t.includes('"status":"refused"'))),
        repairs.map((c) => c.toolResults.filter((t) => t.includes(BROKEN_EXERCISE_ID)).length));
      report.check('the model was told the chapter was dropped rather than left guessing',
        stub.calls.some((c) => c.toolResults.some((t) => t.includes('"status":"dropped"') && t.includes(BROKEN_CHAPTER_ID))),
        stub.calls.map((c) => c.replyName));

      const stored = await getJSON(`${gw.base}/courses/${encodeURIComponent(bad.json?.plan?.id ?? '')}`, 15_000, headers);
      report.check('the active record reads back with the kept chapter only', stored.json?.course?.status === 'active' && (stored.json?.course?.plan?.chapters ?? []).length === 1, stored.json?.course?.status);

      table.push(runRow('run 4 (stub, one chapter dropped)', 'stub', badMS, bad.json));
      await leaveStubGateway();
    }

    /* ---------- clause 7: a run in which nothing verifies ---------- */
    {
      const boot = await bootStubGateway('course-gen-refuse', { title: 'A course that never verifies', breakChapters: STUB_CHAPTERS.map((ch) => ch.id) });
      stub = boot.provider;
      gw = boot.gateway;
      const headers = auth(await signup(gw.base, `course-gen-audit-refuse-${process.pid}@querycraft.local`));

      report.section('clause 7 — a course in which no chapter verifies is refused and stores nothing');
      const refuseStart = Date.now();
      const refused = await postJSON(`${gw.base}/courses/generate`, COURSE_REQUEST, REQUEST_TIMEOUT_MS, headers);
      const refuseMS = Date.now() - refuseStart;
      console.log(`  ..   HTTP ${refused.status} in ${refuseMS} ms, status=${refused.json?.status}`);
      if (refused.json?.error) console.log(`  ..   error: ${refused.json.error.code} @ ${refused.json.error.stage ?? '-'} — ${refused.json.error.message}`);

      report.check('the run is rejected, not activated', refused.json?.status === 'rejected', refused.json?.status);
      report.check('no plan is returned for a refusal', refused.json?.plan == null, JSON.stringify(refused.json?.plan ?? null).slice(0, 120));
      report.check('the refusal names the engine stage it refused at', refused.json?.error?.stage === 'cross_check' && refused.json?.error?.code === 'AI_CROSS_CHECK_FAILED', refused.json?.error);
      report.check('the refusal says the course was refused because nothing verified', String(refused.json?.error?.message ?? '').includes('No chapter verified'), refused.json?.error?.message);
      report.check('every planned chapter is reported dropped', (refused.json?.chapters ?? []).length === STUB_CHAPTERS.length && (refused.json?.chapters ?? []).every((ch) => ch.status === 'dropped'), (refused.json?.chapters ?? []).map((ch) => `${ch.id}:${ch.status}`));

      /* The row the outline wrote is the point: a refusal has to remove it, or
         a learner is offered a course with no chapters in it. */
      const listed = await getJSON(`${gw.base}/courses`, 15_000, headers);
      report.check('the outline\'s building row was removed: GET /courses lists nothing',
        listed.status === 200 && (listed.json?.courses ?? []).length === 0,
        (listed.json?.courses ?? []).map((rec) => `${rec?.plan?.id}:${rec?.status}`));

      table.push(runRow('run 5 (stub, nothing verified)', 'stub', refuseMS, refused.json));
      await leaveStubGateway();
    }

    /* ---------- the Go unit level: the same behaviour, driven from inside the package ---------- */
    report.section('the two unit-level cases, pinned in Go and run here as well');
    console.log('         TestACourseIsStoredWhileItsLaterChaptersAreStillBeingWritten drives the chapter loop');
    console.log('         from a scripted provider and reads the store from the running service: after the first');
    console.log('         chapter verifies the course is readable under "building" while the next one is still');
    console.log('         being handed in, and the finished record is the same id.');
    console.log('         TestACourseWithNoVerifiedChapterIsRefusedAndNothingIsStored asserts that a run in');
    console.log('         which every chapter fails is refused, returns no plan, and leaves no record behind.');
    console.log('         Both run with -count=1, so the PASS printed below is this audit\'s own run.');
    const unitTests = 'TestACourseWithNoVerifiedChapterIsRefusedAndNothingIsStored|TestACourseIsStoredWhileItsLaterChaptersAreStillBeingWritten';
    const unit = runGoTest(unitTests);
    report.check(`go test ./internal/ai/ -run '${unitTests}' -v -count=1 exits 0`, unit.status === 0,
      { status: unit.status, error: unit.error ? String(unit.error.message || unit.error) : undefined, tail: unit.tail });
    report.check('both Go tests reported PASS, so this check did not pass by finding nothing',
      /--- PASS: TestACourseIsStoredWhileItsLaterChaptersAreStillBeingWritten\b/.test(unit.text)
      && /--- PASS: TestACourseWithNoVerifiedChapterIsRefusedAndNothingIsStored\b/.test(unit.text), unit.tail);
    /* Only the verdict lines: `go test -v` also echoes the test PostgreSQL's
       shutdown log, which is not a result and would bury the one that matters. */
    const verdict = unit.text.split('\n').map((line) => line.trim())
      .filter((line) => /^(--- PASS|--- FAIL|PASS$|FAIL$|ok\s|FAIL\s)/.test(line));
    for (const line of verdict) console.log(`  ..   ${line}`);

    report.section('every run, as measured');
    console.log(report.markdownTable(table[0]));
    for (const row of table.slice(1)) console.log(`| ${row.join(' | ')} |`);
    report.check('five runs were measured', table.length === 6, table.length - 1);
  } finally {
    if (gw) stopGateway(gw.child);
    if (stub) await stub.stop();
  }

  if (report.failed) console.log('\ncourse-gen-audit: FAIL');
  report.finish();
}

main().catch((err) => {
  console.error(`course-gen-audit: FAILED — ${err?.message ?? err}`);
  process.exitCode = 1;
});
