/*
 * Metrics and diagnostics audit (technical design §13.1).
 *
 * The adversarial suite (§13.3) and the load test (§13.2) publish numbers taken
 * from this instrumentation, so the instrumentation itself has to be checked:
 * a counter that never increments turns a measurement into a story.
 *
 * Two modes:
 *
 *   node scripts/metrics-audit.mjs
 *     Hermetic. Builds the gateway, boots it with no database on a free port,
 *     and checks the exposition, the declared metric names, the honest
 *     unavailable path and the diagnostics report. `make audit-metrics`,
 *     part of `make test`.
 *
 *   QC_GATEWAY=http://localhost:18077 node scripts/metrics-audit.mjs
 *     Everything above against a gateway that is already running, plus the
 *     outcomes only a live PostgreSQL can produce: a QUERY_TIMEOUT and a
 *     row-capped result, both read back from /metrics.
 *
 *   QC_LIVE=1 node scripts/metrics-audit.mjs
 *     Boot a gateway with its own embedded PostgreSQL and run the live checks.
 *
 * Environment: QC_GO (default `go`), QC_BACKEND (default `backend`),
 * QC_BIN (an already-built gateway binary), QC_GATEWAY (an existing gateway).
 */
import { createRequire } from 'node:module';
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

const DECLARED_METRICS = [
  'querycraft_engine_ready',
  'querycraft_pool_connections',
  'querycraft_pool_max_connections',
  'querycraft_request_duration_seconds',
  'querycraft_requests_total',
  'querycraft_statements_total',
];

const r = new Reporter('metrics-audit');

/**
 * signInLearner creates a throwaway account and returns its bearer token.
 *
 * /diagnostics reports pool state and the learner's privileges, so the route
 * needs a session. The account lives only for the run: a gateway booted with
 * The no_local_db config key keeps it in memory, and a live run drops it with the cluster.
 */
async function signInLearner(base) {
  const email = 'metrics-' + Date.now() + '-' + Math.floor(Math.random() * 1e6) + '@metrics.test';
  const res = await postJSON(base + '/auth/signup', {
    email,
    name: 'Metrics Audit',
    password: 'metrics-audit-password',
  });
  const token = res.json?.token;
  if (!token) {
    throw new Error('could not sign up a learner for the metrics audit: HTTP ' + res.status + ' ' + String(res.body).slice(0, 200));
  }
  return { email, token };
}

async function auditHermetic(base, live, token) {
  console.log(`gateway: ${base}${live ? ' (live PostgreSQL)' : ' (no database)'}`);

  r.section('request instrumentation');
  const health = await getJSON(`${base}/health`);
  r.check('/health answers', health.status === 200, health.status);
  r.check('/health is not counted in its own response', !health.body.includes('querycraft_requests_total'));

  let m = await getText(`${base}/metrics`);
  r.check('/metrics answers 200', m.status === 200, m.status);
  r.check('/metrics is Prometheus text', m.contentType.startsWith('text/plain'), m.contentType);
  for (const name of DECLARED_METRICS) {
    r.check(`declares ${name}`, m.body.includes(`# TYPE ${name} `));
  }
  r.check(
    'the health request is counted',
    (seriesValue(m.body, 'querycraft_requests_total', { method: 'GET', route: '/health', status: '200' }) ?? 0) >= 1,
  );
  r.check(
    'latency is recorded for the health route',
    m.body.includes('querycraft_request_duration_seconds_count{method="GET",route="/health"}'),
  );
  r.check(
    'a multi-series metric carries its label',
    m.body.includes('querycraft_pool_connections{state="total"}'),
  );

  r.section('a route label is a bounded pattern, not a path');
  await fetch(`${base}/definitely/not/a/route/abc123`);
  m = await getText(`${base}/metrics`);
  r.check('the probed path is not a metric label', !m.body.includes('abc123'));
  // The SPA catch-all is itself a route, so an unknown path lands on "/" or on
  // "unmatched". Either way it is bounded: the point is that it is not the path.
  const bounded =
    (seriesValue(m.body, 'querycraft_requests_total', { route: '/' }) ?? 0) +
    (seriesValue(m.body, 'querycraft_requests_total', { route: 'unmatched' }) ?? 0);
  r.check('the probe is still counted against a bounded route', bounded >= 1, bounded);

  r.section('statement outcomes');
  const empty = await postJSON(`${base}/query`, '{"sql":"   "}');
  r.check('blank SQL is rejected', empty.status === 400, empty.status);
  r.check('blank SQL is EMPTY_QUERY', empty.json?.error?.code === 'EMPTY_QUERY', empty.json?.error?.code);

  const real = await postJSON(`${base}/query`, '{"sql":"select 1"}');
  if (live) {
    r.check('a real statement succeeds', real.status === 200, real.status);
  } else {
    r.check('with no database the gateway reports ENVIRONMENT_UNAVAILABLE',
      real.json?.error?.code === 'ENVIRONMENT_UNAVAILABLE', real.json?.error?.code);
  }

  m = await getText(`${base}/metrics`);
  r.check(
    'EMPTY_QUERY is counted with its code',
    (seriesValue(m.body, 'querycraft_statements_total', { code: 'EMPTY_QUERY', status: 'failed' }) ?? 0) === 1,
  );
  if (!live) {
    r.check(
      'ENVIRONMENT_UNAVAILABLE is counted with its code',
      (seriesValue(m.body, 'querycraft_statements_total', { code: 'ENVIRONMENT_UNAVAILABLE', status: 'failed' }) ?? 0) === 1,
    );
  }

  r.section('/metrics itself stays cheap and honest');
  r.check('pool gauges are sampled at scrape time', seriesValue(m.body, 'querycraft_pool_max_connections') !== null);
  if (live) {
    r.check('a live gateway reports a real pool', (seriesValue(m.body, 'querycraft_pool_max_connections') ?? 0) > 0);
    r.check('a live gateway is ready', seriesValue(m.body, 'querycraft_engine_ready') === 1);
  } else {
    r.check('a gateway with no database reports zero capacity',
      seriesValue(m.body, 'querycraft_pool_max_connections') === 0);
    r.check('a gateway with no database is not ready', seriesValue(m.body, 'querycraft_engine_ready') === 0);
  }

  r.section('diagnostics');
  const diag = await getJSON(`${base}/diagnostics`, 15_000, { authorization: 'Bearer ' + token });
  r.check('/diagnostics answers 200', diag.status === 200, diag.status);
  const d = diag.json || {};
  r.check('engine readiness is measured', d.engine?.ready === live, d.engine?.ready);
  if (live) {
    r.check('a ready engine has no reason to give', d.engine?.reason === '', d.engine?.reason);
    r.check('the server version is reported', /^PostgreSQL /.test(d.engine?.version || ''), d.engine?.version);
    r.check('the pool is described', (d.engine?.pool?.max_conns ?? 0) > 0, d.engine?.pool);
  } else {
    r.check('an unready engine says why', typeof d.engine?.reason === 'string' && d.engine.reason.length > 0, d.engine?.reason);
    r.check('an unready engine advertises no pool', d.engine?.pool?.max_conns === 0, d.engine?.pool);
  }
  r.check('the dataset version is reported', d.dataset?.version === 1, d.dataset?.version);
  r.check('the dataset schema is named', d.dataset?.schema === 'dataset', d.dataset?.schema);
  r.check('the store kind is reported', d.store?.kind === (live ? 'postgres' : 'memory'), d.store?.kind);
  r.check('a non-persistent store says so', d.store?.persistent === live, d.store?.persistent);
  r.check('the metric names are listed', Array.isArray(d.metrics) && DECLARED_METRICS.every((n) => d.metrics.includes(n)), d.metrics);
  r.check('the hard limits are reported', d.engine?.limits?.max_rows > 0 && d.engine?.limits?.timeout_ms > 0, d.engine?.limits);
  if (live) {
    r.check('learner SQL runs as the restricted role', d.learner?.current_user === 'querycraft_learner', d.learner?.current_user);
    r.check('learner SQL cannot reach gateway metadata', d.learner?.can_read_app === false, d.learner?.can_read_app);
    r.check('learner SQL cannot read the catalog', (d.learner?.search_path || '').includes('dataset'), d.learner?.search_path);
  } else {
    r.check('the learner section reports unavailability', d.learner?.unavailable === true, d.learner);
  }

  r.section('no secrets in a response body');
  const secretish = ['password', '://', 'user=', 'sslmode'];
  for (const page of [diag.body, m.body, (await getText(`${base}/capabilities`)).body]) {
    const hit = secretish.find((s) => page.toLowerCase().includes(s));
    r.check(`no ${hit ?? 'secret'} in the response body`, !hit, page.slice(0, 200));
  }

  r.section('progress version matches the frontend');
  try {
    const require = createRequire('/tmp/');
    const progress = require('/tmp/progress.cjs');
    r.check(
      `/diagnostics reports the frontend's PROGRESS_VERSION (${progress.PROGRESS_VERSION})`,
      d.progress?.version === progress.PROGRESS_VERSION,
      d.progress?.version,
    );
  } catch {
    r.skip('progress version matches the frontend', 'bundle /tmp/progress.cjs not found (run `make audit-progress` first)');
  }
}

async function auditLiveOutcomes(base) {
  r.section('live outcomes (timeout, row cap)');
  const before = await getText(`${base}/metrics`);

  const slow = await postJSON(`${base}/query`, '{"sql":"select pg_sleep(3)","limits":{"timeout_ms":300}}');
  r.check('a statement past its deadline is stopped', slow.status === 400, slow.status);
  r.check('and reported as QUERY_TIMEOUT', slow.json?.error?.code === 'QUERY_TIMEOUT', slow.json?.error?.code);
  r.check('the timeout is measured, not reported as a syntax error', slow.json?.error?.code !== 'QUERY_SYNTAX_ERROR', slow.json?.error?.code);

  const capped = await postJSON(`${base}/query`, '{"sql":"select generate_series(1,1000)","limits":{"max_rows":10}}');
  r.check('a row-capped statement still succeeds', capped.status === 200, capped.status);
  r.check('and is marked truncated', capped.json?.truncated === true, capped.json);
  r.check('and returns exactly the cap', capped.json?.row_count === 10, capped.json?.row_count);

  // An adversarial outcome is not a crash: the service must still serve.
  const health = await getJSON(`${base}/health`);
  r.check('the gateway is still serving after a timeout', health.status === 200, health.status);

  const after = await getText(`${base}/metrics`);
  const delta = (metric, labels) =>
    (seriesValue(after.body, metric, labels) ?? 0) - (seriesValue(before.body, metric, labels) ?? 0);
  r.check(
    'QUERY_TIMEOUT appears in /metrics',
    delta('querycraft_statements_total', { code: 'QUERY_TIMEOUT', status: 'failed' }) >= 1,
  );
  r.check(
    'the row-capped success appears in /metrics',
    delta('querycraft_statements_total', { code: '', status: 'succeeded' }) >= 1,
    after.body.split('\n').filter((l) => l.startsWith('querycraft_statements_total')),
  );
  r.check(
    'the histogram observed the timeout request',
    delta('querycraft_request_duration_seconds_count', { method: 'POST', route: '/query' }) >= 2,
  );
}

/* ---------- main ---------- */

let child = null;
try {
  let base = '';
  let live = process.env.QC_LIVE === '1';
  if (process.env.QC_GATEWAY) {
    base = process.env.QC_GATEWAY.replace(/\/$/, '');
  } else {
    const bin = process.env.QC_BIN || buildGateway('metrics-audit');
    const started = await startGateway(bin, live ? await liveEnv('metrics-audit') : { no_local_db: true });
    child = started.child;
    base = started.base;
  }
  /* /diagnostics needs a session, so the audit signs in before it reads the
     report. Signup works in every mode: a gateway with no database keeps its
     accounts in memory. */
  const learner = await signInLearner(base);
  if (process.env.QC_GATEWAY) {
    const diag = await getJSON(`${base}/diagnostics`, 15_000, { authorization: 'Bearer ' + learner.token });
    live = diag.json?.engine?.ready === true;
  }
  await auditHermetic(base, live, learner.token);
  if (live) await auditLiveOutcomes(base);
} catch (err) {
  r.failed++;
  console.log(`  FAIL   audit harness — ${err.message}`);
} finally {
  stopGateway(child);
}

r.finish();
