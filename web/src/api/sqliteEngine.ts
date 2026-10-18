/*
 * The in-browser SQLite engine (M6 step 6, D12).
 *
 * `@sqlite.org/sqlite-wasm` compiled to WebAssembly, running in the page next
 * to PGlite, so a SQLite chapter executes on a real SQLite engine without a
 * server. This module is the engine itself and knows nothing about the seam,
 * the contracts or the UI: it opens a database, hands SQL to SQLite, and
 * returns what SQLite said — values *and* SQLite's own error sentence with the
 * driver's result code. Classification and encoding belong to `./sqlite.ts`,
 * exactly as `dialect.Profile.ClassifyError` and the executor's encoding belong
 * to the Go side rather than to `modernc.org/sqlite`.
 *
 * Why this build (ADR 0007, measured in the spike at /tmp/sqlite-spike):
 *
 *   - It is the only candidate that can reach `sqlite3_column_decltype`, and
 *     therefore the only one that can label a column the way the gateway's
 *     `ColumnTypes().DatabaseTypeName()` does (`INTEGER`, `TEXT`, …, and the
 *     profile's `expression` fallback). `sql.js` and `wa-sqlite` wrap 70-odd
 *     C calls and decltype is not among them, so every column would diverge
 *     from the gateway in the parity audit.
 *   - It exposes `sqlite3_progress_handler`, so a deadline can stop a statement
 *     *inside* the engine (SQLITE_INTERRUPT, result code 9), which is what the
 *     profile's `interrupt` capability promises. `sql.js` has no in-engine stop
 *     at all.
 *   - Both go through the profile's rules. The Go raw driver reports
 *     sqlite3_errmsg unframed, so only this build frames its errors, as
 *     `SQLITE_ERROR: sqlite3 result code 1: incomplete input`; `sqliteUnframe`
 *     in `./sqliteFraming.ts` strips that before the rules run.
 *
 * This module is imported by `./sqlite.worker.ts` (the Worker) and by
 * `./sqlite.ts` (the main-thread fallback), so there is one engine
 * implementation and two places to run it.
 */
import sqlite3InitModule from '@sqlite.org/sqlite-wasm';
import type { PreparedStatement, SqlValue } from '@sqlite.org/sqlite-wasm';
import sqliteWasmUrl from '@sqlite.org/sqlite-wasm/sqlite3.wasm?url';
import { schemaSql, seedSql } from './sqliteDataset';

type Sqlite3 = Awaited<ReturnType<typeof sqlite3InitModule>>;
type SqliteDatabase = InstanceType<Sqlite3['oo1']['DB']>;

/** SQLite's own sentence for a failure, plus the result code the driver
    reported. Both travel unchanged to `./sqlite.ts`, which unframes the sentence
    and classifies it with the shared profile. */
export interface EngineFailure {
  message: string;
  code: number | null;
  name: string;
}

/** What one statement produced, before encoding. `decltypes[i]` is null when the
    column is an expression; `./sqlite.ts` turns that into the profile's
    `expression` fallback label. */
export interface EngineRows {
  columns: string[];
  decltypes: (string | null)[];
  rows: SqlValue[][];
  truncated: boolean;
  /** The first row that did not fit, when the byte cap stopped the read. The
      caller reports `RESULT_LIMIT_EXCEEDED` and the rows already collected, the
      same trade the gateway's `engine.Execute` makes. */
  byteLimited: boolean;
}

export interface EngineTable {
  name: string;
  rows: number;
  /** [column, declared type, note] — the drawer's own shape. */
  cols: [string, string, string][];
  preview: string[][];
}

/** How many VM instructions SQLite may run between two deadline checks. Small
    enough that the deadline is honoured to a few milliseconds, large enough that
    the check is not the cost of the statement. */
const PROGRESS_INTERVAL = 1000;

let enginePromise: Promise<Sqlite3> | null = null;

/* The package's own type declares `init(): Promise<Sqlite3Static>` with no
   parameters, but the build it ships reads `moduleArg.locateFile` (index.mjs,
   the `locateFile` helper at the top of the Emscripten glue). The cast states
   what the shipped code does; the alternative is to let Vite's rewritten
   `import.meta.url` decide where the WASM lives, which is the failure this
   parameter exists to prevent. */
const bootSqlite3 = sqlite3InitModule as unknown as (config?: {
  locateFile?: (path: string, scriptDirectory: string) => string;
}) => Promise<Sqlite3>;

/** Boot the WASM module, once per thread.
 *
 * `locateFile` is explicit rather than left to the build's `import.meta.url`
 * default: Vite rewrites that path when it pre-bundles a dependency, and the
 * engine then fetches the SPA's `index.html` instead of its 868 KB WASM build.
 * The URL comes from Vite's own `?url` import, so the asset is hashed and
 * emitted by the bundler and works in dev and in a production build alike. */
function getSqlite3(): Promise<Sqlite3> {
  if (enginePromise === null) {
    enginePromise = bootSqlite3({ locateFile: () => sqliteWasmUrl }).catch((e: unknown) => {
      enginePromise = null;
      throw e;
    });
  }
  return enginePromise;
}

 /** A statement's outcome, either the rows or the engine's failure. */
type ExecOutcome = { ok: true; value: EngineRows } | { ok: false; error: EngineFailure };

function asFailure(e: unknown): EngineFailure {
  if (typeof e === 'object' && e !== null) {
    const o = e as { message?: unknown; code?: unknown; name?: unknown; resultCode?: unknown };
    const code = typeof o.code === 'number' ? o.code : typeof o.resultCode === 'number' ? o.resultCode : null;
    return {
      message: typeof o.message === 'string' ? o.message : String(e),
      code,
      name: typeof o.name === 'string' ? o.name : 'Error',
    };
  }
  return { message: String(e), code: null, name: 'Error' };
}

/**
 * An open SQLite database with the shipped schema and seed applied.
 *
 * The database is in memory (`:memory:`), which is the browser's honest
 * equivalent of the gateway's per-account file: it is private to this page, it
 * disappears when the tab does, and it is restored by reopening and re-seeding —
 * the measured reset for the file-per-account case is a re-copy of the sealed
 * template (ADR 0006), and a re-open of an in-memory database is the same idea
 * with the copy made unnecessary.
 */
export interface SqliteWorkspace {
  /** The engine's own version, from `sqlite3_version`. */
  version: string;
  /** Run SQL. `timeoutMs` is a real deadline inside the VM (progress handler),
      and `maxRows`/`maxBytes` are the same caps the gateway's executor applies. */
  exec(sql: string, timeoutMs: number, maxRows: number, maxBytes: number): ExecOutcome;
  /** The learner-visible relations of the workspace, for the tables drawer. */
  tables(): EngineTable[];
  /** Drop everything and re-apply the shipped schema and seed. Returns the
      engine's own elapsed time in milliseconds. */
  reset(): number;
  close(): void;
}

/** Open, configure and seed a fresh in-memory database. Synchronous: once the
    module is booted nothing here waits, which is what lets `reset()` return the
    engine's own elapsed time without a half-reset workspace in between. */
function openDatabase(sqlite3: Sqlite3): { db: SqliteDatabase; version: string } {
  const db = new sqlite3.oo1.DB(':memory:', 'c');
  const version = sqlite3.version.libVersion;
  /* Foreign keys are off by default in SQLite and are per-connection, not part
     of the file. The dataset declares its references, so enforcement is what a
     learner should meet; the gateway's executor turns it on for the same reason.
     `query_only` is deliberately NOT set: an in-browser SQLite chapter is a
     writable workspace (ADR 0003), and reset is how the learner gets back. */
  db.exec('PRAGMA foreign_keys = ON');
  db.exec(schemaSql);
  db.exec(seedSql);
  return { db, version };
}

/**
 * The number of rows a statement is allowed to hand back and the number of bytes
 * their JSON takes, mirroring the gateway's `engine.DefaultLimits` so that a
 * capped result is described the same way in both adapters: hitting the row cap
 * is a successful, truncated result, and hitting the byte cap is
 * `RESULT_LIMIT_EXCEEDED` with the rows already collected.
 *
 * `./pglite.ts` keeps its own 200-row guard, which is deliberately stricter —
 * it exists to keep a 10 000-row `SELECT *` out of the DOM, not to define the
 * contract. The two numbers are stated here rather than shared because they
 * answer different questions; a parity case that crosses either limit is what
 * would tell us they need to be one number.
 */
export const SQLITE_MAX_ROWS = 500;
export const SQLITE_MAX_BYTES = 1 << 20;

export async function openWorkspace(): Promise<SqliteWorkspace> {
  const sqlite3 = await getSqlite3();
  let current = openDatabase(sqlite3);

  const exec: SqliteWorkspace['exec'] = (sql, timeoutMs, maxRows, maxBytes) => {
    const db = current.db;
    if (!db.isOpen()) return { ok: false, error: { message: 'database is closed', code: null, name: 'SQLite3Error' } };

    const columns: string[] = [];
    const rows: SqlValue[][] = [];
    let truncated = false;
    let byteLimited = false;
    let bytes = 0;

    /* The deadline lives in the engine, not in a race against it: the progress
       handler runs inside SQLite's step loop, sees that the wall clock has
       passed, and returns 1, which is SQLITE_INTERRUPT(9) — a result code the
       profile names and the probe measured at 200.4 ms for a 200 ms deadline. A
       statement stopped this way leaves the connection usable. */
    const deadlineAt = (typeof performance !== 'undefined' ? performance.now() : Date.now()) + timeoutMs;
    const pointer = db.pointer;
    if (pointer !== undefined) {
      sqlite3.capi.sqlite3_progress_handler(
        pointer,
        PROGRESS_INTERVAL,
        () => ((typeof performance !== 'undefined' ? performance.now() : Date.now()) > deadlineAt ? 1 : 0),
        0,
      );
    }

    try {
      db.exec({
        sql,
        rowMode: 'array',
        columnNames: columns,
        callback: (row: SqlValue[]) => {
          if (rows.length >= maxRows) {
            truncated = true;
            /* Returning literal false stops the step loop *without* an error,
               which is what makes a capped read cheap: the gateway cancels the
               statement at the cap for the same reason (measured there: 4003 ms
               to drain versus 4 ms to stop). */
            return false;
          }
          const encoded = JSON.stringify(row);
          const size = encoded === undefined ? 0 : encoded.length;
          if (bytes + size > maxBytes) {
            truncated = true;
            byteLimited = true;
            return false;
          }
          bytes += size;
          rows.push(row);
          return undefined;
        },
      });
    } catch (e) {
      return { ok: false, error: asFailure(e) };
    } finally {
      if (pointer !== undefined) sqlite3.capi.sqlite3_progress_handler(pointer, 0, 0, 0);
    }

    return {
      ok: true,
      value: { columns: [...columns], decltypes: decltypesFor(db, sqlite3, sql, columns.length), rows, truncated, byteLimited },
    };
  };

  return {
    version: current.version,
    exec,
    tables: () => readTables(current.db),
    reset: () => {
      const t0 = typeof performance !== 'undefined' ? performance.now() : Date.now();
      try {
        current.db.close();
      } catch {
        /* already closed */
      }
      /* Closing and re-opening replaces the database by construction: the
         in-memory file is gone, and what comes back is the shipped schema and
         seed. Reopening is the whole reset — there is no state to drop. */
      current = openDatabase(sqlite3);
      return (typeof performance !== 'undefined' ? performance.now() : Date.now()) - t0;
    },
    close: () => {
      try {
        current.db.close();
      } catch {
        /* already closed */
      }
    },
  };
}

/**
 * The declared type of each result column, verbatim, or null where the column is
 * an expression.
 *
 * The profile's rule is `sqlite3_column_decltype` with the `expression`
 * fallback, and this is the only build that can reach it. The label is the
 * engine's own text — `INTEGER`, `TEXT`, `NUMERIC`, `DATE` — never the value's
 * storage class, which is the dialect's central lesson: a `DATE` column labels
 * `DATE` while its values are TEXT.
 *
 * The compiled statement is thrown away without being stepped: reading a column's
 * declared type has no side effect, so asking about a `CREATE TABLE` again is
 * safe. A statement that cannot be recompiled (or that produced a different
 * number of columns than the executed result, which is how a multi-statement
 * script announces itself) reports null for every column, and the caller labels
 * them `expression` — the fallback, not a guess.
 */
function decltypesFor(db: SqliteDatabase, sqlite3: Sqlite3, sql: string, columnCount: number): (string | null)[] {
  let stmt: PreparedStatement | null = null;
  try {
    stmt = db.prepare(sql);
    if (stmt.columnCount !== columnCount) return new Array(columnCount).fill(null);
    const pointer = stmt.pointer;
    if (pointer === undefined) return new Array(columnCount).fill(null);
    const out: (string | null)[] = [];
    for (let i = 0; i < columnCount; i += 1) {
      const declared = sqlite3.capi.sqlite3_column_decltype(pointer, i);
      out.push(typeof declared === 'string' && declared !== '' ? declared : null);
    }
    return out;
  } catch {
    return new Array(columnCount).fill(null);
  } finally {
    try {
      stmt?.finalize();
    } catch {
      /* A statement that never compiled has nothing to finalize. */
    }
  }
}

/* ---------- introspection for the tables drawer ---------- */

/** Bookkeeping, not learner data: it is not a relation a lesson can address. */
const INTERNAL_TABLES = new Set(['dataset_meta', 'sqlite_sequence']);

function readTables(db: SqliteDatabase): EngineTable[] {
  const names: string[] = [];
  db.exec({
    sql: "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name",
    rowMode: 'array',
    callback: (row: SqlValue[]) => {
      const name = String(row[0]);
      if (!INTERNAL_TABLES.has(name) && !name.startsWith('sqlite_')) names.push(name);
    },
  });

  const tables: EngineTable[] = [];
  for (const name of names) {
    const quoted = `"${name.replace(/"/g, '""')}"`;
    let rows = 0;
    db.exec({
      sql: `SELECT count(*) FROM ${quoted}`,
      rowMode: 'array',
      callback: (row: SqlValue[]) => {
        rows = Number(row[0] ?? 0);
      },
    });

    /* `PRAGMA table_info` reports the declared type verbatim — the same string
       `sqlite3_column_decltype` returns — and `pk` is the primary-key ordinal, so
       the drawer's "PK" note is the engine's answer rather than a parse of the
       DDL. */
    const cols: [string, string, string][] = [];
    const primaryKeys = new Set<string>();
    db.exec({
      sql: `PRAGMA table_info(${quoted})`,
      rowMode: 'object',
      callback: (row) => {
        const column = String(row['name'] ?? '');
        const declared = String(row['type'] ?? '');
        const notNull = Number(row['notnull'] ?? 0) !== 0;
        if (Number(row['pk'] ?? 0) !== 0) primaryKeys.add(column);
        cols.push([column, declared === '' ? 'expression' : declared, notNull ? 'NOT NULL' : 'nullable']);
        return undefined;
      },
    });

    const foreignKeys = new Set<string>();
    db.exec({
      sql: `PRAGMA foreign_key_list(${quoted})`,
      rowMode: 'object',
      callback: (row) => {
        const from = row['from'];
        if (typeof from === 'string') foreignKeys.add(from);
        return undefined;
      },
    });

    const preview: string[][] = [cols.map((c) => c[0])];
    db.exec({
      sql: `SELECT * FROM ${quoted} LIMIT 5`,
      rowMode: 'array',
      callback: (row: SqlValue[]) => {
        preview.push(row.map((v) => (v === null || v === undefined ? 'NULL' : String(v))));
        return undefined;
      },
    });

    tables.push({
      name,
      rows,
      cols: cols.map(([column, declared, note]) => [
        column,
        declared,
        primaryKeys.has(column) ? 'PK' : foreignKeys.has(column) ? 'FK' : note,
      ]),
      preview,
    });
  }
  return tables;
}
