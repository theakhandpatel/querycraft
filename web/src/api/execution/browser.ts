import { Effect, Layer } from 'effect';
import { GatewayError, authFailure } from '../errors';
import { browserAiMethods } from '../ai';
import { checksFromComparison, compareColumns, compareResult } from '../compare';
import type { Comparison, ExpectedResult } from '../compare';
import {
  PGLITE_ENGINE,
  engineCapabilities,
  executeSql,
  explainSql,
  fetchModuleContent,
  fetchTables,
  resetEnvironment as resetPgliteEnvironment,
  warmUpDataset,
} from '../pglite';
import {
  executeSqlite,
  fetchSqliteTables,
  resetSqliteEnvironment,
  sqliteEngineCapabilities,
  warmUpSqlite,
} from '../sqlite';
import { STORE_ATTEMPTS, STORE_PROGRESS, idbGet, idbPut, idbPutMany } from '../idb';
import { bundleFromProgress } from '../../lib/offlineBundle';
import { attemptKey } from '../../lib/progress';
import { EXPORT_FORMAT_VERSION } from '../../lib/exportBundle';
import { datasetFor } from '../../lib/dataset';
import { dialectFor } from '../../lib/dialect';
import { datasetVersion } from '../dataset';
import { DEFAULT_DATASET, DEFAULT_DIALECT } from '../../types';
import type { ApiConfig, Course, DatasetId, Dialect, EnvironmentInfo } from '../../types';
import { ExecutionService, clearAuthToken, nowMs, offlineDraft } from './shared';
import type { ExecutionServiceShape, NoteFn } from './shared';

/* The browser layer: PGlite runs PostgreSQL chapters and the SQLite WASM
   engine runs SQLite chapters, both inside the page.

   One layer owns both engines because the chapter's dialect decides where a
   statement runs, and one place has to make that decision (see `runOnDialect`).
   The engine adapters stay in `../pglite.ts` and `../sqlite.ts`. */

const round = (n: number): number => Number(n.toFixed(1));
/* The OUTER Evaluation.duration_ms is a string on the contract (the gateway
   emits milliseconds as "8.4"); the QueryResult's own duration_ms is a number.
   This formats the outer one the way the gateway does, so the two adapters agree
   and the TS type does not need a number|string union. */
const secs = (n: number): string => round(n).toFixed(1);
const rid = (): string => 'rq_' + Math.random().toString(36).slice(2, 8);

function delayMs(config: ApiConfig): number {
  if (!config.latency) return 0;
  const lo = config.latencyMin ?? 90;
  const hi = config.latencyMax ?? 240;
  return lo + Math.random() * (hi - lo);
}
/* ---------- PGlite adapter: a real PostgreSQL running inside the page ---------- */

export const PGLITE_CONFIG: ApiConfig = {
  baseUrl: 'pglite://memory/querycraft',
  engine: PGLITE_ENGINE,
  dialect: 'postgresql',
  latency: false,
};

/* Offline learner data lives in IndexedDB (see ./idb) so the browser layer
   keeps one honest store: a snapshot read back on the next boot, and a
   failed write that surfaces as PERSISTENCE_FAILED instead of a silent
   { ok: true }. Course generation stays in memory; it is a draft, not data. */
type LocalMethods = Pick<ExecutionServiceShape, 'getProgress' | 'saveProgress' | 'logEvent' | 'draftCourse' | 'draftChallenge'>;

const READ_FAILURE = 'Could not read your saved progress in this browser (storage unavailable).';
const SAVE_FAILURE = 'Could not save your progress in this browser (storage unavailable or full).';

/** Offline storage failed: carry the contract code so callers can branch on it. */
function persistenceFailure(message: string, cause: unknown): GatewayError {
  return new GatewayError({
    code: 'PERSISTENCE_FAILED',
    message,
    detail: cause instanceof Error ? cause.message : String(cause),
  });
}

/** Snapshots cross into structured storage; a JSON clone keeps them plain and
    strips anything (proxies, class instances) IndexedDB would refuse to store. */
function jsonClone<T>(value: T): T {
  if (value === undefined) return value;
  return JSON.parse(JSON.stringify(value)) as T;
}

/** An id for an event that arrived without one. Prefers the platform UUID. */
function newEventId(): string {
  const c = typeof crypto === 'undefined' ? undefined : crypto;
  if (c && typeof c.randomUUID === 'function') return 'evt_' + c.randomUUID();
  return 'evt_' + Date.now().toString(36) + Math.random().toString(36).slice(2, 10);
}

function localMethods(config: ApiConfig, note: NoteFn): LocalMethods {
  return {
    getProgress: (courseId) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const key = courseId ?? 'selected';
        yield* Effect.sleep(delayMs(config));
        const snapshot = yield* Effect.tryPromise({
          try: () => idbGet<unknown>(STORE_PROGRESS, key),
          catch: (cause) => {
            note('GET', `/progress/${key}`, 500, nowMs() - t0);
            return persistenceFailure(READ_FAILURE, cause);
          },
        });
        note('GET', `/progress/${key}`, 200, nowMs() - t0);
        /* Nothing stored yet: the store treats the in-memory copy as authoritative. */
        return snapshot == null ? { ok: true } : { ok: true, snapshot };
      }),
    saveProgress: (snapshot, courseId) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const key = courseId ?? 'selected';
        const value = jsonClone(snapshot);
        /* Design 8.1: one snapshot in memory, split across the per-entity
           stores on the way to disk -- and written in a single transaction, so
           a note is never stored without the bookmark beside it. */
        const rows = bundleFromProgress(value, {
          courseId: key,
          datasetVersion: datasetVersion,
          exportFormatVersion: EXPORT_FORMAT_VERSION,
        }).map((row) => [row.store, row.key, row.value] as const);
        yield* Effect.sleep(delayMs(config));
        yield* Effect.tryPromise({
          try: () => idbPutMany(rows),
          catch: (cause) => {
            note('PUT', `/progress/${key}`, 500, nowMs() - t0);
            return persistenceFailure(SAVE_FAILURE, cause);
          },
        });
        note('PUT', `/progress/${key}`, 200, nowMs() - t0);
        return { ok: true };
      }),
    logEvent: (evt) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        /* Keyed by the client request id (8.2), not the exercise id, so a
           second attempt at the same exercise is a new row, not an overwrite. */
        const id = evt && (evt.id || evt.attemptId) ? attemptKey(evt) : newEventId();
        const record = jsonClone({ ...evt, id });
        yield* Effect.sleep(delayMs(config));
        yield* Effect.tryPromise({
          try: () => idbPut(STORE_ATTEMPTS, id, record),
          catch: (cause) => {
            note('POST', '/events', 500, nowMs() - t0);
            return persistenceFailure(SAVE_FAILURE, cause);
          },
        });
        note('POST', '/events', 202, nowMs() - t0);
        return { ok: true, id };
      }),
    draftCourse: (req) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('POST', '/courses/generate', 201, nowMs() - t0);
        return offlineDraft(req).course;
      }),
    draftChallenge: (req) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('POST', '/challenges/generate', 201, nowMs() - t0);
        return offlineDraft(req).challenge;
      }),
  };
}

function engineFailure(cause: unknown): GatewayError {
  return new GatewayError({
    code: 'ENVIRONMENT_UNAVAILABLE',
    message: 'The in-browser database could not be reached.',
    detail: cause instanceof Error ? cause.message : String(cause),
  });
}

/* ---------- Offline auth: the PGlite adapter has no server session ----------

   PGlite runs PostgreSQL inside the page; there is no auth server behind it, so
   this layer cannot authenticate anyone. Rather than fabricate a server
   session, signup/login fail with a typed AUTH_UNAVAILABLE and `me` reports no
   session. The app therefore runs offline as a local workspace (see the store),
   and the gateway is what makes real accounts possible. */

function offlineAuthMethods(): Pick<ExecutionServiceShape, 'signup' | 'login' | 'logout' | 'me'> {
  const unavailable = () =>
    Effect.fail(
      authFailure('AUTH_UNAVAILABLE', 'Accounts need the Querycraft gateway; the offline engine has no sign-in.', 503),
    );
  return {
    signup: () => unavailable(),
    login: () => unavailable(),
    /* Nothing to revoke in the offline engine, but drop any stale token so a
       later switch to gateway mode does not carry an invalid one. */
    logout: () => Effect.sync(() => clearAuthToken()),
    me: () => Effect.succeed(null),
  };
}

/**
 * The browser adapter runs *two* engines, and this is the only place that decides
 * which one a statement goes to.
 *
 * PostgreSQL chapters run on PGlite and SQLite chapters on the WASM SQLite
 * engine, both in this page. The decision is made from the resolved dialect
 * rather than from the SQL, because guessing an engine from the text is exactly
 * the mistake the M6 exit gate names. `config.dialect` is the adapter's default
 * (PostgreSQL), not an answer for a particular statement.
 */
function runOnDialect(dialect: Dialect, sql: string, dataset: DatasetId) {
  return dialect === 'sqlite' ? executeSqlite(sql) : executeSql(sql, dataset);
}

export function pgliteLayer(config: ApiConfig = PGLITE_CONFIG, note: NoteFn = () => {}): Layer.Layer<ExecutionService> {
  /* Reference solutions are executed once per exercise and reused. */
  const referenceCache = new Map<string, ExpectedResult | null>();

  const service: ExecutionServiceShape = {
    config,
    /* Generation policy lives on the gateway, so the in-page engine reports
       the lab unavailable rather than imitating a pipeline. */
    ...browserAiMethods(),
    ...offlineAuthMethods(),
    /* Reported by the engine itself: 'worker' keeps the deadline real, 'main'
       is the fallback a strict page policy forces (design §5.2). */
    /* The page runs two engines, so the capability answer is composed rather
       than taken from either one: the placement and deadline are PGlite's (a
       page that blocks Workers blocks them for both), `dialects` names what this
       adapter can actually run, and `explainDialects` says which of those can be
       planned. Answering with PGlite's own block alone would tell a SQLite
       chapter it is unsupported, and answering with the union of features would
       advertise a plan for an engine that has none. */
    /* There is no gateway to ask for its readiness report, so the answer is
       null rather than a report about the page-local engine: the field's
       consumers render a gateway panel, and a lookalike would be a lie. */
    diagnostics: () => Effect.succeed(null),
    /* There is no server to keep preferences on, and inventing a durable
       answer would be a lie; the browser's own localStorage already carries
       the theme offline. */
    preferences: () => Effect.succeed(null),
    savePreferences: (prefs) => Effect.succeed(prefs),
    capabilities: () =>
      Effect.sync(() => {
        const postgres = engineCapabilities();
        const sqlite = sqliteEngineCapabilities();
        if (postgres === null && sqlite === null) return null;
        const base = postgres ?? sqlite;
        if (base === null) return null;
        return {
          ...base,
          dialect: 'postgresql' as Dialect,
          dialects: ['postgresql', 'sqlite'] as Dialect[],
          explainDialects: ['postgresql'] as Dialect[],
        };
      }),
    /* The offline workspace has no server account, so there is no blank-password
       administrator to advertise. */
    adminPasswordEmpty: () => Effect.succeed(false),
    warmUp: (dataset = DEFAULT_DATASET, dialect: Dialect = DEFAULT_DIALECT) =>
      Effect.sync(() => {
        /* Warming the engine the chapter will not use is pure cost: a SQLite
           chapter needs the SQLite WASM build, and booting PGlite's 6 MB data
           bundle for it would spend the learner's bandwidth on nothing. */
        if (dialect === 'sqlite') warmUpSqlite();
        else warmUpDataset(dataset);
      }),
    runQuery: (sql, context) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const dataset = datasetFor(context);
        const dialect = dialectFor(context);
        const out = yield* Effect.tryPromise({ try: () => runOnDialect(dialect, sql, dataset), catch: engineFailure });
        const ms = nowMs() - t0;
        note('POST', '/query', out.status === 'succeeded' ? 200 : 400, ms);
        /* `engine` and `dialect` come off the result, where the engine that
           produced the rows named them, rather than off the adapter's config —
           otherwise a SQLite chapter would be labelled PostgreSQL. */
        return { ...out, request_id: rid(), engine: out.engine ?? config.engine, dialect: out.dialect, duration_ms: round(ms) };
      }),
    /* M5. The browser's own PostgreSQL captures the plan; `explainSql` builds
       the same bundle the gateway would, so a lesson renders identically on
       either adapter. */
    explain: (req) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const out = yield* Effect.tryPromise({
          try: () => explainSql(req.sql, req.analyze === true, rid(), datasetFor({ dataset: req.dataset, exercise: req.exercise })),
          catch: engineFailure,
        });
        const ms = nowMs() - t0;
        note('POST', '/explain', out.status === 'succeeded' ? 200 : 400, ms);
        return out;
      }),
    checkAnswer: (sql, cell) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const block = cell.item;
        const dataset = datasetFor({ dataset: cell.dataset, course: cell.course, block });
        const dialect = dialectFor({ dialect: cell.dialect, course: cell.course, module: cell.module, block });
        const out = yield* Effect.tryPromise({ try: () => runOnDialect(dialect, sql, dataset), catch: engineFailure });
        const ms = nowMs() - t0;
        note('POST', '/answers/evaluate', out.status === 'succeeded' ? 200 : 400, ms);
        const qr = { ...out, request_id: rid(), engine: out.engine ?? config.engine, dialect: out.dialect, duration_ms: round(ms) };

        if (out.status !== 'succeeded') {
          return { qr, checks: [], passed: false, duration_ms: secs(ms) };
        }

        const actual = { columns: out.columns.map((c) => c.name), rows: out.rows };
        const reference = String(block?.solution ?? '').trim();
        /* The dataset is part of the cache key, not just the query: the same
           exercise run on the two fixtures has two different reference results,
           and a key that omitted the dataset would serve the commerce one to a
           perf submission and grade it against rows from the wrong fixture. */
        const cacheKey = `${dialect}::${dataset}::${block?.id ?? 'anon'}::${reference}`;

        let comparison: Comparison;
        let expected: ExpectedResult | null = null;
        if (reference) {
          if (referenceCache.has(cacheKey)) {
            expected = referenceCache.get(cacheKey) ?? null;
          } else {
            const refOut = yield* Effect.tryPromise({ try: () => runOnDialect(dialect, reference, dataset), catch: engineFailure });
            expected = refOut.status === 'succeeded' && !refOut.truncated
              ? { columns: refOut.columns.map((c) => c.name), rows: refOut.rows }
              : null;
            referenceCache.set(cacheKey, expected);
          }
        } else if (block?.result) {
          expected = { columns: block.result.columns, rows: block.result.rows };
        }

        if (expected) {
          comparison = compareResult(expected, block?.contract, actual);
        } else {
          comparison = compareColumns(block?.contract, actual);
        }

        const checks = checksFromComparison(comparison, block?.contract);
        return { qr, checks, passed: comparison.ok, duration_ms: secs(ms) };
      }),
    getTables: (dataset = DEFAULT_DATASET, dialect: Dialect = DEFAULT_DIALECT) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        /* The drawer describes the workspace the learner is actually in. A
           SQLite chapter that listed the PostgreSQL relations would be
           describing a database it is not running on — the two schemas are
           mirrors, but their declared types are not the same claim. */
        const live = yield* Effect.tryPromise({
          try: () => (dialect === 'sqlite' ? fetchSqliteTables() : fetchTables(dataset)),
          catch: engineFailure,
        });
        note('GET', `/datasets/${dataset}/tables`, 200, nowMs() - t0);
        return { dataset, version: 1, tables: live.tables, rows: live.rows };
      }),
    /* The browser engine is the learner's own database, created on first use
       and writable, so it always has an environment to describe. */
    getEnvironment: () =>
      Effect.succeed<EnvironmentInfo | null>({ database: config.baseUrl, exists: true, writable: true }),
    resetEnvironment: (dataset = DEFAULT_DATASET, dialect: Dialect = DEFAULT_DIALECT, course: Course | null = null) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        /* "Restore my data" restores the workspace the learner is in, so the
           dialect picks the engine exactly as it does for a statement.

           A stored course's setup is re-applied after the template, from the
           same record the lesson loaded, so the learner's database is the one
           the exercises were verified in. A course is a PostgreSQL record, so
           naming one for the SQLite workspace is refused rather than silently
           ignored. */
        const setup = courseSetupStatements(course);
        if (dialect === 'sqlite' && course) {
          return yield* Effect.fail(
            new GatewayError({
              code: 'ENVIRONMENT_UNAVAILABLE',
              message: 'A course setup is a PostgreSQL workspace; the SQLite workspace cannot restore it.',
            }),
          );
        }
        const out = yield* Effect.tryPromise({
          try: () => (dialect === 'sqlite' ? resetSqliteEnvironment() : resetPgliteEnvironment(dataset, setup)),
          catch: engineFailure,
        });
        note('POST', '/environment/reset', 200, nowMs() - t0);
        return {
          ok: out.ok,
          duration_ms: out.duration_ms,
          /* The SQLite workspace has no course setup of its own, so it reports
             the zero it applied rather than a value it never had. */
          setup_statements: 'setup_statements' in out && typeof out.setup_statements === 'number' ? out.setup_statements : 0,
          environment: { database: config.baseUrl, exists: true, writable: true },
        };
      }),
    ...localMethods(config, note),
    /* Chapter content comes out of the database, not the bundled markdown the
       gateway adapter serves: this layer has a real Postgres to read it from. */
    getContent: (moduleId) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const docs = yield* Effect.tryPromise({ try: () => fetchModuleContent(moduleId), catch: engineFailure });
        note('GET', `/content/${moduleId}`, 200, nowMs() - t0);
        return docs;
      }),
  };
  return Layer.succeed(ExecutionService, service);
}

/** The setup statements a stored course's record carries.
 *
 * The statements are copied from the record the lesson already loaded — never
 * invented here — so the offline adapter reproduces exactly what the verifier
 * applied, and a course with no scratch objects contributes none. */
function courseSetupStatements(course?: Course | null): readonly string[] {
  const statements = course?.setup?.statements;
  return Array.isArray(statements) ? statements.filter((s): s is string => typeof s === 'string') : [];
}

