/*
 * Exercise-solution audit.
 *
 * Runs every authored exercise `solution` through a live gateway and grades it
 * against itself, so a reference query that no longer matches its own contract
 * is caught before a learner sees it. See docs/course-audit.md.
 *
 *   make audit-courses           # bundles the catalog and runs this script
 *   node scripts/course-audit.mjs
 *
 * Why it is worth this much ceremony: the first version of this script POSTed
 * `{sql}` and nothing else, which asked the gateway to run every statement as
 * PostgreSQL against the commerce dataset. Once chapters were authored for
 * SQLite (M6), 16 exercises "broke" for a reason that had nothing to do with the
 * content, and five more *looked* green only because their SQL happened to be
 * valid PostgreSQL too — a false pass. The statement was also judged with no
 * `dialect` and no `dataset`, so a plan-lesson exercise would have been graded
 * on the wrong fixture. Worse, the script printed its own summary and never set
 * an exit code, so a broken exercise could not fail a build.
 *
 * Now the dialect and the dataset come from the app's own resolvers —
 * `web/src/lib/dialect.ts` and `web/src/lib/dataset.ts`, bundled here by the
 * make target — with exactly the precedence a screen uses (block, then module,
 * then course, then the default), and both are sent with the statement. The
 * audit is a `Reporter`, so a single broken exercise exits non-zero.
 *
 * Modes:
 *   node scripts/course-audit.mjs
 *     Hermetic. Builds the gateway and boots it on its own embedded PostgreSQL.
 *   QC_GATEWAY=http://127.0.0.1:18077 node scripts/course-audit.mjs
 *     Use a gateway someone else started (nothing is built or stopped). It must
 *     have a live database: grading a reference against ENVIRONMENT_UNAVAILABLE
 *     proves nothing.
 */
import { createRequire } from 'node:module';
import { assertFreshBundle } from './lib/bundle.mjs';
import { Reporter, buildGateway, getJSON, liveEnv, postJSON, startGateway, stopGateway } from './lib/gateway.mjs';

/* Long enough for the perf fixture (10 000 orders) to be prepared on first use
   of a fresh account, which is what a plan lesson triggers. */
const REQUEST_TIMEOUT_MS = 180_000;

const require = createRequire('/tmp/');
assertFreshBundle('/tmp/curriculum-for-courses.cjs', 'audit-courses', ['web/src/data/curriculum.ts', 'web/src/types.ts']);
assertFreshBundle('/tmp/dialect-for-courses.cjs', 'audit-courses', ['web/src/lib/dialect.ts']);
assertFreshBundle('/tmp/dataset-for-courses.cjs', 'audit-courses', ['web/src/lib/dataset.ts']);
const c = require('/tmp/curriculum-for-courses.cjs');
const { dialectFor, dialectReason } = require('/tmp/dialect-for-courses.cjs');
const { datasetFor, datasetReason } = require('/tmp/dataset-for-courses.cjs');

const r = new Reporter('course-audit');

/* Derived from the catalog, not hard-coded: a listed set cannot notice a new
   course, which is how `plans` shipped with every authored solution unrun. */
const courses = (c.COURSES || [])
  .map((course) => ({ course, modules: course.modules || [] }))
  .filter(({ modules }) => modules.length > 0);
const exercises = [];
for (const { course, modules } of courses) {
  for (const module of modules) {
    for (const block of module.blocks || []) {
      if (block.type === 'exercise') exercises.push({ course, module, block });
    }
  }
}

let bin = null;
let gw = null;
const external = process.env.QC_GATEWAY ? process.env.QC_GATEWAY.replace(/\/$/, '') : null;
try {
  if (external) {
    console.log(`course-audit: using the gateway at ${external}`);
  } else {
    bin = buildGateway('courses');
    gw = await startGateway(bin, await liveEnv('courses'));
  }
  const BASE = external || gw.base;

  const availability = await postJSON(`${BASE}/query`, { sql: 'SELECT 1;' });
  if (availability.json?.status === 'failed' && availability.json?.error?.code === 'ENVIRONMENT_UNAVAILABLE') {
    throw new Error(`${BASE}/query has no database (ENVIRONMENT_UNAVAILABLE); grading would compare two error strings`);
  }

  /* Speak as a signed-in learner, because that is who the exercises are for.
     The perf fixture (the `plans` course) exists only inside a per-account
     writable environment — an anonymous request is *correctly* refused with
     "sign in and run the lesson again", and the first version of this rewrite
     read that refusal as a broken exercise. A learner's account is also what
     makes a SQLite write land in their own file rather than the shared one. */
  const stamp = `${process.pid}-${Date.now()}`;
  const signup = await postJSON(`${BASE}/auth/signup`, {
    email: `course-audit-${stamp}@querycraft.local`,
    name: 'Course Audit',
    password: 'course-audit-passphrase',
  }, 30_000);
  const token = signup.json?.token;
  if (!token) throw new Error(`could not sign up a learner to grade with: HTTP ${signup.status} ${String(signup.body).slice(0, 200)}`);
  const auth = { authorization: `Bearer ${token}` };
  r.section('the audit grades as a signed-in learner');
  r.check('the learner signed up', !!token);
  const env = await getJSON(`${BASE}/environment`, 60_000, auth);
  r.check('the learner has a writable workspace (which is where the perf fixture lives)', env.json?.environment?.writable === true, env.json?.environment);
  r.check('the workspace is the learner\'s own, not the shared dataset', env.json?.environment?.database !== '', String(env.json?.environment?.database || ''));

  const rows = [];
  for (const { course, module, block } of exercises) {
    /* The same two calls a screen makes, through the same shipping modules. */
    const dialectRef = { block, module, course };
    const datasetRef = { block, exercise: block, course };
    const dialect = dialectFor(dialectRef);
    const dataset = datasetFor(datasetRef);
    const dialectWhy = dialectReason(dialectRef);
    const datasetWhy = datasetReason(datasetRef);

    const solution = String(block.solution || '');
    const want = (block.contract && block.contract.columns) || [];
    const row = {
      id: block.id, course: course.id, lesson: module.id, dialect, dataset,
      dialectWhy, datasetWhy, want, status: 'NO_SQL', rows: 0, cols: [], colsOk: null, self: null, err: null,
    };

    if (solution.trim()) {
      const run = await postJSON(`${BASE}/query`, { sql: solution, dialect, dataset }, REQUEST_TIMEOUT_MS, auth);
      const q = run.json || {};
      row.status = q.status;
      row.rows = q.row_count;
      row.cols = (q.columns || []).map((x) => x.name);
      row.err = q.error && q.error.code;
      /* The dialect the *result* claims has to be the one that was asked for, or
         the gateway quietly ran the wrong engine. */
      row.dialectRan = q.dialect;
      row.ok = q.status === 'succeeded' && !q.truncated;
      row.colsOk = want.length === 0 ? null : JSON.stringify(want) === JSON.stringify(row.cols);

      const graded = await postJSON(`${BASE}/answers/evaluate`, {
        sql: solution, dialect, dataset,
        exercise: { id: block.id, version: 1, lesson_id: module.id, contract: block.contract, reference_solution: solution },
      }, REQUEST_TIMEOUT_MS, auth);
      row.self = graded.json && graded.json.passed;
    }

    /* A learner often presses Run before reading the whole page. The starter
       must be a syntactically valid query that returns a visibly wrong result;
       a parse error is the wrong first reply. */
    const starter = String(block.starter || '');
    if (starter.trim()) {
      const started = await postJSON(`${BASE}/query`, { sql: starter, dialect, dataset }, REQUEST_TIMEOUT_MS, auth);
      if (started.json?.status === 'failed' && started.json?.error?.code === 'QUERY_SYNTAX_ERROR') {
        row.starterError = started.json.error.message;
      }
    }

    row.broken = !row.ok || row.colsOk === false || row.self === false;
    rows.push(row);
  }

  const bad = rows.filter((x) => x.broken);
  const dialectsRun = [...new Set(rows.map((x) => x.dialectRan).filter(Boolean))].sort();

  r.section('every authored solution runs and grades against itself');
  for (const x of bad) {
    console.log(`         ${x.course}/${x.id} [${x.dialect} · ${x.dataset}] status=${x.status} rows=${x.rows} colsOk=${x.colsOk} self=${x.self} err=${x.err || ''}`);
    if (x.colsOk === false) console.log(`           want ${JSON.stringify(x.want)} got ${JSON.stringify(x.cols)}`);
  }
  r.check(`all ${rows.length} exercise solutions ran, matched their contract columns and passed their own grading`, bad.length === 0, bad.map((x) => x.id));

  /* The first course is the one a new learner meets. Its starters are gated
     because a parse error there is a failed first Run. Other courses are
     reported but not gated until their authors replace the old fill-in
     placeholders; see docs/content-authoring.md. */
  const firstRun = 'sqlite-essentials';
  const starterBad = rows.filter((x) => x.starterError);
  const firstRunBad = starterBad.filter((x) => x.course === firstRun);
  const firstRunRows = rows.filter((x) => x.course === firstRun);
  r.section('the first-run course starters run without a syntax error');
  for (const x of firstRunBad) console.log(`         ${x.course}/${x.id}: ${x.starterError}`);
  r.check(`all ${firstRunRows.length} ${firstRun} starters are syntactically valid`, firstRunBad.length === 0, firstRunBad.map((x) => x.id));
  if (starterBad.length > firstRunBad.length) {
    console.log(`  note   ${starterBad.length - firstRunBad.length} starter(s) in other courses still use fill-in placeholders; not gating`);
  }

  r.section('the audit is not vacuous');
  r.check('the catalog produced exercises to run', rows.length > 0, rows.length);
  r.check('at least one built course is in the catalog', courses.some(({ course }) => course.built !== false), courses.length);
  /* A course whose statements were silently sent as the wrong dialect is the
     defect this rewrite exists for, so the run proves which engines answered. */
  const declared = [...new Set(rows.map((x) => x.dialect))].sort();
  r.check(`every declared dialect was served (${declared.join(', ')})`, declared.every((d) => dialectsRun.includes(d)), { declared, dialectsRun });
  const unknown = rows.filter((x) => x.dialectWhy === 'unknown-dialect' || x.datasetWhy === 'unknown-dataset');
  r.check('no exercise names a dialect or dataset the app does not serve', unknown.length === 0, unknown.map((x) => `${x.id} ${x.dialectWhy}/${x.datasetWhy}`));
  const wrongEngine = rows.filter((x) => x.dialectRan && x.dialectRan !== x.dialect);
  r.check('the engine that answered is the dialect that was asked for', wrongEngine.length === 0, wrongEngine.map((x) => `${x.id}: asked ${x.dialect}, ran ${x.dialectRan}`));

  r.section('by course');
  for (const { course, modules } of courses) {
    const mine = rows.filter((x) => x.course === course.id);
    const broken = mine.filter((x) => x.broken).length;
    const set = [...new Set(mine.map((x) => `${x.dialect}/${x.dataset}`))].join(' ');
    console.log(`  ${broken === 0 ? 'ok    ' : 'FAIL  '} ${course.id.padEnd(22)} ${String(mine.length).padStart(2)} exercise(s)  ${set}`);
  }

  r.finish();
} finally {
  if (gw) stopGateway(gw.child);
}
