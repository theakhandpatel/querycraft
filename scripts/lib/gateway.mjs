/*
 * Shared harness for the audit scripts that need a running gateway.
 *
 * These helpers existed three times over (metrics, load, adversarial) and
 * drifted: one copy killed a process with `pkill`, which also killed the shell
 * that started it. One copy is one behaviour.
 *
 * Everything here boots the gateway the same way a developer would — a real
 * binary, a real embedded PostgreSQL, an isolated port and data directory — so
 * an audit never touches whatever the developer is running.
 */
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

/** backendDir resolves QC_BACKEND (default `<repo>/backend`). */
export function backendDir() {
  return process.env.QC_BACKEND ? path.resolve(process.env.QC_BACKEND) : path.join(repoRoot, 'backend');
}

/** freePort asks the OS for an unused TCP port. */
export function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const port = srv.address().port;
      srv.close(() => resolve(port));
    });
  });
}

async function fetchWithTimeout(url, init, timeoutMs) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), timeoutMs);
  try {
    return await fetch(url, { ...init, signal: ac.signal });
  } finally {
    clearTimeout(timer);
  }
}

/** getJSON fetches a URL and decodes JSON, tolerating a non-JSON body. */
export async function getJSON(url, timeoutMs = 15_000, headers = {}) {
  const res = await fetchWithTimeout(url, { headers }, timeoutMs);
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* leave null */ }
  return { status: res.status, json, body: text };
}

/** getText fetches a URL as text with its status and content type. */
export async function getText(url, timeoutMs = 15_000) {
  const res = await fetchWithTimeout(url, {}, timeoutMs);
  return { status: res.status, contentType: res.headers.get('content-type') || '', body: await res.text() };
}

/** postJSON posts a body (string or object) and decodes the JSON response. */
export async function postJSON(url, payload, timeoutMs = 30_000, headers = {}) {
  const res = await fetchWithTimeout(url, {
    method: 'POST',
    /* Extra headers are how a caller speaks as a signed-in learner instead of
       an anonymous visitor — the difference between a writable workspace and
       the shared, read-only dataset. */
    headers: { 'content-type': 'application/json', ...headers },
    body: typeof payload === 'string' ? payload : JSON.stringify(payload),
  }, timeoutMs);
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* leave null */ }
  return { status: res.status, json, body: text };
}

/**
 * buildGateway compiles cmd/api into a temporary binary. Returns its path.
 * It is used instead of `go run` because `go run` execs the server as a child:
 * killing the tool does not reliably kill the server, and audit scripts must
 * not leave a gateway behind.
 */
export function buildGateway(tag = 'audit') {
  const bin = path.join(os.tmpdir(), `qc-${tag}-${process.pid}`, 'querycraft');
  fs.mkdirSync(path.dirname(bin), { recursive: true });
  const out = spawnSync(process.env.QC_GO || 'go', ['build', '-o', bin, './cmd/api'], {
    cwd: backendDir(),
    env: { ...process.env, GOTOOLCHAIN: 'local' },
    encoding: 'utf8',
  });
  if (out.status !== 0) {
    throw new Error(`could not build the gateway:\n${out.stderr || out.stdout}`);
  }
  return bin;
}

/**
 * startGateway launches a gateway and waits for /health.
 *
 * extraEnv is merged over the environment: QC_NO_LOCAL_DB=1 for the degraded
 * shape, or a QC_PG_PORT/QC_PG_DATA/QC_PG_RUNTIME trio for an isolated embedded
 * PostgreSQL (see `liveEnv`).
 */
export async function startGateway(bin, extraEnv = {}, readyTimeoutMs = 120_000) {
  const port = await freePort();
  const child = spawn(bin, [], {
    env: { ...process.env, QC_ADDR: `:${port}`, ...extraEnv },
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: true,
  });
  let log = '';
  child.stdout.on('data', (d) => { log += d.toString(); });
  child.stderr.on('data', (d) => { log += d.toString(); });

  const base = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + readyTimeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`gateway exited with ${child.exitCode} before becoming ready:\n${log}`);
    }
    try {
      const res = await fetch(`${base}/health`);
      if (res.ok) return { child, base, port, log: () => log };
    } catch { /* not up yet */ }
    await new Promise((r) => setTimeout(r, 200));
  }
  stopGateway(child);
  throw new Error(`gateway never became ready:\n${log}`);
}

/**
 * stopGateway terminates a gateway by PID and then by process group.
 *
 * Never `pkill -f`: a pattern match would also match the shell running the
 * audit, which is how an audit kills itself.
 */
export function stopGateway(child) {
  if (!child || child.exitCode !== null) return;
  try { process.kill(child.pid, 'SIGTERM'); } catch { /* already gone */ }
  try { process.kill(-child.pid, 'SIGTERM'); } catch { /* no process group */ }
}

/** liveEnv returns an environment for a gateway with its own embedded PostgreSQL. */
export async function liveEnv(tag = 'audit') {
  const dir = path.join(os.tmpdir(), `qc-${tag}-pg-${process.pid}`);
  return {
    QC_PG_PORT: String(await freePort()),
    QC_PG_DATA: path.join(dir, 'data'),
    QC_PG_RUNTIME: path.join(dir, 'runtime'),
  };
}

/**
 * seriesValue reads one sample from Prometheus text, matching a subset of
 * labels: every requested label must be present with the right value, and extra
 * labels are allowed. An empty requested value means "this label is absent",
 * which is how the registry itself models an unset label.
 */
export function seriesValue(body, metric, labels = {}) {
  const want = Object.entries(labels)
    .filter(([, v]) => v !== '')
    .map(([k, v]) => [k, `"${v}"`]);
  for (const line of body.split('\n')) {
    if (line.startsWith('#')) continue;
    const space = line.lastIndexOf(' ');
    if (space < 0) continue;
    const brace = line.indexOf('{');
    const name = brace === -1 ? line.slice(0, space) : line.slice(0, brace);
    if (name !== metric) continue;

    const got = new Map();
    if (brace !== -1 && brace < space) {
      const end = line.indexOf('}', brace);
      if (end === -1 || end > space) continue;
      for (const pair of line.slice(brace + 1, end).split(',')) {
        if (!pair) continue;
        const eq = pair.indexOf('=');
        got.set(pair.slice(0, eq).trim(), pair.slice(eq + 1).trim());
      }
    }
    if (want.every(([k, v]) => got.get(k) === v)) return Number(line.slice(space + 1));
  }
  return null;
}

/**
 * Reporter accumulates ok/FAIL/skip lines the way every audit script prints
 * them, and exits non-zero when anything failed.
 */
export class Reporter {
  constructor(name) {
    this.name = name;
    this.failed = 0;
    this.skipped = 0;
    this.rows = [];
  }

  section(title) {
    console.log(`\n${title}`);
  }

  check(name, cond, detail) {
    if (cond) {
      console.log(`  ok     ${name}`);
    } else {
      this.failed++;
      console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
    }
    return Boolean(cond);
  }

  /** record adds a row to the markdown table the script can print. */
  record(...cells) {
    this.rows.push(cells);
  }

  skip(name, why) {
    this.skipped++;
    console.log(`  skip   ${name} — ${why}`);
  }

  markdownTable(header) {
    const lines = [`| ${header.join(' | ')} |`, `| ${header.map(() => '---').join(' | ')} |`];
    for (const row of this.rows) lines.push(`| ${row.join(' | ')} |`);
    return lines.join('\n');
  }

  finish() {
    if (this.failed) {
      console.log(`\n${this.name}: ${this.failed} check(s) failed${this.skipped ? `, ${this.skipped} skipped` : ''}`);
      process.exit(1);
    }
    console.log(`\n${this.name}: OK${this.skipped ? ` (${this.skipped} skipped)` : ''}`);
  }
}
