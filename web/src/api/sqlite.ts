/*
 * The browser's SQLite adapter (M6 step 6, D12).
 *
 * This is the browser counterpart of `backend/internal/engine/sqlite.go`: the
 * place where a *driver* ends and the dialect's rules begin. Three things live
 * here and nowhere else:
 *
 *   1. `sqliteUnframe` — the driver's framing, removed. The shipped build
 *      (`@sqlite.org/sqlite-wasm` 3.53.4) renders an engine failure as
 *      `SQLITE_ERROR: sqlite3 result code 1: incomplete input`, and the
 *      profile's `equals`/`prefix` rules are written for SQLite's own sentence
 *      (`incomplete input`). The Go executor strips modernc's *different*
 *      framing (`<errstr>: <msg> (<rc>)`) in the same relative position, which
 *      is why the profile itself stays a pure matcher
 *      (`./sqliteProfile.ts`) and can serve both.
 *   2. The transport. The engine runs in a Worker (`./sqlite.worker.ts`) so a
 *      runaway statement cannot freeze the page; a page whose policy refuses
 *      `new Worker` runs the identical engine on the main thread, and
 *      `engineCapabilities()` says which of the two happened instead of
 *      pretending the deadline is enforceable everywhere.
 *   3. The contract edges — caps, encoding, error codes, the engine label —
 *      all applied through `./sqliteProfile.ts`, so no rule is copied here.
 *
 * Nothing in this module knows about Postgres. `./execution.ts` picks between
 * the two engines on the chapter's dialect; a result that cannot say which
 * engine produced it cannot be graded, so the dialect travels with the rows.
 */
import type { Dialect, EngineCapabilities, QueryErrorCode, QueryErrorShape, RowValue } from '../types';
import type { EngineResult, LiveTable } from './pglite';
import {
  SQLITE_ENGINE_LABEL,
  classifySqliteMessage,
  encodeSqliteValue,
  sqliteCapabilities,
  sqliteColumnTypeLabel,
  sqlitePlanRefusal,
} from './sqliteProfile';
import { SQLITE_MAX_BYTES, SQLITE_MAX_ROWS, type EngineFailure, type EngineRows, type EngineTable } from './sqliteEngine';
import type { WorkerRequest } from './sqlite.worker';

/** The dialect this adapter runs. Named beside the engine string so the two can
    never drift apart. */
export const SQLITE_DIALECT: Dialect = 'sqlite';

/** The engine string a result row carries before the engine has booted; the
    live version replaces it as soon as the workspace is open, the same
    `SQLite 3.53.4` shape the gateway's `SQLiteExecutor.Version` reports. */
export const SQLITE_ENGINE_FALLBACK = SQLITE_ENGINE_LABEL;

/** Mirror of the gateway's `QC_TIMEOUT_MS`. Unlike PGlite, this engine honours
    it *inside* the engine: the deadline is a `sqlite3_progress_handler` and the
    statement stops with SQLITE_INTERRUPT(9). */
export const SQLITE_TIMEOUT_MS = 5000;

/* ---------- the driver's framing ---------- */

/* `sqliteUnframe` and `sqliteErrorCode` live in ./sqliteFraming, and are
   re-exported here because this module is the adapter's entry point: a caller
   that holds the driver's message should not have to know how the file is split.
   That module is deliberately free of any Vite or Worker import so
   `scripts/sqlite-error-codes.mjs` can bundle it and pin the exact strings this
   build produces. */
export { sqliteUnframe, sqliteErrorCode } from './sqliteFraming';
import { sqliteUnframe } from './sqliteFraming';

/* ---------- transport ---------- */

/* What the adapter needs from wherever the engine is running. The Worker and the
   main thread are two implementations of this one interface, so no rule can
   live in the Worker and be missing on the fallback. */
interface Transport {
  open(): Promise<{ version: string }>;
  exec(sql: string): Promise<{ ok: true; value: EngineRows } | { ok: false; error: EngineFailure }>;
  tables(): Promise<EngineTable[]>;
  reset(): Promise<{ duration_ms: number }>;
  close(): void;
}

interface Pending {
  resolve: (value: unknown) => void;
  reject: (reason: unknown) => void;
}

/** The Worker transport: one request in flight per id, replies matched by id. */
function workerTransport(worker: Worker): Transport {
  const pending = new Map<number, Pending>();
  let nextId = 1;

  worker.onmessage = (event: MessageEvent<{ id: number; ok: boolean; value?: unknown; error?: EngineFailure }>) => {
    const { id, ok, value, error } = event.data;
    const entry = pending.get(id);
    if (entry === undefined) return;
    pending.delete(id);
    if (ok) entry.resolve(value);
    else entry.reject(error ?? new Error('sqlite worker failed'));
  };

  const call = <T>(op: WorkerRequest['op'], args: unknown[] = []): Promise<T> =>
    new Promise<T>((resolve, reject) => {
      const id = nextId++;
      pending.set(id, { resolve: resolve as (value: unknown) => void, reject });
      try {
        worker.postMessage({ id, op, args } satisfies WorkerRequest);
      } catch (e) {
        pending.delete(id);
        reject(e);
      }
    });

  return {
    async open() {
      const out = await call<{ version: string }>('open');
      return { version: out?.version ?? '' };
    },
    exec: (sql) => call('exec', [sql, SQLITE_TIMEOUT_MS, SQLITE_MAX_ROWS, SQLITE_MAX_BYTES]),
    tables: () => call('tables'),
    reset: () => call('reset'),
    close: () => {
      try {
        worker.terminate();
      } catch {
        /* never started */
      }
    },
  };
}

/**
 * The main-thread transport, for a page that refuses `new Worker`.
 *
 * It runs the same `openWorkspace()` the Worker runs, so the rows are identical;
 * what changes is the deadline. The progress handler still fires (it runs on the
 * same thread as the engine), but a *hung* statement — one that never reaches
 * the VM, or whose handler cannot run because the thread is blocked — cannot be
 * taken away from the page. `engineCapabilities().hardTimeout` reports `false`
 * here rather than claiming otherwise.
 */
async function mainThreadTransport(): Promise<Transport> {
  const { openWorkspace } = await import('./sqliteEngine');
  const ws = await openWorkspace();
  return {
    open: async () => ({ version: ws.version }),
    exec: async (sql) => ws.exec(sql, SQLITE_TIMEOUT_MS, SQLITE_MAX_ROWS, SQLITE_MAX_BYTES),
    tables: async () => ws.tables(),
    reset: async () => ({ duration_ms: ws.reset() }),
    close: () => ws.close(),
  };
}

interface Slot {
  transport: Promise<Transport> | null;
  worker: Worker | null;
  placement: 'worker' | 'main' | null;
  /** The engine's own version, once it answered. */
  version: string | null;
}

const slot: Slot = { transport: null, worker: null, placement: null, version: null };

async function bootstrap(): Promise<Transport> {
  if (slot.transport === null) {
    slot.transport = (async () => {
      let transport: Transport;
      try {
        const worker = new Worker(new URL('./sqlite.worker.ts', import.meta.url), { type: 'module' });
        try {
          transport = workerTransport(worker);
          const info = await transport.open();
          slot.worker = worker;
          slot.placement = 'worker';
          slot.version = info.version || slot.version;
        } catch (e) {
          worker.terminate();
          throw e;
        }
      } catch (workerError) {
        transport = await mainThreadTransport();
        const info = await transport.open();
        slot.placement = 'main';
        slot.version = info.version || slot.version;
        console.warn(
          '[querycraft] this page blocks Web Workers, so SQLite runs on the main thread and a runaway statement cannot be taken away from the page.',
          workerError,
        );
      }
      return transport;
    })().catch((e: unknown) => {
      /* A failed boot must not be cached: the next Run tries again, and the
         failure is reported by that Run rather than remembered as fact. */
      slot.transport = null;
      slot.placement = null;
      throw e;
    });
  }
  return slot.transport;
}

/** The engine behind the next statement. */
export function getSqliteTransport(): Promise<Transport> {
  return bootstrap();
}

export function disposeSqlite(): void {
  const transport = slot.transport;
  slot.transport = null;
  slot.worker = null;
  slot.placement = null;
  void transport?.then((t) => t.close()).catch(() => undefined);
}

/**
 * Start the engine (WASM download, module compile, schema, seed) without
 * blocking the caller.
 *
 * A SQLite chapter pays for an 868 KB WASM build and a 387-line seed before its
 * first statement runs, and the learner should spend that on the reading rather
 * than on the first keystroke — the same reason `warmUpDataset` exists for the
 * PGlite tracks.
 */
export function warmUpSqlite(): void {
  void getSqliteTransport().catch(() => {
    /* surfaced on the first real query */
  });
}

/** `null` until the engine has booted, so the UI can say "starting" rather than
    advertise a capability it has not established. */
export function sqliteEngineCapabilities(): EngineCapabilities | null {
  if (slot.placement === null) return null;
  return sqliteCapabilities({
    placement: slot.placement,
    /* The deadline is the engine's own interrupt, which exists on both
       placements; what the main-thread fallback cannot do is stop a statement
       that never reaches the engine at all. */
    hardTimeout: true,
    timeoutMs: SQLITE_TIMEOUT_MS,
    writable: true,
  });
}

/** The engine's own label — `SQLite 3.53.4` — or the profile's placeholder
    before the engine has answered. Never a hardcoded version. */
export function sqliteEngineLabel(): string {
  return slot.version === null || slot.version === '' ? SQLITE_ENGINE_FALLBACK : `SQLite ${slot.version}`;
}

/** The profile's own refusal for a plan request on this dialect, for a caller
    that must disable the action with the reason the profile gives. */
export function sqlitePlanRefusalReason(): string | null {
  return sqlitePlanRefusal();
}

/* ---------- execution ---------- */

function stripComments(sql: string): string {
  return sql
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/--[^\n]*/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

function failed(code: QueryErrorCode, message: string, detail?: string | null): EngineResult {
  return {
    status: 'failed',
    columns: [],
    rows: [],
    row_count: 0,
    truncated: false,
    error: { code, message, detail: detail ?? null },
    dialect: SQLITE_DIALECT,
    engine: sqliteEngineLabel(),
  };
}

function blankResult(): EngineResult {
  return {
    status: 'succeeded',
    columns: [],
    rows: [],
    row_count: 0,
    truncated: false,
    error: null,
    dialect: SQLITE_DIALECT,
    engine: sqliteEngineLabel(),
  };
}

/**
 * Execute learner SQL for real, on the in-browser SQLite engine.
 *
 * The result is described by the same contract as every other adapter: column
 * labels from `sqlite3_column_decltype` with the profile's `expression`
 * fallback, values encoded by storage class (`encodeSqliteValue` — a TEXT value
 * in an INTEGER-affinity column stays text, because that is what the engine
 * stored), and a failure's code from the profile's own rule table.
 */
export async function executeSqlite(sql: string): Promise<EngineResult> {
  const text = String(sql ?? '');
  const statement = stripComments(text);
  if (!statement) return failed('EMPTY_QUERY', 'Blank SQL. Write a statement, then Run.');

  let transport: Transport;
  try {
    transport = await getSqliteTransport();
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    return failed('ENVIRONMENT_UNAVAILABLE', 'The in-browser SQLite engine could not start.', message);
  }

  let outcome: Awaited<ReturnType<Transport['exec']>>;
  try {
    outcome = await transport.exec(text);
  } catch (e) {
    /* The transport itself failed (the Worker died, a message could not be
       cloned) — not the engine. It is reported as what it is. */
    const message = e instanceof Error ? e.message : String(e);
    return failed('ENVIRONMENT_UNAVAILABLE', 'The in-browser SQLite engine stopped answering.', message);
  }

  if (!outcome.ok) {
    const sentence = sqliteUnframe(outcome.error.message);
    const code = classifySqliteMessage(sentence);
    return failed(code, sentence, outcome.error.name === 'Error' ? null : `sqlite result code ${outcome.error.code ?? 'unknown'}`);
  }

  const { columns, decltypes, rows, truncated, byteLimited } = outcome.value;
  const labels = columns.map((name, i) => ({ name, type: sqliteColumnTypeLabel(decltypes[i]) }));
  const encoded: RowValue[][] = rows.map((row) => row.map((v) => encodeSqliteValue(v)));

  if (byteLimited) {
    /* The gateway's rule, mirrored: a result that cannot be represented inside
       the byte budget is a failure coded RESULT_LIMIT_EXCEEDED, and the rows it
       did collect come back with it rather than being thrown away. */
    return {
      status: 'failed',
      columns: labels,
      rows: encoded,
      row_count: encoded.length,
      truncated: true,
      error: {
        code: 'RESULT_LIMIT_EXCEEDED',
        message: `result exceeded the ${SQLITE_MAX_BYTES}-byte cap; returning the first ${encoded.length} row(s)`,
        detail: null,
      },
      dialect: SQLITE_DIALECT,
      engine: sqliteEngineLabel(),
    };
  }

  if (labels.length === 0) return blankResult();

  return {
    status: 'succeeded',
    columns: labels,
    rows: encoded,
    row_count: encoded.length,
    truncated,
    error: null,
    dialect: SQLITE_DIALECT,
    engine: sqliteEngineLabel(),
  };
}

/* ---------- introspection for the tables drawer ---------- */

/** The workspace as the tables drawer shows it: the live relations, their
    declared types (verbatim, because a declared type here is an affinity and
    not a constraint), and a real preview. */
export async function fetchSqliteTables(): Promise<{ tables: LiveTable[]; rows: number }> {
  const transport = await getSqliteTransport();
  const raw = await transport.tables();
  let total = 0;
  const tables: LiveTable[] = raw.map((t) => {
    total += t.rows;
    return { name: t.name, rows: t.rows, cols: t.cols, preview: t.preview };
  });
  return { tables, rows: total };
}

/** Restore the shipped dataset: close the workspace and reopen it from the
    schema and seed. Measured in ADR 0006's sibling for SQLite: the copy is the
    reopen, and the engine reports its own elapsed time. */
export async function resetSqliteEnvironment(): Promise<{ ok: boolean; duration_ms: number }> {
  const transport = await getSqliteTransport();
  const out = await transport.reset();
  return { ok: true, duration_ms: Number((out?.duration_ms ?? 0).toFixed(1)) };
}

export { SQLITE_MAX_BYTES, SQLITE_MAX_ROWS };
export type { EngineFailure, EngineRows, EngineTable };
export type { QueryErrorShape };
