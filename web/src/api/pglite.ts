import type { PGliteInterface } from '@electric-sql/pglite';
import { perfSql, schemaSql, seedSql } from './dataset';
import { seedContentDocs } from './content';
import { DEFAULT_DATASET } from '../types';
import type { ContentSection, DatasetId, Dialect, EngineCapabilities, QueryErrorCode, QueryErrorShape, RowValue } from '../types';
import {
  buildPlanBundle,
  emptyEnvironment,
  failedPlanBundle,
  type PlanBundle,
  type PlanEnvironment,
} from './planContract';

/* The real engine: PostgreSQL compiled to WebAssembly, running in the page.

   This is what makes "Run" honest. Every statement runs on a real Postgres
   server against the same schema and seed the Go gateway serves.

   The workspace is writable (ADR 0003). A statement runs in an ordinary
   transaction, and the engine commits a statement that succeeds, so an INSERT
   in one Run stays visible to the next Run in the same session. That fact
   makes the writing half of SQL teachable, and resetEnvironment() is how the
   learner gets the original dataset back. A statement that manages its own
   transaction (`BEGIN`/`COMMIT`/`ROLLBACK`) runs exactly as written, unwrapped,
   so a transaction chapter can be practised.

   Postgres runs in a Web Worker (./pglite.worker.ts), so a runaway statement
   can be terminated by the host instead of freezing the tab. Results are
   capped as well. */

export const PGLITE_ENGINE = 'PostgreSQL 18 · PGlite (in-browser)';

/** The dialect this engine runs (M6). Named beside the engine string so the
    two can never drift apart. */
export const PGLITE_DIALECT: Dialect = 'postgresql';

/** Rows returned to the UI before the result is flagged truncated. */
const MAX_ROWS = 200;

/** Mirror of the gateway's QC_TIMEOUT_MS. Enforced by terminating the worker;
    `statement_timeout` is ignored by this engine (design Appendix A). */
const QUERY_TIMEOUT_MS = 5000;

export interface EngineResult {
  status: 'succeeded' | 'failed';
  columns: { name: string; type: string }[];
  rows: RowValue[][];
  row_count: number;
  truncated: boolean;
  error: QueryErrorShape | null;
  /** The dialect this engine runs. It is named here, by the engine that
      produced the rows, for the same reason the Go executor names its own
      (`engine.DialectOf`): a caller that has to remember to attach it is a
      caller that will forget, and a result that cannot say which engine
      produced it cannot be graded. */
  dialect: Dialect;
  /** The engine's own label, when it is not the adapter's constant — the
      SQLite sibling reports `SQLite 3.53.4` from `sqlite3_version` rather than
      a version baked into the bundle, exactly as the gateway reports the
      driver's `sqlite_version()`. Absent means "the caller's configured
      engine", which is what the PGlite results have always meant. */
  engine?: string;
}

export interface LiveTable {
  name: string;
  rows: number;
  cols: [string, string, string][];
  preview: string[][];
}

/* PostgreSQL's own type names, keyed by OID.

   These are `pg_type.typname` spellings (`int4`, `int8`, `bool`, …), not the SQL
   standard names (`integer`, `bigint`, `boolean`, …), because the gateway side
   reports typnames too (backend/internal/engine/pgx.go, `typeName`) and
   docs/technical-design.md §4.2 uses `int8` in its own example. The same column
   has to be described identically in both adapters. */
const OID_TYPE: Record<number, string> = {
  16: 'bool',
  17: 'bytea',
  20: 'int8',
  21: 'int2',
  23: 'int4',
  25: 'text',
  114: 'json',
  700: 'float4',
  701: 'float8',
  1042: 'bpchar',
  1043: 'varchar',
  1082: 'date',
  1083: 'time',
  1114: 'timestamp',
  1184: 'timestamptz',
  1186: 'interval',
  1266: 'timetz',
  1700: 'numeric',
  2950: 'uuid',
  3802: 'jsonb',
};

const OID_DATE = 1082;
const OID_TIME = 1083;
const OID_TIMETZ = 1266;

function oidTypeName(oid: number): string {
  /* An unknown OID is reported as unknown. Calling it `text` would be a claim
     about the value that nothing checked — the one thing this app must not do.
     The gateway falls back to `oid:<n>` for the same reason. */
  return OID_TYPE[oid] ?? `oid:${oid}`;
}

/**
 * The OIDs whose contract value is the engine's own text, never a value
 * re-rendered by the host.
 *
 * The Go gateway keeps the identical list (backend/internal/engine/normalize.go,
 * `textOIDs`) and the two must agree: one lesson runs against both adapters, so
 * a price that reads "118.00" in the browser must not read 118 over HTTP.
 *
 * PGlite's default parsers turn `date`/`time`/`timestamp` into a JS `Date` and
 * `numeric` into a JS `number`, and both lose information the lesson text
 * depends on:
 *
 *   - a naive `timestamp` has no offset, so materialising it as a Date and
 *     rendering it back with UTC getters moves the wall clock by the host's UTC
 *     offset (measured: 10:30:00 reads 05:00:00 under TZ=Asia/Calcutta).
 *     `SET TIME ZONE 'UTC'` does not help — the parser does.
 *   - `numeric` drops its scale: 118.00 arrives as 118.
 *
 * An identity parser keeps the bytes the server sent.
 */
export const TEXT_VALUE_OIDS = [1082, 1083, 1114, 1184, 1186, 1266, 1700];

/** Identity parsers for TEXT_VALUE_OIDS, handed to every query that reads
    learner data. `@electric-sql/pglite` calls these instead of its own. */
const TEXT_PARSERS: Record<number, (value: string) => string> = Object.fromEntries(
  TEXT_VALUE_OIDS.map((oid) => [oid, (value: string) => value]),
);

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

function dateOnly(d: Date): string {
  return `${d.getUTCFullYear()}-${pad2(d.getUTCMonth() + 1)}-${pad2(d.getUTCDate())}`;
}

function dateTime(d: Date): string {
  return `${dateOnly(d)} ${pad2(d.getUTCHours())}:${pad2(d.getUTCMinutes())}:${pad2(d.getUTCSeconds())}`;
}

/** Convert a raw PGlite value into the JSON-safe shape the UI and the contract
    comparison both expect. Dates become strings so results are portable and
    comparable. */
function normalizeValue(raw: unknown, oid: number): RowValue {
  if (raw === null || raw === undefined) return null;
  if (raw instanceof Date) {
    if (oid === OID_DATE) return dateOnly(raw);
    return dateTime(raw);
  }
  if (typeof raw === 'string' || typeof raw === 'number' || typeof raw === 'boolean') return raw;
  if (typeof raw === 'bigint') {
    return raw >= BigInt(Number.MIN_SAFE_INTEGER) && raw <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(raw) : raw.toString();
  }
  if (raw instanceof Uint8Array) {
    let hex = '';
    for (const b of raw) hex += pad2Hex(b);
    return `\\x${hex}`;
  }
  if (oid === OID_TIME || oid === OID_TIMETZ) return String(raw);
  try {
    const s = JSON.stringify(raw);
    return typeof s === 'string' ? s : String(raw);
  } catch {
    return String(raw);
  }
}

function pad2Hex(b: number): string {
  return b.toString(16).padStart(2, '0');
}

function emptyResult(): EngineResult {
  return { status: 'succeeded', columns: [], rows: [], row_count: 0, truncated: false, error: null, dialect: PGLITE_DIALECT };
}

function failed(code: QueryErrorCode, message: string, detail?: string | null): EngineResult {
  return { status: 'failed', columns: [], rows: [], row_count: 0, truncated: false, error: { code, message, detail: detail ?? null }, dialect: PGLITE_DIALECT };
}

/* ---------- database lifecycle ---------- */

/* One engine per dataset (M5).
 *
 * Starting with M5 there are two workspaces and they are not interchangeable:
 * the commerce fixture is small enough that the planner never has a choice, and
 * the perf fixture is large enough that it does. So "which database" is a
 * first-class part of a request rather than a module-level singleton, and the
 * registry below is the only place that answer lives.
 *
 * `commerce` is booted eagerly — every lesson starts there. `perf` is created
 * the first time a perf statement runs, so a learner who never opens the plans
 * course never pays for its 10 000-row seed. */
interface EngineSlot {
  db: Promise<PGliteInterface> | null;
  /* The Worker behind `db`, kept so a runaway query can be terminated. */
  worker: Worker | null;
  /* Where the engine ended up. A Worker is a responsiveness tool: it is what
     lets the host stop a runaway statement. If the page's security policy
     blocks worker creation we fall back to the main thread and say so (design
     §5.2), instead of claiming a deadline we cannot enforce. */
  placement: 'worker' | 'main' | null;
}

const slots: Record<DatasetId, EngineSlot> = {
  commerce: { db: null, worker: null, placement: null },
  perf: { db: null, worker: null, placement: null },
};

/* One tab-election per dataset.
 *
 * PGlite's worker elects a leader across every worker in the page (and every
 * tab) that shares an id, and the id defaults to the worker's own URL — the
 * same string for both datasets. Two datasets under one election is not a
 * cosmetic problem:
 *
 *   - starting the second engine makes the first one's in-flight calls fail
 *     with `LeaderChangedError`, because the newcomer announces itself on the
 *     shared channel and every pending RPC is cancelled as indeterminate;
 *   - worse, the *winner* is the one that runs the SQL. The loser's client
 *     proxies to the leader, so a perf statement would be executed against
 *     whichever database won the election — commerce. The two workspaces would
 *     silently become one, which is exactly the defect this whole change is
 *     about.
 *
 * Naming one id per dataset keeps the two engines in two elections, so "two
 * databases" is true of the browser too.
 *
 * The suffix is per page load, so two *tabs* are two elections as well. Tabs
 * used to share an id "by construction". The tab that loses an election then
 * proxies every query to the leader, so when the leader tab is reloaded or
 * closed the follower waits on a leader that will never answer. Measured in a
 * follower tab: a single `fetchModuleContent('s3')` was still pending after
 * 53 s, all 52 reading documents never arrived, and every lesson rendered the
 * short curriculum prose in place of the authored chapter, with no error
 * anywhere. Sharing one in-memory database between tabs buys nothing (there is
 * no file behind it) and costs exactly that, so each tab takes its own. */
const TAB_ID = Math.random().toString(36).slice(2, 10);
const WORKER_ID: Record<DatasetId, string> = {
  commerce: `querycraft-dataset-commerce:${TAB_ID}`,
  perf: `querycraft-dataset-perf:${TAB_ID}`,
};

export type { EngineCapabilities };

/** `null` until the engine has booted, so the UI can say "starting" rather
    than advertise a capability it has not established. */
export function engineCapabilities(): EngineCapabilities | null {
  /* Every slot reports the same answer: `placement` is a property of the *page*
     (a blocked Worker blocks it for both datasets), and the hard deadline is
     the same constant for both, so asking one slot is asking all of them. */
  const placement = slots[DEFAULT_DATASET].placement;
  if (placement === null) return null;
  /* `writable` is what lets the UI offer EXPLAIN ANALYZE (ADR 0003); `explain`
     is the plan capability itself. The page engine is a real PostgreSQL, so it
     can do both. */
  return {
    placement,
    hardTimeout: placement === 'worker',
    timeoutMs: QUERY_TIMEOUT_MS,
    writable: true,
    explain: true,
    dialect: PGLITE_DIALECT,
  };
}

/* Open the engine off the main thread when the page allows it. */
async function openWorkerEngine(slot: EngineSlot, dataset: DatasetId): Promise<PGliteInterface> {
  const { PGliteWorker } = await import('@electric-sql/pglite/worker');
  const w = new Worker(new URL('./pglite.worker.ts', import.meta.url), { type: 'module' });
  /* Recorded before the handshake, not after: a handshake that never answers has
     to be terminable by the caller, and `PGliteWorker.create` resolving is
     exactly what may never happen. */
  slot.worker = w;
  try {
    return await PGliteWorker.create(w, { id: WORKER_ID[dataset] });
  } catch (e) {
    await stopWorker(slot);
    throw e;
  }
}

/** Terminate a worker the engine is no longer using. Safe to call twice. */
async function stopWorker(slot: EngineSlot): Promise<void> {
  const w = slot.worker;
  slot.worker = null;
  if (!w) return;
  try {
    w.terminate();
  } catch {
    /* never started */
  }
}

/* Hosted previews and strict CSPs can refuse `new Worker` outright; the engine
   still works on the page, it just cannot be pre-empted mid-statement. */
async function openMainThreadEngine(): Promise<PGliteInterface> {
  const { PGlite } = await import('@electric-sql/pglite');
  return PGlite.create();
}

/* A boot either finishes or is treated as failed.
 *
 * `PGliteWorker.create` and the first seed have no timeout of their own, and a
 * worker that never answers leaves every caller pending for the life of the tab
 * with nothing raised: the lesson keeps its fallback prose, `Run` never returns,
 * and no screen can tell that from "still loading". A boot that cannot answer in
 * this long is given up on and the engine is built on the page instead. */
const BOOT_TIMEOUT_MS = 20000;

function withBootTimeout<T>(work: Promise<T>, what: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${what} did not answer within ${BOOT_TIMEOUT_MS} ms`)), BOOT_TIMEOUT_MS);
    work.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  });
}

async function bootstrap(dataset: DatasetId): Promise<PGliteInterface> {
  const slot = slots[dataset];
  /* Loaded on demand: the WASM build is large and only needed once a query runs. */
  let db: PGliteInterface;
  try {
    db = await withBootTimeout(
      (async () => {
        const engine = await openWorkerEngine(slot, dataset);
        await seedDatabase(engine, dataset);
        return engine;
      })(),
      'the in-browser database worker',
    );
    slot.placement = 'worker';
  } catch (workerError) {
    /* Either the page refuses Workers outright, or the worker never answered.
       Both leave the same choice: run the engine on the page, where there is no
       second party to wait for. */
    await stopWorker(slot);
    db = await withBootTimeout(openMainThreadEngine(), 'the in-page database');
    slot.placement = 'main';
    console.warn(
      '[querycraft] the database could not run in a Web Worker, so SQL runs on the main thread and a runaway query cannot be stopped by the host.',
      workerError,
    );
    try {
      await seedDatabase(db, dataset);
    } catch (e) {
      await disposeDatabase(dataset);
      throw e;
    }
  }
  return db;
}

/**
 * Apply the shipped dataset (schema + seed), the `app` content schema and the
 * authored markdown, then mirror the gateway's per-session search path.
 *
 * First boot and a workspace reset both go through here, so the two can never
 * drift: a reset that forgot the content seed, or one that seeded a different
 * schema than boot did, would be a bug the reset test could not see.
 */
async function seedDatabase(db: PGliteInterface, dataset: DatasetId): Promise<void> {
  if (dataset === 'perf') {
    /* The perf fixture brings its own schema, seed, indexes and statistics. It
       deliberately does not carry the `app` content schema: chapter markdown is
       app metadata, not part of a dataset, and it is read from the default
       workspace (see fetchModuleContent), so seeding it here would be a second
       copy of the same rows with no second reader. */
    await db.exec(perfSql);
  } else {
    await db.exec(schemaSql);
    await db.exec(seedSql);
    await createContentSchema(db);
    await seedContentDocs(db);
  }
  /* The dataset tables resolve unqualified (FROM orders) while `app` metadata
     stays off the path. Session-scoped, so it also holds for a statement that
     brings its own transaction. */
  await db.exec('SET search_path = dataset, public');
}

/* Tear the worker down and drop the handle. The next query rebuilds the database
   from the same schema and seed, so the dataset is restored by construction. */
async function disposeDatabase(dataset: DatasetId): Promise<void> {
  const slot = slots[dataset];
  const w = slot.worker;
  slot.worker = null;
  slot.placement = null;
  slot.db = null;
  try {
    w?.terminate();
  } catch {
    /* already gone */
  }
}

/** Course reading content lives in the database as markdown documents.
    It sits in the `app` schema, alongside gateway metadata, not in the
    learner-visible `dataset` schema. */
async function createContentSchema(db: PGliteInterface): Promise<void> {
  await db.exec(`
    CREATE SCHEMA IF NOT EXISTS app;
    CREATE TABLE IF NOT EXISTS app.content_docs (
      id         serial PRIMARY KEY,
      course_id  text NOT NULL,
      module_id  text NOT NULL,
      ordinal    integer NOT NULL,
      section    text NOT NULL,
      markdown   text NOT NULL,
      updated_at timestamptz NOT NULL DEFAULT now()
    );
    CREATE INDEX IF NOT EXISTS content_docs_module_idx ON app.content_docs (course_id, module_id, ordinal);
    /* One row per seeded value. The content fingerprint lives here, so a bundle
       that changed can be told apart from one that did not. */
    CREATE TABLE IF NOT EXISTS app.content_meta (
      key        text PRIMARY KEY,
      value      text NOT NULL,
      updated_at timestamptz NOT NULL DEFAULT now()
    );
  `);
}

export function getDatabase(dataset: DatasetId = DEFAULT_DATASET): Promise<PGliteInterface> {
  const slot = slots[dataset];
  if (slot.db === null) {
    slot.db = bootstrap(dataset).catch((e: unknown) => {
      slot.db = null;
      throw e;
    });
  }
  return slot.db;
}

/**
 * Kick off WASM download + initdb for one workspace without blocking the caller.
 *
 * Booting is the one part of a query the learner has to wait for and never
 * caused: the WASM build, `initdb`, the seed and `ANALYZE` all happen before the
 * first statement runs. Starting it when the *screen* that will need it opens —
 * rather than when the learner presses Run — moves that wait into the reading.
 * A course that never uses the perf fixture therefore never pays for it, which
 * is why this takes the dataset rather than warming both.
 */
export function warmUpDataset(dataset: DatasetId = DEFAULT_DATASET): void {
  void getDatabase(dataset).catch(() => {
    /* surfaced on first real query */
  });
}

/**
 * Restore the original workspace: drop everything the learner put in the
 * `dataset` schema — their tables, their rows, their dropped columns — and
 * re-apply the shipped schema, seed and chapter content.
 *
 * `DROP SCHEMA ... CASCADE` rather than the schema's own `DROP TABLE`s: the
 * learner may have created a table the seed script does not know about, and a
 * reset that leaves those behind is not a reset. The database stays open and
 * warm, so the next Run works with no reload.
 *
 * The measurement is the engine's, not the UI's: the caller reports what this
 * returns, so a "0.4s" badge is what the engine actually spent.
 *
 * A stored course's `setup.statements` (M9) are applied after the template, in
 * order, because the exercises were verified in a scratch environment that had
 * them: without them the learner's database is not the verified one and an
 * exercise that checked green dies with "relation does not exist". A statement
 * that fails throws, so a reset that did not take is never reported as one.
 */
export async function resetEnvironment(
  dataset: DatasetId = DEFAULT_DATASET,
  setup: readonly string[] = [],
): Promise<{ ok: boolean; duration_ms: number; setup_statements: number }> {
  const t0 = typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now();
  const db = await getDatabase(dataset);
  /* A learner may have left a transaction open on purpose (`BEGIN;` with no
     `COMMIT;`). Reset is the documented way out, so close it first — otherwise
     the drop would run inside their transaction and the content seed, which
     opens its own, would fail. */
  await db.exec('ROLLBACK').catch(() => undefined);
  await db.exec('DROP SCHEMA IF EXISTS dataset CASCADE');
  await seedDatabase(db, dataset);
  let applied = 0;
  for (const statement of setup) {
    await db.exec(statement);
    applied += 1;
  }
  const ms = (typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now()) - t0;
  return { ok: true, duration_ms: Number(ms.toFixed(1)), setup_statements: applied };
}

/* ---------- execution ---------- */

function stripComments(sql: string): string {
  return sql
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/--[^\n]*/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

function sqlStateOf(e: unknown): { code: string; message: string; detail: string | null; hint: string | null; position: unknown } {
  if (typeof e === 'object' && e !== null) {
    const o = e as Record<string, unknown>;
    return {
      code: typeof o.code === 'string' ? o.code : '',
      message: typeof o.message === 'string' ? o.message : String(e),
      detail: typeof o.detail === 'string' ? o.detail : null,
      hint: typeof o.hint === 'string' ? o.hint : null,
      position: o.position,
    };
  }
  return { code: '', message: String(e), detail: null, hint: null, position: null };
}

/** Point at the offending character the way psql does. */
function caretDetail(sql: string, position: unknown): string | null {
  const pos = typeof position === 'string' ? Number(position) : typeof position === 'number' ? position : NaN;
  if (!Number.isFinite(pos) || pos <= 0) return null;
  const upto = Math.min(pos - 1, sql.length);
  let line = 1;
  let lineStart = 0;
  for (let i = 0; i < upto; i++) {
    if (sql[i] === '\n') {
      line++;
      lineStart = i + 1;
    }
  }
  let lineEnd = sql.indexOf('\n', lineStart);
  if (lineEnd === -1) lineEnd = sql.length;
  const source = sql.slice(lineStart, lineEnd);
  const caret = ' '.repeat(Math.max(0, Math.min(pos - 1 - lineStart, source.length))) + '^';
  return `LINE ${line}\n  ${source}\n  ${caret}`;
}

/** Map a Postgres SQLSTATE onto the contract's error vocabulary. */
function mapError(e: unknown, sql: string): QueryErrorShape {
  const { code, message, detail, position } = sqlStateOf(e);
  const where = caretDetail(sql, position);
  const withDetail = where ?? detail;
  switch (code) {
    case '42601':
    case '42603':
    case '42604':
      return { code: 'QUERY_SYNTAX_ERROR', message, detail: withDetail };
    case '42P01':
    case '42703':
    case '42883':
    case '42804':
    case '22P02':
    case '22007':
    case '42803':
    case '42702':
      return { code: 'QUERY_RUNTIME_ERROR', message, detail: withDetail };
    case '57014':
    case '55P03':
      return { code: 'QUERY_TIMEOUT', message: 'The query was stopped because it ran too long.', detail: message };
    case '54000':
    case '53100':
    case '53400':
      return { code: 'RESULT_LIMIT_EXCEEDED', message, detail: withDetail };
    case '53300':
    case '3D000':
    case '08006':
    case '08003':
      return { code: 'ENVIRONMENT_UNAVAILABLE', message: 'The in-browser database is not available.', detail: message };
    default:
      return { code: 'QUERY_RUNTIME_ERROR', message, detail: withDetail };
  }
}

/**
 * Whether the learner's own text controls the transaction.
 *
 * A submission that says `BEGIN`, `COMMIT` or `ROLLBACK` is a transaction
 * lesson, and wrapping it in a transaction of ours would make its own `BEGIN`
 * fail with "already a transaction in progress". When the learner manages the
 * transaction, their statements are run exactly as written and we neither open
 * nor close one for them.
 */
function managesOwnTransaction(sql: string): boolean {
  return /(^|;)\s*(begin|start\s+transaction|commit|end|rollback|savepoint|release)\b/i.test(sql);
}

function pickResult(results: Array<{ rows: unknown[]; fields: { name: string; dataTypeID: number }[]; command?: string }>): { rows: unknown[][]; fields: { name: string; dataTypeID: number }[] } | null {
  if (!Array.isArray(results) || results.length === 0) return null;
  for (let i = results.length - 1; i >= 0; i--) {
    const r = results[i];
    if (r && Array.isArray(r.fields) && r.fields.length > 0) {
      return { rows: r.rows as unknown[][], fields: r.fields };
    }
  }
  return null;
}

/** Mirror of the gateway's QC_TIMEOUT_MS, enforced by terminating the worker. */
class QueryTimeout extends Error {}

function withHardTimeout<T>(run: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new QueryTimeout()), ms);
    run.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (err) => {
        clearTimeout(timer);
        reject(err);
      },
    );
  });
}

/**
 * Execute learner SQL for real, in an ordinary (writable) transaction.
 *
 * A statement that succeeds is committed, so the workspace keeps what the
 * learner did; a statement that fails is rolled back, so a half-applied
 * submission never sticks. A submission that manages its own transaction is
 * handed to PostgreSQL unwrapped — that is what a transaction chapter needs.
 */
export async function executeSql(sql: string, dataset: DatasetId = DEFAULT_DATASET): Promise<EngineResult> {
  const text = String(sql ?? '');
  const statement = stripComments(text);
  if (!statement) return failed('EMPTY_QUERY', 'Blank SQL. Write a statement, then Run.');

  let db: PGliteInterface;
  try {
    db = await getDatabase(dataset);
  } catch (e) {
    return failed('ENVIRONMENT_UNAVAILABLE', 'The in-browser database could not start.', sqlStateOf(e).message);
  }

  const wrapped = !managesOwnTransaction(statement);
  let results: Array<{ rows: unknown[]; fields: { name: string; dataTypeID: number }[]; command?: string }>;
  let succeeded = false;
  try {
    const attempt = (async () => {
      if (wrapped) await db.exec('BEGIN');
      return db.exec(text, { rowMode: 'array', parsers: TEXT_PARSERS });
    })();
    /* Only a Worker can be stopped from outside. On the main-thread fallback
       there is nothing to kill, so wrapping it in a timeout would just report a
       stop that never happens; `engineCapabilities()` already tells the UI that
       this engine has no hard deadline. */
    results = slots[dataset].worker !== null ? await withHardTimeout(attempt, QUERY_TIMEOUT_MS) : await attempt;
    succeeded = true;
  } catch (e) {
    if (e instanceof QueryTimeout) {
      /* The worker is still spinning inside the WASM loop; terminating it is the
         only way to stop the statement. The next query re-seeds from scratch. */
      await disposeDatabase(dataset);
      return failed(
        'QUERY_TIMEOUT',
        `The query was stopped after ${QUERY_TIMEOUT_MS / 1000}s.`,
        'A statement that never finishes cannot be interrupted inside the browser engine, so the database was rebuilt.',
      );
    }
    return { status: 'failed', columns: [], rows: [], row_count: 0, truncated: false, error: mapError(e, text), dialect: PGLITE_DIALECT };
  } finally {
    /* A terminated worker has no transaction left to finish and its client is
       gone (`database` is null after disposeDatabase), so this runs only on a
       live connection — including the main-thread fallback, where skipping it
       would leave the transaction open and the write uncommitted. */
    if (wrapped && slots[dataset].db !== null) {
      await db.exec(succeeded ? 'COMMIT' : 'ROLLBACK').catch(() => undefined);
    }
  }

  const chosen = pickResult(results);
  if (chosen === null) {
    return emptyResult();
  }

  const columns = chosen.fields.map((f) => ({ name: f.name, type: oidTypeName(f.dataTypeID) }));
  const allRows = chosen.rows.map((row) => row.map((v, i) => normalizeValue(v, chosen.fields[i]?.dataTypeID ?? 25)));
  const truncated = allRows.length > MAX_ROWS;
  const rows = truncated ? allRows.slice(0, MAX_ROWS) : allRows;

  return { status: 'succeeded', columns, rows, row_count: rows.length, truncated, error: null, dialect: PGLITE_DIALECT };
}

/* ---------- plans (M5) ---------- */

/** Planner inputs that change the chosen plan. Reported read-only: M5 never
    rewrites one of these to make an exercise pass. */
const PLANNER_SETTINGS = [
  'random_page_cost',
  'effective_cache_size',
  'work_mem',
  'enable_seqscan',
  'enable_indexscan',
  'enable_bitmapscan',
  'enable_hashjoin',
  'enable_mergejoin',
  'enable_nestloop',
];

function roundMs(ms: number): number {
  return Number(ms.toFixed(1));
}

async function scalar(db: PGliteInterface, sql: string): Promise<string> {
  const rows = await queryArrays<unknown>(db, sql);
  return rows[0] === undefined ? '' : String(rows[0][0] ?? '');
}

/**
 * The environment block a plan is reported against (M5 exit gate): the engine
 * version, the scale it ran at, per-relation row counts, the index list and the
 * planner settings. A number is never shown without the scale and settings that
 * produced it, so this is captured beside every plan, not once at boot.
 */
async function readPlanEnvironment(db: PGliteInterface, dataset: DatasetId): Promise<PlanEnvironment> {
  const version = await scalar(db, 'SHOW server_version');
  const tableRows = await queryArrays<unknown>(db, `
    SELECT table_name FROM information_schema.tables
    WHERE table_schema = 'dataset' AND table_type = 'BASE TABLE'
    ORDER BY table_name;
  `);
  const rowCounts: Record<string, number> = {};
  for (const row of tableRows) {
    const name = String(row[0]);
    /* `dataset_meta` is bookkeeping, not learner data; it is not a relation a
       plan can scan, so reporting its row count would be noise. */
    if (name === 'dataset_meta') continue;
    const count = await scalar(db, `SELECT count(*)::int FROM dataset."${name.replace(/"/g, '""')}"`);
    rowCounts[name] = Number(count);
  }
  const indexRows = await queryArrays<unknown>(db, `
    SELECT tablename, indexname, indexdef FROM pg_indexes
    WHERE schemaname = 'dataset' ORDER BY tablename, indexname;
  `);
  const settingsList = PLANNER_SETTINGS.map((name) => `'${name}'`).join(', ');
  const settingRows = await queryArrays<unknown>(db, `
    SELECT name, setting FROM pg_settings WHERE name IN (${settingsList}) ORDER BY name;
  `);
  const settings: Record<string, string> = {};
  for (const row of settingRows) settings[String(row[0])] = String(row[1]);

  /* The fixture names itself, and its scale is the fact table's own row count —
     the same two numbers the Go gateway reports (`dataset_meta` and
     `dataset.FactRowCount`). Reading them from the database rather than from
     the `dataset` argument is the difference between "the plan was chosen
     against 10 000 orders" being a claim and being a measurement; a lesson that
     teaches a 10 000-row access path and a plan captured against 80 must never
     be confusable. */
  const meta = await queryArrays<unknown>(db, 'SELECT "dataset" FROM "dataset"."dataset_meta" ORDER BY "dataset" LIMIT 1');
  const datasetName = meta[0] === undefined ? dataset : String(meta[0][0] ?? dataset);
  const scale = rowCounts.orders ?? 0;

  return {
    engine: 'postgresql',
    engine_version: version,
    scale_factor: scale,
    dataset: datasetName,
    row_counts: rowCounts,
    indexes: indexRows.map((r) => ({ relation: String(r[0]), name: String(r[1]), definition: String(r[2]) })),
    settings,
  };
}

/** The `EXPLAIN (FORMAT JSON)` value, wherever PGlite put it in its results. */
function planValueOf(results: Array<{ rows: unknown[]; fields: { name: string; dataTypeID: number }[]; command?: string }>): unknown {
  for (const r of results) {
    if (r && Array.isArray(r.rows) && r.rows.length > 0) {
      const row = r.rows[0] as unknown[];
      if (Array.isArray(row) && row.length > 0) return row[0];
    }
  }
  return null;
}

/**
 * Capture a plan for learner SQL, for real, in the browser engine.
 *
 * `analyze` runs `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`: the statement is
 * executed, so the per-node numbers are measured. That execution happens inside
 * a transaction that is always rolled back — a plan is a view of the statement,
 * not a licence to keep its writes — and it is bounded by the same hard
 * deadline and worker kill as a query. Without `analyze` only the planner's
 * cost model runs, so the bundle says `timing_precision: 'estimate'` and no
 * timing is reported at all.
 *
 * The bundle is assembled by `buildPlanBundle` (./planContract), the same walk
 * the Go engine performs, so the two adapters cannot describe one plan two ways.
 */
export async function explainSql(
  sql: string,
  analyze: boolean,
  requestId: string,
  dataset: DatasetId = DEFAULT_DATASET,
): Promise<PlanBundle> {
  const text = String(sql ?? '');
  const statement = stripComments(text);
  const t0 = typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now();
  const elapsed = (): number => (typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now()) - t0;

  if (!statement) {
    return failedPlanBundle('postgresql', 'EMPTY_QUERY', 'Blank SQL. Write a statement, then look at its plan.');
  }

  let db: PGliteInterface;
  try {
    db = await getDatabase(dataset);
  } catch (e) {
    return failedPlanBundle('postgresql', 'ENVIRONMENT_UNAVAILABLE', 'The in-browser database could not start.', sqlStateOf(e).message);
  }

  let environment: PlanEnvironment;
  try {
    environment = await readPlanEnvironment(db, dataset);
  } catch {
    /* The plan can still be captured; only the block that explains it is
       missing, and saying so beats refusing the whole call. */
    environment = emptyEnvironment('postgresql');
  }

  const planSql = `${analyze ? 'EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)' : 'EXPLAIN (FORMAT JSON)'} ${text}`;
  let results: Array<{ rows: unknown[]; fields: { name: string; dataTypeID: number }[]; command?: string }>;
  try {
    const attempt = (async () => {
      await db.exec('BEGIN');
      try {
        return await db.exec(planSql, { rowMode: 'array' });
      } finally {
        await db.exec('ROLLBACK').catch(() => undefined);
      }
    })();
    results = slots[dataset].worker !== null ? await withHardTimeout(attempt, QUERY_TIMEOUT_MS) : await attempt;
  } catch (e) {
    if (e instanceof QueryTimeout) {
      await disposeDatabase(dataset);
      return failedPlanBundle(
        'postgresql',
        'QUERY_TIMEOUT',
        `The statement was stopped after ${QUERY_TIMEOUT_MS / 1000}s before a plan could be captured.`,
        'A statement that never finishes cannot be interrupted inside the browser engine, so the database was rebuilt.',
        environment,
      );
    }
    const err = mapError(e, text);
    return failedPlanBundle('postgresql', err.code, err.message, err.detail ?? null, environment);
  }

  return buildPlanBundle({
    plan: planValueOf(results),
    analyze,
    timing_precision: analyze ? 'analyze' : 'estimate',
    engine: 'postgresql',
    environment,
    duration_ms: roundMs(elapsed()),
    statement: text,
    request_id: requestId,
  });
}

/* ---------- introspection for the tables drawer ---------- */

const INTERNAL_TABLES = new Set(['dataset_meta', 'content_docs', 'content_meta']);

async function queryWithFields(db: PGliteInterface, sql: string): Promise<{ rows: unknown[][]; fields: { name: string; dataTypeID: number }[] }> {
  const res = await db.exec(sql, { rowMode: 'array', parsers: TEXT_PARSERS });
  for (let i = res.length - 1; i >= 0; i--) {
    if (res[i] && res[i].fields.length > 0) {
      return { rows: res[i].rows as unknown as unknown[][], fields: res[i].fields };
    }
  }
  return { rows: [], fields: [] };
}

async function queryArrays<T>(db: PGliteInterface, sql: string): Promise<T[][]> {
  const { rows } = await queryWithFields(db, sql);
  return rows as T[][];
}

export async function fetchTables(dataset: DatasetId = DEFAULT_DATASET): Promise<{ tables: LiveTable[]; rows: number }> {
  const db = await getDatabase(dataset);

  const tableRows = await queryArrays<unknown>(db, `
    SELECT table_name FROM information_schema.tables
    WHERE table_schema = 'dataset' AND table_type = 'BASE TABLE'
    ORDER BY table_name;
  `);
  const names = tableRows.map((r) => String(r[0])).filter((n) => !INTERNAL_TABLES.has(n));

  const columnRows = await queryArrays<unknown>(db, `
    SELECT c.table_name, c.column_name, c.data_type, c.is_nullable,
           COALESCE(pk.is_pk, false) AS is_pk,
           COALESCE(fk.is_fk, false) AS is_fk
    FROM information_schema.columns c
    LEFT JOIN (
      SELECT tc.table_name, kcu.column_name, true AS is_pk
      FROM information_schema.table_constraints tc
      JOIN information_schema.key_column_usage kcu
        ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
      WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = 'dataset'
    ) pk ON pk.table_name = c.table_name AND pk.column_name = c.column_name
    LEFT JOIN (
      SELECT tc.table_name, kcu.column_name, true AS is_fk
      FROM information_schema.table_constraints tc
      JOIN information_schema.key_column_usage kcu
        ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
      WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'dataset'
    ) fk ON fk.table_name = c.table_name AND fk.column_name = c.column_name
    WHERE c.table_schema = 'dataset'
    ORDER BY c.table_name, c.ordinal_position;
  `);

  const tables: LiveTable[] = [];
  let total = 0;
  for (const name of names) {
    const countRows = await queryArrays<unknown>(db, `SELECT count(*)::int FROM dataset."${name.replace(/"/g, '""')}"`);
    const count = Number(countRows[0]?.[0] ?? 0);
    total += count;

    const cols: [string, string, string][] = columnRows
      .filter((r) => String(r[0]) === name)
      .map((r) => {
        const column = String(r[1]);
        const type = String(r[2]);
        const nullable = String(r[3]).toUpperCase() === 'YES';
        const isPk = r[4] === true;
        const isFk = r[5] === true;
        const note = isPk ? 'PK' : isFk ? 'FK' : nullable ? 'nullable' : '';
        return [column, type, note] as [string, string, string];
      });

    /* Preview uses the real field OIDs so a `date` column reads 2025-11-02,
       not a midnight timestamp. */
    const previewRes = await queryWithFields(db, `SELECT * FROM dataset."${name.replace(/"/g, '""')}" LIMIT 5`);
    const preview: string[][] = [previewRes.fields.length > 0 ? previewRes.fields.map((f) => f.name) : cols.map((c) => c[0])];
    for (const row of previewRes.rows) {
      preview.push(row.map((v, i) => {
        const normalized = normalizeValue(v, previewRes.fields[i]?.dataTypeID ?? 25);
        if (normalized === null) return 'NULL';
        return String(normalized);
      }));
    }

    tables.push({ name, rows: count, cols, preview });
  }

  return { tables, rows: total };
}

export { normalizeValue };

/** Chapter reading content, read back from the database as markdown. */
export async function fetchModuleContent(moduleId: string): Promise<ContentSection[]> {
  /* Content is app metadata, not part of a dataset: it always reads from the
     default workspace, whichever dataset the learner is currently running SQL
     against. A `plans` chapter therefore reads its markdown from the same place
     a commerce chapter does, and the perf workspace never needs a second copy. */
  const db = await getDatabase();
  const res = await db.query<{ section: string; markdown: string }>(
    'SELECT section, markdown FROM app.content_docs WHERE module_id = $1 ORDER BY ordinal',
    [moduleId],
  );
  return res.rows.map((r) => ({ section: r.section, markdown: r.markdown }));
}
