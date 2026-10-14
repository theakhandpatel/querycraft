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
             read from the one Go source of truth, plus the version from the
             schema header. */
          const schemaSql = fs.readFileSync(path.join(DATASET_DIR, 'schema.sql'), 'utf8');
          const seedSql = fs.readFileSync(path.join(DATASET_DIR, 'seed.sql'), 'utf8');
          const datasetVersion = Number(/dataset_version:\s*(\d+)/.exec(schemaSql)?.[1] ?? 1);
          return {
            loader: 'js',
            contents: 'export const schemaSql = ' + JSON.stringify(schemaSql) + ';\n'
              + 'export const seedSql = ' + JSON.stringify(seedSql) + ';\n'
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
 * bundleAdapters compiles the real frontend modules into a temp directory and
 * imports them, returning the curriculum, the §6.1 comparator and — unless
 * QC_SKIP_PGLITE is set — the web adapter's own executeSql.
 */
async function bundleAdapters(tmpDir) {
  const esbuild = webRequire('esbuild');
  const pgliteIndex = path.join(repoRoot, 'web', 'node_modules', '@electric-sql', 'pglite', 'dist', 'index.js');
  const pgliteWorker = path.join(repoRoot, 'web', 'node_modules', '@electric-sql', 'pglite', 'dist', 'worker', 'index.js');

  const commonOut = path.join(tmpDir, 'qc-curriculum-and-compare.cjs');
  await esbuild.build({
    entryPoints: ['qc:common'],
    bundle: true,
    format: 'cjs',
    platform: 'node',
    target: 'node20',
    outfile: commonOut,
    plugins: [commonEntryPlugin()],
    logLevel: 'silent',
  });
  const common = webRequire(commonOut);

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
function collectStatements(courses) {
  const statements = [];
  const skipped = [];
  const displayOnly = [];
  for (const course of courses) {
    if (!Array.isArray(course.modules) || course.modules.length === 0) {
      skipped.push({ what: 'course "' + course.title + '" (' + course.id + ')', why: 'not built — no modules' });
      continue;
    }
    for (const mod of course.modules) {
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
  }
  for (const probe of PROBES) {
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
function firstValueDiff(a, b) {
  const cols = Math.min(a.columns.length, b.columns.length);
  const rows = Math.min(a.rows.length, b.rows.length);
  for (let c = 0; c < cols; c++) {
    for (let row = 0; row < rows; row++) {
      const av = a.rows[row]?.[c];
      const bv = b.rows[row]?.[c];
      if (!valueEqual61(av, bv)) {
        return 'row ' + (row + 1) + ' column ' + a.columns[c].name
          + ' (' + canonicalType(a.columns[c].type) + '): web=' + JSON.stringify(av)
          + ' gateway=' + JSON.stringify(bv);
      }
    }
  }
  return '';
}

/** The first cell whose JSON shape differs while §6.1 still calls it equal. */
function firstRepresentationDiff(a, b) {
  const cols = Math.min(a.columns.length, b.columns.length);
  const rows = Math.min(a.rows.length, b.rows.length);
  for (let c = 0; c < cols; c++) {
    for (let row = 0; row < rows; row++) {
      const av = a.rows[row]?.[c];
      const bv = b.rows[row]?.[c];
      if (!sameCell(av, bv) && valueEqual61(av, bv) && jsonKind(av) !== jsonKind(bv)) {
        return 'column ' + a.columns[c].name + ' (' + canonicalType(a.columns[c].type) + '): web='
          + jsonKind(av) + ' ' + JSON.stringify(av) + ' gateway=' + jsonKind(bv) + ' ' + JSON.stringify(bv);
      }
    }
  }
  for (let c = 0; c < cols; c++) {
    for (let row = 0; row < rows; row++) {
      const av = a.rows[row]?.[c];
      const bv = b.rows[row]?.[c];
      if (!sameCell(av, bv)) {
        return 'column ' + a.columns[c].name + ' (' + canonicalType(a.columns[c].type) + '): web='
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
function compareFrames(compareResult, a, b, ordered) {
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
    if (canonicalType(ta) !== canonicalType(tb)) {
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
        const sample = firstRepresentationDiff(a, b);
        representation.push(sample || 'rows differ only in JSON representation');
        findings.push('rows agree under the §6.1 rule but not in JSON shape' + (sample ? ' — ' + sample : ''));
      } else {
        const sample = firstValueDiff(a, b);
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

/* ---------- main ---------- */

let child = null;
let tmpDir = null;

try {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qc-parity-'));
  const { courses, compareResult, executeSql, resetEnvironment } = await bundleAdapters(tmpDir);
  const resetWeb = resetEnvironment ?? null;
  const validate = contractValidator();

  /* A validator that cannot reject anything would make every contract check
     vacuous, so prove it can reject before trusting it. */
  r.section('contract');
  r.check('contract validator rejects a malformed result', validate({ status: 'succeeded' }) === false);
  r.check('contract validator accepts a well-formed result', validate({
    status: 'succeeded', columns: [], rows: [], row_count: 0, truncated: false,
    duration_ms: 0, error: null, engine: 'test',
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
