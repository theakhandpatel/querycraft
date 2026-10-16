/*
 * Load test (technical design §13.2).
 *
 * "Query endpoint under concurrent load with timeouts and pool metrics
 * recorded." The harness drives N workers against POST /query, mixing a
 * representative analytic statement with a statement that is expected to hit
 * its deadline, and reports client-observed latency/throughput next to the
 * gateway's own instrumentation (/metrics, /diagnostics) so the two can be
 * compared instead of trusted.
 *
 *   node scripts/load-test.mjs
 *     Builds the gateway, boots it on a free port with its own embedded
 *     PostgreSQL (fresh data directory), runs the workload, prints the numbers
 *     and the assertions, and tears the server down by PID.
 *
 *   QC_GATEWAY=http://127.0.0.1:18077 node scripts/load-test.mjs
 *     Uses a gateway someone else started (nothing is built or stopped). The
 *     gateway must have a live PostgreSQL: a load test against
 *     ENVIRONMENT_UNAVAILABLE measures the 400 path, not the engine.
 *
 * Environment: QC_GO (default `go`), QC_BACKEND (default `backend`),
 * QC_BIN (an already-built gateway binary), QC_GATEWAY, QC_MAX_CONCURRENT
 * (passed through when booting), QC_LOAD_WORKERS (8), QC_LOAD_REQUESTS (40 per
 * worker) or QC_LOAD_DURATION (seconds), QC_LOAD_TIMEOUT_SHARE (0.1),
 * QC_LOAD_SATURATION_SLEEP_MS (800).
 */
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..');
const goBin = process.env.QC_GO || 'go';
const backendDir = process.env.QC_BACKEND
  ? path.resolve(process.env.QC_BACKEND)
  : path.join(repoRoot, 'backend');

function num(name, dflt) {
  const raw = process.env[name];
  if (raw === undefined || raw === '') return dflt;
  const v = Number(raw);
  if (!Number.isFinite(v)) throw new Error(`${name} must be a number, got ${JSON.stringify(raw)}`);
  return v;
}

const WORKERS = Math.max(1, Math.trunc(num('QC_LOAD_WORKERS', 8)));
const DURATION_S = num('QC_LOAD_DURATION', 0);
const REQUESTS_PER_WORKER = Math.max(1, Math.trunc(num('QC_LOAD_REQUESTS', 40)));
const TIMEOUT_SHARE = Math.min(1, Math.max(0, num('QC_LOAD_TIMEOUT_SHARE', 0.1)));
const MAX_CONCURRENT = process.env.QC_MAX_CONCURRENT || '';
const SATURATION_SLEEP_MS = Math.trunc(num('QC_LOAD_SATURATION_SLEEP_MS', 800));

// A representative analytic statement: three tables, a join, an aggregate, a
// HAVING and an ORDER BY. Cheap (~1 ms on this dataset) but not `SELECT 1`: a
// trivial statement would measure the HTTP layer and call it the engine.
const ANALYTIC_SQL = [
  'SELECT c.city, count(*) AS line_items, round(avg(oi.unit_price), 2) AS avg_price',
  'FROM customers c',
  'JOIN orders o ON o.customer_id = c.id',
  'JOIN order_items oi ON oi.order_id = o.id',
  'GROUP BY c.city',
  'HAVING count(*) >= 2',
  'ORDER BY line_items DESC',
].join(' ');

// A statement that cannot finish inside its deadline: pg_sleep runs in the
// server, so the row/byte caps never see it and only statement_timeout (or the
// client deadline) can end it. This is the §13.3 "pg_sleep beyond the deadline"
// case measured under load.
const TIMEOUT_SQL = 'SELECT pg_sleep(1)';
const TIMEOUT_REQUEST_MS = 120;

// The closed error-code enum from contracts/query-result.schema.json. A response
// that fails must name one of these; anything else is an unreported outcome.
const CONTRACT_CODES = [
  'EMPTY_QUERY', 'QUERY_SYNTAX_ERROR', 'QUERY_RUNTIME_ERROR', 'QUERY_TIMEOUT',
  'RESULT_LIMIT_EXCEEDED', 'ENVIRONMENT_UNAVAILABLE', 'PERSISTENCE_FAILED', 'EVALUATION_ERROR',
];

let failed = 0;
let skipped = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}
function skip(name, why) {
  skipped++;
  console.log(`  skip   ${name} — ${why}`);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/* ---------- gateway lifecycle (same shape as scripts/metrics-audit.mjs) ---------- */

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const port = srv.address().port;
      srv.close(() => resolve(port));
    });
  });
}

function buildGateway(dir) {
  const bin = path.join(dir, 'querycraft');
  fs.mkdirSync(path.dirname(bin), { recursive: true });
  const out = spawnSync(goBin, ['build', '-o', bin, './cmd/api'], {
    cwd: backendDir,
    env: { ...process.env, GOTOOLCHAIN: 'local' },
    encoding: 'utf8',
  });
  if (out.status !== 0) {
    console.error(out.stderr || out.stdout);
    throw new Error('could not build the gateway for the load test');
  }
  return bin;
}

async function startGateway(bin, config) {
  const port = await freePort();
  const cfgFile = path.join(tmpRoot, `gateway-${Date.now()}-${process.pid}.json`);
  fs.writeFileSync(cfgFile, JSON.stringify({ addr: `127.0.0.1:${port}`, ...config }, null, 2));
  const child = spawn(bin, ['--config', cfgFile], {
    env: { ...process.env, TMPDIR: tmpRoot },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let log = '';
  child.stdout.on('data', (d) => { log += d.toString(); });
  child.stderr.on('data', (d) => { log += d.toString(); });

  const base = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`gateway exited with ${child.exitCode} before becoming ready:\n${log}`);
    }
    try {
      const res = await fetch(`${base}/health`);
      if (res.ok) return { child, base, port, log: () => log };
    } catch { /* not up yet */ }
    await sleep(200);
  }
  child.kill('SIGKILL');
  throw new Error(`gateway never became ready:\n${log}`);
}

// Never pkill: SIGTERM the exact pid, then its process group if it made one, and
// wait for the process to actually leave before reporting.
async function stopGateway(child, timeoutMs = 20_000) {
  if (!child || child.exitCode !== null) return 'not-running';
  const exited = new Promise((r) => child.once('exit', r));
  try { process.kill(child.pid, 'SIGTERM'); } catch { /* already gone */ }
  try { process.kill(-child.pid, 'SIGTERM'); } catch { /* no group */ }
  const res = await Promise.race([exited, sleep(timeoutMs).then(() => 'timeout')]);
  if (res !== 'timeout') return 'stopped';
  try { process.kill(child.pid, 'SIGKILL'); } catch { /* gone */ }
  try { process.kill(-child.pid, 'SIGKILL'); } catch { /* no group */ }
  await Promise.race([exited, sleep(5000)]);
  return 'killed';
}

function listeningPorts() {
  const out = spawnSync('ss', ['-ltn'], { encoding: 'utf8' });
  return out.stdout || '';
}
function isListening(text, port) {
  return new RegExp(`:${port}\\b`).test(text);
}

/* ---------- http + prometheus helpers ---------- */

async function getText(url) {
  const res = await fetch(url);
  return { status: res.status, contentType: res.headers.get('content-type') || '', body: await res.text() };
}
async function getJSON(url, headers = {}) {
  const res = await fetch(url, { headers });
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* leave null */ }
  return { status: res.status, json, body: text };
}

// signInLearner creates a throwaway account and returns its bearer token.
// /diagnostics reports pool state and the learner's privileges, so the route
// needs a session. POST /query stays anonymous on purpose: the workload
// measures the shared, read-only dataset, and a session would send it to a
// private workspace.
async function signInLearner(base) {
  const email = `load-${Date.now()}-${Math.floor(Math.random() * 1e6)}@load.test`;
  const res = await fetch(`${base}/auth/signup`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email, name: 'Load Test', password: 'load-test-password' }),
  });
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* not JSON */ }
  const token = json?.token;
  if (!token) {
    throw new Error(`could not sign up a learner for the load test: HTTP ${res.status} ${text.slice(0, 200)}`);
  }
  return { email, token };
}

// parseSeries turns Prometheus text into { name, labels, value } records.
function parseSeries(body) {
  const out = [];
  for (const line of body.split('\n')) {
    if (!line || line.startsWith('#')) continue;
    const space = line.lastIndexOf(' ');
    if (space < 0) continue;
    const value = Number(line.slice(space + 1));
    if (!Number.isFinite(value)) continue;
    const brace = line.indexOf('{');
    const name = brace === -1 ? line.slice(0, space) : line.slice(0, brace);
    const labels = new Map();
    if (brace !== -1) {
      const end = line.indexOf('}', brace);
      if (end === -1 || end > space) continue;
      for (const pair of line.slice(brace + 1, end).split(',')) {
        if (!pair) continue;
        const eq = pair.indexOf('=');
        if (eq < 0) continue;
        labels.set(pair.slice(0, eq).trim(), pair.slice(eq + 1).trim().replace(/^"|"$/g, ''));
      }
    }
    out.push({ name, labels, value });
  }
  return out;
}
function matches(labels, filter) {
  for (const [k, v] of Object.entries(filter)) {
    // An empty filter value means "this label must be absent", which is how the
    // registry itself models an unset label.
    if (v === '') { if (labels.has(k)) return false; }
    else if (labels.get(k) !== v) return false;
  }
  return true;
}
function seriesValue(body, metric, filter = {}) {
  for (const s of parseSeries(body)) {
    if (s.name === metric && matches(s.labels, filter)) return s.value;
  }
  return null;
}
function sumSeries(body, metric, filter = {}) {
  let sum = 0;
  let found = false;
  for (const s of parseSeries(body)) {
    if (s.name === metric && matches(s.labels, filter)) { sum += s.value; found = true; }
  }
  return found ? sum : null;
}
// labelTotals groups a metric's values by one label, honouring a filter.
function labelTotals(body, metric, label, filter = {}) {
  const m = new Map();
  for (const s of parseSeries(body)) {
    if (s.name !== metric || !matches(s.labels, filter)) continue;
    const v = s.labels.get(label) ?? '';
    m.set(v, (m.get(v) ?? 0) + s.value);
  }
  return m;
}
function deltaMap(before, after) {
  const out = new Map();
  for (const [k, v] of after) out.set(k, v - (before.get(k) ?? 0));
  return out;
}
function fmtMap(m) {
  return [...m.entries()].map(([k, v]) => `${k || '(none)'}=${v}`).join(' ');
}

function percentile(sorted, q) {
  if (!sorted.length) return 0;
  const idx = Math.min(sorted.length - 1, Math.max(0, Math.ceil(q * sorted.length) - 1));
  return sorted[idx];
}
const ms = (n) => (Math.round(n * 10) / 10).toFixed(1);

/* ---------- workload ---------- */

async function driveLoad(base) {
  const outcomes = [];
  let issued = 0;
  let inflight = 0;
  let maxInflight = 0;
  const timeoutEvery = TIMEOUT_SHARE > 0 ? Math.max(1, Math.round(1 / TIMEOUT_SHARE)) : 0;
  const startedAt = Date.now();
  const deadline = DURATION_S > 0 ? startedAt + DURATION_S * 1000 : Infinity;

  async function request() {
    const index = issued++;
    const isTimeout = timeoutEvery > 0 && index % timeoutEvery === timeoutEvery - 1;
    const payload = isTimeout
      ? { sql: TIMEOUT_SQL, limits: { timeout_ms: TIMEOUT_REQUEST_MS } }
      : { sql: ANALYTIC_SQL };
    const start = performance.now();
    inflight++;
    if (inflight > maxInflight) maxInflight = inflight;
    let outcome;
    try {
      const res = await fetch(`${base}/query`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const text = await res.text();
      let json = null;
      try { json = JSON.parse(text); } catch { /* not JSON */ }
      outcome = {
        index,
        kind: isTimeout ? 'timeout' : 'analytic',
        status: res.status,
        code: json?.error?.code ?? '',
        message: json?.error?.message ?? '',
        rowCount: json?.row_count ?? null,
        ms: performance.now() - start,
      };
    } catch (err) {
      outcome = {
        index,
        kind: isTimeout ? 'timeout' : 'analytic',
        status: 0,
        code: 'CLIENT_ERROR',
        message: String((err && err.message) || err),
        rowCount: null,
        ms: performance.now() - start,
      };
    } finally {
      inflight--;
    }
    outcomes.push(outcome);
  }

  async function worker() {
    let mine = 0;
    for (;;) {
      if (DURATION_S > 0) {
        if (Date.now() >= deadline) return;
      } else if (mine >= REQUESTS_PER_WORKER) {
        return;
      }
      mine++;
      await request();
    }
  }

  const t0 = performance.now();
  await Promise.all(Array.from({ length: WORKERS }, worker));
  const elapsedMs = performance.now() - t0;
  return { outcomes, elapsedMs, maxInflight };
}

// sampleEngine polls /diagnostics while load runs, so pool saturation is
// observed during the load rather than only before and after it.
async function sampleEngine(base, window, token) {
  const samples = [];
  while (!window.done) {
    try {
      const d = await getJSON(`${base}/diagnostics`, { authorization: 'Bearer ' + token });
      if (d.json?.engine) {
        samples.push({
          ready: d.json.engine.ready === true,
          pool: d.json.engine.pool ?? null,
          cap: d.json.engine.limits?.max_concurrent ?? null,
        });
      }
    } catch { /* the gateway is busy; a missed sample is not a failure */ }
    await sleep(60);
  }
  return samples;
}

// saturate is the direct, deterministic proof that the concurrency cap is
// enforced rather than advertised. It fills every statement slot with a
// pg_sleep that cannot finish inside the window, then asks for two more with a
// deadline far shorter than the occupancy, and requires those two to be refused
// with the cap named in the message. Without this the harness could only say
// "no request was dropped", which is not the same claim.
async function saturate(base, cap) {
  const result = { wave1: [], wave2: [] };
  const holdMs = SATURATION_SLEEP_MS;
  const wave1 = Array.from({ length: cap }, () =>
    fetch(`${base}/query`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sql: `SELECT pg_sleep(${holdMs / 1000})`, limits: { timeout_ms: 10_000 } }),
    }).then(async (res) => ({ status: res.status, json: await res.json().catch(() => null) })).catch((e) => ({ status: 0, error: String(e) })));
  await sleep(200); // let wave 1 take every slot
  const wave2 = Array.from({ length: 2 }, () =>
    fetch(`${base}/query`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sql: ANALYTIC_SQL, limits: { timeout_ms: 120 } }),
    }).then(async (res) => ({ status: res.status, json: await res.json().catch(() => null) })).catch((e) => ({ status: 0, error: String(e) })));
  result.wave2 = await Promise.all(wave2);
  result.wave1 = await Promise.all(wave1);
  return result;
}

/* ---------- main ---------- */

let child = null;
let base = process.env.QC_GATEWAY;
let pgPort = null;
let booted = false;
/* One per-run scratch root for the binary, the cluster and the embedded
   server's postgres log, removed in the finally below however the run ends. */
const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'qc-load-test-'));

try {
  if (base) {
    base = base.replace(/\/$/, '');
    console.log(`load-test: ${base} (gateway provided via QC_GATEWAY; nothing built, nothing stopped)`);
  } else {
    const bin = process.env.QC_BIN || buildGateway(tmpRoot);
    pgPort = await freePort();
    const config = {
      pg_port: pgPort,
      pg_data: path.join(tmpRoot, 'pg', 'data'),
      pg_runtime: path.join(tmpRoot, 'pg', 'runtime'),
    };
    if (MAX_CONCURRENT) config.max_concurrent = MAX_CONCURRENT;
    const started = await startGateway(bin, config);
    child = started.child;
    base = started.base;
    booted = true;
    console.log(`load-test: ${base} (harness-booted gateway, embedded PostgreSQL on ${pgPort})`);
  }

  /* /diagnostics needs a session, so the harness signs in before it reads the
     engine's own report. Signup works in both modes: a harness-booted gateway
     has a live store, and a QC_GATEWAY keeps its own. */
  const learner = await signInLearner(base);

  const before = await getJSON(`${base}/diagnostics`, { authorization: 'Bearer ' + learner.token });
  const cap = before.json?.engine?.limits?.max_concurrent ?? null;
  const ready = before.json?.engine?.ready === true;
  console.log(`engine: ready=${ready} version=${JSON.stringify(before.json?.engine?.version ?? '')} pool=${JSON.stringify(before.json?.engine?.pool)}`);
  console.log(`limits: ${fmtMap(new Map(Object.entries(before.json?.engine?.limits ?? {})))}`);
  console.log(`workload: ${WORKERS} workers × ${DURATION_S > 0 ? `${DURATION_S}s` : `${REQUESTS_PER_WORKER} requests`}, timeout share ${TIMEOUT_SHARE}${MAX_CONCURRENT ? `, QC_MAX_CONCURRENT=${MAX_CONCURRENT}` : ''}`);
  if (!ready) {
    console.log('  FAIL   the engine is not ready; a load test against ENVIRONMENT_UNAVAILABLE measures the 400 path, not the engine');
    failed++;
  }

  console.log('main load');
  const metricsBefore = (await getText(`${base}/metrics`)).body;
  const window = { done: false };
  const samplerPromise = sampleEngine(base, window, learner.token);
  const load = await driveLoad(base);
  window.done = true;
  const poolSamples = await samplerPromise;
  const metricsAfter = (await getText(`${base}/metrics`)).body;

  /* --- client-observed numbers --- */
  const sent = load.outcomes.length;
  const latencies = load.outcomes.map((o) => o.ms).sort((a, b) => a - b);
  const rps = sent / (load.elapsedMs / 1000);
  const byStatus = new Map();
  const byCode = new Map();
  for (const o of load.outcomes) {
    const key = o.status === 0 ? 'no-response' : `${Math.floor(o.status / 100)}xx`;
    byStatus.set(key, (byStatus.get(key) ?? 0) + 1);
    byCode.set(o.code || '(none)', (byCode.get(o.code || '(none)') ?? 0) + 1);
  }
  const refusedByCap = load.outcomes.filter((o) => o.status === 400 && o.code === 'QUERY_TIMEOUT' && /maximum of \d+ statements/.test(o.message));

  console.log('');
  console.log(`  requests            ${sent}`);
  console.log(`  elapsed_ms          ${ms(load.elapsedMs)}`);
  console.log(`  throughput_rps      ${ms(rps)}`);
  const analyticLat = load.outcomes.filter((o) => o.kind === 'analytic').map((o) => o.ms).sort((a, b) => a - b);
  const timeoutLat = load.outcomes.filter((o) => o.kind === 'timeout').map((o) => o.ms).sort((a, b) => a - b);
  console.log(`  latency_ms          p50=${ms(percentile(latencies, 0.5))} p95=${ms(percentile(latencies, 0.95))} p99=${ms(percentile(latencies, 0.99))} max=${ms(latencies[latencies.length - 1] ?? 0)}`);
  console.log(`  latency_ms analytic p50=${ms(percentile(analyticLat, 0.5))} p95=${ms(percentile(analyticLat, 0.95))} p99=${ms(percentile(analyticLat, 0.99))} max=${ms(analyticLat[analyticLat.length - 1] ?? 0)}`);
  console.log(`  latency_ms deadline p50=${ms(percentile(timeoutLat, 0.5))} p95=${ms(percentile(timeoutLat, 0.95))} max=${ms(timeoutLat[timeoutLat.length - 1] ?? 0)}`);
  console.log(`  outcomes            ${fmtMap(byStatus)}`);
  console.log(`  error codes         ${fmtMap(byCode)}`);
  console.log(`  client in flight    peak ${load.maxInflight} (worker-bounded, not the server cap ${cap})`);
  console.log(`  saturation refusals ${refusedByCap.length}`);
  console.log('');

  /* --- gateway instrumentation --- */
  const reqBefore = labelTotals(metricsBefore, 'querycraft_requests_total', 'status', { route: '/query', method: 'POST' });
  const reqAfter = labelTotals(metricsAfter, 'querycraft_requests_total', 'status', { route: '/query', method: 'POST' });
  const reqDelta = deltaMap(reqBefore, reqAfter);
  const codeBefore = labelTotals(metricsBefore, 'querycraft_statements_total', 'code');
  const codeAfter = labelTotals(metricsAfter, 'querycraft_statements_total', 'code');
  const codeDelta = deltaMap(codeBefore, codeAfter);
  const histCountBefore = sumSeries(metricsBefore, 'querycraft_request_duration_seconds_count', { method: 'POST', route: '/query' }) ?? 0;
  const histCountAfter = sumSeries(metricsAfter, 'querycraft_request_duration_seconds_count', { method: 'POST', route: '/query' }) ?? 0;
  const histSumBefore = sumSeries(metricsBefore, 'querycraft_request_duration_seconds_sum', { method: 'POST', route: '/query' }) ?? 0;
  const histSumAfter = sumSeries(metricsAfter, 'querycraft_request_duration_seconds_sum', { method: 'POST', route: '/query' }) ?? 0;
  const histCountDelta = histCountAfter - histCountBefore;

  console.log('gateway instrumentation (POST /query deltas across the load)');
  console.log(`  requests_total      ${fmtMap(reqDelta)}`);
  console.log(`  statements_total    ${fmtMap(codeDelta)}`);
  console.log(`  histogram           count +${histCountDelta} sum +${(histSumAfter - histSumBefore).toFixed(3)}s`);
  console.log(`  pool max_conns      ${seriesValue(metricsAfter, 'querycraft_pool_max_connections')}`);
  for (const state of ['total', 'idle', 'acquired', 'constructing']) {
    const v = seriesValue(metricsAfter, 'querycraft_pool_connections', { state });
    if (v !== null) console.log(`  pool ${state.padEnd(12)} ${v}`);
  }
  const sampledAcquired = poolSamples.map((s) => s.pool?.acquired_conns ?? 0);
  // max_conns is a constant of the pool, so it is the same in a sample as in the
  // scrape; falling back to the scrape keeps a very short load from reporting 0.
  const scrapedMaxConns = seriesValue(metricsAfter, 'querycraft_pool_max_connections') ?? 0;
  const sampledMaxConns = poolSamples.map((s) => s.pool?.max_conns ?? scrapedMaxConns);
  const poolMaxConns = Math.max(scrapedMaxConns, ...sampledMaxConns);
  const peakAcquired = sampledAcquired.length ? Math.max(...sampledAcquired) : 0;
  console.log(`  pool samples        ${poolSamples.length} over the load; acquired peak ${peakAcquired}, max_conns ${poolMaxConns}`);
  console.log('');

  /* --- assertions --- */
  console.log('outcomes are bounded and reported');
  check('every request received a response', load.outcomes.every((o) => o.status !== 0),
    load.outcomes.filter((o) => o.status === 0).slice(0, 3));
  check('no response was a 5xx', load.outcomes.every((o) => o.status < 500),
    load.outcomes.filter((o) => o.status >= 500).slice(0, 3));
  check('every failed response names a code from the contract enum',
    load.outcomes.filter((o) => o.status !== 200).every((o) => CONTRACT_CODES.includes(o.code)),
    [...new Set(load.outcomes.filter((o) => o.status !== 200).map((o) => o.code))]);
  check('every analytic statement succeeded',
    load.outcomes.filter((o) => o.kind === 'analytic').every((o) => o.status === 200),
    load.outcomes.filter((o) => o.kind === 'analytic' && o.status !== 200).slice(0, 3));
  check('the analytic statement returned rows',
    load.outcomes.filter((o) => o.kind === 'analytic').every((o) => (o.rowCount ?? 0) > 0),
    load.outcomes.filter((o) => o.kind === 'analytic' && !((o.rowCount ?? 0) > 0)).slice(0, 3));
  const timeoutEvery = TIMEOUT_SHARE > 0 ? Math.max(1, Math.round(1 / TIMEOUT_SHARE)) : 0;
  if (timeoutEvery === 0) {
    skip('at least one deadline statement ran', 'QC_LOAD_TIMEOUT_SHARE=0');
  } else if (load.outcomes.length < timeoutEvery) {
    skip('at least one deadline statement ran', `${load.outcomes.length} requests is fewer than the 1-in-${timeoutEvery} cadence`);
  } else {
    check('at least one deadline statement ran', load.outcomes.some((o) => o.kind === 'timeout'));
  }
  check('every deadline statement was stopped as QUERY_TIMEOUT',
    load.outcomes.filter((o) => o.kind === 'timeout').every((o) => o.status === 400 && o.code === 'QUERY_TIMEOUT'),
    load.outcomes.filter((o) => o.kind === 'timeout' && !(o.status === 400 && o.code === 'QUERY_TIMEOUT')).slice(0, 3));
  check('the gateway counted every request it answered',
    (sumSeries(metricsAfter, 'querycraft_requests_total', { route: '/query', method: 'POST' }) ?? 0)
      - (sumSeries(metricsBefore, 'querycraft_requests_total', { route: '/query', method: 'POST' }) ?? 0) === sent,
    { delta: (sumSeries(metricsAfter, 'querycraft_requests_total', { route: '/query', method: 'POST' }) ?? 0) - (sumSeries(metricsBefore, 'querycraft_requests_total', { route: '/query', method: 'POST' }) ?? 0), sent });
  check('the gateway recorded one statement per request, none dropped',
    histCountDelta === sent, { histogramCountDelta: histCountDelta, sent });
  check('a timeout statement is counted with its code',
    (codeDelta.get('QUERY_TIMEOUT') ?? 0) === load.outcomes.filter((o) => o.kind === 'timeout').length,
    { counted: codeDelta.get('QUERY_TIMEOUT') ?? 0, sent: load.outcomes.filter((o) => o.kind === 'timeout').length });

  console.log('the pool never exceeded its own maximum');
  check('acquired_conns never exceeded max_conns',
    sampledAcquired.every((v, i) => v <= Math.max(1, sampledMaxConns[i])), { peakAcquired, max_conns: poolMaxConns });
  check('the pool reported a real maximum', poolMaxConns > 0, poolMaxConns);
  check('the pool is shared, not the cap: peak acquired <= max_concurrent + the gateway probes',
    peakAcquired <= (cap ?? 0) + 2, { peakAcquired, cap });
  const timeoutsSent = load.outcomes.filter((o) => o.kind === 'timeout').length;
  check('every 400 was either a deadline statement or a cap refusal',
    load.outcomes.filter((o) => o.status === 400).length === timeoutsSent + refusedByCap.length,
    { bad400: load.outcomes.filter((o) => o.status === 400).length, timeoutsSent, refusals: refusedByCap.length });
  check('a cap refusal names the cap in its message',
    refusedByCap.every((o) => new RegExp(`maximum of ${cap} statements`).test(o.message)),
    refusedByCap.slice(0, 3).map((o) => o.message));

  console.log('the concurrency cap is enforced, not just advertised');
  if (cap === null) {
    skip('saturation probe', '/diagnostics did not report engine.limits.max_concurrent');
  } else if (cap > 12) {
    skip('saturation probe', `max_concurrent is ${cap}; the probe would have to hold ${cap} slots`);
  } else {
    const sat = await saturate(base, cap);
    const held = sat.wave1.filter((r) => r.status === 200);
    const refused = sat.wave2.filter((r) => r.status === 400 && r.json?.error?.code === 'QUERY_TIMEOUT');
    const named = refused.filter((r) => new RegExp(`maximum of ${cap} statements`).test(r.json?.error?.message ?? ''));
    check(`all ${cap} slots were filled by the slow statements`, held.length === cap,
      { held: held.length, statuses: sat.wave1.map((r) => r.status) });
    check('the two requests past the cap were refused, not dropped', refused.length === 2,
      { statuses: sat.wave2.map((r) => r.status), bodies: sat.wave2.map((r) => r.json) });
    check('the refusal names the configured cap', named.length === 2,
      { messages: refused.map((r) => r.json?.error?.message) });
    check('the slow statements still completed once their slots were free',
      held.every((r) => r.json?.status === 'succeeded'), sat.wave1.slice(0, 2).map((r) => r.json));
  }

  console.log('the service is healthy after the load');
  const health = await getJSON(`${base}/health`);
  check('/health answers 200', health.status === 200, health.status);
  const after = await getJSON(`${base}/diagnostics`, { authorization: 'Bearer ' + learner.token });
  check('the engine is still ready', after.json?.engine?.ready === true, after.json?.engine);
  check('the learner role is still the restricted one',
    after.json?.learner?.current_user === 'querycraft_learner', after.json?.learner);

  console.log('slowest requests');
  const slowest = [...load.outcomes].sort((a, b) => b.ms - a.ms).slice(0, 5);
  for (const o of slowest) {
    console.log(`  ${ms(o.ms).padStart(8)} ms  ${o.kind.padEnd(8)} ${o.status} ${o.code || 'ok'}`);
  }
} catch (err) {
  failed++;
  console.log(`  FAIL   load harness — ${err.message}`);
} finally {
  if (booted) {
    const how = await stopGateway(child);
    console.log(`\nshutdown: gateway ${how}`);
    if (pgPort !== null) {
      let text = listeningPorts();
      for (let i = 0; i < 20 && isListening(text, pgPort); i++) {
        await sleep(500);
        text = listeningPorts();
      }
      if (isListening(text, pgPort)) {
        console.log(`  FAIL   embedded PostgreSQL is still listening on ${pgPort}`);
        failed++;
      } else {
        console.log(`  ok     embedded PostgreSQL released ${pgPort}`);
      }
    }
  } else {
    console.log('\nshutdown: nothing to stop (QC_GATEWAY)');
  }
  /* Runs on the failure path too: the binary, the cluster and the postgres log
     all live under tmpRoot, so removing it removes all three. */
  try {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
    console.log('shutdown: removed the harness scratch root');
  } catch (err) {
    console.log(`  WARN   could not remove ${tmpRoot}: ${err.message}`);
  }
}

if (failed) {
  console.log(`\nload-test: ${failed} check(s) failed${skipped ? `, ${skipped} skipped` : ''}`);
  process.exit(1);
}
console.log(`\nload-test: OK${skipped ? ` (${skipped} skipped)` : ''}`);
