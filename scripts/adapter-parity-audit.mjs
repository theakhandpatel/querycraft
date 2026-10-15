/*
 * Adapter parity audit — M3 exit gate, step 6.1:
 * "the same lesson content runs against both adapters unchanged".
 *
 * Adapter A is PostgreSQL compiled to WebAssembly (PGlite), the default
 * server-less engine a learner gets with `npm run dev`; in this report it is
 * called the *web* side. Adapter B is the Go gateway over a native PostgreSQL;
 * it is called the *gateway* side. Both answer the same ExecutionService
 * contract (contracts/query-result.schema.json), so the same lesson SQL has to
 * produce the same result through either one.
 *
 * The audit imports the shipping code rather than a copy:
 *
 *   web       web/src/api/pglite.ts, bundled with esbuild and executed in
 *             Node. The OID to type-name mapping, the date and number
 *             formatting and the SQLSTATE to error-code mapping under test are
 *             the shipped ones. The dataset is applied the way the adapter
 *             applies it (schema, then seed, then
 *             `SET LOCAL search_path = dataset, public` in a read-only
 *             transaction). Node has no Worker, so the adapter takes its
 *             documented main-thread fallback; the deadline path is not
 *             exercised here.
 *
 *   gateway   the compiled gateway over its own embedded PostgreSQL, driven
 *             through POST /query exactly as the browser drives it.
 *
 * The statements are the real lesson SQL: the reference solution of every
 * exercise, every worked example and every syntax block in
 * web/src/data/curriculum.ts, plus probes for the failure and type-formatting
 * paths a curated lesson never hits. The lesson text is never rewritten for
 * either adapter.
 *
 * Modes:
 *   node scripts/adapter-parity-audit.mjs
 *     Hermetic. Builds the gateway, boots it with its own embedded PostgreSQL
 *     on a free port, boots PGlite in-process, runs every statement through
 *     both, and tears the gateway down by PID.
 *
 *   QC_GATEWAY=http://127.0.0.1:18077 node scripts/adapter-parity-audit.mjs
 *     Use a gateway someone else started (nothing is built or stopped). It must
 *     have a live PostgreSQL: parity against ENVIRONMENT_UNAVAILABLE would
 *     compare two error strings, not two engines.
 *
 *   QC_SKIP_PGLITE=1 node scripts/adapter-parity-audit.mjs
 *     Gateway half only. Every statement is recorded as a skip, never a pass.
 *
 *   TZ=UTC node scripts/adapter-parity-audit.mjs
 *     Same audit on a UTC host. The timezone section below prints the naive
 *     timestamp case under both a UTC and a non-UTC `TZ`, so the web-side shift
 *     can be seen to appear and disappear by changing `TZ` alone.
 *
 *   --include-volatile   compare volatile statements (EXPLAIN, random(), now())
 *                        instead of excluding them for manual inspection.
 *   --dialect sqlite     run the same gate for the second dialect (M6 step 8):
 *                        Adapter A is the browser's wasm SQLite adapter
 *                        (web/src/api/sqlite.ts) and Adapter B is the gateway
 *                        with {"dialect":"sqlite"}. Default is postgresql.
 *   --markdown           print the results and type-name tables for docs/.
 *
 * Environment: QC_GO (default `go`), QC_BACKEND (default `backend`),
 * QC_BIN (an already-built gateway binary), QC_GATEWAY, QC_SKIP_PGLITE.
 *
 * How a statement is judged
 * -------------------------
 *   - status, error code, column names, row count and the truncated flag must
 *     match exactly;
 *   - rows are compared cell by cell with the project's own §6.1 rule
 *     (web/src/api/compare.ts `valueEqual`): numeric strings and numbers are
 *     equal, text is never normalised. In order when the statement orders rows
 *     (its own ORDER BY), as a multiset when it does not;
 *   - a row difference §6.1 still calls "the same result" is a REPRESENTATION
 *     finding: the values agree, the JSON shape does not;
 *   - column *types* are compared through a canonical table, because the two
 *     drivers spell the same type differently. The canonical spelling is the
 *     SQL-standard name (see the type-name table this script prints); the raw
 *     labels are printed whenever they differ, with the side that must change.
 *
 * Two kinds of statement are excluded from the *value* comparison, each with a
 * printed reason (never silently): statements whose result is not reproducible
 * (EXPLAIN plan text, EXPLAIN ANALYZE timings, random(), now(), pg_sleep), and
 * nothing else. `--include-volatile` runs them anyway.
 *
 * The SQLite half
 * ---------------
 * The same idea with a different pair of engines: the browser's SQLite (the
 * `@sqlite.org/sqlite-wasm` build, in the page) on one side, and the gateway's
 * SQLite (modernc.org/sqlite, pure Go) on the other. They are two *builds* of
 * SQLite — measured, the wasm one is 3.53.4 and the pure-Go one 3.50.3 — so the
 * engine string each result carries is printed as information and never
 * compared. What is compared is the semantics the contract actually promises:
 * the dialect, the column names, the column labels, the row values, the row
 * count, the truncated flag and the error code.
 *
 * The statement set is every `dialect: 'sqlite'` module's reference solutions
 * and worked examples, plus probes for the paths a curated lesson never reaches
 * (syntax error, incomplete input, missing relation/column/function, a UNIQUE
 * and a NOT NULL constraint, storage classes, division by zero, the row cap and
 * the deadline). It excludes, with a printed reason, the two things the two
 * adapters are *not* required to spell the same way: EXPLAIN QUERY PLAN (the
 * planner's own text; the PostgreSQL half excludes EXPLAIN for the same
 * reason) and a value mentioning `rowid` (a physical address, per file), plus
 * the storage-layer pragmas that describe the file itself. Each exclusion is
 * verified, not trusted: every other column in such a statement must still
 * agree exactly.
 *
 * Exit code: 0 only when every compared statement matched and nothing but the
 * documented exclusions remained; 1 when any divergence or representation
 * finding was observed. Nothing is normalised away silently.
 */
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import {
  buildGateway,
  getJSON,
  liveEnv,
  postJSON,
  Reporter,
  repoRoot,
  startGateway,
  stopGateway,
} from './lib/gateway.mjs';

/* The closed error-code enum from contracts/query-result.schema.json. A failure
   response that names anything else is a contract break on its own. */
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

/* Must match PGLITE_ENGINE in web/src/api/pglite.ts — the label the adapter
   reports for itself in QueryResult.engine. */
const PGLITE_ENGINE = 'PostgreSQL 18 · PGlite (in-browser)';

/* Canonical column-type spelling, and the reason this table exists at all.
 *
 * PostgreSQL names the same type twice: `information_schema.columns.data_type`
 * says `integer`, `bigint`, `boolean`, while `pg_type.typname` — what the wire
 * protocol and the QueryResult's `columns[].type` carry — says `int4`, `int8`,
 * `bool`. The two adapters used to disagree (the browser emitted the SQL-standard
 * spelling, the gateway the pg_type one), so the same result column was
 * described two ways depending on which engine ran it.
 *
 * Settled: the canonical spelling is `pg_type.typname`. It is what design §4.2's
 * own example uses (`"type": "int8"`), it is what the engine itself calls the
 * type, and it means neither adapter has to translate. Both sides emit it now
 * (the browser through its OID→name table, the gateway through the type map), so
 * the *check* is an exact string comparison and this table exists only to
 * explain a mismatch when one reappears: it prints the canonical name and which
 * side deviates. The drawer that lists *table* columns is a different surface
 * and keeps information_schema's spelling on both adapters, so it is consistent
 * across adapters too.
 */
const CANONICAL_TYPE = {
  int2: 'int2', smallint: 'int2',
  int4: 'int4', integer: 'int4',
  int8: 'int8', bigint: 'int8',
  bool: 'bool', boolean: 'bool',
  float4: 'float4', real: 'float4',
  float8: 'float8', 'double precision': 'float8',
  varchar: 'varchar', 'character varying': 'varchar',
  bpchar: 'bpchar', character: 'bpchar',
  timestamptz: 'timestamptz', 'timestamp with time zone': 'timestamptz',
  timestamp: 'timestamp', 'timestamp without time zone': 'timestamp',
  timetz: 'timetz', time: 'time', date: 'date',
  numeric: 'numeric', decimal: 'numeric',
  text: 'text', bytea: 'bytea', json: 'json', jsonb: 'jsonb', uuid: 'uuid',
  interval: 'interval',
};

function canonicalType(t) {
  const s = String(t ?? '').toLowerCase().trim();
  return CANONICAL_TYPE[s] ?? s;
}

/* SQLite's canonical column label, from `contracts/dialects/sqlite.json`'s
   `value_encoding.column_type_label`: `sqlite3_column_decltype` verbatim, with
   the profile's own `expression` fallback when the column has no declared type.
   There is no spelling to translate — a declared type here is an affinity, not
   a type, and the profile deliberately keeps whatever the schema wrote.

   Measured: the dataset's INTEGER/TEXT/NUMERIC/DATE/TIMESTAMP/BOOLEAN columns
   and even `sqlite_master`'s own columns arrive upper-case from both the wasm
   engine and the gateway's raw driver (sqlite3_column_decltype returns the
   declared text, and the dataset authors it upper-case), and an expression
   arrives as null from one and "" from the other, which is why the fallback is
   applied to both. So the check is an exact string comparison; a case
   difference would be reported as a divergence rather than silently folded. */
function canonicalSqliteType(t) {
  const s = String(t ?? '').trim();
  return s === '' ? 'expression' : s;
}

/* Statements whose result is not reproducible across two runs. Each carries the
   reason printed next to the exclusion, so the list can be argued with. */
const VOLATILE_PATTERNS = [
  { re: /\bexplain\b[\s\S]*\banalyze\b/i, why: 'EXPLAIN ANALYZE prints measured timings (actual time=…), which cannot be equal across two runs' },
  { re: /^\s*explain\b/i, why: 'EXPLAIN plan text embeds planner cost/row estimates, which are not guaranteed equal across two servers' },
  { re: /\brandom\s*\(/i, why: 'random() returns a different value on every call' },
  { re: /\bnow\s*\(/i, why: 'now() returns the transaction start time' },
  { re: /\bcurrent_timestamp\b/i, why: 'current_timestamp changes between runs' },
  { re: /\bcurrent_date\b/i, why: 'current_date changes between runs' },
  { re: /\bcurrent_time\b/i, why: 'current_time changes between runs' },
  { re: /\bclock_timestamp\s*\(/i, why: 'clock_timestamp() returns the wall clock at call time' },
  { re: /\bstatement_timestamp\s*\(/i, why: 'statement_timestamp() changes per statement' },
  { re: /\btransaction_timestamp\s*\(/i, why: 'transaction_timestamp() changes per transaction' },
  { re: /\btimeofday\s*\(/i, why: 'timeofday() embeds the wall clock' },
  { re: /\bpg_sleep\s*\(/i, why: 'pg_sleep blocks for the requested duration; the elapsed time is not a value' },
];

function volatileReason(sql) {
  const s = stripComments(sql);
  for (const p of VOLATILE_PATTERNS) if (p.re.test(s)) return p.why;
  return null;
}

const r = new Reporter('adapter-parity-audit');
const argv = process.argv.slice(2);
const markdown = argv.includes('--markdown');
const includeVolatile = argv.includes('--include-volatile');
const skipPglite = process.env.QC_SKIP_PGLITE === '1';
const originalTz = process.env.TZ;

/* Which dialect this run compares. PostgreSQL is the default (ADR 0004), so an
   unqualified invocation is the M3 gate exactly as it was; `--dialect sqlite`
   adds M6's second dialect to the same gate. `--dialect=value` is accepted
   alongside `--dialect value`. */
const dialectFlag = argv.find((a) => a === '--dialect' || a.startsWith('--dialect='));
const dialectName = dialectFlag === undefined
  ? 'postgresql'
  : (dialectFlag === '--dialect' ? argv[argv.indexOf('--dialect') + 1] : dialectFlag.slice('--dialect='.length));
const dialect = String(dialectName ?? '').trim().toLowerCase();
if (dialect !== 'postgresql' && dialect !== 'sqlite') {
  console.error('adapter-parity-audit: --dialect takes "postgresql" or "sqlite", not ' + JSON.stringify(dialectName) + '.');
  process.exit(2);
}

/* web/node_modules holds esbuild, ajv and PGlite; the repo root has no
   node_modules, so a createRequire rooted in web/ is how a script resolves
   them. */
const webRequire = createRequire(path.join(repoRoot, 'web', 'package.json'));
const API_DIR = path.join(repoRoot, 'web', 'src', 'api');
const DATASET_DIR = path.join(repoRoot, 'backend', 'internal', 'dataset');

const tally = {
  match: 0,
  representation: 0,
  diverges: 0,
  excluded: 0,
  multiStatement: 0,
};
const typeLabelCounts = new Map();
const reprCounts = new Map();
const volatileExcluded = [];

function pad(s, n) {
  const str = String(s);
  return str.length >= n ? str + ' ' : str + ' '.repeat(n - str.length);
}

/* ---------- bundling the real adapters ---------- */

/** A plugin that swaps the two Vite-only imports (`?raw`, import.meta.glob)
    for Node-visible shims, so the shipping pglite.ts can run under Node. */
function viteStubPlugin() {
  return {
    name: 'qc-vite-stubs',
    setup(build) {
      build.onResolve({ filter: /(^|\/)dataset$/ }, () => ({ path: 'qc-dataset', namespace: 'qc-stub' }));
      build.onResolve({ filter: /(^|\/)content$/ }, () => ({ path: 'qc-content', namespace: 'qc-stub' }));
      build.onLoad({ filter: /.*/, namespace: 'qc-stub' }, (args) => {
        if (args.path === 'qc-dataset') {
          /* Exactly what web/src/api/dataset.ts exports: the schema and seed
             read from the one Go source of truth, the perf fixture (M5's second
             dataset) and the version from the schema header. The stub has to
             mirror the real module's export list, or a new fixture in the app
             becomes a build error here rather than a missing test. */
          const schemaSql = fs.readFileSync(path.join(DATASET_DIR, 'schema.sql'), 'utf8');
          const seedSql = fs.readFileSync(path.join(DATASET_DIR, 'seed.sql'), 'utf8');
          const perfSql = fs.readFileSync(path.join(DATASET_DIR, 'perf.sql'), 'utf8');
          const datasetVersion = Number(/dataset_version:\s*(\d+)/.exec(schemaSql)?.[1] ?? 1);
          return {
            loader: 'js',
            contents: 'export const schemaSql = ' + JSON.stringify(schemaSql) + ';\n'
              + 'export const seedSql = ' + JSON.stringify(seedSql) + ';\n'
              + 'export const perfSql = ' + JSON.stringify(perfSql) + ';\n'
              + 'export const datasetVersion = ' + datasetVersion + ';',
          };
        }
        if (args.path === 'qc-content') {
          /* Chapter prose lives in app.content_docs and never reaches a lesson
             query; stubbing the seeder keeps it out of the parity run. */
          return { loader: 'js', contents: 'export const BUNDLED_CONTENT = {};\nexport async function seedContentDocs() {}' };
        }
        return null;
      });
    },
  };
}

/** A virtual entry that re-exports the two shipping modules the audit needs. */
function commonEntryPlugin() {
  return {
    name: 'qc-common-entry',
    setup(build) {
      build.onResolve({ filter: /^qc:common$/ }, () => ({ path: 'common', namespace: 'qc-common' }));
      build.onLoad({ filter: /.*/, namespace: 'qc-common' }, () => ({
        loader: 'js',
        resolveDir: repoRoot,
        contents: 'export { COURSES } from ' + JSON.stringify(path.join(repoRoot, 'web', 'src', 'data', 'curriculum.ts')) + ';\n'
          + 'export { compareResult } from ' + JSON.stringify(path.join(API_DIR, 'compare.ts')) + ';\n',
      }));
    },
  };
}

/**
 * bundleCommon compiles the two shipping modules both halves import — the
 * curriculum and the §6.1 comparator — into one CommonJS module.
 */
async function bundleCommon(tmpDir) {
  const esbuild = webRequire('esbuild');
  const out = path.join(tmpDir, 'qc-curriculum-and-compare.cjs');
  await esbuild.build({
    entryPoints: ['qc:common'],
    bundle: true,
    format: 'cjs',
    platform: 'node',
    target: 'node20',
    outfile: out,
    plugins: [commonEntryPlugin()],
    logLevel: 'silent',
  });
  return webRequire(out);
}

/**
 * bundleAdapters compiles the real frontend modules into a temp directory and
 * imports them, returning the curriculum, the §6.1 comparator and — unless
 * QC_SKIP_PGLITE is set — the web adapter's own executeSql.
 */
async function bundleAdapters(tmpDir) {
  const esbuild = webRequire('esbuild');
  const pgliteIndex = path.join(repoRoot, 'web', 'node_modules', '@electric-sql', 'pglite', 'dist', 'index.js');
  const pgliteWorker = path.join(repoRoot, 'web', 'node_modules', '@electric-sql', 'pglite', 'dist', 'worker', 'index.js');

  const common = await bundleCommon(tmpDir);

  let executeSql = null;
  let resetEnvironment = null;
  if (!skipPglite) {
    const pgliteOut = path.join(tmpDir, 'qc-pglite-adapter.mjs');
    await esbuild.build({
      entryPoints: [path.join(API_DIR, 'pglite.ts')],
      bundle: true,
      format: 'esm',
      platform: 'node',
      target: 'node20',
      outfile: pgliteOut,
      plugins: [viteStubPlugin()],
      /* PGlite stays external and is rewired to its real file so its own
         import.meta.url-relative WASM and data files still resolve. */
      alias: { '@electric-sql/pglite': pgliteIndex, '@electric-sql/pglite/worker': pgliteWorker },
      external: [pgliteIndex, pgliteWorker],
      logLevel: 'silent',
    });
    const adapter = await import(pathToFileURL(pgliteOut).href);
    executeSql = adapter.executeSql;
    /* ADR 0003 gives the browser adapter the same restore the gateway has, so a
       write probe can be undone on both sides before the next probe reads. */
    resetEnvironment = adapter.resetEnvironment;
  }

  return { courses: common.COURSES, compareResult: common.compareResult, executeSql, resetEnvironment };
}

/**
 * The two Vite-only import forms the SQLite adapter uses, answered for Node.
 *
 * `sqliteEngine.ts` imports its 868 KB wasm build as
 * `@sqlite.org/sqlite-wasm/sqlite3.wasm?url`, because Vite rewrites
 * `import.meta.url` when it pre-bundles a dependency and the engine would then
 * fetch the SPA's index.html instead of its own wasm. esbuild cannot resolve a
 * specifier with a query, and the module hands whatever it gets straight to the
 * build's `locateFile`. Under Node that build calls `fs.readFileSync`, so the
 * module must receive an **absolute filesystem path**, not a `file://` URL —
 * which is why the plugin resolves the specifier through Node (its package
 * `exports` map answers `./sqlite3.wasm`) and loads it as a JS module whose
 * default export is that path.
 *
 * `sqliteDataset.ts` imports the Go module's schema and seed as `?raw` text, so
 * the same plugin answers that form by handing esbuild the file's contents as
 * a `text` module. Neither module is stubbed: the shipping text is what runs.
 */
function sqliteVitePlugin() {
  const resolveAsset = (args, query, namespace) => {
    const specifier = args.path.slice(0, -query.length);
    try {
      return {
        path: specifier.startsWith('.') || specifier.startsWith('/')
          ? path.resolve(args.resolveDir, specifier)
          : webRequire.resolve(specifier),
        namespace,
      };
    } catch (e) {
      return { errors: [{ text: 'the SQLite adapter imports ' + args.path + ', which cannot be resolved: ' + e.message }] };
    }
  };
  return {
    name: 'qc-sqlite-vite-imports',
    setup(build) {
      build.onResolve({ filter: /\?url$/ }, (args) => resolveAsset(args, '?url', 'qc-url'));
      build.onResolve({ filter: /\?raw$/ }, (args) => resolveAsset(args, '?raw', 'qc-raw'));
      build.onLoad({ filter: /.*/, namespace: 'qc-url' }, (args) => ({
        loader: 'js',
        contents: 'export default ' + JSON.stringify(args.path) + ';\n',
      }));
      build.onLoad({ filter: /.*/, namespace: 'qc-raw' }, (args) => ({
        loader: 'text',
        contents: fs.readFileSync(args.path, 'utf8'),
      }));
    },
  };
}

/**
 * bundleSqliteAdapter compiles the shipping browser SQLite adapter
 * (web/src/api/sqlite.ts) into a temp module and imports it.
 *
 * `@sqlite.org/sqlite-wasm` is aliased to its Node entry and left external —
 * the same arrangement PGlite gets above — so its own `import.meta.url` still
 * decides where its glue lives, and the module is the one the package ships for
 * Node rather than a browser bundle esbuild guesses at.
 *
 * Node has no `Worker`, so the adapter takes its documented main-thread
 * fallback and announces it; that is the transport this half runs, and the
 * adapter's own capabilities report the placement truthfully.
 */
async function bundleSqliteAdapter(tmpDir) {
  const esbuild = webRequire('esbuild');
  const wasmNode = path.join(repoRoot, 'web', 'node_modules', '@sqlite.org', 'sqlite-wasm', 'dist', 'node.mjs');
  const out = path.join(tmpDir, 'qc-sqlite-adapter.mjs');
  await esbuild.build({
    entryPoints: [path.join(API_DIR, 'sqlite.ts')],
    bundle: true,
    format: 'esm',
    platform: 'node',
    target: 'node20',
    outfile: out,
    plugins: [sqliteVitePlugin()],
    alias: { '@sqlite.org/sqlite-wasm': wasmNode },
    external: [wasmNode],
    logLevel: 'silent',
  });
  return import(pathToFileURL(out).href);
}

/* ---------- collecting the lesson SQL ---------- */

/** Probe statements: the failure, type and formatting paths a curated lesson
    does not reach. Each is deterministic (ordered or single-row). */
const PROBES = [
  { id: 'probe unknown relation', sql: 'SELECT * FROM orderss ORDER BY 1' },
  { id: 'probe unknown column', sql: 'SELECT nope FROM customers ORDER BY 1' },
  { id: 'probe syntax error', sql: 'SELEC id FROM customers' },
  { id: 'probe division by zero', sql: 'SELECT 1 / 0' },
  { id: 'probe bad cast', sql: "SELECT 'not a number'::integer" },
  { id: 'probe write (writable workspace)', sql: 'UPDATE products SET stock = 0' },
  { id: 'probe number types', sql: 'SELECT id, price, stock, active FROM products ORDER BY id' },
  { id: 'probe date column', sql: 'SELECT id, signup_date FROM customers ORDER BY id' },
  { id: 'probe timestamp column', sql: 'SELECT id, ordered_at, status FROM orders ORDER BY id' },
  { id: 'probe null types', sql: 'SELECT NULL::text AS a, NULL::integer AS b' },
  { id: 'probe literal types', sql: "SELECT 'x' AS a, 1 AS b, true AS c, 1.5 AS d" },
  { id: 'probe bigint aggregate', sql: 'SELECT count(*) AS n FROM orders' },
  { id: 'probe numeric average', sql: 'SELECT round(avg(price), 2) AS avg_price FROM products' },
  { id: 'probe empty result', sql: 'SELECT name FROM customers WHERE id < 0 ORDER BY name' },
];

/**
 * Probe statements for the SQLite half. Each is deterministic (single-row or
 * ordered) and, except for the two that are *meant* to be expensive, cheap: the
 * row-cap probe stops at 500 rows and the deadline probe stops at the 5 s
 * deadline both adapters share (engine.DefaultLimits vs SQLITE_TIMEOUT_MS).
 *
 */
const SQLITE_PROBES = [
  { id: 'probe sqlite syntax error', sql: 'SELEC id FROM products' },
  { id: 'probe sqlite incomplete input', sql: 'SELECT id FROM products WHERE' },
  { id: 'probe sqlite no such table', sql: 'SELECT * FROM order_item' },
  { id: 'probe sqlite no such column', sql: 'SELECT nope FROM products' },
  { id: 'probe sqlite no such function', sql: "SELECT date_trunc('month', signup_date) FROM customers" },
  { id: 'probe sqlite unique constraint', sql: "INSERT INTO products (id, name, category, price, stock, active) VALUES (1, 'Probe', 'Probe', 1, 1, 1)" },
  { id: 'probe sqlite not null constraint', sql: "INSERT INTO customers (id, name, signup_date) VALUES (9001, NULL, '2026-01-01')" },
  { id: 'probe sqlite storage classes', sql: 'SELECT id, typeof(id) AS id_class, typeof(name) AS name_class, typeof(price) AS price_class, typeof(active) AS active_class FROM products ORDER BY id' },
  { id: 'probe sqlite division by zero', sql: 'SELECT 1 / 0 AS ratio' },
  { id: 'probe sqlite empty result', sql: 'SELECT name FROM customers WHERE id < 0 ORDER BY name' },
  { id: 'probe sqlite word-count aggregate', sql: 'SELECT count(*) AS n FROM order_items' },
  { id: 'probe sqlite declared DATE column', sql: 'SELECT id, signup_date FROM customers ORDER BY id' },
  { id: 'probe sqlite declared TIMESTAMP column', sql: 'SELECT id, ordered_at FROM orders ORDER BY id' },
  { id: 'probe sqlite row cap (500)', sql: 'WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM c WHERE i < 600) SELECT i FROM c' },
  { id: 'probe sqlite deadline (5 s)', sql: 'WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM c) SELECT count(*) FROM c' },
];

/* Storage-layer pragmas: what an engine reports about the database *file* — its
   journal mode, its page geometry, its caches, its own sanity — rather than
   about the rows in it. Two engines over two different files are not required
   to agree on any of them, and measured they do not: `PRAGMA journal_mode` is
   "memory" in the browser (its workspace is an in-memory database, ADR 0007)
   and "delete" in the gateway (its file is on disk), and `PRAGMA page_size` is
   8192 from the wasm build and 4096 from the pure-Go one, with `page_count` and
   anything derived from it moving with it. The names are listed rather than
   matched loosely so that a *data* pragma — `pragma_table_info`,
   `pragma_foreign_key_list`, the ones the schema lessons read — is still
   compared.

   The trailing boundary is a `\b` and the leading one is not, because the table
   form spells the same setting `pragma_journal_mode()` and `pragma_page_size()`:
   `_` is a word character, so a leading `\b` would miss exactly the calls the
   schema lessons use. */
const SQLITE_FILE_PRAGMAS = /(journal_mode|page_size|page_count|freelist_count|max_page_count|cache_size|cache_spill|mmap_size|locking_mode|synchronous|auto_vacuum|temp_store|encoding|integrity_check|data_version|wal_checkpoint)\b/i;

/**
 * The claim a verified exclusion makes, checked rather than trusted: the two
 * adapters may differ only in columns `mayDiffer` accepts. Every other column
 * must agree under §6.1, and at least one accepted column must actually differ
 * — an exclusion that has stopped being needed fails and asks to be removed,
 * instead of sitting in the list covering nothing.
 */
function sqliteDifferenceConfinedTo(mayDiffer) {
  return (a, b) => {
    if (a.status !== b.status) return { ok: false, detail: 'status: web=' + a.status + ' gateway=' + b.status };
    if (a.status !== 'succeeded') return { ok: false, detail: 'the statement failed (' + (a.error?.code ?? 'no code') + '), so there is nothing to confine' };
    if (a.row_count !== b.row_count) return { ok: false, detail: 'row count: web=' + a.row_count + ' gateway=' + b.row_count };
    let acceptedColumnDiffered = false;
    for (let c = 0; c < Math.max(a.columns.length, b.columns.length); c++) {
      const accepted = mayDiffer(a.columns[c]);
      for (let row = 0; row < Math.min(a.rows.length, b.rows.length); row++) {
        const av = a.rows[row]?.[c];
        const bv = b.rows[row]?.[c];
        if (accepted) {
          if (av !== bv) acceptedColumnDiffered = true;
          continue;
        }
        if (!valueEqual61(av, bv)) {
          return {
            ok: false,
            detail: 'column ' + (a.columns[c]?.name ?? '#' + (c + 1)) + ' (label ' + a.columns[c]?.type + ') row ' + (row + 1)
              + ': web=' + JSON.stringify(av) + ' gateway=' + JSON.stringify(bv),
          };
        }
      }
    }
    if (!acceptedColumnDiffered) {
      return { ok: false, detail: 'no column the exclusion covers differed; the exclusion is no longer needed — delete it and compare this statement' };
    }
    return { ok: true, detail: null };
  };
}

/**
 * SQLite statements excluded from the *value* comparison, each with the reason
 * printed beside it. Nothing is normalised away silently: an excluded statement
 * is still run through both adapters, what differed is printed, and where a
 * `verify` is attached the exclusion's own claim is asserted as a check.
 *
 * `matches` recognises the exclusion's shape in lesson SQL, so a statement
 * needs no per-statement tag to fall under one.
 */
const SQLITE_EXCLUSIONS = [
  {
    id: 'explain-query-plan',
    matches: (sql) => /^\s*explain\s+query\s+plan\b/i.test(stripComments(sql)),
    why: 'EXPLAIN QUERY PLAN returns the planner\'s own text — which scan or search each step chose — and that is a property of the build and its statistics, not a value the contract covers. The two sides are different SQLite builds, so their plans are not required to agree. The PostgreSQL half excludes EXPLAIN for the same reason.',
  },
  {
    id: 'rowid',
    matches: (sql) => /\browid\b/i.test(stripComments(sql)),
    why: 'rowid is a physical address, not a key: SQLite documents it as unstable across VACUUM, and each adapter\'s database is its own file with its own insertion history, so the two are not required to number rows the same way. The label differs too — the wasm build reports INTEGER from sqlite3_column_decltype while the Go driver reports expression, because decltype answers for a declared column and rowid is implicit. Both are properties of the file, like a plan, so this is excluded rather than compared.',
  },
  {
    id: 'storage-layer-pragma',
    /* `/\bpragma/`, not `/\bpragma\b/`: the table-valued form is spelled
       `pragma_journal_mode()`, and `_` is a word character, so a trailing
       boundary would miss every call the schema lessons make. */
    matches: (sql) => /\bpragma/i.test(stripComments(sql)) && SQLITE_FILE_PRAGMAS.test(stripComments(sql)),
    why: 'These read a fact about the database *file* — journal mode, page size, page count, a cache setting — and the two adapters are two files: the browser\'s workspace is an in-memory database (ADR 0007) while the gateway\'s is a file on disk, and the two SQLite builds ship different defaults. Measured: PRAGMA journal_mode is "memory" on the web side and "delete" on the gateway; PRAGMA page_size is 8192 on the web side and 4096 on the gateway, and page_count and anything computed from it move with it. A plan and a rowid are excluded for the same reason — these are properties of the file, not values from the dataset. The exclusion is verified: every *declared* column in such a statement must still agree, so a real divergence in stored data cannot hide here.',
    verify: sqliteDifferenceConfinedTo((col) => canonicalSqliteType(col?.type) === 'expression'),
  },
];

/** The exclusion a statement falls under, or null. */
function sqliteExclusionFor(st) {
  for (const e of SQLITE_EXCLUSIONS) {
    if (typeof e.matches === 'function' && e.matches(st.sql)) return e;
  }
  return null;
}

/**
 * collectStatements pulls the runnable lesson SQL out of the curriculum.
 *
 * Exercises contribute their reference solution (the text the app grades a
 * correct answer against) and examples contribute their SQL. Starters are
 * skipped on purpose: they are scaffolds full of `-- placeholder` comments,
 * not statements. A course that is not built has no modules to run.
 *
 * `syntax` blocks are collected separately as display-only. LessonScreen renders
 * them as a static `<pre className="syntaxblock">{b.sql}</pre>` — no editor and
 * no Run button — so the learner can never execute one, and one of them is a
 * deliberate two-statement contrast ("Not safe: … / Safe: …") that exists to be
 * read. Comparing the adapters' execution of it would measure something the UI
 * does not do; what the audit can honestly assert instead is that every block
 * the learner *can* run is a single statement.
 */
/**
 * collectModuleBlocks walks one module's blocks and appends what it finds to
 * the three lists. It is shared by both halves so the rules cannot drift: an
 * exercise contributes its reference solution (never its starter, which is a
 * scaffold), an example contributes its SQL, and a syntax block is display-only
 * because LessonScreen renders it as a static `<pre>` with no editor and no Run
 * button — the learner can never execute one.
 */
function collectModuleBlocks(course, mod, statements, skipped, displayOnly) {
  let syntaxSeq = 0;
  for (const block of mod.blocks ?? []) {
    if (block.type === 'exercise') {
      if (typeof block.solution === 'string' && block.solution.trim()) {
        statements.push({ kind: 'solution', id: block.id ?? (mod.id + '.exercise'), course: course.id, sql: block.solution });
      } else {
        skipped.push({ what: course.id + '/' + (block.id ?? 'exercise'), why: 'no reference solution' });
      }
      if (typeof block.starter === 'string' && block.starter.trim()) {
        skipped.push({ what: (block.id ?? mod.id) + ' starter', why: 'scaffold with placeholder comments, not a statement' });
      }
      continue;
    }
    if (block.type === 'example' && typeof block.sql === 'string' && block.sql.trim()) {
      statements.push({ kind: 'example', id: block.id ?? (mod.id + '.example'), course: course.id, sql: block.sql });
      continue;
    }
    if (block.type === 'syntax' && typeof block.sql === 'string' && block.sql.trim()) {
      syntaxSeq += 1;
      displayOnly.push({
        id: mod.id + ' syntax ' + syntaxSeq,
        course: course.id,
        statements: countStatements(block.sql),
        sql: block.sql,
      });
    }
  }
}

/** The PostgreSQL half's statement set: every built course, every module. */
function collectStatements(courses) {
  const statements = [];
  const skipped = [];
  const displayOnly = [];
  for (const course of courses) {
    if (!Array.isArray(course.modules) || course.modules.length === 0) {
      skipped.push({ what: 'course "' + course.title + '" (' + course.id + ')', why: 'not built — no modules' });
      continue;
    }
    for (const mod of course.modules) collectModuleBlocks(course, mod, statements, skipped, displayOnly);
  }
  for (const probe of PROBES) {
    statements.push({ kind: 'probe', id: probe.id, course: 'probes', sql: probe.sql });
  }
  return { statements, skipped, displayOnly };
}

/**
 * The SQLite half's statement set: the modules whose dialect is `sqlite`, and
 * SQLITE_PROBES.
 *
 * The dialect is resolved the way the app resolves it (`web/src/lib/dialect.ts`):
 * a chapter's own `dialect` wins, and a course that names one covers the
 * chapters that do not. A chapter must be compared on the engine it teaches —
 * comparing SQLite lesson SQL on PostgreSQL would measure nothing and would
 * pass by accident wherever both engines happen to fail the same way.
 *
 * A course with no SQLite chapter is reported as a skip with its reason rather
 * than dropped, so the report says which courses were left out and why.
 */
function collectSqliteStatements(courses) {
  const statements = [];
  const skipped = [];
  const displayOnly = [];
  for (const course of courses) {
    const modules = (course.modules ?? []).filter((mod) => (mod.dialect ?? course.dialect) === 'sqlite');
    if (modules.length === 0) {
      skipped.push({
        what: 'course "' + course.title + '" (' + course.id + ')',
        why: 'teaches ' + (course.dialect ?? 'postgresql') + ', not sqlite — no chapter for these adapters to run',
      });
      continue;
    }
    for (const mod of modules) collectModuleBlocks(course, mod, statements, skipped, displayOnly);
  }
  for (const probe of SQLITE_PROBES) {
    statements.push({ kind: 'probe', id: probe.id, course: 'probes', sql: probe.sql });
  }
  return { statements, skipped, displayOnly };
}

/** How many statements a script holds, ignoring a trailing semicolon. */
function countStatements(sql) {
  const body = stripComments(sql).trim().replace(/;\s*$/, '');
  return body ? body.split(';').filter((s) => s.trim()).length : 0;
}

/* ---------- comparing two results ---------- */

function stripComments(sql) {
  return sql.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/--[^\n]*/g, ' ');
}

/** A statement is compared in order when its own text orders the rows. */
function isOrdered(sql) {
  return /\border\s+by\b/i.test(stripComments(sql));
}

/** A block that holds more than one statement (a trailing `;` is not one). */
function isMultiStatement(sql) {
  const body = stripComments(sql).trim().replace(/;\s*$/, '');
  return body.includes(';');
}

function jsonKind(v) {
  if (v === null || v === undefined) return 'null';
  if (Array.isArray(v)) return 'array';
  return typeof v;
}

/* The project's §6.1 value rule, mirroring valueEqual in web/src/api/compare.ts:
   numbers compare with tolerance; a string coerces to a number only when the
   other side is already numeric; NULL only equals NULL; text is never
   normalised. */
const DECIMAL61 = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;

function isNumeric61(v) {
  return typeof v === 'number' || typeof v === 'bigint';
}

function toNumber61(v) {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null;
  if (typeof v === 'bigint') return Number(v);
  if (typeof v === 'string') {
    const s = v.trim();
    if (s === '' || !DECIMAL61.test(s)) return null;
    const n = Number(s);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

function toBool61(v) {
  if (typeof v === 'boolean') return v;
  if (typeof v === 'string') {
    const s = v.trim().toLowerCase();
    if (s === 'true' || s === 't' || s === '1') return true;
    if (s === 'false' || s === 'f' || s === '0') return false;
  }
  return null;
}

function valueEqual61(a, b) {
  if (a === null || a === undefined || b === null || b === undefined) return (a ?? null) === (b ?? null);
  if (isNumeric61(a) || isNumeric61(b)) {
    const an = toNumber61(a);
    const bn = toNumber61(b);
    if (an !== null && bn !== null) return an === bn;
    return false;
  }
  const ab = toBool61(a);
  const bb = toBool61(b);
  if (ab !== null && bb !== null) return ab === bb;
  return a === b;
}

/** Strict cell equality: `"59.00"` and `59` are different, on purpose. */
function sameCell(a, b) {
  if (a === null || a === undefined || b === null || b === undefined) return (a ?? null) === (b ?? null);
  if (typeof a !== typeof b) return false;
  if (typeof a === 'number' || typeof a === 'string' || typeof a === 'boolean') return a === b;
  return JSON.stringify(a) === JSON.stringify(b);
}

function sameRowStrict(a, b) {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  return a.every((v, i) => sameCell(v, b[i]));
}

function rowsMatchStrict(a, b, ordered) {
  if (a.length !== b.length) return false;
  if (ordered) return a.every((row, i) => sameRowStrict(row, b[i]));
  const left = a.map((row) => JSON.stringify(row)).sort();
  const right = b.map((row) => JSON.stringify(row)).sort();
  return left.every((v, i) => v === right[i]);
}

/** Names the first cell the project's own rule says differs, with its type. */
function firstValueDiff(a, b, canonical = canonicalType) {
  const cols = Math.min(a.columns.length, b.columns.length);
  const rows = Math.min(a.rows.length, b.rows.length);
  for (let c = 0; c < cols; c++) {
    for (let row = 0; row < rows; row++) {
      const av = a.rows[row]?.[c];
      const bv = b.rows[row]?.[c];
      if (!valueEqual61(av, bv)) {
        return 'row ' + (row + 1) + ' column ' + a.columns[c].name
          + ' (' + canonical(a.columns[c].type) + '): web=' + JSON.stringify(av)
          + ' gateway=' + JSON.stringify(bv);
      }
    }
  }
  return '';
}

/** The first cell whose JSON shape differs while §6.1 still calls it equal. */
function firstRepresentationDiff(a, b, canonical = canonicalType) {
  const cols = Math.min(a.columns.length, b.columns.length);
  const rows = Math.min(a.rows.length, b.rows.length);
  for (let c = 0; c < cols; c++) {
    for (let row = 0; row < rows; row++) {
      const av = a.rows[row]?.[c];
      const bv = b.rows[row]?.[c];
      if (!sameCell(av, bv) && valueEqual61(av, bv) && jsonKind(av) !== jsonKind(bv)) {
        return 'column ' + a.columns[c].name + ' (' + canonical(a.columns[c].type) + '): web='
          + jsonKind(av) + ' ' + JSON.stringify(av) + ' gateway=' + jsonKind(bv) + ' ' + JSON.stringify(bv);
      }
    }
  }
  for (let c = 0; c < cols; c++) {
    for (let row = 0; row < rows; row++) {
      const av = a.rows[row]?.[c];
      const bv = b.rows[row]?.[c];
      if (!sameCell(av, bv)) {
        return 'column ' + a.columns[c].name + ' (' + canonical(a.columns[c].type) + '): web='
          + JSON.stringify(av) + ' gateway=' + JSON.stringify(bv);
      }
    }
  }
  return '';
}

/** The project's own §6.1 comparison, the documented meaning of "same result". */
function projectComparison(compareResult, a, b, ordered) {
  const columns = b.columns.map((c) => c.name);
  return compareResult(
    { columns, rows: b.rows },
    { columns, column_order: 'exact', row_order: ordered ? 'exact' : 'ignored', duplicates: 'preserved', numeric_tolerance: 0 },
    { columns: a.columns.map((c) => c.name), rows: a.rows },
  );
}

/**
 * compareFrames judges one statement and returns four lists:
 *   divergences    differences that change the answer a learner sees
 *   findings       row differences the project's own §6.1 rule still calls
 *                  equal: same values, a different JSON representation
 *   typeLabels     columns whose raw type spelling differs (canonicalised for
 *                  the check, printed for the record)
 *   representation the sample cells behind `findings`
 */
function compareFrames(compareResult, a, b, ordered, canonical = canonicalType) {
  const divergences = [];
  const findings = [];
  const typeLabels = [];
  const representation = [];

  if (a.status !== b.status) {
    divergences.push('status: web=' + a.status + ' gateway=' + b.status);
    return { divergences, findings, typeLabels, representation };
  }

  if (a.status === 'failed') {
    const ca = a.error?.code ?? '(none)';
    const cb = b.error?.code ?? '(none)';
    if (ca !== cb) divergences.push('error code: web=' + ca + ' gateway=' + cb);
    return { divergences, findings, typeLabels, representation };
  }

  const namesA = a.columns.map((c) => c.name);
  const namesB = b.columns.map((c) => c.name);
  if (JSON.stringify(namesA) !== JSON.stringify(namesB)) {
    divergences.push('column names: web=[' + namesA.join(', ') + '] gateway=[' + namesB.join(', ') + ']');
  }
  for (let i = 0; i < Math.max(namesA.length, namesB.length); i++) {
    const ta = a.columns[i]?.type;
    const tb = b.columns[i]?.type;
    if (canonical(ta) !== canonical(tb)) {
      divergences.push('column ' + (i + 1) + ' type: web=' + ta + ' gateway=' + tb);
    } else if (ta !== tb) {
      typeLabels.push({ column: namesA[i] ?? namesB[i] ?? ('#' + (i + 1)), web: ta, gateway: tb });
    }
  }

  if (a.row_count !== b.row_count) divergences.push('row count: web=' + a.row_count + ' gateway=' + b.row_count);
  if (a.truncated !== b.truncated) divergences.push('truncated: web=' + a.truncated + ' gateway=' + b.truncated);

  if (!divergences.length && a.row_count === b.row_count) {
    if (!rowsMatchStrict(a.rows, b.rows, ordered)) {
      const cmp = projectComparison(compareResult, a, b, ordered);
      if (cmp.ok) {
        const sample = firstRepresentationDiff(a, b, canonical);
        representation.push(sample || 'rows differ only in JSON representation');
        findings.push('rows agree under the §6.1 rule but not in JSON shape' + (sample ? ' — ' + sample : ''));
      } else {
        const sample = firstValueDiff(a, b, canonical);
        divergences.push('rows differ (' + (cmp.codes.join(', ') || 'strict mismatch') + ')'
          + (sample ? ' — ' + sample : ''));
      }
    }
  }
  return { divergences, findings, typeLabels, representation };
}

/* ---------- contract validation ---------- */

/** An ajv 2020 validator for contracts/query-result.schema.json. */
function contractValidator() {
  const Ajv2020 = webRequire('ajv/dist/2020').default;
  const schema = JSON.parse(fs.readFileSync(path.join(repoRoot, 'contracts', 'query-result.schema.json'), 'utf8'));
  return new Ajv2020({ allErrors: true, strict: false }).compile(schema);
}

function validateResult(validate, value) {
  if (value === null || value === undefined) return ['no body'];
  if (validate(value)) return [];
  return (validate.errors ?? []).slice(0, 3).map((e) => (e.instancePath || '/') + ' ' + e.message);
}

/* ---------- running one statement through each adapter ---------- */

async function queryGateway(base, sql, token = null) {
  const headers = token ? { authorization: 'Bearer ' + token } : {};
  const res = await postJSON(base + '/query', { sql, limits: {} }, 30_000, headers);
  return { httpStatus: res.status, result: res.json };
}

async function queryWeb(executeSql, sql) {
  const started = Date.now();
  const out = await executeSql(sql);
  /* The adapter returns the six contract fields; execution.ts wraps it with
     engine, request_id and duration_ms. Attach the same three so the contract
     schema can judge the result the way the app sees it. */
  return { ...out, engine: PGLITE_ENGINE, request_id: null, duration_ms: Date.now() - started };
}

/** One statement through the gateway's SQLite executor — the exact body the
    browser's HTTP adapter sends for a SQLite chapter. */
async function querySqliteGateway(base, sql, token = null) {
  const headers = token ? { authorization: 'Bearer ' + token } : {};
  const res = await postJSON(base + '/query', { sql, dialect: 'sqlite', limits: {} }, 30_000, headers);
  return { httpStatus: res.status, result: res.json };
}

/** One statement through the browser's wasm SQLite adapter. The adapter already
    fills `engine` (its own version string) and `dialect`, so only the two
    envelope fields `execution.ts` adds are attached here. */
async function querySqliteWeb(executeSqlite, sql) {
  const started = Date.now();
  const out = await executeSqlite(sql);
  return { ...out, request_id: null, duration_ms: Date.now() - started };
}

/**
 * signUpLearner creates a throwaway account and returns its bearer token.
 *
 * ADR 0003 is why the comparison is authenticated: the browser adapter's
 * workspace is writable, and a signed-in gateway session gets its own writable
 * database copied from the sealed template, so the two are the same *kind* of
 * thing. Comparing a writable browser against an anonymous gateway session
 * would compare two different products and call the difference a bug. The
 * account is the audit's own and is dropped with the cluster.
 */
async function signUpLearner(base) {
  const email = 'parity-' + Date.now() + '-' + Math.floor(Math.random() * 1e6) + '@parity.test';
  const res = await postJSON(base + '/auth/signup', {
    email,
    name: 'Parity Audit',
    password: 'parity-audit-password',
  });
  const token = res.json?.token;
  if (!token) {
    throw new Error('could not sign up a learner for the parity run: HTTP ' + res.status + ' ' + String(res.body).slice(0, 200));
  }
  return { email, token };
}

/**
 * resetBoth restores the sealed dataset in both workspaces, so a write probe
 * can never change what a later probe reads. The gateway resets by dropping and
 * re-copying the template; the browser drops and re-seeds its schema.
 */
async function resetBoth(base, token, resetWeb) {
  const started = Date.now();
  const gw = await postJSON(base + '/environment/reset', {}, 60_000, { authorization: 'Bearer ' + token });
  const web = resetWeb ? await resetWeb() : null;
  return { gateway: gw.json, web, durationMs: Date.now() - started };
}

/* Statements that change data or shape. After one runs, both workspaces are
   restored, so probe order cannot manufacture a difference. */
const WRITES = /^\s*(insert|update|delete|create|drop|alter|truncate|grant|revoke|comment)\b/i;
function isWrite(sql) {
  return WRITES.test(stripComments(sql));
}

/** The wall-clock time-of-day inside a date/time string, for the TZ case. */
function timeOfDay(v) {
  const m = /(\d{2}):(\d{2})/.exec(String(v ?? ''));
  return m ? m[1] + ':' + m[2] : null;
}

/* ---------- the SQLite half (M6 step 8) ---------- */

/** What differed in an excluded statement, for the record beside its reason. */
function sqliteCellDiff(a, b) {
  if (a.status === 'failed' || b?.status === 'failed') {
    return 'status web=' + a.status + ' gateway=' + (b?.status ?? '?')
      + ', codes ' + (a.error?.code ?? '-') + '/' + (b?.error?.code ?? '-');
  }
  return firstValueDiff(a, b, canonicalSqliteType) || 'no cell differed';
}

/**
 * runSqliteParity compares the browser's SQLite adapter against the gateway's
 * for every statement the SQLite chapters contain, plus the probes.
 *
 * It is a complete run of its own — it bundles the shipping wasm adapter, boots
 * its own gateway, reports and calls the Reporter's finish (which exits) — so
 * the PostgreSQL half below is reachable only for `--dialect postgresql`.
 * Everything the two halves share (the bundling helpers, `compareFrames`, the
 * §6.1 rule, the contract validator, the Reporter) is shared code, not a copy,
 * so the two dialect runs cannot drift apart in how they judge.
 *
 * The two engines are different *builds* of SQLite, so their version strings
 * are printed and never compared; the comparison is the contract's own fields.
 */
async function runSqliteParity(tmpDir, validate) {
  let child = null;
  const tally = { match: 0, representation: 0, diverges: 0, excluded: 0, multiStatement: 0 };
  const excludedList = [];
  /* The full reason is long and several statements share one; it is printed
     next to the first statement that hits it, and every later one says which
     exclusion it is under. The recap below lists them all by id. */
  const exclusionReasonsShown = new Set();
  const sqliteTypeLabels = new Map();
  const sqliteReprCounts = new Map();

  try {
    const common = await bundleCommon(tmpDir);
    const courses = common.COURSES;
    const compareResult = common.compareResult;
    const adapter = skipPglite ? null : await bundleSqliteAdapter(tmpDir);
    const executeSqlite = adapter ? adapter.executeSqlite : null;

    const { statements, skipped, displayOnly } = collectSqliteStatements(courses);

    /* A validator that cannot reject anything would make every contract check
       vacuous, so prove it can reject before trusting it (the same two checks
       the PostgreSQL half makes, on a sqlite-flavoured result). */
    r.section('contract');
    r.check('contract validator rejects a malformed result', validate({ status: 'succeeded' }) === false);
    r.check('contract validator accepts a well-formed result', validate({
      status: 'succeeded', columns: [], rows: [], row_count: 0, truncated: false,
      duration_ms: 0, error: null, engine: 'test', dialect: 'sqlite',
    }) === true);

    let base = '';
    if (process.env.QC_GATEWAY) {
      base = process.env.QC_GATEWAY.replace(/\/$/, '');
    } else {
      const bin = process.env.QC_BIN || buildGateway('parity-sqlite');
      const started = await startGateway(bin, { ...(await liveEnv('parity-sqlite')), TZ: 'UTC' });
      child = started.child;
      base = started.base;
    }
    console.log('gateway: ' + base);

    /* `/capabilities` is the surface a client asks before it offers a chapter:
       it is where the gateway lists the dialects it can route to, which is the
       difference between comparing two engines and comparing two refusals. */
    const caps = await getJSON(base + '/capabilities');
    const dialects = Array.isArray(caps.json?.dialects) ? caps.json.dialects : [];
    const diag = await getJSON(base + '/diagnostics');
    console.log('         dialects served: [' + dialects.join(', ') + ']');
    if (diag.json?.engine?.ready === true) {
      console.log('         its PostgreSQL engine is ' + diag.json.engine.version + ' (the other half; not used here)');
    }
    r.check('the gateway serves the sqlite dialect — otherwise this run compares two refusals, not two engines',
      dialects.includes('sqlite'), { dialects });

    /* ADR 0003's split, on the SQLite side: the gateway hands a signed-in
       learner their own writable database file, and the browser adapter's
       workspace is writable too. Being signed in is what makes the two the same
       kind of thing — the anonymous path is a single shared, read-only fixture,
       so a constraint probe would be refused for the wrong reason there and the
       audit would be comparing a read-only engine against a writable one. */
    const learner = await signUpLearner(base);
    console.log('session: ' + learner.email + ' (a writable SQLite file on the gateway, a writable workspace in the browser)');
    console.log('note:    no statement in this half changes data — the constraint probes fail and change nothing — and the');
    console.log('         gateway has no route that restores a SQLite workspace, so the run never has anything to undo.');

    /* One statement first, so an environment fault announces itself here rather
       than as dozens of "divergences", and so the two engine labels can be
       printed: they are information, never a check (see the header). */
    const smoke = await querySqliteGateway(base, 'SELECT count(*) AS n FROM customers', learner.token);
    const smokeCount = smoke.result?.rows?.[0]?.[0];
    const gatewayEngine = String(smoke.result?.engine ?? '(unknown)');
    r.check('the gateway answers a SQLite statement (count(customers) = 30)', smokeCount === 30, smoke.result);

    let webEngine = '(not run)';
    if (executeSqlite) {
      const notes = [];
      const originalWarn = console.warn;
      console.warn = (...args) => notes.push(args.map(String).join(' '));
      let warm = null;
      try {
        warm = await executeSqlite('SELECT 1 AS warmup');
      } finally {
        console.warn = originalWarn;
      }
      webEngine = String(warm?.engine ?? '(unknown)');
      console.log('web:     the browser adapter in-process — Node has no Worker, so it takes its documented main-thread fallback');
      console.log('         ' + (notes.length ? notes.join(' ').slice(0, 240) : 'the adapter logged no fallback note'));
    } else {
      console.log('web:     skipped (QC_SKIP_PGLITE=1 — in this half that skips the *browser* adapter, which is the web side)');
    }
    console.log('engines: web="' + webEngine + '" gateway="' + gatewayEngine + '"');
    console.log('         two different builds of SQLite: the semantics in between are the contract, the version string is');
    console.log('         information. A version-string difference is expected and is never a finding.');

    r.section('lesson statements (' + statements.length + ')');

    for (const st of statements) {
      const ordered = isOrdered(st.sql);
      const label = st.id + ' (' + st.kind + ')';
      const exclusion = sqliteExclusionFor(st);

      let gateway;
      try {
        gateway = await querySqliteGateway(base, st.sql, learner.token);
      } catch (err) {
        r.check(label, false, 'gateway request failed: ' + err.message);
        r.record(st.id, 'error', 'error', '-', '-', 'harness error');
        continue;
      }
      const b = gateway.result;
      const bInvalid = validateResult(validate, b);

      let a = null;
      if (executeSqlite) {
        try {
          a = await querySqliteWeb(executeSqlite, st.sql);
        } catch (err) {
          r.check(label, false, 'web adapter threw: ' + err.message);
          r.record(st.id, 'threw', b?.status ?? 'error', '-', String(b?.row_count ?? '-'), 'harness error');
          continue;
        }
      }

      const divergences = [];
      if (bInvalid.length) divergences.push('gateway response violates the contract: ' + bInvalid.join('; '));
      if (executeSqlite) {
        const aInvalid = validateResult(validate, a);
        if (aInvalid.length) divergences.push('web response violates the contract: ' + aInvalid.join('; '));
        if (a.status === 'failed' && !CLOSED_CODES.has(a.error?.code)) {
          divergences.push('web error code outside the closed enum: ' + a.error?.code);
        }
        /* The dialect travels with the rows, and a result that cannot say which
           engine produced it cannot be graded — so it is compared. */
        if (a.status === 'succeeded' && a.dialect !== 'sqlite') {
          divergences.push('web dialect: ' + JSON.stringify(a.dialect) + ', expected "sqlite"');
        }
      }
      if (b?.status === 'failed' && !CLOSED_CODES.has(b.error?.code)) {
        divergences.push('gateway error code outside the closed enum: ' + b.error?.code);
      }
      if (b?.status === 'succeeded' && b.dialect !== 'sqlite') {
        divergences.push('gateway dialect: ' + JSON.stringify(b.dialect) + ', expected "sqlite"');
      }

      /* The same content rule the PostgreSQL half enforces: what the learner
         can run is one statement, because POST /query takes one. */
      if (st.kind !== 'probe' && isMultiStatement(st.sql)) {
        tally.multiStatement += 1;
        r.check(label, false, 'runnable lesson content holds more than one statement; submissions are one statement, so this block cannot run on the gateway at all');
        r.record(st.id, a?.status ?? '?', b?.status ?? '?', '-', String(b?.row_count ?? '-'), 'multi-statement content');
        continue;
      }

      if (!executeSqlite) {
        r.skip(label, 'QC_SKIP_PGLITE=1 — the browser SQLite adapter was not run');
        r.record(st.id, 'skipped', b?.status ?? '?', '-', String(b?.row_count ?? '-'), 'skipped');
        continue;
      }

      /* A statement under an exclusion is still run through both adapters; what
         changes is how it is judged. The exclusion's own claim — where it makes
         one — is asserted, so an exclusion can never hide a second difference. */
      if (exclusion) {
        const detail = sqliteCellDiff(a, b);
        const claim = typeof exclusion.verify === 'function' ? exclusion.verify(a, b) : { ok: true, detail: null };
        const ok = divergences.length === 0 && claim.ok === true;
        r.check(label + ' — the exclusion holds', ok, { divergences, claim: claim.detail });
        console.log('  skip   ' + st.id + ' (' + a.status + '/' + (b?.status ?? '?') + ') — excluded (' + exclusion.id + '): ' + detail);
        if (!exclusionReasonsShown.has(exclusion.id)) {
          exclusionReasonsShown.add(exclusion.id);
          console.log('           reason: ' + exclusion.why);
        }
        excludedList.push({ id: st.id, exclusion: exclusion.id, why: exclusion.why, what: detail });
        tally.excluded += 1;
        r.record(st.id, a.status, b?.status ?? '?', String(a?.row_count ?? '-'), String(b?.row_count ?? '-'), 'excluded (driver rule)');
        continue;
      }

      const cmp = compareFrames(compareResult, a, b, ordered, canonicalSqliteType);
      divergences.push(...cmp.divergences);
      const findings = cmp.findings;
      for (const tl of cmp.typeLabels) {
        const key = tl.web + ' | ' + tl.gateway;
        if (!sqliteTypeLabels.has(key)) sqliteTypeLabels.set(key, new Set());
        sqliteTypeLabels.get(key).add(tl.column);
      }
      for (const detail of cmp.representation) {
        sqliteReprCounts.set(detail, (sqliteReprCounts.get(detail) ?? 0) + 1);
      }

      const rows = b?.row_count ?? '-';
      const verdict = divergences.length ? 'DIVERGES' : findings.length ? 'representation' : 'match';
      if (verdict === 'match') tally.match += 1;
      else if (verdict === 'representation') tally.representation += 1;
      else tally.diverges += 1;

      const ok = divergences.length === 0 && findings.length === 0;
      r.check(label + ' — ' + rows + ' row(s)', ok, { divergences, findings });
      if (!ordered && ok) console.log('           (no ORDER BY — compared as a multiset)');
      for (const f of findings) console.log('  FINDING  ' + st.id + ': ' + f);
      r.record(st.id, a.status, b?.status ?? '?', String(a.row_count), String(rows), verdict);
    }

    /* Statements that were not runnable, grouped so the report says plainly
       what was left out and why. */
    if (skipped.length) {
      r.section('not run (' + skipped.length + ')');
      const byReason = new Map();
      for (const s of skipped) {
        if (!byReason.has(s.why)) byReason.set(s.why, []);
        byReason.get(s.why).push(s.what);
      }
      for (const [why, whats] of byReason) {
        console.log('  skip   ' + whats.length + ' item(s) — ' + why + ': ' + whats.slice(0, 4).join(', ') + (whats.length > 4 ? ', …' : ''));
        r.skipped += 1;
      }
    }

    r.section('display-only listings (' + displayOnly.length + ') — never executed by the UI');
    for (const b of displayOnly) {
      console.log('  skip   ' + b.id + ' — ' + b.statements + ' statement(s) in a static <pre>');
    }
    if (displayOnly.length) {
      console.log('  note   LessonScreen renders a syntax block as a <pre> with no editor and no Run button, so the');
      console.log('         learner can never execute one; the blocks exist to be read, and one deliberately contrasts a');
      console.log('         "not safe" form with a "safe" one.');
    }

    if (excludedList.length) {
      r.section('excluded from the value comparison (' + excludedList.length + ', all still executed on both sides)');
      const byExclusion = new Map();
      for (const e of excludedList) {
        if (!byExclusion.has(e.exclusion)) byExclusion.set(e.exclusion, []);
        byExclusion.get(e.exclusion).push(e);
      }
      for (const [id, entries] of byExclusion) {
        console.log('  [' + id + '] ' + entries.length + ' statement(s): ' + entries.map((e) => e.id).join(', '));
        for (const e of entries) console.log('      ' + e.id + ' — ' + e.what);
        console.log('      reason: ' + entries[0].why);
        r.skipped += 1;
      }
    }

    if (sqliteTypeLabels.size) {
      r.section('column labels — raw spelling and who must change');
      console.log('  canonical spelling for SQLite is the declared type verbatim, with `expression` for a column that');
      console.log('  has none (contracts/dialects/sqlite.json value_encoding.column_type_label). A difference here is');
      console.log('  a divergence, not a finding:');
      console.log('  ' + pad('web', 16) + pad('gateway', 16) + pad('canonical', 16) + 'columns');
      for (const [key, cols] of sqliteTypeLabels) {
        const [webName, gatewayName] = key.split(' | ');
        console.log('  ' + pad(webName, 16) + pad(gatewayName, 16) + pad(canonicalSqliteType(webName), 16) + [...cols].slice(0, 8).join(', '));
      }
      r.failed += sqliteTypeLabels.size;
    }

    if (sqliteReprCounts.size) {
      r.section('representation findings (fail: values agree under §6.1, JSON shape does not)');
      for (const [detail, count] of sqliteReprCounts) {
        console.log('  FINDING  ' + detail + ' (' + count + ' statement(s))');
      }
      r.failed += sqliteReprCounts.size;
    }
  } catch (err) {
    r.failed += 1;
    console.log('  FAIL   audit harness — ' + err.message);
  } finally {
    stopGateway(child);
    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch { /* best effort */ }
  }

  r.section('summary');
  console.log('  statements compared:    ' + (tally.match + tally.representation + tally.diverges));
  console.log('  match:                  ' + tally.match);
  console.log('  representation:         ' + tally.representation + ' (same values under §6.1, different JSON shape)');
  console.log('  diverge:                ' + tally.diverges);
  console.log('  excluded (driver rule): ' + tally.excluded);
  console.log('  multi-statement:        ' + tally.multiStatement);

  if (markdown) {
    console.log('\n--- markdown ---');
    console.log(r.markdownTable(['statement', 'status web', 'status gateway', 'rows web', 'rows gateway', 'verdict']));
  }
  r.finish();
}

/* ---------- main ---------- */

let child = null;
let tmpDir = null;

try {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qc-parity-'));
  const validate = contractValidator();

  /* `--dialect sqlite` is the second dialect's half of the same gate, and it is
     a complete run of its own: it bundles the browser adapter and the common
     modules, boots its own gateway, prints its own sections and summary, and
     exits through the Reporter. Everything below is the PostgreSQL half (the
     default), which is why it is left exactly as it was. */
  if (dialect === 'sqlite') {
    await runSqliteParity(tmpDir, validate);
    process.exit(r.failed ? 1 : 0);
  }

  const { courses, compareResult, executeSql, resetEnvironment } = await bundleAdapters(tmpDir);
  const resetWeb = resetEnvironment ?? null;

  /* A validator that cannot reject anything would make every contract check
     vacuous, so prove it can reject before trusting it. */
  r.section('contract');
  r.check('contract validator rejects a malformed result', validate({ status: 'succeeded' }) === false);
  r.check('contract validator accepts a well-formed result', validate({
    status: 'succeeded', columns: [], rows: [], row_count: 0, truncated: false,
    duration_ms: 0, error: null, engine: 'test', dialect: 'postgresql',
  }) === true);

  const { statements, skipped, displayOnly } = collectStatements(courses);

  let base = '';
  if (process.env.QC_GATEWAY) {
    base = process.env.QC_GATEWAY.replace(/\/$/, '');
  } else {
    const bin = process.env.QC_BIN || buildGateway('parity');
    /* The server has one timezone in production, so pin the gateway to UTC; the
       web side is the one whose host timezone varies (the timezone section
       below varies it deliberately). */
    const started = await startGateway(bin, { ...(await liveEnv('parity')), TZ: 'UTC' });
    child = started.child;
    base = started.base;
  }
  console.log('gateway: ' + base);

  const diag = await getJSON(base + '/diagnostics');
  if (diag.json?.engine?.ready !== true) {
    throw new Error(
      'the parity audit needs a live PostgreSQL (engine.ready=' + diag.json?.engine?.ready + '); '
      + (diag.json?.engine?.reason ?? 'no reason given'),
    );
  }
  console.log('engine:  ' + diag.json.engine.version);

  if (executeSql) {
    /* PGlite boots here, and the adapter logs its worker fallback. Capture that
       one note instead of letting it interleave with the report. */
    const notes = [];
    const originalWarn = console.warn;
    console.warn = (...args) => notes.push(args.map(String).join(' '));
    try {
      await executeSql('SELECT 1 AS warmup');
    } finally {
      console.warn = originalWarn;
    }
    console.log('web:     PGlite in-process (Node has no Worker, so the adapter uses its documented main-thread fallback)');
    console.log('         host TZ is ' + (originalTz || '(system default)') + '; the timezone section below runs the naive-timestamp case under a UTC and a non-UTC TZ');
    if (notes.length) console.log('         ' + notes.join(' ').slice(0, 200));
  } else {
    console.log('web:     skipped (QC_SKIP_PGLITE=1)');
  }

  /* ADR 0003: both sides are writable workspaces, so the whole comparison runs
     as a signed-in learner. Anonymous read-only is a separate, explicit check
     below — it is a property, not a mode the browser even has.

     This runs after the PGlite warm-up above on purpose: the browser adapter
     boots (and announces its main-thread fallback) on first use, and doing that
     inside the warm-up keeps the announcement in the notes instead of letting it
     fall out of a reset call. */
  const learner = await signUpLearner(base);
  console.log('session: ' + learner.email + ' (writable workspace on both sides)');
  const sealed = await resetBoth(base, learner.token, executeSql ? resetWeb : null);
  if (sealed.gateway?.ok !== true) {
    throw new Error('the gateway would not reset the learner workspace: ' + JSON.stringify(sealed.gateway));
  }
  console.log('sealed:  both workspaces reset before the run (' + sealed.durationMs + ' ms)');

  if (includeVolatile) {
    console.log('note:    --include-volatile — volatile statements are compared instead of excluded');
  }

  r.section('lesson statements (' + statements.length + ')');

  for (const st of statements) {
    const ordered = isOrdered(st.sql);
    const label = st.id + ' (' + st.kind + ')';

    let gateway;
    try {
      gateway = await queryGateway(base, st.sql, learner.token);
    } catch (err) {
      r.check(label, false, 'gateway request failed: ' + err.message);
      r.record(st.id, 'error', 'error', '-', '-', 'harness error');
      continue;
    }
    const b = gateway.result;
    const bInvalid = validateResult(validate, b);

    let a = null;
    if (executeSql) {
      try {
        a = await queryWeb(executeSql, st.sql);
      } catch (err) {
        r.check(label, false, 'web adapter threw: ' + err.message);
        r.record(st.id, 'threw', b?.status ?? 'error', '-', String(b?.row_count ?? '-'), 'harness error');
        continue;
      }
    }

    const divergences = [];
    if (bInvalid.length) divergences.push('gateway response violates the contract: ' + bInvalid.join('; '));
    if (executeSql) {
      const aInvalid = validateResult(validate, a);
      if (aInvalid.length) divergences.push('web response violates the contract: ' + aInvalid.join('; '));
      if (a.status === 'failed' && !CLOSED_CODES.has(a.error?.code)) {
        divergences.push('web error code outside the closed enum: ' + a.error?.code);
      }
    }
    if (b?.status === 'failed' && !CLOSED_CODES.has(b.error?.code)) {
      divergences.push('gateway error code outside the closed enum: ' + b.error?.code);
    }

    /* What the learner can run must be one statement: `POST /query` takes one,
       and the design decides submissions are one statement. A runnable block
       that is a script is a content defect, named as such rather than left to
       surface as a status mismatch. (Syntax listings are display-only and were
       never collected here; see collectStatements.) */
    if (st.kind !== 'probe' && isMultiStatement(st.sql)) {
      tally.multiStatement += 1;
      r.check(label, false, 'runnable lesson content holds more than one statement; submissions are one statement, so this block cannot run on the gateway at all');
      r.record(st.id, a?.status ?? '?', b?.status ?? '?', '-', String(b?.row_count ?? '-'), 'multi-statement content');
      continue;
    }

    if (!executeSql) {
      r.skip(label, 'QC_SKIP_PGLITE=1 — web adapter not run');
      r.record(st.id, 'skipped', b?.status ?? '?', '-', String(b?.row_count ?? '-'), 'skipped');
      continue;
    }

    /* Non-deterministic statements are excluded from the value comparison, with
       the reason printed, unless --include-volatile asks for them anyway. */
    const volatile = volatileReason(st.sql);
    if (volatile && !includeVolatile) {
      const sameStatus = a.status === b.status;
      const rows = b?.row_count ?? '-';
      if (sameStatus) {
        r.skip(label + ' — ' + rows + ' row(s)', 'excluded from value comparison: ' + volatile);
        tally.excluded += 1;
      } else {
        r.check(label, false, { divergences: ['status: web=' + a.status + ' gateway=' + b.status], excludedVolatile: volatile });
        tally.diverges += 1;
      }
      volatileExcluded.push({ id: st.id, why: volatile, web: a.status, gateway: b?.status });
      r.record(st.id, a.status, b?.status ?? '?', String(a?.row_count ?? '-'), String(rows), 'excluded (volatile)');
      continue;
    }

    let findings = [];
    let typeLabels = [];
    const cmp = compareFrames(compareResult, a, b, ordered);
    divergences.push(...cmp.divergences);
    findings = cmp.findings;
    typeLabels = cmp.typeLabels;
    for (const tl of typeLabels) {
      const key = tl.web + ' | ' + tl.gateway;
      if (!typeLabelCounts.has(key)) typeLabelCounts.set(key, new Set());
      typeLabelCounts.get(key).add(tl.column);
    }
    for (const sample of cmp.representation) {
      reprCounts.set(sample, (reprCounts.get(sample) ?? 0) + 1);
    }

    const rows = b?.row_count ?? '-';
    const verdict = divergences.length ? 'DIVERGES' : findings.length ? 'representation' : 'match';
    if (verdict === 'match') tally.match += 1;
    else if (verdict === 'representation') tally.representation += 1;
    else tally.diverges += 1;

    const ok = divergences.length === 0 && findings.length === 0;
    r.check(label + ' — ' + rows + ' row(s)', ok, { divergences, findings });
    if (!ordered && ok) console.log('           (no ORDER BY — compared as a multiset)');
    for (const f of findings) console.log('  FINDING  ' + st.id + ': ' + f);
    r.record(st.id, a.status, b?.status ?? '?', String(a.row_count), String(rows), verdict);

    /* A statement that changed data or shape must not change what the next
       probe reads. Restore both workspaces before moving on, so the run
       compares the same sealed dataset from beginning to end. */
    if (isWrite(st.sql)) {
      const back = await resetBoth(base, learner.token, resetWeb);
      console.log('  note   ' + st.id + ' — wrote to both workspaces; both restored afterwards ('
        + back.durationMs + ' ms, gateway ok=' + back.gateway?.ok + ')');
    }
  }

  /* ---------- timezone sensitivity (documented) ---------- */

  /* ---------- display-only lesson content ---------- */

  r.section('display-only listings (' + displayOnly.length + ') — never executed by the UI');
  for (const b of displayOnly) {
    console.log('  skip   ' + b.id + ' — ' + b.statements + ' statement(s) in a static <pre>');
  }
  if (displayOnly.length) {
    console.log('  note   LessonScreen renders a syntax block as a <pre> with no editor and no Run');
    console.log('         button, so the learner can never execute one; the blocks exist to be read,');
    console.log('         and one deliberately contrasts a "not safe" form with a "safe" one.');
  }

  /* ---------- anonymous sessions stay read-only (ADR 0003) ---------- */

  r.section('anonymous sessions (ADR 0003)');
  const anonWrite = await queryGateway(base, 'UPDATE products SET stock = 0');
  r.check('an anonymous caller cannot write to the shared dataset',
    anonWrite.result?.status === 'failed' && anonWrite.result?.error?.code === 'QUERY_RUNTIME_ERROR',
    { status: anonWrite.result?.status, code: anonWrite.result?.error?.code, message: anonWrite.result?.error?.message });
  const afterAnon = await queryGateway(base, 'SELECT stock FROM products ORDER BY id LIMIT 1');
  r.check('the shared dataset is unchanged after the anonymous attempt',
    afterAnon.result?.rows?.[0]?.[0] === 40,
    { stock: afterAnon.result?.rows?.[0]?.[0] });
  const anonEnv = await getJSON(base + '/environment');
  r.check('an anonymous caller is told it has no workspace rather than given one',
    anonEnv.json?.environment?.exists === false && anonEnv.json?.environment?.writable === false,
    anonEnv.json?.environment);

  /* ---------- the same statement, signed in ---------- */

  const signedInWrite = await queryGateway(base, 'UPDATE products SET stock = 0', learner.token);
  r.check('a signed-in learner can write in their own workspace',
    signedInWrite.result?.status === 'succeeded',
    { status: signedInWrite.result?.status, code: signedInWrite.result?.error?.code });
  const envFacts = await getJSON(base + '/environment', 15_000, { authorization: 'Bearer ' + learner.token });
  r.check('the learner has a workspace and it is writable',
    envFacts.json?.environment?.exists === true && envFacts.json?.environment?.writable === true,
    envFacts.json?.environment);
  const restored = await resetBoth(base, learner.token, resetWeb);
  r.check('resetting the learner workspace restores the sealed dataset',
    restored.gateway?.ok === true, { duration_ms: restored.gateway?.duration_ms });
  const afterReset = await queryGateway(base, 'SELECT stock FROM products ORDER BY id LIMIT 1', learner.token);
  r.check('the sealed row is back after the reset', afterReset.result?.rows?.[0]?.[0] === 40,
    { stock: afterReset.result?.rows?.[0]?.[0] });

  /* ---------- course setup parity (M9) ---------- */

  /*
   * The defect: a course's `setup.statements` created scratch objects in the
   * verifier's throwaway database, but the learner's environment was only the
   * sealed template — so an exercise that verified green died for the learner
   * with "relation does not exist". Both adapters now rebuild the stored
   * course's own setup after the template: the gateway from `?course=<id>`
   * (owner-scoped, refused otherwise), the browser from the record the lesson
   * already loaded. This section pins the contract the web adapter depends on
   * and proves the browser half end to end; the gateway's applied path is
   * proved in Go (backend/internal/httpapi/environment_setup_test.go), which
   * runs under `go test ./...`.
   */
  r.section('course setup parity (M9)');

  const plainReset = await postJSON(base + '/environment/reset', {}, 60_000, { authorization: 'Bearer ' + learner.token });
  r.check('the gateway reports 0 setup statements when no course is named',
    plainReset.json?.setup_statements === 0, plainReset.json);
  const unknownCourse = await postJSON(base + '/environment/reset?course=parity-no-such-course', {}, 60_000, { authorization: 'Bearer ' + learner.token });
  r.check('the gateway refuses a reset that names an unknown course rather than ignoring it',
    unknownCourse.status === 404, { status: unknownCourse.status, body: unknownCourse.json });
  const coursePerf = await postJSON(base + '/environment/reset?course=parity-no-such-course&dataset=perf', {}, 60_000, { authorization: 'Bearer ' + learner.token });
  r.check('the gateway refuses a course combined with the perf dataset',
    coursePerf.status === 400, { status: coursePerf.status, body: coursePerf.json });

  if (executeSql && resetWeb) {
    /* The browser adapter is the default mode, so it must build the same
       workspace the verifier saw. It applies exactly the statements it is
       handed — a course's own record — and reports how many, never a guess. */
    const SCRATCH = [
      'CREATE TABLE IF NOT EXISTS parity_scratch (note text)',
      "INSERT INTO parity_scratch (note) VALUES ('verified')",
    ];
    const applied = await resetWeb(undefined, SCRATCH);
    r.check('the browser adapter applies the setup statements it is handed',
      applied?.setup_statements === SCRATCH.length, applied);
    const scratch = await queryWeb(executeSql, 'SELECT note FROM parity_scratch');
    r.check('the scratch object the verifier saw is queryable in the browser workspace',
      scratch?.status === 'succeeded' && scratch?.rows?.[0]?.[0] === 'verified', scratch);
    const cleared = await resetWeb();
    r.check('a browser reset that names no course reports 0 setup statements',
      cleared?.setup_statements === 0, cleared);
    const gone = await queryWeb(executeSql, 'SELECT note FROM parity_scratch');
    r.check('a browser reset that names no course leaves no scratch object behind',
      gone?.status === 'failed', gone);
  } else {
    console.log('  skip   browser half — QC_SKIP_PGLITE=1 or the browser adapter is not available');
  }

  if (executeSql) {
    r.section('timezone sensitivity — naive timestamp');
    const naiveSql = "SELECT '2026-01-04 10:12:00'::timestamp AS ts";
    const gw = await queryGateway(base, naiveSql);
    const gwText = String(gw.result?.rows?.[0]?.[0] ?? '');
    const gwTime = timeOfDay(gwText);
    console.log('  host TZ: ' + (originalTz || '(system default)') + '; gateway: ' + base + ' pinned TZ=UTC');
    const seen = [];
    for (const tz of ['UTC', 'Asia/Kolkata']) {
      process.env.TZ = tz;
      const out = await queryWeb(executeSql, naiveSql);
      const webText = String(out.rows?.[0]?.[0] ?? '');
      const webTime = timeOfDay(webText);
      seen.push({ tz, webText, webTime });
      console.log('  TZ=' + pad(tz, 14) + 'web="' + webText + '"  gateway="' + gwText + '"');
      r.check('naive timestamp wall clock matches gateway under TZ=' + tz, webTime !== null && webTime === gwTime, {
        web: webText,
        gateway: gwText,
        webTimeOfDay: webTime,
        gatewayTimeOfDay: gwTime,
      });
    }
    if (seen.length === 2 && seen[0].webTime !== seen[1].webTime) {
      console.log('  FINDING  the web adapter is host-timezone dependent: the same literal reads '
        + seen[0].webTime + ' under TZ=' + seen[0].tz + ' and ' + seen[1].webTime + ' under TZ=' + seen[1].tz
        + ', while the gateway reads ' + gwTime + ' either way. Run with TZ=UTC to make it disappear.');
    }
    if (originalTz === undefined) delete process.env.TZ;
    else process.env.TZ = originalTz;
  }

  /* Statements that were not runnable, grouped so the report says plainly what
     was left out and why. */
  if (skipped.length) {
    r.section('not run (' + skipped.length + ')');
    const byReason = new Map();
    for (const s of skipped) {
      if (!byReason.has(s.why)) byReason.set(s.why, []);
      byReason.get(s.why).push(s.what);
    }
    for (const [why, whats] of byReason) {
      console.log('  skip   ' + whats.length + ' item(s) — ' + why + ': ' + whats.slice(0, 4).join(', ') + (whats.length > 4 ? ', …' : ''));
      r.skipped += 1;
    }
  }

  if (volatileExcluded.length) {
    r.section('excluded from value comparison (volatile, ' + volatileExcluded.length + ')');
    for (const e of volatileExcluded) {
      console.log('  skip   ' + e.id + ' — ' + e.why);
      r.skipped += 1;
    }
  }

  /* The type-name table: canonical spelling and the side that must change. */
  if (typeLabelCounts.size) {
    r.section('type names — canonical spelling and who must change');
    console.log('  canonical spelling is the SQL-standard name (what information_schema.columns.data_type');
    console.log('  and the schema drawer already use). The web adapter (PGlite) emits it; the gateway must change.');
    console.log('  ' + pad('web', 12) + pad('gateway', 12) + pad('canonical', 12) + pad('must change', 14) + 'columns');
    for (const [key, cols] of typeLabelCounts) {
      const [webName, gatewayName] = key.split(' | ');
      const change = webName === canonicalType(webName) ? 'gateway' : 'web';
      console.log('  ' + pad(webName, 12) + pad(gatewayName, 12) + pad(canonicalType(webName), 12) + pad(change, 14) + [...cols].slice(0, 8).join(', '));
    }
    r.failed += typeLabelCounts.size;
  }

  if (reprCounts.size) {
    r.section('representation findings (fail: values agree under §6.1, JSON shape does not)');
    for (const [diff, count] of reprCounts) {
      console.log('  FINDING  ' + diff + ' (' + count + ' statement(s))');
    }
    r.failed += reprCounts.size;
  }
} catch (err) {
  r.failed += 1;
  console.log('  FAIL   audit harness — ' + err.message);
} finally {
  stopGateway(child);
  if (originalTz === undefined) delete process.env.TZ;
  else process.env.TZ = originalTz;
  if (tmpDir) {
    try { fs.rmSync(tmpDir, { recursive: true, force: true }); } catch { /* best effort */ }
  }
}

r.section('summary');
console.log('  statements compared:  ' + (tally.match + tally.representation + tally.diverges));
console.log('  match:                ' + tally.match);
console.log('  representation:       ' + tally.representation + ' (same values under §6.1, different JSON shape)');
console.log('  diverge:              ' + tally.diverges);
console.log('  excluded (volatile):  ' + tally.excluded);
console.log('  multi-statement:      ' + tally.multiStatement);

if (markdown) {
  console.log('\n--- markdown ---');
  console.log(r.markdownTable(['statement', 'status web', 'status gateway', 'rows web', 'rows gateway', 'verdict']));
  if (typeLabelCounts.size) {
    const lines = [
      '| type (web) | type (gateway) | canonical | must change | columns |',
      '| --- | --- | --- | --- | --- |',
    ];
    for (const [key, cols] of typeLabelCounts) {
      const [webName, gatewayName] = key.split(' | ');
      const change = webName === canonicalType(webName) ? 'gateway' : 'web';
      lines.push('| ' + webName + ' | ' + gatewayName + ' | ' + canonicalType(webName) + ' | ' + change + ' | ' + [...cols].slice(0, 12).join(', ') + ' |');
    }
    console.log(lines.join('\n'));
  }
}
r.finish();
