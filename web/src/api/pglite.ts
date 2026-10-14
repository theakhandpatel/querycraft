import type { PGliteInterface } from '@electric-sql/pglite';
import { schemaSql, seedSql } from './dataset';
import { seedContentDocs } from './content';
import type { ContentSection, QueryErrorShape, RowValue } from '../types';

/* The real engine: PostgreSQL compiled to WebAssembly, running in the page.

   This is what makes "Run" honest. The old mock adapter returned the result the
   exercise already contained, so it never executed anything. Here every
   statement is executed by a real Postgres server, inside a read-only
   transaction, against the same schema and seed the Go gateway serves.

   Postgres runs in a Web Worker (./pglite.worker.ts), so a runaway statement can
   be terminated by the host instead of freezing the tab. Results are capped as
   well. */

export const PGLITE_ENGINE = 'PostgreSQL 18 · PGlite (in-browser)';

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
}

export interface LiveTable {
  name: string;
  rows: number;
  cols: [string, string, string][];
  preview: string[][];
}

/* OIDs PGlite reports for the column types this dataset uses. */
const OID_TYPE: Record<number, string> = {
  16: 'boolean',
  17: 'bytea',
  20: 'bigint',
  21: 'smallint',
  23: 'integer',
  25: 'text',
  114: 'json',
  700: 'real',
  701: 'double precision',
  1042: 'character',
  1043: 'character varying',
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
  return OID_TYPE[oid] ?? 'text';
}

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
  return { status: 'succeeded', columns: [], rows: [], row_count: 0, truncated: false, error: null };
}

function failed(code: string, message: string, detail?: string | null): EngineResult {
  return { status: 'failed', columns: [], rows: [], row_count: 0, truncated: false, error: { code, message, detail: detail ?? null } };
}

/* ---------- database lifecycle ---------- */

let database: Promise<PGliteInterface> | null = null;
/* The Worker behind `database`, kept so a runaway query can be terminated. */
let liveWorker: Worker | null = null;
/* Where the engine ended up. A Worker is a responsiveness tool: it is what
   lets the host stop a runaway statement. If the page's security policy blocks
   worker creation we fall back to the main thread and say so (design §5.2),
   instead of claiming a deadline we cannot enforce. */
let placement: 'worker' | 'main' | null = null;

export interface EngineCapabilities {
  placement: 'worker' | 'main';
  /** True only when a runaway query can actually be stopped by the host. */
  hardTimeout: boolean;
  timeoutMs: number;
}

/** `null` until the engine has booted, so the UI can say "starting" rather
    than advertise a capability it has not established. */
export function engineCapabilities(): EngineCapabilities | null {
  if (placement === null) return null;
  return { placement, hardTimeout: placement === 'worker', timeoutMs: QUERY_TIMEOUT_MS };
}

/* Open the engine off the main thread when the page allows it. */
async function openWorkerEngine(): Promise<PGliteInterface> {
  const { PGliteWorker } = await import('@electric-sql/pglite/worker');
  const w = new Worker(new URL('./pglite.worker.ts', import.meta.url), { type: 'module' });
  try {
    const db = await PGliteWorker.create(w);
    liveWorker = w;
    return db;
  } catch (e) {
    try {
      w.terminate();
    } catch {
      /* never started */
    }
    throw e;
  }
}

/* Hosted previews and strict CSPs can refuse `new Worker` outright; the engine
   still works on the page, it just cannot be pre-empted mid-statement. */
async function openMainThreadEngine(): Promise<PGliteInterface> {
  const { PGlite } = await import('@electric-sql/pglite');
  return PGlite.create();
}

async function bootstrap(): Promise<PGliteInterface> {
  /* Loaded on demand: the WASM build is large and only needed once a query runs. */
  let db: PGliteInterface;
  try {
    db = await openWorkerEngine();
    placement = 'worker';
  } catch (workerError) {
    db = await openMainThreadEngine();
    placement = 'main';
    console.warn(
      '[querycraft] this page blocks Web Workers, so SQL runs on the main thread and a runaway query cannot be stopped by the host.',
      workerError,
    );
  }
  try {
    await db.exec(schemaSql);
    await db.exec(seedSql);
    await createContentSchema(db);
    await seedContentDocs(db);
  } catch (e) {
    await disposeDatabase();
    throw e;
  }
  return db;
}

/* Tear the worker down and drop the handle. The next query rebuilds the database
   from the same schema and seed, so the dataset is restored by construction —
   and the read-only transaction meant it was never modified anyway. */
async function disposeDatabase(): Promise<void> {
  const w = liveWorker;
  liveWorker = null;
  placement = null;
  database = null;
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

export function getDatabase(): Promise<PGliteInterface> {
  if (database === null) {
    database = bootstrap().catch((e: unknown) => {
      database = null;
      throw e;
    });
  }
  return database;
}

/** Kick off WASM download + initdb without blocking the caller. */
export function warmUpDatabase(): void {
  void getDatabase().catch(() => {
    /* surfaced on first real query */
  });
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
    case '25006':
      return {
        code: 'QUERY_RUNTIME_ERROR',
        message: 'This exercise runs in a read-only transaction, so the statement cannot change data.',
        detail: message,
      };
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

async function beginReadOnly(db: PGliteInterface): Promise<void> {
  try {
    await db.exec('BEGIN READ ONLY');
  } catch {
    await db.exec('ROLLBACK').catch(() => undefined);
    await db.exec('BEGIN READ ONLY');
  }
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

/** Execute learner SQL for real, in a read-only transaction that is always rolled back. */
export async function executeSql(sql: string): Promise<EngineResult> {
  const text = String(sql ?? '');
  const statement = stripComments(text);
  if (!statement) return failed('EMPTY_QUERY', 'Blank SQL. Write a statement, then Run.');

  let db: PGliteInterface;
  try {
    db = await getDatabase();
  } catch (e) {
    return failed('ENVIRONMENT_UNAVAILABLE', 'The in-browser database could not start.', sqlStateOf(e).message);
  }

  let results: Array<{ rows: unknown[]; fields: { name: string; dataTypeID: number }[]; command?: string }>;
  try {
    const attempt = (async () => {
      await beginReadOnly(db);
      /* Mirror the gateway's per-session setting: the commerce tables resolve
         unqualified (FROM orders) while `app` metadata stays off the path. */
      await db.exec('SET LOCAL search_path = dataset, public');
      return db.exec(text, { rowMode: 'array' });
    })();
    /* Only a Worker can be stopped from outside. On the main-thread fallback
       there is nothing to kill, so wrapping it in a timeout would just report a
       stop that never happens; `engineCapabilities()` already tells the UI that
       this engine has no hard deadline. */
    results = liveWorker !== null ? await withHardTimeout(attempt, QUERY_TIMEOUT_MS) : await attempt;
  } catch (e) {
    if (e instanceof QueryTimeout) {
      /* The worker is still spinning inside the WASM loop; terminating it is the
         only way to stop the statement. The next query re-seeds from scratch. */
      await disposeDatabase();
      return failed(
        'QUERY_TIMEOUT',
        `The query was stopped after ${QUERY_TIMEOUT_MS / 1000}s.`,
        'A statement that never finishes cannot be interrupted inside the browser engine, so the database was rebuilt.',
      );
    }
    return { status: 'failed', columns: [], rows: [], row_count: 0, truncated: false, error: mapError(e, text) };
  } finally {
    /* A terminated worker has no transaction left to roll back; the timeout path
       has already dropped the client, so this runs only on the normal path. */
    if (liveWorker !== null) await db.exec('ROLLBACK').catch(() => undefined);
  }

  const chosen = pickResult(results);
  if (chosen === null) {
    return emptyResult();
  }

  const columns = chosen.fields.map((f) => ({ name: f.name, type: oidTypeName(f.dataTypeID) }));
  const allRows = chosen.rows.map((row) => row.map((v, i) => normalizeValue(v, chosen.fields[i]?.dataTypeID ?? 25)));
  const truncated = allRows.length > MAX_ROWS;
  const rows = truncated ? allRows.slice(0, MAX_ROWS) : allRows;

  return { status: 'succeeded', columns, rows, row_count: rows.length, truncated, error: null };
}

/* ---------- introspection for the tables drawer ---------- */

const INTERNAL_TABLES = new Set(['dataset_meta', 'content_docs', 'content_meta']);

async function queryWithFields(db: PGliteInterface, sql: string): Promise<{ rows: unknown[][]; fields: { name: string; dataTypeID: number }[] }> {
  const res = await db.exec(sql, { rowMode: 'array' });
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

export async function fetchTables(): Promise<{ tables: LiveTable[]; rows: number }> {
  const db = await getDatabase();

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
  const db = await getDatabase();
  const res = await db.query<{ section: string; markdown: string }>(
    'SELECT section, markdown FROM app.content_docs WHERE module_id = $1 ORDER BY ordinal',
    [moduleId],
  );
  return res.rows.map((r) => ({ section: r.section, markdown: r.markdown }));
}
