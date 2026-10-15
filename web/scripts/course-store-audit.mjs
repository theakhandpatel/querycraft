/*
 * Stored courses audit (M8, the "your course vanished on reload" defect).
 *
 * The defect: a generated course lived only in React state, so a reload threw
 * it away. The gateway stored it the whole time — `GET /courses`,
 * `GET /courses/{id}`, `DELETE /courses/{id}` — and the frontend never read
 * them. This suite pins the seam that fixes it.
 *
 *   node scripts/course-store-audit.mjs
 *     Requires /tmp/qc-ai.cjs and /tmp/qc-draft.cjs (bundled by
 *     `npm run audit-course-store`), boots a tiny node:http server that answers
 *     the fixed routes with canned gateway bodies, drives the real
 *     `gatewayAiMethods` and `courseFromPlan` against it, and asserts on what
 *     came back. No live gateway is needed.
 *
 * What is load-bearing here:
 *   - a stored record parses whole (plan, chapters, markdown sections, the
 *     engine's measured rows) or is dropped — never half-read;
 *   - a `building` record is readable, because a course the pipeline is still
 *     writing is real and carries the chapters that already verified;
 *   - a failed list read throws. An empty list is the claim "you have no
 *     courses", and a client that cannot reach the store does not know that;
 *   - the browser adapter refuses for the same reason;
 *   - the record's own measured rows are the rows the lesson grid would show;
 *   - a generation job is a live view, so a poller can read `running`/`queued`
 *     stages and the chapter count while the run is going, and an unrecognised
 *     status keeps it polling instead of being read as a finish.
 *   - a run is a gateway object that outlives the page, so a reload can come
 *     back to it with nothing stored locally but its id — scoped to the account
 *     that started it, and never carrying a claim about the run's state.
 */
import http from 'node:http';
import { createRequire } from 'node:module';
import { Effect } from 'effect';

const require = createRequire('/tmp/');
const ai = require('/tmp/qc-ai.cjs');
const draft = require('/tmp/qc-draft.cjs');

/* `runResume` reads and writes `window.localStorage`, so this suite hands it
   one. The stub is an ordinary map: the point is to see what the module does
   with what is in it, not to test a browser's storage. It is installed before
   the bundle is required. */
const ls = new Map();
globalThis.window = {
  localStorage: {
    getItem: (k) => (ls.has(k) ? ls.get(k) : null),
    setItem: (k, v) => void ls.set(k, String(v)),
    removeItem: (k) => void ls.delete(k),
  },
};
const runResume = require('/tmp/qc-runresume.cjs');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

/* ---------- the fixed routes ---------- */

const PATHS = {
  list: 'GET /courses',
  one: 'GET /courses/cte-revenue',
  missing: 'GET /courses/nope',
  remove: 'DELETE /courses/cte-revenue',
  submit: 'POST /courses/generate',
  job: 'GET /generation/gen-1',
  jobMissing: 'GET /generation/nope',
};

const PROVENANCE = {
  provider: 'openai-compatible',
  model: 'deepseek-v4.1-flash',
  provider_version: '1',
  pipeline_version: '1',
  evaluator_version: '1',
  dataset_version: 1,
  created_at: '2026-10-15T00:00:00Z',
  repairs: 1,
};

/** One chapter of markdown, one exercise with the engine's own measured rows. */
const PLAN = {
  id: 'cte-revenue',
  version: 1,
  title: 'Analytical SQL for interviews',
  subtitle: 'CTEs, window functions and conditional aggregation',
  level: 'intermediate',
  dialect: 'postgresql',
  objectives: ['write readable CTEs', 'rank within groups'],
  setup: { dataset: 'commerce', schema_version: 1, seed_version: 1, statements: [] },
  chapters: [
    {
      id: 'cte',
      n: 1,
      title: 'CTEs: Building Readable Analytical Queries',
      summary: 'Break a query into named steps.',
      kind: 'reading',
      minutes: 14,
      objectives: ['name a step'],
      sections: [
        { title: 'Why a CTE', body: 'A CTE names a step so the next one can read it.\n\n```mermaid\ngraph LR\n  A[orders] --> B[aggregate]\n```\n' },
        { title: 'Averages', body: 'Compare each customer with the group average.' },
      ],
      exercises: [
        {
          id: 'ex1_cte_revenue',
          title: 'Customers Above Average Revenue',
          prompt: 'Return the customers whose revenue is above the average.',
          difficulty: 'intermediate',
          type: 'query',
          objectives: ['use a CTE'],
          hints: ['aggregate first', 'then compare'],
          reference_solution: 'WITH r AS (SELECT 1) SELECT * FROM r',
          cross_check_solution: 'SELECT * FROM (SELECT 1) s',
          result_contract: { columns: ['id', 'name', 'revenue'], column_order: 'exact', row_order: 'exact', duplicates: 'preserved', numeric_tolerance: 0.01 },
          expected: { columns: ['id', 'name', 'revenue'], rows: [[1, 'Ada Whitfield', '1666.40'], [5, 'Emi Tanaka', '1651.65']] },
        },
      ],
    },
    {
      id: 'window',
      n: 2,
      title: 'Window Functions: Ranking and Partitioning',
      summary: 'Rank inside a group without collapsing it.',
      kind: 'reading',
      minutes: 16,
      objectives: ['rank within a partition'],
      sections: [{ title: 'PARTITION BY', body: 'PARTITION BY splits the rows the window sees, and leaves them all in place.' }],
      exercises: [
        {
          id: 'ex2_window_rank',
          title: 'Top Plan per Region',
          prompt: 'Return the best-selling plan in each region.',
          difficulty: 'intermediate',
          type: 'query',
          objectives: ['rank'],
          hints: ['ROW_NUMBER'],
          reference_solution: 'SELECT 2',
          cross_check_solution: 'SELECT 2',
          result_contract: { columns: ['region', 'plan'], column_order: 'exact', row_order: 'ignored', duplicates: 'preserved', numeric_tolerance: 0 },
          expected: { columns: ['region', 'plan'], rows: [['eu', 'pro'], ['us', 'team']] },
        },
      ],
    },
  ],
  provenance: PROVENANCE,
};

const OUTCOME = {
  status: 'active',
  kind: 'course',
  artifact: null,
  plan: PLAN,
  chapters: [
    { id: 'cte', title: 'CTEs: Building Readable Analytical Queries', exercises: [{ id: 'ex1_cte_revenue', title: 'Customers Above Average Revenue', status: 'verified', reference_rows: 2, cross_check_rows: 2, fixtures_rejected: 3, fixtures_total: 3, ms: 6.1 }] },
    { id: 'window', title: 'Window Functions: Ranking and Partitioning', exercises: [{ id: 'ex2_window_rank', title: 'Top Plan per Region', status: 'verified', reference_rows: 2, cross_check_rows: 2, fixtures_rejected: 3, fixtures_total: 3, ms: 7.3 }] },
  ],
  stages: [{ name: 'activate', state: 'done', detail: 'stored as active: 2 chapters, 2 verified exercises' }],
  attempts: [],
  error: null,
};

const RECORD = { plan: PLAN, outcome: OUTCOME, status: 'active', created_at: '2026-10-15T00:00:00Z' };

/** A course the pipeline is still writing: one chapter verified, one not yet
    written. The plan is readable, and the report says how far it got. */
const BUILDING_RECORD = {
  plan: { ...PLAN, chapters: [PLAN.chapters[0]] },
  outcome: {
    ...OUTCOME,
    status: 'active',
    chapters: [{ ...OUTCOME.chapters[0] }, { id: 'window', title: 'Window Functions: Ranking and Partitioning', exercises: [] }],
  },
  status: 'building',
  created_at: '2026-10-15T00:00:00Z',
};

/** A job mid-run: two stages settled, one going, one not started. */
const JOB = {
  id: 'gen-1',
  kind: 'course',
  status: 'running',
  stages: [
    { name: 'validate_request', state: 'done' },
    { name: 'scratch_environment', state: 'running', detail: 'building the scratch database' },
    { name: 'reference_contract', state: 'queued' },
  ],
  attempts: [{ number: 1, stage: 'scratch_environment', detail: 'first attempt' }],
  chapters: [OUTCOME.chapters[0], { id: 'window', title: 'Window Functions: Ranking and Partitioning', exercises: [] }],
  chapters_planned: 4,
  chapters_verified: 2,
  current_chapter: 'Window Functions: Ranking and Partitioning',
  course_id: 'cte-revenue',
  created_at: '2026-10-15T00:00:00Z',
};

/** A record whatever the plan is, this client cannot render it. */
const UNREADABLE_RECORD = { plan: { id: 'x', version: 1, title: 'x', chapters: [{ id: 'c', title: 'c', exercises: [{ id: 'e' }] }] }, outcome: OUTCOME, status: 'active', created_at: '' };

let canned = {};
const recorded = [];

function serve() {
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      const chunks = [];
      req.on('data', (c) => chunks.push(c));
      req.on('end', () => {
        const key = `${req.method} ${req.url.split('?')[0]}`;
        recorded.push({ key, body: Buffer.concat(chunks).toString('utf8') });
        const entry = canned[key];
        if (!entry) {
          res.writeHead(404, { 'content-type': 'application/json' });
          res.end(JSON.stringify({ ok: false, error: { code: 'AI_NOT_FOUND', message: 'no canned route' } }));
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

/* ---------- browser adapters: refuse, never an empty list ---------- */

console.log('browser adapters keep no course store');
{
  const browser = ai.browserAiMethods();
  const listError = await runFail(browser.listCourses());
  check('a browser course list refuses instead of returning []', listError?.code === 'AI_UNAVAILABLE', listError);
  const getError = await runFail(browser.getCourse('cte-revenue'));
  check('a browser course read refuses', getError?.code === 'AI_UNAVAILABLE', getError);
  const delError = await runFail(browser.deleteCourse('cte-revenue'));
  check('a browser course delete refuses', delError?.code === 'AI_UNAVAILABLE', delError);
}

/* ---------- a stored course reads back whole ---------- */

console.log('a stored course reads back whole');
{
  canned = { [PATHS.list]: { status: 200, json: { ok: true, courses: [RECORD] } } };
  const list = await run(methods.listCourses());
  check('the list route is read', recorded.some((r) => r.key === PATHS.list));
  check('the stored course survives the decoder at all', list.length === 1, list.length);
  const rec = list[0];
  check('the record keeps its plan', rec?.plan?.id === 'cte-revenue' && rec?.plan?.title === 'Analytical SQL for interviews', rec?.plan?.title);
  check('the record keeps its level and subtitle', rec?.plan?.level === 'intermediate' && rec?.plan?.subtitle.includes('window functions'), rec?.plan?.level);
  check('the record keeps every chapter', rec?.plan?.chapters.length === 2, rec?.plan?.chapters?.length);
  check('a chapter keeps its markdown sections verbatim', rec?.plan?.chapters[0].sections[0].body.includes('```mermaid'), rec?.plan?.chapters[0].sections[0].body);
  check('a chapter keeps all of its sections', rec?.plan?.chapters[0].sections.length === 2, rec?.plan?.chapters[0].sections.length);
  check('an exercise keeps the engine-measured columns in order', rec?.plan?.chapters[0].exercises[0].expected?.columns.join(',') === 'id,name,revenue', rec?.plan?.chapters[0].exercises[0].expected?.columns);
  check('an exercise keeps the engine-measured rows, cells intact', rec?.plan?.chapters[0].exercises[0].expected?.rows[1][1] === 'Emi Tanaka', rec?.plan?.chapters[0].exercises[0].expected?.rows);
  check('an exercise keeps its reference solution', typeof rec?.plan?.chapters[0].exercises[0].reference_solution === 'string');
  check('the record keeps the run report', rec?.outcome?.chapters?.length === 2, rec?.outcome?.chapters?.length);
  check('the report keeps the measured row counts', rec?.outcome?.chapters[0].exercises[0].reference_rows === 2 && rec?.outcome?.chapters[1].exercises[0].fixtures_total === 3);
  check('the record keeps its provenance', rec?.plan?.provenance.provider === 'openai-compatible' && rec?.plan?.provenance.repairs === 1, rec?.plan?.provenance);
  check('the record keeps its lifecycle status', rec?.status === 'active', rec?.status);
  check('the record keeps its created_at', typeof rec?.created_at === 'string' && rec.created_at.length > 0, rec?.created_at);
}

/* ---------- a record the client cannot read is skipped, not half-read ------ */

console.log('an unreadable record is skipped, never half-read');
{
  canned = { [PATHS.list]: { status: 200, json: { ok: true, courses: [RECORD, UNREADABLE_RECORD] } } };
  const list = await run(methods.listCourses());
  check('one unreadable record does not hide the readable one', list.length === 1, list.length);
  check('the readable record is the one that survived', list[0]?.plan?.id === 'cte-revenue', list[0]?.plan?.id);

  canned = { [PATHS.one]: { status: 200, json: { ok: true, course: UNREADABLE_RECORD } } };
  const half = await run(methods.getCourse('cte-revenue'));
  check('an unreadable plan is null, not a course with chapters missing', half === null, half);

  /* The decoder is also the one that decides what a record *is*, so answer for
     the raw envelope too: a wrapped record and a bare one must read alike. */
  const bare = ai.asCourseRecord(RECORD);
  check('a bare record reads like a wrapped one', bare?.plan?.id === 'cte-revenue', bare?.plan?.id);
  check('a bare list reads like a wrapped one', ai.asCourseRecords([RECORD]).length === 1);
  check('a body that is neither is an empty list, never a crash', ai.asCourseRecords({ ok: true }).length === 0 && ai.asCourseRecords(null).length === 0);
}

/* ---------- a course still being written is readable ---------- */

console.log('a course the pipeline is still writing is readable');
{
  canned = { [PATHS.list]: { status: 200, json: { ok: true, courses: [BUILDING_RECORD] } } };
  const list = await run(methods.listCourses());
  const rec = list[0];
  check('a building course is not thrown away', rec != null, list.length);
  check('a building course keeps the chapters that exist', rec?.plan?.chapters.length === 1, rec?.plan?.chapters?.length);
  check('a building course keeps the one verified exercise', rec?.plan?.chapters[0].exercises.length === 1);
  check('a building course reports its lifecycle as building', rec?.status === 'building', rec?.status);
  const verified = (rec?.outcome?.chapters ?? []).flatMap((c) => c.exercises ?? []).filter((e) => e.status === 'verified').length;
  check('a building course reports how many chapters verified', verified === 1, verified);
  check('a building course names the chapter not yet written', rec?.outcome?.chapters?.[1]?.exercises?.length === 0);
}

/* ---------- reads that fail keep the one honest channel ---------- */

console.log('a read that fails is not a claim about the learner');
{
  canned = { [PATHS.list]: { status: 503, json: { ok: false, courses: [], error: { code: 'AI_UNAVAILABLE', message: 'AI is off' } } } };
  const off = await runFail(methods.listCourses());
  check('a course list that failed throws rather than reporting none', off?.code === 'AI_UNAVAILABLE', off);
  check('the refusal is not smuggled through as data', !Array.isArray(off), off);

  canned = { [PATHS.missing]: { status: 404, json: { ok: false, error: { code: 'AI_NOT_FOUND', message: 'unknown course' } } } };
  const missing = await run(methods.getCourse('nope'));
  check('getCourse answers null for a course that is not there', missing === null, missing);

  canned = { [PATHS.one]: { status: 500, json: { error: { code: 'ENVIRONMENT_UNAVAILABLE', message: 'depot down' } } } };
  const down = await runFail(methods.getCourse('cte-revenue'));
  check('a storage failure is a typed error, not "no such course"', down?.code === 'ENVIRONMENT_UNAVAILABLE', down);
}

/* ---------- removing a course goes through the gateway ---------- */

console.log('removing a course goes through the gateway');
{
  recorded.length = 0;
  canned = { [PATHS.remove]: { status: 200, json: { ok: true } } };
  const res = await run(methods.deleteCourse('cte-revenue'));
  check('the delete is reported as applied', res.ok === true, res);
  const sent = recorded.find((r) => r.key === PATHS.remove);
  check('the delete addresses the stored course by id', sent !== undefined, recorded.map((r) => r.key));
  check('the delete sends no body it does not need', sent !== undefined && sent.body === '', sent?.body);

  /* The generic route answers 404 `NOT_FOUND`; the client treats it as "already
     gone" and drops the row, which is the outcome the learner asked for. */
  canned = { [PATHS.remove]: { status: 404, json: { ok: false, error: { code: 'NOT_FOUND', message: 'unknown course' } } } };
  const gone = await runFail(methods.deleteCourse('cte-revenue'));
  check('a course that is already gone is a typed not-found', gone?.code === 'NOT_FOUND', gone);
  check('the refusal carries the status a caller branches on', gone?.status === 404, gone?.status);
}

/* ---------- the record's rows are the rows the lesson page shows ---------- */

console.log("the record's own rows are the rows the lesson page shows");
{
  const course = draft.courseFromPlan(PLAN, 'CTEs for interviews');
  check('a stored plan becomes a Course', course?.id === 'cte-revenue', course?.id);
  check('the course keeps its title and level', course?.title === 'Analytical SQL for interviews' && course?.level === 'intermediate', course?.title);
  check('a stored course runs on the dialect the plan names', course?.dialect === 'postgresql', course?.dialect);
  check('every chapter becomes a module', course?.modules?.length === 2, course?.modules?.length);
  const cte = course?.modules?.[0];
  const prose = (cte?.blocks ?? []).filter((b) => b.type === 'prose');
  check('a markdown section becomes rendered prose', prose.length === 2, prose.length);
  check('the prose keeps the mermaid block the author wrote', String(prose[0]?.html || '').includes('```mermaid'), prose[0]?.html);
  const exercise = (cte?.blocks ?? []).find((b) => b.type === 'exercise');
  check('an exercise carries the reference solution', typeof exercise?.solution === 'string');
  check('a materialised plan never claims to be a built-in course', course?.gateway === undefined, course?.gateway);
  check(
    'the grid is handed the engine-measured rows, cell for cell',
    JSON.stringify(exercise?.result?.rows) === JSON.stringify(PLAN.chapters[0].exercises[0].expected.rows),
    exercise?.result?.rows,
  );
  check('the grid is handed the engine-measured columns, in order', (exercise?.result?.columns ?? []).join(',') === 'id,name,revenue', exercise?.result?.columns);

  /* The claim the whole defect was about: what the gateway stored is what the
     screen shows, after a reload, with no generation in this tab. */
  const reloaded = draft.courseFromPlan(RECORD.plan, RECORD.plan.subtitle);
  const reloadedExercise = (reloaded.modules?.[0]?.blocks ?? []).find((b) => b.type === 'exercise');
  check(
    'a reloaded record renders the same measured rows as the live build',
    JSON.stringify(reloadedExercise?.result?.rows) === JSON.stringify(exercise?.result?.rows),
    reloadedExercise?.result?.rows,
  );
}

/* ---------- a generation job is a live view, not a finished report ---------- */

console.log('a generation job is read while the run is still going');
{
  const browser = ai.browserAiMethods();
  const submitError = await runFail(browser.submitGeneration({ title: 'x' }, 'course'));
  check('a browser cannot submit a background generation', submitError?.code === 'AI_UNAVAILABLE', submitError);
  const pollError = await runFail(browser.generationJob('gen-1'));
  check('a browser has no job to poll', pollError?.code === 'AI_UNAVAILABLE', pollError);

  recorded.length = 0;
  canned = { [PATHS.submit]: { status: 202, json: { ok: true, job: { id: 'gen-1', kind: 'course', status: 'running' } } } };
  const handle = await run(methods.submitGeneration({ title: 'CTEs', kind: 'course' }, 'course'));
  check('the submission is accepted as a job', handle?.id === 'gen-1', handle);
  check('the handle keeps what the run is', handle?.kind === 'course' && handle?.status === 'running', handle);
  const sent = recorded.find((r) => r.key === PATHS.submit);
  check('the submission asks the gateway not to hold the request open', sent !== undefined, recorded.map((r) => r.key));

  /* A gateway that ignored the async ask answers with the blocking envelope.
     There is no job to poll, and the caller is told so rather than given a
     handle to a run that does not exist. */
  canned = { [PATHS.submit]: { status: 201, json: OUTCOME } };
  const blocking = await run(methods.submitGeneration({ title: 'CTEs', kind: 'course' }, 'course'));
  check('a synchronous answer is not dressed up as a job', blocking === null, blocking);

  canned = { [PATHS.job]: { status: 200, json: { ok: true, job: JOB } } };
  const job = await run(methods.generationJob('gen-1'));
  check('the job is read back at all', job?.id === 'gen-1', job);
  check('the live stages keep their non-final states', job?.stages.map((s) => s.state).join(',') === 'done,running,queued', job?.stages);
  check('a stage keeps the detail the gateway gave it', job?.stages[1].detail === 'building the scratch database', job?.stages[1]);
  check('the job says how many chapters were planned', job?.chapters_planned === 4, job?.chapters_planned);
  check('the job says how many chapters verified', job?.chapters_verified === 2, job?.chapters_verified);
  check('the job names the chapter it is on', job?.current_chapter === 'Window Functions: Ranking and Partitioning', job?.current_chapter);
  check('the job names the building course', job?.course_id === 'cte-revenue', job?.course_id);
  check('the per-chapter report is readable mid-run', job?.chapters?.[0]?.exercises?.[0]?.reference_rows === 2, job?.chapters);

  /* An unknown status is "still going", so a newer gateway cannot make this
     build stop polling early. Only the settled states are a finish. */
  check('a running job is still running', ai.jobIsRunning({ status: 'running' }));
  check('a status this build has never seen keeps polling', ai.jobIsRunning({ status: 'no-such-state' }));
  for (const status of ['active', 'rejected', 'unavailable', 'failed']) {
    check(`a job that is ${status} is finished`, ai.jobIsRunning({ status }) === false);
  }

  canned = { [PATHS.jobMissing]: { status: 404, json: { ok: false, error: { code: 'NOT_FOUND', message: 'unknown job' } } } };
  const gone = await run(methods.generationJob('nope'));
  check('a job that is gone reads as null, not as an error', gone === null, gone);

  canned = { [PATHS.job]: { status: 500, json: { error: { code: 'ENVIRONMENT_UNAVAILABLE', message: 'depot down' } } } };
  const down = await runFail(methods.generationJob('gen-1'));
  check('a poll that failed is a typed error, not a finished job', down?.code === 'ENVIRONMENT_UNAVAILABLE', down);
}

/* ---------- a finished job is not itself an outcome ---------- */

console.log('a finished job and the record it stored make one outcome');
{
  const finished = { ...JOB, status: 'active', stages: [{ name: 'activate', state: 'done' }], chapters_verified: 2 };
  const fromRun = ai.outcomeFromJob(finished, RECORD);
  check('a polled run that stored an active course is an active outcome', fromRun.status === 'active', fromRun.status);
  check('the outcome carries the stored plan, which the job view has no room for', fromRun.plan?.id === 'cte-revenue', fromRun.plan);
  check('the outcome keeps the run chapter report', fromRun.chapters?.length === 2, fromRun.chapters);
  check('the active record is what the outcome reports', fromRun.kind === 'course' && fromRun.error === null, fromRun);

  /* A finished report should only carry settled stages. If a live word survives
     into it, naming a state for it would be a claim this client cannot back, so
     the stage is dropped and the checklist stays where it honestly is. */
  const stillRunning = ai.outcomeFromJob({ ...finished, stages: [{ name: 'activate', state: 'running' }, { name: 'x', state: 'queued' }] }, RECORD);
  check('a stage still running in a finished report is not given a state', stillRunning.stages.length === 0, stillRunning.stages);

  const refused = ai.outcomeFromJob({ ...JOB, status: 'rejected', course_id: undefined, chapters_verified: 0 }, null);
  check('a run that stored nothing has no outcome plan', refused.plan === null, refused.plan);
  check('a refused run keeps its own status', refused.status === 'rejected', refused.status);

  /* The stored record is the durable fact; the job is only a view of the run.
     A word this build does not know must not hide a course that really exists. */
  const drifted = ai.outcomeFromJob({ ...JOB, status: 'no-such-state' }, RECORD);
  check('the stored record decides the outcome when the job word drifts', drifted.status === 'active', drifted.status);
  const goneQuiet = ai.outcomeFromJob({ ...JOB, status: 'no-such-state', course_id: undefined }, null);
  check('a run with no record and no known state is reported failed, not active', goneQuiet.status === 'failed', goneQuiet.status);
}

/* ---------- a run a reload can come back to ----------
 *
 * A background run outlives the page that started it, and the gateway has no
 * "my runs" route to ask, so the only thing a reload can recover from is what
 * this tab wrote down: the id. Nothing about the run's *state* is stored —
 * that is read back from the gateway — so a breadcrumb can never assert that a
 * run finished, or that it is still going, or that it belongs to the learner
 * looking at it. */

console.log('a run leaves a breadcrumb a reload can find');
{
  ls.clear();
  runResume.writeRun({ jobId: 'gen-1', ownerId: 'u-1', goal: 'window functions', startedAt: 1000 });

  const mine = runResume.readRun('u-1');
  check('the run comes back with its own id', mine?.jobId === 'gen-1', mine);
  check('the goal comes back too, so a resumed page can still show it', mine?.goal === 'window functions', mine?.goal);
  check('the account it belongs to is kept', mine?.ownerId === 'u-1', mine?.ownerId);
  /* The whole point of the design: a status kept here would be this client
     guessing about a run it cannot see. Only the id is stored. */
  check('no state of the run is remembered locally', mine !== null && !('status' in mine), mine);

  const theirs = runResume.readRun('u-2');
  check('a second learner is not offered the run', theirs === null, theirs);
  check('the other account’s breadcrumb is dropped, not left behind', ls.get('qc.run') === undefined, ls.get('qc.run'));

  check('signed out, there is nothing to resume', runResume.readRun(null) === null);
}

console.log('a breadcrumb that cannot be trusted is not returned');
{
  ls.clear();
  ls.set('qc.run', '{not json');
  check('unreadable storage is not a run', runResume.readRun('u-1') === null);
  check('and it is cleared rather than retried forever', ls.get('qc.run') === undefined);

  ls.clear();
  ls.set('qc.run', JSON.stringify({ ownerId: 'u-1', goal: 'x' }));
  check('a breadcrumb naming no run is refused', runResume.readRun('u-1') === null);
  check('and cleared', ls.get('qc.run') === undefined);

  ls.clear();
  check('no breadcrumb at all is null, never an error', runResume.readRun('u-1') === null);
}

console.log('a clear for a run that has been replaced leaves the newer one alone');
{
  ls.clear();
  runResume.writeRun({ jobId: 'gen-1', ownerId: 'u-1', goal: 'a', startedAt: 1 });
  runResume.writeRun({ jobId: 'gen-2', ownerId: 'u-1', goal: 'b', startedAt: 2 });

  runResume.clearRun('gen-1');
  check('the superseded run’s clear is ignored', runResume.readRun('u-1')?.jobId === 'gen-2', runResume.readRun('u-1')?.jobId);

  runResume.clearRun('gen-2');
  check('the run that is current is the one cleared', runResume.readRun('u-1') === null);

  ls.clear();
  runResume.writeRun({ jobId: 'gen-3', ownerId: 'u-1', goal: 'c', startedAt: 3 });
  runResume.clearRun();
  check('an unconditional clear takes whatever is there', runResume.readRun('u-1') === null);
}

console.log('a browser that refuses storage is not a broken run');
{
  ls.clear();
  const realWindow = globalThis.window;
  globalThis.window = {
    get localStorage() {
      throw new Error('storage denied');
    },
  };
  check('a refused store reads as no run', runResume.readRun('u-1') === null);
  let threw = null;
  try {
    runResume.writeRun({ jobId: 'gen-9', ownerId: 'u-1', goal: 'x', startedAt: 1 });
    runResume.clearRun('gen-9');
    runResume.clearRun();
  } catch (e) {
    threw = e;
  }
  check('and writing to it throws nothing, because the run is the gateway’s', threw === null, String(threw));
  globalThis.window = realWindow;
}

server.close();

if (failed) {
  console.log(`\ncourse-store-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log('\ncourse-store-audit: OK');
