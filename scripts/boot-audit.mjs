/*
 * The in-browser engine's boot, driven when the worker never answers.
 *
 * Two defects made the app look like a mock, and neither could be seen from the
 * outside until someone drove a lesson:
 *
 *   1. the worker id was a constant shared by every tab. The tab that loses the
 *      leader election proxies its queries to the winner, so when the winning
 *      tab is reloaded or closed the follower waits on a leader that will never
 *      answer. Measured in a follower tab: one `fetchModuleContent('s3')` was
 *      still pending after 53 s and all 52 reading documents never arrived, so
 *      every lesson rendered the short curriculum prose instead of the authored
 *      chapter — with no error to notice anywhere.
 *
 *   2. nothing bounded the boot. `PGliteWorker.create` and the first seed have
 *      no timeout of their own, so a boot that cannot finish leaves every caller
 *      pending for the life of the tab.
 *
 * This script bundles the shipping adapter (`web/src/api/pglite.ts`) with a
 * worker stub whose `create` returns a promise that never settles, and checks
 * what the adapter does about it: it must give up, build the engine on the page,
 * say so out loud, and then really run the dataset. It also checks the engine's
 * identity is per dataset *and* per page load, by booting two module instances
 * and comparing the ids they presented.
 *
 * What it does not prove: that a real browser's `Worker`, its BroadcastChannel
 * leader election and its tab lifecycle behave this way. Node has no `Worker`
 * (the script installs a stub) and no `BroadcastChannel` election, so the
 * election itself is out of reach here — the rules are checked against the
 * adapter's own decisions, and the browser half is checked by driving the app.
 *
 *   make audit-boot
 *   node scripts/boot-audit.mjs
 */
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { Reporter, repoRoot, scratchDir } from './lib/gateway.mjs';

const webRequire = createRequire(path.join(repoRoot, 'web', 'package.json'));
const API_DIR = path.join(repoRoot, 'web', 'src', 'api');
const DATASET_DIR = path.join(repoRoot, 'backend', 'internal', 'dataset');
/* The bundle goes in the run's scratch root, so it leaves with the run. */
const OUT = path.join(scratchDir('bundle'), 'qc-boot-adapter.mjs');
const report = new Reporter('boot-audit');

const readFixture = (name) => fs.readFileSync(path.join(DATASET_DIR, name), 'utf8');

/* The two Vite-only imports are stubbed with the same contents the app carries;
   `@electric-sql/pglite` stays the real package so the fallback engine is real. */
function stubPlugin() {
  return {
    name: 'qc-boot-stubs',
    setup(build) {
      build.onResolve({ filter: /(^|\/)dataset$/ }, () => ({ path: 'qc-dataset', namespace: 'qc-stub' }));
      build.onResolve({ filter: /(^|\/)content$/ }, () => ({ path: 'qc-content', namespace: 'qc-stub' }));
      build.onResolve({ filter: /^@electric-sql\/pglite\/worker$/ }, () => ({ path: 'qc-pglite-worker', namespace: 'qc-stub' }));
      build.onLoad({ filter: /.*/, namespace: 'qc-stub' }, (args) => {
        if (args.path === 'qc-dataset') {
          return {
            loader: 'js',
            contents:
              'export const schemaSql = ' + JSON.stringify(readFixture('schema.sql')) + ';\n' +
              'export const seedSql = ' + JSON.stringify(readFixture('seed.sql')) + ';\n' +
              'export const perfSql = ' + JSON.stringify(readFixture('perf.sql')) + ';\n' +
              'export const datasetVersion = 1;',
          };
        }
        if (args.path === 'qc-content') {
          return { loader: 'js', contents: 'export const BUNDLED_CONTENT = {};\nexport async function seedContentDocs() {}' };
        }
        /* A worker handshake that never resolves, which is the case a follower
           tab is stuck in. The id it was handed is recorded for the identity
           checks below. */
        return {
          loader: 'js',
          contents:
            'export const PGliteWorker = { create(_w, opts) {\n' +
            '  (globalThis.__qcWorkerIds ||= []).push(opts && opts.id);\n' +
            '  return new Promise(() => {});\n' +
            '} };',
        };
      });
    },
  };
}

async function bundleAdapter() {
  const esbuild = webRequire('esbuild');
  const pgliteIndex = path.join(repoRoot, 'web', 'node_modules', '@electric-sql', 'pglite', 'dist', 'index.js');
  await esbuild.build({
    entryPoints: [path.join(API_DIR, 'pglite.ts')],
    bundle: true,
    format: 'esm',
    platform: 'node',
    target: 'node20',
    outfile: OUT,
    plugins: [stubPlugin()],
    alias: { '@electric-sql/pglite': pgliteIndex },
    external: [pgliteIndex],
    logLevel: 'silent',
  });
  return OUT;
}

/* The adapter creates its boot timeout as its very first timer, and waiting the
   real 20 s out is not something a test suite should do. The first timer after
   this is installed is scheduled at `ms` instead, and every later timer is the
   real thing — so the fallback engine gets its full budget. */
function capNextTimer(ms) {
  const real = globalThis.setTimeout;
  let armed = true;
  globalThis.setTimeout = function (fn, delay, ...rest) {
    if (armed) {
      armed = false;
      globalThis.setTimeout = real;
      return real.call(globalThis, fn, ms, ...rest);
    }
    return real.call(globalThis, fn, delay, ...rest);
  };
  return () => {
    if (armed) {
      armed = false;
      globalThis.setTimeout = real;
    }
  };
}

/* Node's global is `worker_threads`, not a Web Worker; the adapter only needs
   something it can terminate. */
function installWorkerStub() {
  if (typeof globalThis.Worker === 'function') return;
  globalThis.Worker = class {
    terminate() {}
  };
}

async function main() {
  installWorkerStub();
  const url = pathToFileURL(await bundleAdapter()).href;
  globalThis.__qcWorkerIds = [];

  const warns = [];
  const realWarn = console.warn;
  console.warn = (...args) => {
    warns.push(String(args[0]));
  };

  report.section('a worker that never answers');
  const first = await import(url);
  const uncap = capNextTimer(60);
  const t0 = Date.now();
  let settled = false;
  let failure = null;
  try {
    await withHardDeadline(first.getDatabase(), 90000, 'getDatabase');
    settled = true;
  } catch (e) {
    /* A boot that failed is still a boot that returned; only the audit's own
       deadline firing means the call never came back at all. */
    settled = !(e instanceof HardDeadline);
    failure = String((e && e.message) || e);
  }
  uncap();
  console.warn = realWarn;
  const ms = Date.now() - t0;
  report.check('the boot returns instead of hanging', settled, failure);
  report.check('it took the page engine instead of never starting', first.engineCapabilities()?.placement === 'main', first.engineCapabilities());
  report.check('the visitor is told why, once, in the console', warns.filter((w) => w.includes('Web Worker')).length === 1, warns);
  report.record('a worker that never answers', settled ? `settled in ${ms} ms, on the ${first.engineCapabilities()?.placement} engine` : `still pending (${failure})`);

  report.section('the engine it fell back to is real');
  /* The engine hands rows back as arrays (rowMode 'array'), and the commerce
     fixture holds exactly 80 orders, so a wrong engine cannot pass this. */
  const counted = await first.executeSql('SELECT count(*) AS n FROM orders');
  const n = Number((counted.rows?.[0] ?? [])[0]);
  report.check('the fallback engine runs the seeded dataset', counted.status === 'succeeded' && n === 80, { status: counted.status, rows: counted.rows, columns: counted.columns.map((c) => c.name) });
  const tables = await first.fetchTables();
  report.check('and it is the commerce fixture, not an empty one', tables.tables.some((t) => t.name === 'orders'), tables.tables.map((t) => t.name));

  report.section('one engine per dataset, per page load');
  const ids = globalThis.__qcWorkerIds.slice();
  report.check('the dataset is named in the id', ids.length === 1 && /^querycraft-dataset-commerce:/.test(ids[0] ?? ''), ids);
  const commerceSuffix = (ids[0] ?? '').slice('querycraft-dataset-commerce:'.length);
  report.check('the suffix is per page load, not a constant', /^[a-z0-9]{8}$/.test(commerceSuffix), commerceSuffix);

  /* A second instance of the module is a second page load: it must not present
     the same id, or two tabs are back in one election. */
  const second = await import(`${url}?tab=2`);
  const uncap2 = capNextTimer(60);
  const warn2 = console.warn;
  console.warn = () => {};
  try {
    await withHardDeadline(second.getDatabase('perf'), 90000, 'getDatabase(perf)');
  } finally {
    uncap2();
    console.warn = warn2;
  }
  const perfId = globalThis.__qcWorkerIds[globalThis.__qcWorkerIds.length - 1];
  report.check('the second dataset gets its own election', /^querycraft-dataset-perf:/.test(perfId ?? ''), perfId);
  report.check('a second page load gets its own election', perfId !== ids[0] && perfId.slice('querycraft-dataset-perf:'.length) !== commerceSuffix, [ids[0], perfId]);

  report.finish();
}

/** The audit's own deadline: a call that never comes back must fail the run. */
class HardDeadline extends Error {}

function withHardDeadline(promise, ms, what) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new HardDeadline(`${what} did not settle within ${ms} ms`)), ms);
    promise.then(
      (v) => {
        clearTimeout(timer);
        resolve(v);
      },
      (e) => {
        clearTimeout(timer);
        reject(e);
      },
    );
  });
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
