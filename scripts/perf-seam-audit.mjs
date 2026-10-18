/*
 * The perf seam, driven through the adapter the learner actually uses.
 *
 * M5 added a second dataset, and the whole point of the change is that a `plans`
 * exercise is graded against the 10 000-row fixture rather than the 80-row one
 * every other course uses. The other perf gates check the *fixture* and the
 * *content* (perf-content-audit) and the gateway's routing (the Go tests). None
 * of them checks what the browser adapter does with a dataset id, and that is
 * where the defect lived:
 *
 *   - `executeSql` and `explainSql` had no dataset parameter, so `Run` and
 *     `Analyze` in the default (PGlite) mode always ran on commerce while the
 *     lesson's authored rows were written against perf;
 *   - a second `PGliteWorker` for the second fixture joined the first one's
 *     leader election (PGlite derives the election channel from the worker URL
 *     when no `id` is given), so opening a plan lesson broke in-flight queries
 *     on the commerce engine with `LeaderChangedError` — and, if the other
 *     worker had won, would have executed perf SQL against the commerce tables.
 *
 * This script bundles `web/src/api/pglite.ts` — the shipping adapter, stubs
 * only for the two Vite-only imports — and drives that seam directly: the real
 * fixtures, the real curriculum, the real plan grader.
 *
 * It runs PGlite on the main thread (Node has no `Worker`), so it cannot see an
 * election problem; the two-engine *identity* fix for that is guarded by the
 * explicit per-dataset `id` in `web/src/api/pglite.ts` and by driving the app.
 * What this does prove is that the seam routes each statement to the right
 * fixture and that the authored answers come back from it.
 *
 *   make audit-perf-seam
 *   node scripts/perf-seam-audit.mjs
 */
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { assertFreshBundle } from './lib/bundle.mjs';
import { Reporter, repoRoot, scratchDir } from './lib/gateway.mjs';
import { firstDifference, sameRows } from './lib/rowtruth.mjs';

const webRequire = createRequire(path.join(repoRoot, 'web', 'package.json'));
const require = createRequire('/tmp/');
for (const [bundle, sources] of [
  ['/tmp/curriculum-for-perf-seam.cjs', ['web/src/data/curriculum.ts']],
  ['/tmp/plan-contract-for-perf-seam.cjs', ['web/src/api/planContract.ts']],
  ['/tmp/dataset-for-seam.cjs', ['web/src/lib/dataset.ts']],
]) {
  assertFreshBundle(bundle, 'audit-perf-seam', sources);
}
const curriculum = require('/tmp/curriculum-for-perf-seam.cjs');
const { gradePlan } = require('/tmp/plan-contract-for-perf-seam.cjs');
const datasetLib = require('/tmp/dataset-for-seam.cjs');

const API_DIR = path.join(repoRoot, 'web', 'src', 'api');
const DATASET_DIR = path.join(repoRoot, 'backend', 'internal', 'dataset');
const report = new Reporter('perf-seam-audit');

/** Read a fixture from the one Go source of truth, exactly as the app does. */
const readFixture = (name) => fs.readFileSync(path.join(DATASET_DIR, name), 'utf8');

/**
 * The two Vite-only imports (`?raw`, `import.meta.glob`) are the only things
 * Node cannot resolve, so they are stubbed with the same contents the app's
 * dataset.ts and content.ts carry.
 */
function viteStubPlugin() {
  return {
    name: 'qc-vite-stubs',
    setup(build) {
      build.onResolve({ filter: /(^|\/)dataset$/ }, () => ({ path: 'qc-dataset', namespace: 'qc-stub' }));
      build.onResolve({ filter: /(^|\/)content$/ }, () => ({ path: 'qc-content', namespace: 'qc-stub' }));
      build.onLoad({ filter: /.*/, namespace: 'qc-stub' }, (args) => {
        if (args.path === 'qc-dataset') {
          return {
            loader: 'js',
            contents: 'export const schemaSql = ' + JSON.stringify(readFixture('schema.sql')) + ';\n'
              + 'export const seedSql = ' + JSON.stringify(readFixture('seed.sql')) + ';\n'
              + 'export const perfSql = ' + JSON.stringify(readFixture('perf.sql')) + ';\n'
              + 'export const datasetVersion = 1;',
          };
        }
        if (args.path === 'qc-content') {
          return { loader: 'js', contents: 'export const BUNDLED_CONTENT = {};\nexport async function seedContentDocs() {}' };
        }
        return null;
      });
    },
  };
}

async function bundleAdapter() {
  const esbuild = webRequire('esbuild');
  const pgliteIndex = path.join(repoRoot, 'web', 'node_modules', '@electric-sql', 'pglite', 'dist', 'index.js');
  const pgliteWorker = path.join(repoRoot, 'web', 'node_modules', '@electric-sql', 'pglite', 'dist', 'worker', 'index.js');
  const out = path.join(scratchDir('bundle'), 'qc-perf-seam-adapter.mjs');
  await esbuild.build({
    entryPoints: [path.join(API_DIR, 'pglite.ts')],
    bundle: true, format: 'esm', platform: 'node', target: 'node20', outfile: out,
    plugins: [viteStubPlugin()],
    /* PGlite stays external and is rewired to its real file so its own
       import.meta.url-relative WASM and data files still resolve. */
    alias: { '@electric-sql/pglite': pgliteIndex, '@electric-sql/pglite/worker': pgliteWorker },
    external: [pgliteIndex, pgliteWorker],
    logLevel: 'silent',
  });
  return import(pathToFileURL(out).href);
}

/** Every exercise in the curriculum that carries a perf contract. */
function perfExercises() {
  const out = [];
  for (const course of curriculum.COURSES ?? []) {
    for (const mod of course.modules ?? []) {
      for (const block of mod.blocks ?? []) {
        if (block?.perf) out.push({ course, mod, block });
      }
    }
  }
  return out;
}

async function main() {
  const adapter = await bundleAdapter();
  const exercises = perfExercises();

  report.section('the dataset a chapter resolves to');
  const perfCourses = (curriculum.COURSES ?? []).filter((c) => datasetLib.datasetFor({ course: c }) === 'perf').map((c) => c.id);
  report.check('a course with no dataset runs on commerce', datasetLib.datasetFor({ course: { id: 'x' } }) === 'commerce', datasetLib.datasetFor({ course: { id: 'x' } }));
  report.check('the perf course exists and runs on perf', perfCourses.includes('plans'), perfCourses);
  report.check('every perf exercise resolves to perf', exercises.length > 0 && exercises.every((e) => datasetLib.datasetFor({ course: e.course, block: e.block }) === 'perf'), exercises.filter((e) => datasetLib.datasetFor({ course: e.course, block: e.block }) !== 'perf').map((e) => e.block.id));
  report.check('and the app has a perf exercise to grade', exercises.length >= 4, exercises.length);

  report.section('the fixture the browser adapter runs on');
  const perfOrders = await adapter.executeSql('SELECT count(*) AS n FROM orders', 'perf');
  const commerceOrders = await adapter.executeSql('SELECT count(*) AS n FROM orders', 'commerce');
  report.check('perf orders is the 10 000-row fixture', perfOrders.status === 'succeeded' && Number(perfOrders.rows[0][0]) === 10000, perfOrders.rows?.[0]);
  report.check('commerce orders is still the 80-row fixture', commerceOrders.status === 'succeeded' && Number(commerceOrders.rows[0][0]) === 80, commerceOrders.rows?.[0]);
  report.check('the two datasets are two databases', Number(perfOrders.rows[0][0]) !== Number(commerceOrders.rows[0][0]));

  const perfTables = await adapter.fetchTables('perf');
  const commerceTables = await adapter.fetchTables('commerce');
  const rowsOf = (t, name) => t.tables.find((x) => x.name === name)?.rows;
  report.check('the drawer reads perf row counts from the perf engine', rowsOf(perfTables, 'orders') === 10000 && rowsOf(perfTables, 'customers') === 2000, perfTables.tables.map((t) => `${t.name}=${t.rows}`));
  report.check('and the commerce drawer is unchanged', rowsOf(commerceTables, 'orders') === 80 && rowsOf(commerceTables, 'customers') === 30, commerceTables.tables.map((t) => `${t.name}=${t.rows}`));

  report.section('every authored perf answer comes back from the perf fixture');
  let authoredFailures = 0;
  for (const { block, course, mod } of exercises) {
    const out = await adapter.executeSql(block.solution, 'perf');
    if (out.status !== 'succeeded') {
      authoredFailures++;
      report.check(`${block.id} runs on perf`, false, out.error);
      continue;
    }
    const actual = { columns: out.columns.map((c) => c.name), rows: out.rows };
    /* The authored rows are the lesson's own claim about the dataset, and an
       engine prints a timestamp in its dialect's own shape — PostgreSQL
       `2026-12-28 01:48:00`, SQLite `2026-12-28 01:48` (docs/sqlite-dialect.md
       §5) — so this section is the content-truth comparator, not the
       learner-facing one. The learner is graded against a reference the *same*
       engine produced, which is why design §6.1 rule 7 ("no implicit text
       normalization") can stay strict there: nothing ever has to make
       `10:12` equal `10:12:00` on the grading path. */
    const columnsOk = actual.columns.join('\u0000') === block.result.columns.join('\u0000');
    const rowsOk = sameRows(actual.rows, block.result.rows);
    const ok = columnsOk && rowsOk;
    if (!ok) authoredFailures++;
    report.check(`${block.id} on ${course.id}/${mod.id} — the authored result row is the truth`, ok,
      ok ? undefined : {
        columns: columnsOk ? undefined : { expected: block.result.columns, actual: actual.columns },
        firstDifference: firstDifference(actual.rows, block.result.rows),
      });
  }
  report.check('no authored perf answer failed on the fixture it names', authoredFailures === 0, authoredFailures);

  report.section('a plan captured for a perf exercise is graded against the perf fixture');
  for (const { block, mod } of exercises) {
    const bundle = await adapter.explainSql(block.solution, true, 'rq_seam', 'perf');
    if (bundle.status !== 'succeeded') {
      report.check(`${block.id} — the plan is captured`, false, bundle.error);
      continue;
    }
    report.check(`${block.id} — the environment says perf at scale 10000`,
      bundle.environment?.dataset === 'perf' && Number(bundle.environment?.scale_factor) === 10000,
      { dataset: bundle.environment?.dataset, scale_factor: bundle.environment?.scale_factor });
    const verdict = gradePlan(bundle, block.perf);
    const failed = (verdict.checks || []).filter((k) => !k.ok).map((k) => k.label || k.metric || k.code);
    report.check(`${block.id} — its own solution passes its own contract on the browser engine`, verdict.passed === true, failed.length ? failed : verdict);
  }

  report.section('and the plan for the same SQL on commerce is a different plan');
  /* `ex.p4.1` asks for a 0.5% predicate. At 10 000 rows that is 50 rows and an
     index; at 80 rows there is no selectivity to speak of, which is the whole
     reason the `plans` course has a fixture of its own. If these two plans were
     identical, the dataset routing would be decorative. */
  const probe = exercises.find((e) => e.block.id === 'ex.p4.1') ?? exercises[0];
  const onPerf = await adapter.explainSql(probe.block.solution, false, 'rq_seam_a', 'perf');
  const onCommerce = await adapter.explainSql(probe.block.solution, false, 'rq_seam_b', 'commerce');
  const planKey = (b) => (b.summary?.nodes ?? []).map((n) => `${n.node_type}${n.relation ? '/' + n.relation : ''}`).join(' > ');
  report.check('the same SQL is planned differently on the two fixtures', planKey(onPerf) !== planKey(onCommerce), { perf: planKey(onPerf), commerce: planKey(onCommerce) });
  report.check('the perf plan is the one the exercise teaches', Number(onPerf.environment?.scale_factor) === 10000, onPerf.environment?.scale_factor);
  report.check('the commerce plan reports the small scale', Number(onCommerce.environment?.scale_factor) === 80, onCommerce.environment?.scale_factor);
  report.check('and the plan reports the dataset it was planned against', onCommerce.environment?.dataset === 'commerce', onCommerce.environment?.dataset);

  report.section('restoring one workspace leaves the other alone');
  const write = await adapter.executeSql('CREATE TABLE seam_probe (id integer)', 'perf');
  report.check('a learner can write to the perf workspace', write.status === 'succeeded', write.error);
  const before = await adapter.executeSql("SELECT count(*) AS n FROM information_schema.tables WHERE table_schema = 'dataset' AND table_name = 'seam_probe'", 'perf');
  report.check('the perf write is visible on the next statement', Number(before.rows[0][0]) === 1, before.rows?.[0]);
  await adapter.resetEnvironment('perf');
  const after = await adapter.executeSql("SELECT count(*) AS n FROM information_schema.tables WHERE table_schema = 'dataset' AND table_name = 'seam_probe'", 'perf');
  report.check('reset on perf undoes the perf write', Number(after.rows[0][0]) === 0, after.rows?.[0]);
  const perfAfterReset = await adapter.executeSql('SELECT count(*) AS n FROM orders', 'perf');
  report.check('and the perf fixture is back at scale', Number(perfAfterReset.rows[0][0]) === 10000, perfAfterReset.rows?.[0]);
  const commerceAfterReset = await adapter.executeSql('SELECT count(*) AS n FROM orders', 'commerce');
  report.check('the commerce workspace was not touched by a perf reset', Number(commerceAfterReset.rows[0][0]) === 80, commerceAfterReset.rows?.[0]);

  report.finish();
}

main().catch((e) => {
  console.error('perf-seam-audit: ' + (e instanceof Error ? e.stack ?? e.message : String(e)));
  process.exit(1);
});
