/*
 * The `plans` course's own numbers, checked against the fixture they describe.
 *
 * Every other content audit checks the shape of the course — that a lesson has
 * objectives, an example, an exercise. This one checks the *facts*: the perf
 * course is the one place a lesson states numbers ("90% of the table is shipped
 * and 0.5% is refunded", "customer 7 has 184 orders", "order 1050 has two
 * lines"), and a fixture change that quietly breaks one of them turns the
 * course into teaching material that contradicts the database the learner can
 * query. That is exactly the class of bug this script exists to catch: the
 * authored `result` rows for the perf course were once written against an
 * imagined dataset, and nothing noticed.
 *
 * Two halves, because the perf course makes two kinds of claim:
 *
 *   1. Data truth. `backend/internal/dataset/perf.sql` is loaded into PGlite —
 *      a real PostgreSQL compiled to WebAssembly, and the engine the browser
 *      adapter already runs — and every authored example and solution is
 *      executed against it. PGlite is used rather than the Go gateway because a
 *      plan alone cannot show rows, and because running the *same* seed file in
 *      a second PostgreSQL build is itself evidence: the same facts come back
 *      from 18.6 native (Go test) and 18.6/wasm (here).
 *   2. Plan truth. Each perf exercise's authored solution is planned through the
 *      real gateway (POST /explain, the perf dataset) and graded with the same
 *      grader the app uses (`web/src/api/planContract.ts`), so an exercise whose
 *      contract its own solution cannot satisfy fails here rather than in front
 *      of a learner.
 *
 *   make audit-perf-content
 *   node scripts/perf-content-audit.mjs          # check
 *   node scripts/perf-content-audit.mjs --capture # print what the rows should be
 */
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { assertFreshBundle } from './lib/bundle.mjs';
import { Reporter, buildGateway, liveEnv, postJSON, repoRoot, startGateway, stopGateway } from './lib/gateway.mjs';
import { jsRows, literal, sameRows } from './lib/rowtruth.mjs';

const webRequire = createRequire(path.join(repoRoot, 'web', 'package.json'));
const { PGlite } = webRequire('@electric-sql/pglite');
const require = createRequire('/tmp/');
assertFreshBundle('/tmp/curriculum.cjs', 'audit-perf-content', ['web/src/data/curriculum.ts']);
assertFreshBundle('/tmp/plan-contract.cjs', 'audit-perf-content', ['web/src/api/planContract.ts']);
const curriculum = require('/tmp/curriculum.cjs');
const { gradePlan } = require('/tmp/plan-contract.cjs');

const capture = process.argv.includes('--capture');
const report = new Reporter('perf-content-audit');

/** The perf course, and the fixture it is authored against. */
const course = (curriculum.COURSES || []).find((c) => c.id === 'plans');
if (!course) {
  console.log('perf-content-audit: no course with id "plans"');
  process.exit(1);
}
const perfSqlPath = path.join(repoRoot, 'backend', 'internal', 'dataset', 'perf.sql');
const perfSql = fs.readFileSync(perfSqlPath, 'utf8');

/**
 * Comparing an authored `result:` row against the engine is `scripts/lib/rowtruth.mjs`
 * — one copy, shared with `perf-seam-audit`, because two copies of the rule had
 * already drifted and one of them reported three false failures. The learner-facing
 * comparator is a different thing and stays strict (design §6.1 rule 7). */

/** Every block in the course that carries SQL, in course order. */
function* blocks() {
  for (const mod of course.modules || []) {
    for (const block of mod.blocks || []) {
      if (block && typeof block.sql === 'string') yield { mod, block, sql: block.sql, label: `${mod.id} ${block.type} ${block.id ?? ''}`.trim() };
      if (block && block.type === 'exercise' && typeof block.solution === 'string') {
        yield { mod, block, sql: block.solution, label: `${mod.id} exercise ${block.id}` };
      }
    }
  }
}

async function main() {
  const db = await PGlite.create();
  try {
    /* perf.sql carries its own BEGIN/COMMIT, which `exec` cannot nest; the file
       is otherwise run byte for byte as the gateway embeds it. */
    await db.exec(perfSql.replace(/^BEGIN;$/m, '').replace(/^COMMIT;$/m, ''));
    /* The authored lessons write `FROM orders`, not `FROM dataset.orders`: the
       schema is on the search path, exactly as the executor configures it. */
    await db.exec('SET search_path TO dataset, public;');

    report.section(`the fixture loads in PGlite (${(await db.query('SELECT version()')).rows[0].version.slice(0, 26)})`);
    const counts = await db.query(`SELECT
      (SELECT count(*) FROM dataset.customers)   AS customers,
      (SELECT count(*) FROM dataset.products)    AS products,
      (SELECT count(*) FROM dataset.orders)      AS orders,
      (SELECT count(*) FROM dataset.order_items) AS order_items`);
    const c = counts.rows[0];
    report.check('2000 customers, 500 products, 10 000 orders, 20 000 order items',
      Number(c.customers) === 2000 && Number(c.products) === 500 && Number(c.orders) === 10000 && Number(c.order_items) === 20000,
      c);

    /* The prose states numbers too, and a result table is not the only place a
       course can go stale: "customer 7 has 184 orders" is a claim a fixture
       change can falsify just as quietly. Each row below names the sentence it
       backs, so a failure points at the lesson rather than at a row. */
    report.section('every number the course states in prose is true of the fixture');
    const claims = [
      ['p1: "2 000 customers, 500 products, 10 000 orders and 20 000 order items"',
        `SELECT (SELECT count(*) FROM customers) AS customers, (SELECT count(*) FROM products) AS products,
                (SELECT count(*) FROM orders) AS orders, (SELECT count(*) FROM order_items) AS lines`,
        [2000, 500, 10000, 20000]],
      ['p1 and p4: "one value covers 90% of the table, another 0.5%"',
        `SELECT (SELECT count(*) FROM orders WHERE status = 'shipped') AS shipped,
                (SELECT count(*) FROM orders WHERE status = 'refunded') AS refunded`,
        [9000, 50]],
      ['p5: "customer 7 has 184 orders, and the index on customer_id finds them"',
        `SELECT count(*) FROM orders WHERE customer_id = 7`, [184]],
      ['p5: "one hash join over 10 000 orders"',
        `SELECT count(*) FROM orders`, [10000]],
      ['p7: "One row out of 500, found by reading all 500"',
        `SELECT (SELECT count(*) FROM products) AS products,
                (SELECT count(*) FROM products WHERE name LIKE 'Aurora%') AS aurora`,
        [500, 1]],
      ['p8: "reads all 10 000 orders … the 20 000 order lines"',
        `SELECT (SELECT count(*) FROM orders) AS orders, (SELECT count(*) FROM order_items) AS lines`,
        [10000, 20000]],
      ['p8: "one order and its two lines"',
        `SELECT (SELECT count(*) FROM orders WHERE id = 1050) AS orders,
                (SELECT count(*) FROM order_items WHERE order_id = 1050) AS lines`,
        [1, 2]],
    ];
    for (const [claim, sql, expected] of claims) {
      const res = await db.query(sql);
      const cols = (res.fields || []).map((f) => f.name);
      const got = (res.rows || []).map((r) => cols.map((n) => Number(r[n])));
      const want = [expected];
      report.check(claim, sameRows(got, want), { got, want });
    }
    /* "a predicate wrapped in a cast has no statistic, so the planner assumes
       0.5% of the relation" is a plan claim, not a row claim: the number lives
       in the estimate on the scan node. */
    const castPlan = await db.query(`EXPLAIN (FORMAT JSON) SELECT id FROM orders WHERE customer_id::text = '7'`);
    const castRaw = castPlan.rows[0];
    const castKey = Object.keys(castRaw)[0];
    const castTree = typeof castRaw[castKey] === 'string' ? JSON.parse(castRaw[castKey]) : castRaw[castKey];
    const castEstimate = (function find(node) {
      if (!node || typeof node !== 'object') return null;
      if (node['Node Type'] === 'Seq Scan' && node['Relation Name'] === 'orders') return node['Plan Rows'];
      for (const child of node.Plans || []) {
        const hit = find(child);
        if (hit != null) return hit;
      }
      return null;
    })(castTree[0].Plan);
    report.check('p4 and p7: a cast wrapped around the column falls back to 0.5% of 10 000 rows',
      Number(castEstimate) === 50, { estimate: castEstimate });

    report.section('every authored statement runs, and every authored result row is the truth');
    let mismatches = 0;
    let compared = 0;
    for (const { block, sql, label } of blocks()) {
      let res;
      try {
        res = await db.query(sql);
      } catch (e) {
        report.check(`${label} executes`, false, e instanceof Error ? e.message : String(e));
        continue;
      }
      const columns = (res.fields || []).map((f) => f.name);
      const rows = (res.rows || []).map((r) => columns.map((n) => r[n]));
      const want = block.result;
      if (!want) continue; // a syntax block: executing is the whole claim
      compared++;
      const wantColumns = want.columns || [];
      const wantRows = want.rows || [];
      const columnsOk = columns.length === wantColumns.length && columns.every((n, i) => n === wantColumns[i]);
      const rowsOk = sameRows(rows, wantRows);
      const ok = report.check(`${label} — ${rows.length} row(s) match the authored result`, columnsOk && rowsOk,
        columnsOk ? { authored: wantRows, actual: rows } : { authored: wantColumns, actual: columns });
      if (!ok) {
        mismatches++;
        console.log(`         actual:  ${literal(columns, rows)}`);
        console.log(`         paste:   result:{columns:${JSON.stringify(columns)},rows:${jsRows(rows)}}`);
      }
    }
    report.check(`all ${compared} authored result rows are true`, mismatches === 0, `${mismatches} mismatch(es)`);
    if (capture) {
      console.log('\ncaptured results (paste into web/src/data/curriculum.ts):');
      for (const { block, sql, label } of blocks()) {
        if (!block.result) continue;
        const res = await db.query(sql);
        const columns = (res.fields || []).map((f) => f.name);
        const rows = (res.rows || []).map((r) => columns.map((n) => r[n]));
        console.log(`  ${label}\n    result:{columns:${JSON.stringify(columns)},rows:${jsRows(rows)}},`);
      }
    }
  } finally {
    await db.close().catch(() => undefined);
  }

  /* ---------- plan truth: the authored solution against its own contract ---------- */
  report.section('every perf exercise solution satisfies its own perf contract');
  const bin = buildGateway('perf-content');
  const env = await liveEnv('perf-content');
  const gw = await startGateway(bin, env);
  try {
    const signup = await postJSON(`${gw.base}/auth/signup`, {
      email: `perf-content-${Date.now()}@example.com`, name: 'Perf Content Audit', password: 'perf-content-passphrase',
    });
    if (signup.status !== 201 || !signup.json?.token) throw new Error(`signup failed: HTTP ${signup.status} ${signup.body}`);
    const auth = { authorization: `Bearer ${signup.json.token}` };

    for (const mod of course.modules || []) {
      for (const block of mod.blocks || []) {
        if (block.type !== 'exercise' || !block.perf) continue;
        const res = await postJSON(`${gw.base}/explain`, {
          sql: block.solution,
          analyze: false,
          exercise: { id: block.id, version: block.version ?? 1, lesson_id: mod.id, perf: block.perf },
        }, 180_000, auth);
        if (res.status !== 200 || !res.json || res.json.status !== 'succeeded') {
          report.check(`${block.id} plans on the perf dataset`, false, { status: res.status, body: res.body.slice(0, 300) });
          continue;
        }
        const bundle = res.json;
        const verdict = gradePlan(bundle, block.perf);
        const failed = (verdict.checks || []).filter((k) => !k.ok).map((k) => k.metric || k.code);
        report.check(`${block.id} — the authored solution passes its own contract`, verdict.passed === true,
          failed.length ? failed : verdict);
        /* `dataset` lives on the environment block the bundle reports, not on
           the bundle root: reading it from the root made this check fail for
           every exercise while the plan was in fact captured against perf. */
        report.check(`${block.id} — planned against the perf fixture, not the small one`,
          bundle.environment?.dataset === 'perf' && Number(bundle.environment?.scale_factor) === 10000,
          { dataset: bundle.environment?.dataset, scale_factor: bundle.environment?.scale_factor });
      }
    }
  } finally {
    stopGateway(gw.child);
  }

  report.finish();
}

main().catch((e) => {
  console.error('perf-content-audit: ' + (e instanceof Error ? e.stack ?? e.message : String(e)));
  process.exit(1);
});
