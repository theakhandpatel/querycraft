/*
 * M5 exit-gate audit — "SQL performance lab" (technical design §14 M5,
 * docs/m5-implementation-plan.md).
 *
 * It runs the real binary against its own embedded PostgreSQL and asserts the
 * four clauses of the M5 exit gate as measured facts:
 *
 *   1. Plans are reproducible on pinned fixtures.
 *   2. Results report engine version, scale and indexes.
 *   3. Results do not overstate timing precision.
 *   4. A plan that cannot be produced honestly is refused, not invented.
 *
 * The point of clause 1 is that a plan is *not* the timings: `EXPLAIN ANALYZE`
 * reports per-node milliseconds that move when the machine is busy, so this
 * audit compares the structural facts — node types, relations, indexes, row
 * estimates — and prints every field it excluded, with the reason. Clause 3 is
 * the other half of that rule: the numbers are still reported, but the bundle
 * has to say what they are worth and the UI has to render that label.
 *
 * Every bundle this script sees is validated against
 * contracts/plan.schema.json, so the gate is the contract and not this file's
 * idea of it.
 *
 *   make audit-perf
 *   node scripts/perf-audit.mjs
 */
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { Reporter, buildGateway, getJSON, liveEnv, postJSON, repoRoot, startGateway, stopGateway } from './lib/gateway.mjs';

const webRequire = createRequire(path.join(repoRoot, 'web', 'package.json'));
const planSchema = JSON.parse(fs.readFileSync(path.join(repoRoot, 'contracts', 'plan.schema.json'), 'utf8'));
const Ajv2020 = webRequire('ajv/dist/2020').default;
const validatePlan = new Ajv2020({ allErrors: true, strict: false }).compile(planSchema);

function violations(value) {
  if (value === null || value === undefined) return ['no body'];
  if (validatePlan(value)) return [];
  return (validatePlan.errors ?? []).slice(0, 4).map((e) => (e.instancePath || '/') + ' ' + e.message);
}

const report = new Reporter('perf-audit');

/* One statement with a join, an aggregate and a sort, so the plan has a shape
   worth comparing; one that should be able to use an index on `status`. */
const ANALYTIC_SQL = `SELECT p.category, SUM(oi.quantity * oi.unit_price) AS revenue
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
GROUP BY p.category
ORDER BY revenue DESC;`;
const INDEX_SQL = `SELECT o.id FROM orders o WHERE o.status = 'refunded';`;

/** Fields that are deliberately excluded from a reproducibility comparison. */
const EXCLUDED_FROM_COMPARISON = [
  ['actual_time_ms', 'wall-clock per node: moves with machine load'],
  ['duration_ms', 'wall-clock for the whole statement'],
  ['loops', 'a nested-loop inner node re-reports per loop; it is not a structural fact'],
  ['buffers_read', 'depends on cache state, not on the plan'],
];

/** structuralFingerprint keeps only the facts that must reproduce. */
function structuralFingerprint(bundle) {
  const s = bundle.summary ?? {};
  return JSON.stringify({
    node_types: s.node_types,
    relations: s.relations,
    indexes_used: s.indexes_used,
    max_estimated_rows: s.max_estimated_rows,
    plan_rows: s.plan_rows,
    nodes: (s.nodes ?? []).map((n) => ({
      type: n.node_type,
      relation: n.relation ?? null,
      index: n.index ?? null,
      estimated_rows: n.estimated_rows ?? null,
    })),
  });
}

async function signup(base, email) {
  const res = await postJSON(`${base}/auth/signup`, { email, name: 'Perf Audit', password: 'perf-audit-passphrase' });
  if (res.status !== 201 || !res.json?.token) throw new Error(`signup failed: HTTP ${res.status} ${res.body}`);
  return res.json.token;
}

const auth = (token) => ({ authorization: `Bearer ${token}` });

async function main() {
  const bin = buildGateway('perf');
  const env = await liveEnv('perf');
  const gw = await startGateway(bin, env);
  const seen = [];

  const explain = async (body, token) => {
    const res = await postJSON(`${gw.base}/explain`, body, 180_000, token ? auth(token) : {});
    if (res.json && typeof res.json === 'object') seen.push({ label: JSON.stringify(body).slice(0, 60), bundle: res.json });
    return res;
  };

  try {
    /* ---------- clause 0: the surface is wired in ---------- */
    report.section('the plan lab is advertised');
    const caps = await getJSON(`${gw.base}/capabilities`);
    const plan = caps.json?.plan;
    report.check('capabilities carries a plan block', Boolean(plan), JSON.stringify(plan));
    report.check('the plan lab is available with a database', plan?.available === true, JSON.stringify(plan));
    report.check('it names the datasets it can plan', Array.isArray(plan?.datasets) && plan.datasets.includes('perf'), plan?.datasets);
    report.check('it says analyze needs a session', plan?.analyze_requires_auth === true, plan?.analyze_requires_auth);
    report.check(
      'it names the timing precisions the client renders',
      JSON.stringify(plan?.timing_precision) === JSON.stringify(['analyze', 'estimate', 'none']),
      plan?.timing_precision,
    );

    /* ---------- clause 4a: anonymous can plan, cannot analyze ---------- */
    report.section('a learner who is not signed in can plan but not analyze');
    const anonPlan = await explain({ sql: INDEX_SQL, analyze: false });
    report.check('an anonymous plan is served', anonPlan.status === 200, anonPlan.status);
    report.check('an anonymous plan has no schema violations', violations(anonPlan.json).length === 0, violations(anonPlan.json));
    report.check('an anonymous plan succeeds', anonPlan.json?.status === 'succeeded', anonPlan.json?.status);
    report.check("an unexecuted plan is labelled 'estimate'", anonPlan.json?.timing_precision === 'estimate', anonPlan.json?.timing_precision);
    report.check('it carries no per-node timing', (anonPlan.json?.summary?.nodes ?? []).every((n) => n.actual_time_ms === null), 'a timing leaked into an estimate');
    report.check('analyze is false on the bundle', anonPlan.json?.analyze === false, anonPlan.json?.analyze);

    const anonAnalyze = await explain({ sql: INDEX_SQL, analyze: true });
    report.check('an anonymous analyze is refused', anonAnalyze.status === 400, anonAnalyze.status);
    report.check(
      'the refusal is ENVIRONMENT_UNAVAILABLE',
      anonAnalyze.json?.error?.code === 'ENVIRONMENT_UNAVAILABLE',
      anonAnalyze.json?.error,
    );
    report.check('the refusal invents no plan', anonAnalyze.json?.plan === null && anonAnalyze.json?.timing_precision === 'none', anonAnalyze.json?.timing_precision);
    report.check(
      'the refusal says a session is what is missing',
      /sign in/i.test(anonAnalyze.json?.error?.message ?? ''),
      anonAnalyze.json?.error?.message,
    );
    report.check('the refusal has no schema violations', violations(anonAnalyze.json).length === 0, violations(anonAnalyze.json));

    /* ---------- clause 2: the environment the plan came from ---------- */
    report.section('a plan reports the environment it was produced against');
    const token = await signup(gw.base, `perf-audit-${Date.now()}@example.com`);
    const analyzed = await explain({ sql: ANALYTIC_SQL, analyze: true, dataset: 'perf' }, token);
    report.check('the analyzed plan is served', analyzed.status === 200, `${analyzed.status} ${analyzed.body?.slice(0, 200)}`);
    report.check('the analyzed plan has no schema violations', violations(analyzed.json).length === 0, violations(analyzed.json));
    report.check('the analyzed plan succeeds', analyzed.json?.status === 'succeeded', analyzed.json?.error);
    report.check("the analyzed plan is labelled 'analyze'", analyzed.json?.timing_precision === 'analyze', analyzed.json?.timing_precision);

    const e = analyzed.json?.environment ?? {};
    report.check('it names the engine', e.engine === 'postgresql', e.engine);
    report.check('it reports the engine version', /^\s*1[0-9]/.test(String(e.engine_version ?? '')) || /PostgreSQL/i.test(String(e.engine_version ?? '')), e.engine_version);
    report.check('it reports the scale factor', Number(e.scale_factor) >= 10000, e.scale_factor);
    report.check(
      'the reported scale agrees with the rows actually present',
      Number(e.scale_factor) === Number(e.row_counts?.orders),
      `${e.scale_factor} reported vs ${e.row_counts?.orders} rows in orders`,
    );
    report.check('it reports per-relation row counts', Object.keys(e.row_counts ?? {}).length >= 3, e.row_counts);
    report.check('it reports the indexes', Array.isArray(e.indexes) && e.indexes.length > 0, e.indexes?.length);
    report.check('it reports the planner settings that change a plan', 'random_page_cost' in (e.settings ?? {}), Object.keys(e.settings ?? {}).slice(0, 8));

    const s = analyzed.json?.summary ?? {};
    report.check('the summary lists node types', (s.node_types ?? []).length > 0, s.node_types);
    report.check('the summary names the relations it read', ['orders', 'order_items', 'products'].every((r) => (s.relations ?? []).includes(r)), s.relations);
    report.check('the summary counts the nodes it walked', s.node_count === (s.nodes ?? []).length, `${s.node_count} vs ${(s.nodes ?? []).length}`);
    report.check('the raw plan is reported verbatim', analyzed.json?.plan !== null && typeof analyzed.json?.plan === 'object', typeof analyzed.json?.plan);
    report.check('the statement is echoed', String(analyzed.json?.statement ?? '').includes('order_items'), analyzed.json?.statement?.slice(0, 40));

    report.record('engine', e.engine_version);
    report.record('scale_factor', String(e.scale_factor));
    report.record('relations', (s.relations ?? []).join(', '));
    report.record('node types', (s.node_types ?? []).join(', '));
    report.record('indexes used', (s.indexes_used ?? []).join(', ') || '(none)');
    report.record('plan_rows', String(s.plan_rows));

    /* ---------- clause 1: reproducibility ---------- */
    report.section('plans are reproducible on pinned fixtures');
    for (const [field, why] of EXCLUDED_FROM_COMPARISON) report.record('excluded from comparison', `${field} — ${why}`);
    console.log(
      '\n  comparing structural facts only; excluded: ' +
        EXCLUDED_FROM_COMPARISON.map(([field]) => field).join(', '),
    );

    const runs = [];
    for (let i = 0; i < 3; i++) {
      const r = await explain({ sql: ANALYTIC_SQL, analyze: true, dataset: 'perf' }, token);
      report.check(`run ${i + 1} succeeds`, r.json?.status === 'succeeded', r.json?.error);
      runs.push(r.json);
    }
    const fingerprints = runs.map(structuralFingerprint);
    const distinct = new Set(fingerprints);
    report.check('three runs of one statement produce one structural plan', distinct.size === 1, `${distinct.size} distinct plan(s)`);
    if (distinct.size > 1) {
      for (const [i, fp] of fingerprints.entries()) console.log(`    run ${i + 1}: ${fp.slice(0, 400)}`);
    }
    const timings = runs.map((r) => r?.duration_ms);
    report.check('the excluded timings are nevertheless reported', timings.every((t) => typeof t === 'number' && t >= 0), timings);
    console.log(`  observed duration_ms across the three runs: ${timings.join(', ')} — reported, never compared`);

    /* ---------- clause 3: precision is never overstated ---------- */
    report.section('timing precision is reported, never overstated');
    const ALLOWED = ['analyze', 'estimate', 'none'];
    report.check('every bundle names a known precision', seen.every((x) => ALLOWED.includes(x.bundle?.timing_precision)), seen.map((x) => x.bundle?.timing_precision));
    report.check(
      "an 'estimate' bundle carries no measured timing anywhere",
      seen.filter((x) => x.bundle?.timing_precision === 'estimate').every((x) => (x.bundle.summary?.nodes ?? []).every((n) => n.actual_time_ms === null)),
    );
    report.check(
      "an 'analyze' bundle carries at least one measured node",
      seen.filter((x) => x.bundle?.timing_precision === 'analyze').every((x) => (x.bundle.summary?.nodes ?? []).some((n) => typeof n.actual_time_ms === 'number')),
    );
    report.check(
      "a 'none' bundle carries no plan and no numbers",
      seen.filter((x) => x.bundle?.timing_precision === 'none').every((x) => x.bundle.plan === null && (x.bundle.summary?.nodes ?? []).length === 0),
    );

    /* ---------- clause 4b: a statement that cannot be planned ---------- */
    report.section('a plan that cannot be produced is refused, not invented');
    const broken = await explain({ sql: 'SELECT FROM WHERE;', analyze: false });
    report.check('a broken statement is a failed bundle, not a crash', broken.json?.status === 'failed', broken.json?.status);
    report.check('it carries a stable code from the shared taxonomy', ['QUERY_SYNTAX_ERROR', 'QUERY_RUNTIME_ERROR'].includes(broken.json?.error?.code), broken.json?.error);
    report.check('it has no plan and no numbers', broken.json?.plan === null && broken.json?.timing_precision === 'none', broken.json?.timing_precision);
    report.check('the refusal has no schema violations', violations(broken.json).length === 0, violations(broken.json));

    const sqlite = await explain({ sql: INDEX_SQL, analyze: false, exercise: { perf: { engine: 'sqlite', require_nodes: [] } } }, token);
    report.check('a SQLite exercise is refused on the PostgreSQL plan lab', sqlite.status === 400 && sqlite.json?.status === 'failed', sqlite.status);
    report.check('the refusal names the dialect the lab can plan', /postgresql/i.test(sqlite.json?.error?.message ?? ''), sqlite.json?.error?.message);

    /* ---------- clause 4c: no database, no invention ---------- */
    report.section('with no database the plan lab invents nothing');
    const degraded = await startGateway(bin, { ...env, no_local_db: true });
    try {
      const dCaps = await getJSON(`${degraded.base}/capabilities`);
      report.check('capabilities says the lab is unavailable', dCaps.json?.plan?.available === false, dCaps.json?.plan);
      report.check('and gives the reason', String(dCaps.json?.plan?.reason ?? '').length > 0, dCaps.json?.plan?.reason);
      const dRes = await postJSON(`${degraded.base}/explain`, { sql: 'SELECT 1;', analyze: false });
      report.check('a plan request is refused', dRes.status === 400, dRes.status);
      report.check('with ENVIRONMENT_UNAVAILABLE', dRes.json?.error?.code === 'ENVIRONMENT_UNAVAILABLE', dRes.json?.error);
      report.check('and no plan body', dRes.json?.plan === null && dRes.json?.status === 'failed', dRes.json?.status);
      report.check('the refusal has no schema violations', violations(dRes.json).length === 0, violations(dRes.json));
    } finally {
      stopGateway(degraded.child);
    }

    /* ---------- the fact table ---------- */
    report.section('measured');
    console.log('\n' + report.markdownTable(['fact', 'value']));
  } finally {
    stopGateway(gw.child);
  }

  report.finish();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
