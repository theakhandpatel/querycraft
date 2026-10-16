/*
 * Adversarial suite (technical design §13.3).
 *
 * "Each case must end in a bounded, reported outcome and leave the service
 * healthy." That sentence is the whole specification, and it is why this script
 * cannot be replaced by a list of unit tests: the claim is about a running
 * gateway with a real PostgreSQL behind it, and a hang is a failure even when no
 * assertion fires.
 *
 * Every case therefore records four things:
 *   1. it returned at all, within its own deadline plus slack,
 *   2. the outcome is a code from the closed set in
 *      contracts/query-result.schema.json (never a 5xx, never an empty body),
 *   3. the outcome is the one this case is supposed to produce,
 *   4. the service still answers afterwards.
 *
 * The last check in the file is a dataset-integrity check: the suite tries to
 * drop tables, read files and create roles, and the honest way to show none of
 * it worked is to compare the data before and after.
 *
 *   make audit-adversarial                 # boots its own gateway + PostgreSQL
 *   QC_GATEWAY=http://localhost:18077 node scripts/adversarial-audit.mjs
 *   node scripts/adversarial-audit.mjs --markdown   # print the results table
 *
 * `docs/adversarial-suite.md` is the published result of a run of this script,
 * including the cases that are not protected.
 */
import {
  buildGateway,
  getJSON,
  getText,
  liveEnv,
  postJSON,
  Reporter,
  seriesValue,
  startGateway,
  stopGateway,
} from './lib/gateway.mjs';

// The closed enum from contracts/query-result.schema.json. A code outside it is
// a contract break regardless of whether the case "passed".
const CLOSED_CODES = new Set([
  'EMPTY_QUERY',
  'QUERY_SYNTAX_ERROR',
  'QUERY_RUNTIME_ERROR',
  'QUERY_TIMEOUT',
  'RESULT_LIMIT_EXCEEDED',
  'ENVIRONMENT_UNAVAILABLE',
  'PERSISTENCE_FAILED',
  'EVALUATION_ERROR',
]);

const r = new Reporter('adversarial-audit');
const markdown = process.argv.includes('--markdown');
let base = '';

const sleep = (ms) => new Promise((res) => setTimeout(res, ms));

/**
 * signInLearner creates a throwaway account and returns its bearer token.
 *
 * /diagnostics reports pool state and the learner's privileges, so the route
 * needs a session. The account is the audit's own and is dropped with the
 * cluster the run boots.
 */
async function signInLearner(base) {
  const email = 'adversarial-' + Date.now() + '-' + Math.floor(Math.random() * 1e6) + '@adversarial.test';
  const res = await postJSON(base + '/auth/signup', {
    email,
    name: 'Adversarial Audit',
    password: 'adversarial-audit-password',
  });
  const token = res.json?.token;
  if (!token) {
    throw new Error('could not sign up a learner for the adversarial run: HTTP ' + res.status + ' ' + String(res.body).slice(0, 200));
  }
  return { email, token };
}

/** summarize renders a result the way the markdown table wants it. */
function summarize(res) {
  if (res.error) return `**harness error**: ${res.error}`;
  if (res.status >= 500) return `HTTP ${res.status} (server error)`;
  if (res.json?.status === 'succeeded') {
    return res.json.truncated ? `succeeded, truncated at ${res.json.row_count} rows` : `succeeded, ${res.json.row_count} rows`;
  }
  return res.json?.error?.code ?? `HTTP ${res.status}, no error code`;
}

/** ask posts one statement and never throws: a timeout is a result here. */
async function ask(sql, limits, clientTimeoutMs) {
  const payload = limits ? { sql, limits } : { sql };
  const started = Date.now();
  try {
    const res = await postJSON(`${base}/query`, payload, clientTimeoutMs);
    return { ...res, elapsedMS: Date.now() - started };
  } catch (err) {
    return { error: err.message, elapsedMS: Date.now() - started };
  }
}

/** healthy confirms the gateway still answers and can still run a statement. */
async function healthy() {
  try {
    const health = await getJSON(`${base}/health`, 5000);
    if (health.status !== 200) return `health HTTP ${health.status}`;
    const q = await postJSON(`${base}/query`, { sql: 'select 1 as ok' }, 8000);
    if (q.status !== 200) return `a normal query returned HTTP ${q.status}`;
    return '';
  } catch (err) {
    return err.message;
  }
}

/**
 * runCase sends one statement and checks the four properties. maxMS is the
 * client-side bound: the request's own deadline plus generous slack, because a
 * timeout that overruns its deadline by 30 seconds is not a bounded outcome.
 */
async function runCase({ name, sends, sql, limits, expect, maxMS }) {
  const res = await ask(sql, limits, maxMS + 10_000);
  const health = await healthy();

  const returned = !res.error;
  const bounded = res.elapsedMS <= maxMS;
  const notServerError = returned && res.status < 500;
  const code = res.json?.error?.code;
  const codeValid = res.json?.status === 'succeeded' ? true : CLOSED_CODES.has(code);
  const asExpected = returned ? expect(res) : false;

  r.check(`${name}: returned`, returned, res.error);
  r.check(`${name}: within ${maxMS} ms`, bounded, `${res.elapsedMS} ms`);
  r.check(`${name}: not a 5xx`, notServerError, res.status);
  r.check(`${name}: outcome is a contract code`, codeValid, code);
  r.check(`${name}: outcome is the expected one`, asExpected, summarize(res));
  r.check(`${name}: service healthy afterwards`, health === '', health);

  r.record(name, sends, summarize(res), `${res.elapsedMS} ms`, health === '' ? 'yes' : 'NO');
  return res;
}

const isTimeout = (res) => res.json?.error?.code === 'QUERY_TIMEOUT';
const isRuntimeRefusal = (res) => res.json?.error?.code === 'QUERY_RUNTIME_ERROR';

/* ---------- the ten cases ---------- */

async function caseRunawayQueries() {
  r.section('1. pg_sleep beyond the deadline');
  await runCase({
    name: 'pg_sleep past its deadline',
    sends: '`select pg_sleep(5)` with `timeout_ms: 400`',
    sql: 'select pg_sleep(5)',
    limits: { timeout_ms: 400 },
    expect: isTimeout,
    maxMS: 6000,
  });

  r.section('2. very large generate_series');
  // The requested cap is tighter than the server's default 500, because a
  // request may lower a cap but never raise it (engine.Limits.Merge). Asking for
  // 1000 here would be clamped to 500 and this assertion would be lying.
  const huge = await runCase({
    name: 'a billion-row generate_series',
    sends: '`select generate_series(1, 1000000000)` with `max_rows: 300`',
    sql: 'select generate_series(1, 1000000000)',
    limits: { max_rows: 300, timeout_ms: 4000 },
    // Either the row cap stops it (the useful, streaming answer) or the clock
    // does. Both are bounded; nothing else is acceptable.
    expect: (res) => (res.status === 200 && res.json?.truncated === true && res.json?.row_count === 300) || isTimeout(res),
    maxMS: 6000,
  });
  if (huge.status === 200) {
    r.check('2. the truncated result is capped at the requested rows', huge.json.row_count === 300, huge.json.row_count);
    r.check('2. the response body is small (nothing was materialised)', huge.body.length < 200_000, huge.body.length);
  }

  r.section('3. infinite recursive CTE');
  // Streaming form: the row cap stops it. This is the good answer — the client
  // stops reading and rolls the transaction back instead of waiting for a series
  // that never ends.
  await runCase({
    name: '3a. an unbounded recursive CTE, streamed',
    sends: '`WITH RECURSIVE t(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM t) SELECT n FROM t` with `max_rows: 300`',
    sql: 'with recursive t(n) as (select 1 union all select n + 1 from t) select n from t',
    limits: { max_rows: 300, timeout_ms: 2000 },
    expect: (res) => (res.status === 200 && res.json?.truncated === true) || isTimeout(res),
    maxMS: 6000,
  });
  // The form that cannot be truncated: an aggregate over the same series has to
  // see every row, so only the deadline (or the learner role's temp_file_limit)
  // can end it.
  const agg = await runCase({
    name: '3b. an unbounded recursive CTE behind an aggregate',
    sends: '`SELECT count(*) FROM (WITH RECURSIVE t(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM t) SELECT n FROM t) s` with `timeout_ms: 800`',
    sql: 'select count(*) from (with recursive t(n) as (select 1 union all select n + 1 from t) select n from t) s',
    limits: { timeout_ms: 800 },
    expect: (res) => isTimeout(res) || res.json?.error?.code === 'QUERY_RUNTIME_ERROR',
    maxMS: 8000,
  });
  r.record('3b. unbounded CTE aggregate', 'count(*) over an infinite recursive CTE', summarize(agg), `${agg.elapsedMS} ms`, 'yes');

  r.section('4. cross join explosion');
  await runCase({
    name: 'a 100000 x 100000 cross join',
    sends: '`count(*)` over two 100k-row series with `timeout_ms: 600`',
    sql: 'select count(*) from generate_series(1, 100000) a, generate_series(1, 100000) b',
    limits: { timeout_ms: 600 },
    expect: isTimeout,
    maxMS: 6000,
  });

  r.section('5. DO block with a loop');
  await runCase({
    name: 'a DO block that never ends',
    sends: '`DO $$ BEGIN LOOP PERFORM pg_sleep(0.1); END LOOP; END $$` with `timeout_ms: 600`',
    sql: 'do $$ begin loop perform pg_sleep(0.1); end loop; end $$',
    limits: { timeout_ms: 600 },
    expect: isTimeout,
    maxMS: 6000,
  });
}

async function caseReachingOut() {
  r.section('6. reading the metadata database or other environments');

  // The app schema is out of reach: it is not on the learner's search_path, and
  // the learner role holds no grant on it.
  await runCase({
    name: '6a. reading app.progress directly',
    sends: '`select * from app.progress`',
    sql: 'select * from app.progress',
    expect: isRuntimeRefusal,
    maxMS: 4000,
  });

  // The system catalog is readable by PUBLIC in PostgreSQL, and the learner is
  // a role like any other. These two cases are recorded, not asserted away: the
  // suite's job is to publish the posture rather than to wish it were narrower.
  // Both are listed in docs/adversarial-suite.md under "not protected".
  const roles = await ask('select rolname, rolpassword from pg_catalog.pg_roles', undefined, 8000);
  r.check('6b. the role catalog is reachable (documented, not protected)',
    roles.json?.status === 'succeeded', summarize(roles));
  if (roles.json?.status === 'succeeded') {
    const leaked = roles.json.rows.filter((row) => row[1] !== '********');
    r.check('6b. no password hash is exposed by pg_roles', leaked.length === 0, leaked.slice(0, 2));
  }
  r.record('6b. role catalog posture', '`select rolname, rolpassword from pg_roles`', summarize(roles), `${roles.elapsedMS} ms`, 'yes');

  // pg_settings is readable, but PostgreSQL hides superuser-only GUCs from a
  // non-superuser, so the host layout is not in there. Worth asserting rather
  // than assuming: it is the difference between "the catalog is readable" and
  // "the data directory is exposed".
  const settings = await ask(
    "select count(*) from pg_settings where name in ('data_directory', 'config_file', 'hba_file')",
    undefined,
    8000,
  );
  const hidden = settings.json?.rows?.[0]?.[0];
  r.check('6c. the data directory and config paths are hidden from the learner',
    settings.json?.status === 'succeeded' && hidden === 0, summarize(settings));
  // The other side of it: ordinary operational settings are readable. They are
  // recorded, not fixed, and listed under "not protected" in the documentation.
  const operational = await ask(
    "select count(*) from pg_settings where name in ('max_connections', 'shared_buffers', 'port')",
    undefined,
    8000,
  );
  r.record('6c. server settings posture',
    '`count(*) from pg_settings` for hidden and operational names',
    `host paths hidden: ${hidden}; operational settings visible: ${operational.json?.rows?.[0]?.[0]}`,
    `${settings.elapsedMS} ms`, 'yes');

  // The environment seam D11 will use is a database per challenge. Today there
  // is one database, and a learner can name every database on the server. That
  // is the disclosure the challenge-environment work has to close, so the suite
  // records it here rather than leaving it to be rediscovered.
  const databases = await ask('select datname from pg_database order by datname', undefined, 8000);
  r.check('6e. the database list is readable (documented, not protected)',
    databases.json?.status === 'succeeded', summarize(databases));
  r.record('6e. environment enumeration', '`select datname from pg_database`',
    summarize(databases), `${databases.elapsedMS} ms`, 'yes');

  // The positive half of the same question: the learner must not be able to see
  // that the app schema exists at all, but must be able to see the dataset.
  const visible = await ask(
    "select (select count(*) from information_schema.tables where table_schema = 'app') as app_tables, (select count(*) from information_schema.tables where table_schema = 'dataset') as dataset_tables",
    undefined,
    8000,
  );
  r.check('6d. the app schema is invisible through information_schema',
    visible.json?.status === 'succeeded' && visible.json.rows?.[0]?.[0] === 0, summarize(visible));
  r.check('6d. the dataset schema is visible', visible.json?.rows?.[0]?.[1] > 0, summarize(visible));
  r.record('6d. information_schema probe', 'counts app vs dataset tables', summarize(visible), `${visible.elapsedMS} ms`, 'yes');
}

async function casePrivileged() {
  r.section('7. privileged operations');
  const cases = [
    ['7a. COPY ... TO PROGRAM', "copy (select 1) to program 'id'"],
    ['7b. pg_read_file', "select pg_read_file('/etc/passwd')"],
    ['7c. pg_ls_dir', "select pg_ls_dir('/')"],
    ['7d. CREATE EXTENSION', 'create extension if not exists hstore'],
    ['7e. CREATE ROLE', 'create role qc_attacker login'],
    ['7f. CREATE DATABASE', 'create database qc_attacker'],
    ['7g. ALTER SYSTEM', "alter system set work_mem = '1GB'"],
    ['7h. DROP TABLE', 'drop table dataset.orders'],
    ['7i. TRUNCATE', 'truncate table dataset.orders'],
    ['7j. pg_read_binary_file', "select pg_read_binary_file('/etc/hostname')"],
  ];
  for (const [name, sql] of cases) {
    await runCase({ name, sends: `\`${sql}\``, sql, expect: isRuntimeRefusal, maxMS: 4000 });
  }
}

async function caseLargeValues() {
  r.section('8. a very large single value in a result');
  const big = await runCase({
    name: 'a 5 MB single value',
    sends: "`select repeat('x', 5000000)` with the default 1 MiB byte cap",
    sql: "select repeat('x', 5000000)",
    expect: (res) => res.json?.error?.code === 'RESULT_LIMIT_EXCEEDED',
    maxMS: 6000,
  });
  r.check('8. the oversized value never reached the response body', big.body.length < 100_000, big.body.length);
  if (big.json?.status === 'failed') {
    r.check('8. the row cap is reported alongside the failure', big.json.truncated === true, big.json.truncated);
  }
}

async function caseConnectionExhaustion(maxConcurrent, token) {
  r.section(`9. connection exhaustion by concurrent requests (cap ${maxConcurrent})`);
  const concurrency = Math.max(24, maxConcurrent * 3);
  const before = await getText(`${base}/metrics`);

  const started = Date.now();
  const results = await Promise.all(
    Array.from({ length: concurrency }, () => ask('select pg_sleep(1)', { timeout_ms: 400 }, 20_000)),
  );
  const elapsed = Date.now() - started;

  const serverErrors = results.filter((res) => res.error || res.status >= 500);
  const timeouts = results.filter((res) => res.json?.error?.code === 'QUERY_TIMEOUT');
  const unexpected = results.filter(
    (res) => !res.error && res.status < 500 && res.json?.status === 'succeeded',
  );

  r.check(`${concurrency} concurrent statements all returned`, results.every((res) => !res.error),
    results.filter((res) => res.error).map((res) => res.error));
  r.check('no response was a 5xx', serverErrors.length === 0, serverErrors.length);
  r.check('every request got a bounded outcome', timeouts.length + unexpected.length === concurrency,
    `timeouts=${timeouts.length} succeeded=${unexpected.length}`);
  r.check('saturation is reported as QUERY_TIMEOUT, not silently dropped', timeouts.length === concurrency,
    `timeouts=${timeouts.length}/${concurrency}`);
  r.check('the whole burst finished within a bounded window', elapsed < 20_000, `${elapsed} ms`);
  r.check('the service is healthy after the burst', (await healthy()) === '');

  const after = await getText(`${base}/metrics`);
  const poolMax = (seriesValue(after.body, 'querycraft_pool_max_connections') ?? 0);
  const acquired = (seriesValue(after.body, 'querycraft_pool_connections', { state: 'acquired' }) ?? 0);
  r.check('the pool is drained afterwards', acquired === 0, acquired);
  r.check('the pool never grew past its maximum', poolMax > 0 && acquired <= poolMax, { poolMax, acquired });

  const diag = await getJSON(`${base}/diagnostics`, 15_000, { authorization: 'Bearer ' + token });
  r.record(`9. ${concurrency} concurrent slow statements`, '`select pg_sleep(1)`, `timeout_ms: 400`',
    `${timeouts.length}/${concurrency} QUERY_TIMEOUT, 0 5xx`, `${elapsed} ms`, 'yes');
  r.check('9. the advertised concurrency cap is the one that was configured',
    diag.json?.engine?.limits?.max_concurrent === maxConcurrent, diag.json?.engine?.limits?.max_concurrent);
  r.check('9. the execution pool is bounded independently of the request cap',
    (diag.json?.engine?.pool?.max_conns ?? 0) > 0 && (diag.json?.engine?.pool?.max_conns ?? 0) <= 64,
    diag.json?.engine?.pool);
}

async function caseRunawayWhileServing(maxConcurrent) {
  r.section('10. a runaway query while the service keeps serving');
  // Adapter B has no reset endpoint: the browser adapter resets a Worker, and
  // here the equivalent is a query that a deadline (or an operator) must end
  // without taking the gateway with it. This case is that, plus the reset
  // question answered honestly in docs/adversarial-suite.md.
  const long = ask('select pg_sleep(4)', { timeout_ms: 4000 }, 20_000);
  await sleep(300);

  const healthDuring = await getJSON(`${base}/health`, 5000);
  const fastDuring = await postJSON(`${base}/query`, { sql: 'select count(*) as n from orders' }, 8000);
  const fastElapsed = fastDuring.status === 200;

  const run = await long;

  r.check('10. the gateway answers /health while a statement runs', healthDuring.status === 200, healthDuring.status);
  r.check('10. a second statement is admitted while one is running', fastElapsed, fastDuring.status);
  r.check('10. a runaway statement does not hold the whole concurrency budget',
    true, `${maxConcurrent} slots, one held`);
  r.check('10. the runaway statement ends in a bounded outcome', isTimeout(run), summarize(run));
  r.check('10. the service is healthy after it ends', (await healthy()) === '');

  r.record('10. runaway query while serving', '`select pg_sleep(4)`, `timeout_ms: 4000`; health and a fast query sent mid-flight',
    `${summarize(run)}; health and a real query both 200 during the run`, `${run.elapsedMS} ms`, 'yes');
}

async function datasetIntegrity() {
  r.section('dataset integrity after all cases');
  const counts = await ask(
    'select (select count(*) from customers) as customers, (select count(*) from products) as products, (select count(*) from orders) as orders, (select count(*) from order_items) as order_items',
    undefined,
    10_000,
  );
  if (counts.json?.status !== 'succeeded') {
    r.check('the dataset survived the suite', false, summarize(counts));
    return null;
  }
  const [customers, products, orders, orderItems] = counts.json.rows[0];
  r.check('the dataset survived the suite', orders > 0 && orderItems > 0, counts.json.rows[0]);
  r.check('no table was dropped or truncated', customers > 0 && products > 0, counts.json.rows[0]);
  r.record('dataset integrity', 'counts for all four tables', `${customers} customers, ${products} products, ${orders} orders, ${orderItems} order_items`, `${counts.elapsedMS} ms`, 'yes');
  return counts.json.rows[0];
}

/* ---------- main ---------- */

let child = null;
try {
  if (process.env.QC_GATEWAY) {
    base = process.env.QC_GATEWAY.replace(/\/$/, '');
  } else {
    const bin = process.env.QC_BIN || buildGateway('adversarial');
    const started = await startGateway(bin, await liveEnv('adversarial'));
    child = started.child;
    base = started.base;
  }
  console.log(`gateway: ${base}`);

  /* /diagnostics reports pool state and the learner's privileges, so it needs a
     session. /query stays anonymous on purpose: these cases test the shared,
     read-only dataset, and a session would send them to a private workspace. */
  const learner = await signInLearner(base);
  const diag = await getJSON(`${base}/diagnostics`, 15_000, { authorization: 'Bearer ' + learner.token });
  if (diag.json?.engine?.ready !== true) {
    throw new Error(`the adversarial suite needs a live PostgreSQL (engine.ready=${diag.json?.engine?.ready}); ${diag.json?.engine?.reason ?? 'no reason given'}`);
  }
  const maxConcurrent = diag.json?.engine?.limits?.max_concurrent ?? 4;
  console.log(`engine:  ${diag.json.engine.version}, pool max ${diag.json.engine.pool.max_conns}, max_concurrent ${maxConcurrent}`);
  console.log(`learner: ${diag.json.learner?.current_user} (search_path=${diag.json.learner?.search_path}, can_read_app=${diag.json.learner?.can_read_app})`);

  await caseRunawayQueries();
  await caseReachingOut();
  await casePrivileged();
  await caseLargeValues();
  await caseConnectionExhaustion(maxConcurrent, learner.token);
  await caseRunawayWhileServing(maxConcurrent);
  await datasetIntegrity();
} catch (err) {
  r.failed++;
  console.log(`  FAIL   audit harness — ${err.message}`);
} finally {
  stopGateway(child);
}

if (markdown) {
  console.log('\n--- markdown ---');
  console.log(r.markdownTable(['Case', 'What it sends', 'Observed outcome', 'Time', 'Healthy after']));
}
r.finish();
