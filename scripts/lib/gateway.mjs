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

/*
 * One per-run scratch root.
 *
 * Every artifact this harness makes -- the compiled gateway binary, an isolated
 * PostgreSQL cluster, the embedded server's own postgres log -- lives under a
 * single directory that is removed when the run ends, however it ends. These
 * used to be separate `qc-<tag>-<pid>` paths under os.tmpdir() that nothing
 * owned, so each audit (and each `make test`) left 50-150 MB behind forever; a
 * machine that had run the suite a few hundred times held ~52 GiB of them.
 *
 * The removal is registered on `process.on('exit')`, which also runs for a
 * failed run and for an explicit `process.exit(1)`, plus the fatal signals a
 * harness run actually sees. A SIGKILL cannot be caught -- that run leaks,
 * which is the documented, accepted limit.
 */
const runRoots = new Set();
let runRootDir = null;
let runCleanupInstalled = false;

function removeRunRoots() {
  for (const dir of runRoots) {
    try {
      fs.rmSync(dir, { recursive: true, force: true });
    } catch { /* best effort: the run is ending either way */ }
  }
  runRoots.clear();
}

function installRunCleanup() {
  if (runCleanupInstalled) return;
  runCleanupInstalled = true;
  process.on('exit', removeRunRoots);
  for (const [signal, code] of [['SIGINT', 130], ['SIGTERM', 143], ['SIGHUP', 129]]) {
    process.on(signal, () => {
      removeRunRoots();
      process.exit(code);
    });
  }
}

/**
 * runRoot returns this process's scratch directory, creating it on first use.
 * The name keeps the `qc-` prefix so `make disk` finds the harness footprint
 * with one glob.
 */
export function runRoot(tag = 'run') {
  installRunCleanup();
  if (runRootDir === null) {
    runRootDir = fs.mkdtempSync(path.join(os.tmpdir(), `qc-${tag}-`));
    runRoots.add(runRootDir);
  }
  return runRootDir;
}

/** scratchDir returns (and creates) a named directory inside the run root. */
export function scratchDir(name) {
  const dir = path.join(runRoot(), name);
  fs.mkdirSync(dir, { recursive: true });
  return dir;
}

/**
 * scratchTmp returns the directory handed to a spawned gateway as $TMPDIR.
 *
 * The embedded-postgres library writes its postgres log with
 * `os.CreateTemp("", "embedded_postgres_log")` and never removes it, so owning
 * the directory os.TempDir() resolves to is the only way to own that file.
 */
export function scratchTmp() {
  return scratchDir('tmp');
}

/** freePort asks the OS for an unused TCP port. */
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
  const bin = path.join(scratchDir('bin'), `querycraft-${tag}`);
  const out = spawnSync(process.env.QC_GO || 'go', ['build', '-o', bin, './cmd/api'], {
    cwd: backendDir(),
    env: { ...process.env, GOTOOLCHAIN: 'local' },
    encoding: 'utf8',
  });
  if (out.status !== 0) {
    /* A spawn that never started reports status null with no stderr — the
       message used to read "could not build the gateway:\nnull", which says
       nothing about the actual cause (usually `go` not on PATH). */
    const why = out.error ? String(out.error.message || out.error) : out.stderr || out.stdout || `exit ${out.status}`;
    throw new Error(
      `could not build the gateway (${process.env.QC_GO || 'go'} build ./cmd/api in ${backendDir()}): ${why}`,
    );
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
    env: { ...process.env, QC_ADDR: `127.0.0.1:${port}`, TMPDIR: scratchTmp(), ...extraEnv },
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
  const dir = scratchDir(`pg-${tag}`);
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
