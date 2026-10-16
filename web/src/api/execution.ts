import { Context, Effect, Layer, Schedule } from 'effect';
import { advertisedRunBudgetMs, providerDeadlineMs } from './aiDeadline';
import { GatewayError, asGatewayError, authFailure } from './errors';
import { browserAiMethods, gatewayAiMethods, unavailableAvailability } from './ai';
import type { AiServiceShape, AiPerformOptions, AiPerformResult } from './ai';
import type { AiAvailability } from './aiContract';
import { decodeAuthSession, decodeEvaluation, decodeQueryResult, decodeUser } from './contract';
import { engineExecute } from './engine';
import {
  failedPlanBundle,
  validatePlanBundle,
  type ExplainRequest,
  type PlanBundle,
} from './planContract';
import { checksFromComparison, compareColumns, compareResult } from './compare';
import type { Comparison, ExpectedResult } from './compare';
import {
  PGLITE_ENGINE,
  engineCapabilities,
  executeSql,
  explainSql,
  fetchModuleContent,
  fetchTables,
  resetEnvironment as resetPgliteEnvironment,
  warmUpDataset,
} from './pglite';
import {
  executeSqlite,
  fetchSqliteTables,
  resetSqliteEnvironment,
  sqliteEngineCapabilities,
  warmUpSqlite,
} from './sqlite';
import { STORE_ATTEMPTS, STORE_DRAFTS, STORE_PROGRESS, idbGet, idbPut, idbPutMany } from './idb';
import { bundleFromProgress } from '../lib/offlineBundle';
import { attemptKey } from '../lib/progress';
import { EXPORT_FORMAT_VERSION } from '../lib/exportBundle';
import { datasetFor } from '../lib/dataset';
import { dialectFor } from '../lib/dialect';
import { datasetVersion } from './dataset';
import { BUNDLED_CONTENT } from './content';
import { TABLES } from '../data/curriculum';
import { DEFAULT_DATASET, DEFAULT_DIALECT } from '../types';
import type {
  ApiConfig,
  ApiLogEntry,
  AuthSession,
  Cell,
  ContentSection,
  Course,
  CourseGeneration,
  DatasetId,
  Dialect,
  Evaluation,
  EngineCapabilities,
  EnvironmentInfo,
  EnvironmentReset,
  ExecutionApi,
  GenerationRequest,
  LoginInput,
  QueryContext,
  Progress,
  ProgressEvent,
  QueryResult,
  SignupInput,
  TablesResponse,
  User,
} from '../types';

/* The typed failure every adapter can raise now lives in ./errors so the AI
   seam can raise and inspect it without importing the browser engine. It is
   re-exported here because that is the name callers already import. */
export { GatewayError };

/** The capability surface, expressed as Effects with a typed error channel. */
export interface ExecutionServiceShape extends AiServiceShape {
  readonly config: ApiConfig;
  /* Accounts. `me` resolves to null when there is no valid session (including a
     401 from the gateway, which also clears the stored token) so boot restore
     never has to treat "signed out" as an error. */
  signup(input: SignupInput): Effect.Effect<AuthSession, GatewayError>;
  login(input: LoginInput): Effect.Effect<AuthSession, GatewayError>;
  logout(): Effect.Effect<void, GatewayError>;
  me(): Effect.Effect<User | null, GatewayError>;
  /* M5. The second argument describes *where the SQL came from* — the exercise,
     the chapter, the course — and the adapter turns that into the one dataset to
     run on. A screen that knows nothing about datasets still runs on the right
     one, because the course it is rendering carries the dataset. */
  runQuery(sql: string, context?: QueryContext): Effect.Effect<QueryResult, GatewayError>;
  checkAnswer(sql: string, cell: Cell): Effect.Effect<Evaluation, GatewayError>;
  getTables(dataset?: DatasetId, dialect?: Dialect): Effect.Effect<TablesResponse, GatewayError>;
  /* ADR 0003: the learner's writes live in a workspace that can be restored.
     `getEnvironment()` returns null when the adapter has no such workspace. */
  getEnvironment(): Effect.Effect<EnvironmentInfo | null, GatewayError>;
  /* M5. "Restore my data" is a different workspace depending on the course: the
     challenge database, or the plan lab. The dataset names which. */
  resetEnvironment(dataset?: DatasetId, dialect?: Dialect, course?: Course | null): Effect.Effect<EnvironmentReset, GatewayError>;
  /* Building a workspace is expensive and nobody should pay for it on the first
     Run. This is the seam's "the learner is about to need this dataset" hint:
     the browser adapter starts the engine, an adapter with a server behind it
     has nothing to do. Never fails and never reports — a warm-up that could
     fail the screen would be worse than a slow first query. */
  warmUp(dataset?: DatasetId, dialect?: Dialect): Effect.Effect<void>;
  getProgress(courseId?: string): Effect.Effect<{ ok: boolean; snapshot?: unknown }, GatewayError>;
  saveProgress(snapshot: Progress, courseId?: string): Effect.Effect<{ ok: boolean }, GatewayError>;
  logEvent(evt: ProgressEvent): Effect.Effect<{ ok: boolean; id?: string }, GatewayError>;
  /* Record an authored draft through the API. This is the offline evidence
     path's write; real gateway generation is `generateCourse`/`generateChallenge`
     on the AI seam (./ai), which is a different operation. */
  draftCourse(req: GenerationRequest): Effect.Effect<CourseGeneration, GatewayError>;
  draftChallenge(req: GenerationRequest): Effect.Effect<{ ok: boolean }, GatewayError>;
  getContent(moduleId: string): Effect.Effect<ContentSection[], GatewayError>;
  /** M5. Capture a plan through the same seam as a query. Resolves to a bundle
      for every adapter — a refusal is a failed bundle with `timing_precision:
      'none'`, so a screen renders what happened instead of inventing a plan. */
  explain(req: ExplainRequest): Effect.Effect<PlanBundle, GatewayError>;
  /** Never fails: a missing capability is reported as null, not as an error. */
  capabilities(): Effect.Effect<EngineCapabilities | null>;
  /** Whether the gateway's seeded default administrator can be signed in with
      an empty password. Never fails: an unreadable probe reports false, so the
      sign-in hint stays hidden rather than advertising a blank password it
      could not confirm. The offline adapters always report false. */
  adminPasswordEmpty(): Effect.Effect<boolean>;
}

/** Service key: screens and tests depend on this, never on a concrete adapter. */
export class ExecutionService extends Context.Service<ExecutionService, ExecutionServiceShape>()(
  'Querycraft/ExecutionService',
) {}

type NoteFn = (method: string, path: string, status: number, ms: number) => void;

const nowMs = (): number => (typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now());
const round = (n: number): number => Number(n.toFixed(1));
/* The OUTER Evaluation.duration_ms is a string on the contract (the gateway
   emits milliseconds as "8.4"); the QueryResult's own duration_ms is a number.
   This formats the outer one the way the gateway does, so the two adapters agree
   and the TS type does not need a number|string union. */
const secs = (n: number): string => round(n).toFixed(1);
const rid = (): string => 'rq_' + Math.random().toString(36).slice(2, 8);

/* ---------- bearer token storage ----------

   The gateway's auth surface issues one bearer token; it is the only
   credential the client ever sends. It lives under a single localStorage key so
   `httpLayer` can attach it and `me`/`logout` can clear it. Every access is
   guarded: prerender/SSR (no `window`) and disabled storage both degrade to "no
   token", never a crash. */

export const AUTH_TOKEN_KEY = 'querycraft.token';
/* The signed-in account id, kept beside the token. It is what a patch approval
   echoes as `approved_by`; the gateway still attributes the approval to the
   bearer token, so this is a convenience, never an authority. */
export const AUTH_USER_KEY = 'querycraft.user';

export function readAuthToken(): string | null {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return null;
    return window.localStorage.getItem(AUTH_TOKEN_KEY);
  } catch {
    return null;
  }
}

export function writeAuthToken(token: string): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.setItem(AUTH_TOKEN_KEY, token);
  } catch {
    /* storage disabled: the session lives for this page only */
  }
}

export function readAuthUser(): string | null {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return null;
    return window.localStorage.getItem(AUTH_USER_KEY);
  } catch {
    return null;
  }
}

export function writeAuthUser(userId: string): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.setItem(AUTH_USER_KEY, userId);
  } catch {
    /* storage disabled: the session lives for this page only */
  }
}

export function clearAuthToken(): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.removeItem(AUTH_TOKEN_KEY);
    window.localStorage.removeItem(AUTH_USER_KEY);
  } catch {
    /* storage disabled */
  }
}

export const MOCK_CONFIG: ApiConfig = {
  baseUrl: 'mock://api.querycraft.app/v1',
  engine: 'PostgreSQL 16.3',
  /* Canned rows are shaped like PostgreSQL rows: booleans are booleans and
     timestamps are strings of the same form. The mock adapter reports that
     honestly rather than claiming to be dialect-neutral. */
  dialect: 'postgresql',
  latency: true,
  latencyMin: 90,
  latencyMax: 240,
};

function delayMs(config: ApiConfig): number {
  if (!config.latency) return 0;
  const lo = config.latencyMin ?? 90;
  const hi = config.latencyMax ?? 240;
  return lo + Math.random() * (hi - lo);
}

/* ---------- Mock auth: a canned, explicitly local account store ----------

   The mock adapter has no server to authenticate against, so it keeps a small
   account store in localStorage and is honest about being canned: the default
   admin (`admin@querycraft.local`, empty password) is a fixture, not an account
   anyone created. Progress and events in the mock adapter are likewise local,
   so nothing here pretends a server-scoped write succeeded — this layer never
   claims to have talked to the gateway. */

const MOCK_USERS_KEY = 'querycraft.mock.users';
const MOCK_SESSIONS_KEY = 'querycraft.mock.sessions';
const MOCK_SESSION_TTL_MS = 7 * 24 * 60 * 60 * 1000;

interface MockAccount extends User {
  password: string;
}

const MOCK_ADMIN: MockAccount = {
  id: 'usr_admin',
  email: 'admin@querycraft.local',
  name: 'Admin',
  role: 'admin',
  createdAt: '2024-01-01T00:00:00Z',
  password: '',
};

function readMockJSON<T>(key: string, fallback: T): T {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return fallback;
    const raw = window.localStorage.getItem(key);
    return raw == null ? fallback : (JSON.parse(raw) as T);
  } catch {
    return fallback;
  }
}

function writeMockJSON(key: string, value: unknown): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* storage disabled */
  }
}

function mockAccounts(): MockAccount[] {
  const stored = readMockJSON<MockAccount[]>(MOCK_USERS_KEY, []);
  return stored.some((u) => u.email === MOCK_ADMIN.email) ? stored : [MOCK_ADMIN, ...stored];
}

function mockSessionUser(token: string | null): User | null {
  if (!token) return null;
  const sessions = readMockJSON<Record<string, { user: User; expiresAt: string }>>(MOCK_SESSIONS_KEY, {});
  const entry = sessions[token];
  if (!entry) return null;
  if (Date.parse(entry.expiresAt) < Date.now()) return null;
  return entry.user;
}

function publicUser(account: MockAccount): User {
  const { password: _password, ...user } = account;
  return user;
}

function mockAuthMethods(config: ApiConfig, note: NoteFn): Pick<ExecutionServiceShape, 'signup' | 'login' | 'logout' | 'me'> {
  const issue = (account: MockAccount): AuthSession => {
    const token = 'mock_' + Math.random().toString(36).slice(2) + Date.now().toString(36);
    const expiresAt = new Date(Date.now() + MOCK_SESSION_TTL_MS).toISOString();
    const user = publicUser(account);
    const sessions = readMockJSON<Record<string, { user: User; expiresAt: string }>>(MOCK_SESSIONS_KEY, {});
    sessions[token] = { user, expiresAt };
    writeMockJSON(MOCK_SESSIONS_KEY, sessions);
    writeAuthToken(token);
    return { user, token, expiresAt };
  };

  return {
    signup: (input) =>
      Effect.gen(function* () {
        yield* Effect.sleep(delayMs(config));
        const t0 = nowMs();
        const email = input.email.trim().toLowerCase();
        const name = input.name.trim();
        if (!email || !email.includes('@') || !name || input.password.length < 8) {
          note('POST', '/auth/signup', 400, nowMs() - t0);
          return yield* Effect.fail(
            authFailure('AUTH_INVALID_INPUT', 'Enter a valid email, a name, and a password of at least 8 characters.', 400),
          );
        }
        const accounts = mockAccounts();
        if (accounts.some((u) => u.email === email)) {
          note('POST', '/auth/signup', 409, nowMs() - t0);
          return yield* Effect.fail(authFailure('AUTH_EMAIL_TAKEN', 'That email already has an account.', 409));
        }
        const account: MockAccount = {
          id: 'usr_' + Math.random().toString(36).slice(2, 10),
          email,
          name,
          role: 'learner',
          createdAt: new Date().toISOString(),
          password: input.password,
        };
        writeMockJSON(MOCK_USERS_KEY, [...accounts, account]);
        note('POST', '/auth/signup', 201, nowMs() - t0);
        return issue(account);
      }),
    login: (input) =>
      Effect.gen(function* () {
        yield* Effect.sleep(delayMs(config));
        const t0 = nowMs();
        const email = input.email.trim().toLowerCase();
        const account = mockAccounts().find((u) => u.email === email && u.password === input.password);
        if (!account) {
          note('POST', '/auth/login', 401, nowMs() - t0);
          return yield* Effect.fail(authFailure('AUTH_INVALID_CREDENTIALS', 'That email and password do not match.', 401));
        }
        note('POST', '/auth/login', 200, nowMs() - t0);
        return issue(account);
      }),
    logout: () =>
      Effect.gen(function* () {
        yield* Effect.sleep(delayMs(config));
        const t0 = nowMs();
        const token = readAuthToken();
        if (token) {
          const sessions = readMockJSON<Record<string, { user: User; expiresAt: string }>>(MOCK_SESSIONS_KEY, {});
          delete sessions[token];
          writeMockJSON(MOCK_SESSIONS_KEY, sessions);
        }
        clearAuthToken();
        note('POST', '/auth/logout', 200, nowMs() - t0);
      }),
    me: () =>
      Effect.gen(function* () {
        yield* Effect.sleep(delayMs(config));
        const token = readAuthToken();
        note('GET', '/auth/me', token ? 200 : 401, nowMs());
        return mockSessionUser(token);
      }),
  };
}

/* ---------- Mock adapter: the in-browser engine (Adapter A stand-in) ---------- */

export function mockLayer(config: ApiConfig = MOCK_CONFIG, note: NoteFn = () => {}): Layer.Layer<ExecutionService> {
  /* The one refusal the mock gives for the M5 perf fixture.
   *
   * A canned result is the exercise's own authored rows, and the perf fixture's
   * whole subject is that it holds 10 000 orders — which a canned result cannot
   * be. Grading plan SQL against a commerce exercise's authored rows would mark
   * a correct answer wrong, so the mock says what it cannot do instead, with the
   * same code it already uses to refuse a plan. */
  const perfRefusal = (path: string): QueryResult => {
    note('POST', path, 503, nowMs());
    return failedResult(
      new GatewayError({
        code: 'ENVIRONMENT_UNAVAILABLE',
        message: 'The large practice workspace needs a real PostgreSQL engine. This adapter returns canned results and never runs SQL.',
      }),
      config.dialect,
    );
  };

  const service: ExecutionServiceShape = {
    config,
    /* No provider keys or pipeline here: the lab is honestly unavailable. */
    ...browserAiMethods(),
    ...mockAuthMethods(config, note),
    /* Nothing is executed here — the results are canned — so there is no engine
       to put a deadline on and no capability to advertise. */
    /* Canned results: nothing executes, so there is no deadline to enforce and
       no workspace to write to. */
    capabilities: () =>
      Effect.succeed<EngineCapabilities | null>({
        placement: 'none',
        hardTimeout: false,
        timeoutMs: 0,
        writable: false,
        explain: false,
        dialect: 'postgresql',
      }),
    /* The mock adapter's admin is a canned fixture with no password, but there
       is no gateway to advertise it: the sign-in hint belongs to the gateway
       surface, so this reports false and the hint stays hidden. */
    adminPasswordEmpty: () => Effect.succeed(false),
    runQuery: (sql, context) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        if (datasetFor(context) === 'perf') return perfRefusal('/query');
        const out = engineExecute(sql, context?.block);
        const ms = nowMs() - t0;
        note('POST', '/query', out.status === 'succeeded' ? 200 : 400, ms);
        return {
          ...out,
          request_id: rid(),
          engine: config.engine,
          dialect: config.dialect,
          duration_ms: round(ms),
        };
      }),
    checkAnswer: (sql, cell) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        if (datasetFor({ dataset: cell.dataset, course: cell.course, block: cell.item }) === 'perf') {
          return { qr: perfRefusal('/answers/evaluate'), checks: [], passed: false, duration_ms: '0' };
        }
        const out = engineExecute(sql, cell.item);
        const checks = (cell.item?.checks ?? []).map((chk) => {
          let ok = false;
          try {
            ok = !!chk.test(sql);
          } catch {
            ok = false;
          }
          return { label: chk.label, code: chk.code, ok };
        });
        const ms = nowMs() - t0;
        note('POST', '/answers/evaluate', 200, ms);
        return {
          qr: { ...out, request_id: rid(), engine: config.engine, dialect: config.dialect, duration_ms: round(ms) },
          checks,
          passed: out.status === 'succeeded' && checks.every((c) => c.ok),
          duration_ms: secs(ms),
        };
      }),
    getTables: (dataset = DEFAULT_DATASET, dialect: Dialect = DEFAULT_DIALECT) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('GET', `/datasets/${dataset}/tables`, 200, nowMs() - t0);
        /* This adapter runs no engine, so it cannot describe a SQLite workspace:
           the declared types it would have to print are the PostgreSQL ones.
           Listing them under a "SQLite" label was the mismatch the drawer's own
           comment warns about, so a sqlite request returns no tables and the
           drawer says so rather than showing the wrong schema under the right
           name. */
        if (dialect === 'sqlite') return { dataset, version: 1, tables: [], rows: 0 };
        /* `TABLES` is the commerce schema's authored metadata. The perf fixture
           is the same four relations at a larger scale and this adapter has no
           engine to count rows with, so it lists what it can honestly describe
           and no rows — rather than a preview it would have to invent. */
        if (dataset === 'perf') return { dataset, version: 1, tables: [], rows: 0 };
        const rows = TABLES.reduce((a, t) => a + t.rows, 0);
        return { dataset, version: 1, tables: TABLES, rows };
      }),
    /* The mock adapter holds no database at all, so there is no environment to
       report and nothing a reset could restore. Saying so is the honest result;
       pretending to reset would be the one thing the mock must never do. */
    /* The mock adapter has no engine, so it cannot produce a plan and will not
       pretend to. The refusal is the honest `ENVIRONMENT_UNAVAILABLE`, with a
       reason that says what a plan needs, not a fabricated node tree. */
    explain: () =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('POST', '/explain', 503, nowMs() - t0);
        return failedPlanBundle(
          'none',
          'ENVIRONMENT_UNAVAILABLE',
          'A plan needs a real PostgreSQL engine. This adapter returns canned results and never runs SQL.',
        );
      }),
    getEnvironment: () => Effect.succeed<EnvironmentInfo | null>(null),
    /* Canned results come from the bundle, so there is nothing to build. */
    warmUp: () => Effect.void,
    resetEnvironment: () =>
      Effect.fail(
        new GatewayError({
          code: 'ENVIRONMENT_UNAVAILABLE',
          message: 'Canned results have no workspace to reset. This adapter does not run SQL at all.',
        }),
      ),
    ...localMethods(config, note),
  };
  return Layer.succeed(ExecutionService, service);
}

/* ---------- PGlite adapter: a real PostgreSQL running inside the page ---------- */

export const PGLITE_CONFIG: ApiConfig = {
  baseUrl: 'pglite://memory/querycraft',
  engine: PGLITE_ENGINE,
  dialect: 'postgresql',
  latency: false,
};

/* Offline learner data lives in IndexedDB (see ./idb) so the mock and PGlite
   layers share one honest store: a snapshot read back on the next boot, and a
   failed write that surfaces as PERSISTENCE_FAILED instead of a silent
   { ok: true }. Course generation stays in memory; it is a draft, not data. */
type LocalMethods = Pick<ExecutionServiceShape, 'getProgress' | 'saveProgress' | 'logEvent' | 'draftCourse' | 'draftChallenge' | 'getContent'>;

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
        return { title: req.title, chapters: (req.steps ?? []).length, exercises: req.exercises, datasetVersion: 1 };
      }),
    draftChallenge: () =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('POST', '/challenges/generate', 201, nowMs() - t0);
        return { ok: true };
      }),
    /* The mock and gateway adapters serve the same authored markdown; only the
       PGlite adapter reads it back out of the database. */
    getContent: (moduleId) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('GET', `/content/${moduleId}`, 200, nowMs() - t0);
        return BUNDLED_CONTENT[moduleId] ?? [];
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
    /* Overrides the bundled fallback: content comes out of the database. */
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

/* ---------- HTTP adapter: the Go gateway (Adapter B) ---------- */


export function httpLayer(opts: {
  baseUrl: string;
  note?: NoteFn;
  timeoutMs?: number;
  /** Called when the gateway rejects a request with AUTH_REQUIRED (a missing or
      expired token), so the app can clear its session and return to sign-in. */
  onUnauthorized?: () => void;
}): Layer.Layer<ExecutionService> {
  const root = (opts.baseUrl || 'http://localhost:8080').replace(/\/$/, '');
  const note = opts.note ?? (() => {});
  const timeoutMs = opts.timeoutMs ?? 15000;
  const unauthorized = opts.onUnauthorized ?? (() => {});
  const config: ApiConfig = { baseUrl: root, engine: 'PostgreSQL (server)', dialect: 'postgresql', latency: true };

  /* rawErrorBodies: /query answers a failure with a 400 whose *body* is the
     QueryResult the UI renders (columns, error.detail, …), so that one call
     wants the parsed body rather than a thrown error. Every other call treats a
     4xx/5xx envelope as a typed failure, which is what lets signup surface
     AUTH_EMAIL_TAKEN instead of dying later in a schema decode. */
  /* perform: one round trip that keeps the status, so a caller which must read
     a non-2xx body (the AI outcome calls) has a path without duplicating the
     fetch, token, retry and timeout plumbing. */
  /* The advertised run budget, learned from the first /capabilities answer.
     The app reads capabilities before it offers generation, so this is warm by
     the time a course is asked for; the fallback covers a caller that asks
     without probing first. */
  let aiRunBudgetMs: number | null = null;

  const perform = (
    method: string,
    path: string,
    body?: unknown,
    opts?: AiPerformOptions,
  ): Effect.Effect<AiPerformResult, GatewayError> =>
    Effect.gen(function* () {
      const t0 = nowMs();
      const headers: Record<string, string> = {};
      if (body !== undefined) headers['content-type'] = 'application/json';
      /* Attach the bearer token when one is stored. Progress and events are
         owner-scoped by the token server-side, so no user id is ever sent. */
      const token = readAuthToken();
      if (token) headers['authorization'] = `Bearer ${token}`;
      const res = yield* Effect.tryPromise({
        try: (signal) =>
          fetch(root + path, {
            method,
            headers: Object.keys(headers).length ? headers : undefined,
            body: body !== undefined ? JSON.stringify(body) : undefined,
            signal,
          }),
        catch: (cause) =>
          new GatewayError({
            code: 'ENVIRONMENT_UNAVAILABLE',
            message: 'Could not reach the execution gateway.',
            detail: cause instanceof Error ? cause.message : String(cause),
          }),
      }).pipe(
        /* Retry only idempotent reads. This path serves every http call, so a
           blanket retry also re-sent PUT /progress/:course, POST /events and
           POST /answers/evaluate on a transport failure — if the first write
           landed but its response was lost, the retry applied it twice. A GET
           is safe to repeat; a write is not. Callers can still override with
           opts.retries (the AI long calls set 0, which is preserved). */
        Effect.retry(Schedule.recurs(opts?.retries ?? (method === 'GET' ? 2 : 0))),
        Effect.timeout(opts?.timeoutMs ?? timeoutMs),
        /* A deadline that expires is a transport failure like any other; keep
           the error channel to the one typed error the callers branch on. */
        Effect.mapError((e) =>
          e instanceof GatewayError
            ? e
            : new GatewayError({
                code: 'ENVIRONMENT_UNAVAILABLE',
                message: 'The execution gateway did not answer in time.',
                detail: String(e),
              }),
        ),
      );

      const ms = nowMs() - t0;
      note(method, path, res.status, ms);

      const text = yield* Effect.tryPromise({
        try: () => res.text(),
        catch: (cause) =>
          new GatewayError({
            code: 'ENVIRONMENT_UNAVAILABLE',
            message: 'Gateway response could not be read.',
            detail: cause instanceof Error ? cause.message : String(cause),
          }),
      });
      if (!text) return { status: res.status, body: null };
      let parsed: unknown = null;
      try {
        parsed = JSON.parse(text) as unknown;
      } catch {
        parsed = null;
      }
      /* Every capabilities answer refreshes the AI run budget, whichever
         caller asked for it. */
      if (path === '/capabilities') {
        const advertised = advertisedRunBudgetMs(parsed);
        if (advertised !== null) aiRunBudgetMs = advertised;
      }
      return { status: res.status, body: parsed };
    });

  const request = (method: string, path: string, body?: unknown, opts?: { rawErrorBodies?: boolean }) =>
    Effect.gen(function* () {
      const { status, body: parsed } = yield* perform(method, path, body);
      /* A 401 means the token is absent, expired or invalid. AUTH_REQUIRED is
         the session-invalid signal: clear the stored token and let the app
         bounce to sign-in. Login's own 401 carries AUTH_INVALID_CREDENTIALS and
         must reach the form as a plain error, so it is deliberately not wired
         to the session reset. */
      const envelopeCode = errorCode(parsed);
      if (status === 401) {
        const code = envelopeCode ?? 'AUTH_REQUIRED';
        if (code === 'AUTH_REQUIRED') {
          clearAuthToken();
          unauthorized();
        }
        return yield* Effect.fail(authFailure(code, errorMessage(parsed) ?? 'Authentication required.', 401));
      }
      if (status >= 400 && !opts?.rawErrorBodies) {
        return yield* Effect.fail(
          authFailure(
            envelopeCode ?? 'ENVIRONMENT_UNAVAILABLE',
            errorMessage(parsed) ?? `The gateway rejected the request (HTTP ${status}).`,
            status,
          ),
        );
      }
      return parsed;
    });

  const service: ExecutionServiceShape = {
    config,
    /* AI operations (design §11). Generation always resolves to an outcome the
       screen can render; the non-outcome calls keep the typed error channel.
       A 401 here is the same session-gone signal as elsewhere. */
    ...gatewayAiMethods({
      perform,
      onUnauthorized: () => {
        clearAuthToken();
        unauthorized();
      },
      providerDeadlineMs: () => providerDeadlineMs(aiRunBudgetMs),
    }),
    /* The gateway enforces its own deadline server-side (QC_TIMEOUT_MS) and
       advertises it on /capabilities. A failed probe reports "unknown" rather
       than claiming a capability that could not be confirmed. */
    capabilities: () =>
      request('GET', '/capabilities').pipe(
        Effect.map((json): EngineCapabilities | null => {
          const body = (json ?? {}) as {
            hard_timeout?: unknown;
            explain?: unknown;
            plan?: unknown;
            writable?: unknown;
            dialects?: unknown;
          };
          const hardTimeout = typeof body.hard_timeout === 'boolean' ? body.hard_timeout : true;
          /* The gateway advertises plan support as `explain` (the documented
             capability surface) or as a `plan` block; a gateway that advertises
             neither cannot plan, and the UI disables the action rather than
             asking for a plan and guessing at the refusal. */
          const explain =
            body.explain === true ||
            body.plan === true ||
            (typeof body.plan === 'object' && body.plan !== null && (body.plan as { supported?: unknown }).supported === true);
          /* Writes need an owned environment, which the gateway gives a
             signed-in learner; an anonymous visitor keeps the shared read-only
             dataset (ADR 0003). Two facts have to hold, and the gateway can
             only answer one of them: it says whether it *has* a workspace
             manager, and this client says whether it *has* a session. Asking
             only the second one was a live bug — the answer was computed once
             at boot, before sign-in, and a signed-in learner was told to sign
             in. */
          const gatewayCanWrite = body.writable !== false;
          const writable = gatewayCanWrite && readAuthToken() !== null;
          const dialects = Array.isArray(body.dialects)
            ? body.dialects.filter((d): d is Dialect => d === 'postgresql' || d === 'sqlite')
            : [];
          return {
            placement: 'server',
            hardTimeout,
            timeoutMs: 5000,
            writable,
            explain,
            dialect: 'postgresql',
            ...(dialects.length > 0 ? { dialects } : {}),
          };
        }),
        Effect.catch(() => Effect.succeed<EngineCapabilities | null>(null)),
      ),
    /* GET /health answers whether the seeded administrator can sign in with an
       empty password. It is read on the sign-in screen, where there is no
       session, so it must never reject and must default to the safe answer:
       an unreadable probe reports false and the hint stays hidden. Only
       `true` shows the hint, and the gateway sends `true` only on a
       loopback-only install with a genuinely empty admin password. */
    adminPasswordEmpty: () =>
      request('GET', '/health').pipe(
        Effect.map((json) => (json as { admin_password_empty?: unknown } | null)?.admin_password_empty === true),
        Effect.catch(() => Effect.succeed(false)),
      ),
    signup: (input) =>
      request('POST', '/auth/signup', input).pipe(
        Effect.flatMap((json) => decodeAuthSession(json)),
        Effect.tap((session) =>
          Effect.sync(() => {
            writeAuthToken(session.token);
            writeAuthUser(session.user.id);
          }),
        ),
        Effect.mapError(asGatewayError),
      ),
    login: (input) =>
      request('POST', '/auth/login', input).pipe(
        Effect.flatMap((json) => decodeAuthSession(json)),
        Effect.tap((session) =>
          Effect.sync(() => {
            writeAuthToken(session.token);
            writeAuthUser(session.user.id);
          }),
        ),
        Effect.mapError(asGatewayError),
      ),
    logout: () =>
      Effect.gen(function* () {
        yield* request('POST', '/auth/logout').pipe(Effect.catch(() => Effect.void));
        clearAuthToken();
      }),
    me: () => {
      /* No token to validate: report "signed out" without a round trip. */
      if (!readAuthToken()) return Effect.succeed(null);
      return request('GET', '/auth/me').pipe(
        Effect.flatMap((json) => decodeUser(json).pipe(Effect.mapError(asGatewayError))),
        Effect.tap((user) => Effect.sync(() => writeAuthUser(user.id))),
        Effect.map((user): User | null => user),
        Effect.catch((e) => {
          const err = asGatewayError(e);
          if (err.code === 'AUTH_REQUIRED' || err.status === 401) {
            clearAuthToken();
            return Effect.succeed(null);
          }
          return Effect.fail(err);
        }),
      );
    },
    runQuery: (sql, context) =>
      request(
        'POST',
        '/query',
        { sql, limits: {}, dataset: datasetFor(context), dialect: dialectFor(context) },
        { rawErrorBodies: true },
      ).pipe(
        Effect.flatMap((json) => decodeQueryResult(json)),
        Effect.catch((e) => Effect.succeed(failedResult(e))),
      ),
    /* M5. POST /explain returns the PlanBundle itself, including a failed one
       (the plan error envelope rides inside the bundle). A transport failure or
       an unreadable body becomes a failed bundle rather than a thrown error, so
       the seam has one shape for "here is the plan" and "here is why there is
       none". The request-id and the API-log `note` come from the shared
       `request`/`perform` pair, exactly like /query. */
    explain: (req) =>
      request('POST', '/explain', {
        sql: req.sql,
        analyze: req.analyze === true,
        /* Resolved here for the same reason the browser adapter resolves it:
           `exercise.perf` is the exercise's own statement of scale, so a plan
           request reaches the same database on the two adapters even when the
           screen never learned the word "dataset". */
        dataset: datasetFor({ dataset: req.dataset, exercise: req.exercise }),
        exercise: req.exercise,
      }, { rawErrorBodies: true }).pipe(
        Effect.map((json) => {
          try {
            return validatePlanBundle(json);
          } catch (e) {
            /* Not a bundle: the gateway reported an envelope (or nothing). Keep
               its code and message when it gave one; otherwise say the body was
               unreadable rather than inventing a plan. */
            return failedPlanBundle(
              'postgresql',
              errorCode(json) ?? 'EVALUATION_ERROR',
              errorMessage(json) ?? 'The gateway returned a plan body this client could not read.',
              e instanceof Error ? e.message : String(e),
            );
          }
        }),
        Effect.catch((e) => {
          const err = asGatewayError(e);
          return Effect.succeed(failedPlanBundle('postgresql', err.code, err.message, err.detail ?? null));
        }),
      ),
    checkAnswer: (sql, cell) =>
      request('POST', '/answers/evaluate', {
        sql,
        dataset: datasetFor({ dataset: cell.dataset, course: cell.course, block: cell.item }),
        /* The dialect travels with the submission for the same reason the
           dataset does: the gateway routes a statement to the engine that
           speaks it, and a chapter's dialect is what decides. */
        dialect: dialectFor({ dialect: cell.dialect, course: cell.course, block: cell.item, module: cell.module }),
        exercise: {
          id: cell.item?.id,
          version: 1,
          lesson_id: cell.module?.id,
          contract: cell.item?.contract ?? { columns: [], column_order: 'exact', row_order: 'ignored', duplicates: 'preserved', numeric_tolerance: 0 },
          /* The gateway grades by executing this reference against the same
             engine and comparing rows. Without it the server can only check
             column names, which made every answer pass: a query filtered to the
             wrong category came back "passed" with all checks green. The
             bundled `result` rows are deliberately not sent — they are stale
             relative to the seeded dataset, and the live reference is the only
             honest oracle. */
          reference_solution: cell.item?.solution ?? '',
        },
      }).pipe(
        Effect.flatMap((json) => decodeEvaluation(json)),
        Effect.catch((e) =>
          Effect.succeed({
            qr: failedResult(e),
            checks: [],
            passed: false,
            duration_ms: '0',
          }),
        ),
      ),
    getTables: (dataset = DEFAULT_DATASET, dialect: Dialect = DEFAULT_DIALECT) =>
      /* The dialect travels with the request: a SQLite chapter's drawer must
         list the SQLite relations (declared types are affinities, not the same
         claim as PostgreSQL's). The gateway serves ?dialect=sqlite over the
         workspace the caller is in. */
      request('GET', `/datasets/${dataset}/tables?dialect=${encodeURIComponent(dialect)}`).pipe(
        Effect.map((json) => json as TablesResponse),
        Effect.catch(() => Effect.succeed({ dataset, version: 1, tables: [], rows: 0 })),
      ),
    /* ADR 0003. `GET /environment` describes the caller's workspace; an
       anonymous caller has none, so it answers null rather than an error. A 401
       is still handled by the shared request helper, which drops the token. */
    /* The gateway owns the workspace and its templates are sealed ahead of
       time, so there is nothing for the page to pre-build. Reporting a warm-up
       it did not do would be the one thing this seam must not do. */
    warmUp: () => Effect.void,
    getEnvironment: () =>
      request('GET', '/environment').pipe(
        Effect.map(environmentFrom),
        Effect.catch(() => Effect.succeed<EnvironmentInfo | null>(null)),
      ),
    resetEnvironment: (dataset = DEFAULT_DATASET, dialect: Dialect = DEFAULT_DIALECT, course: Course | null = null) =>
      /* The dataset, dialect and course are query parameters rather than body
         fields because the route's body is unused and all three are workspace
         selectors, not statements. Absent is the challenge workspace, which is
         what every caller meant before M5; a named course tells the gateway to
         re-apply that course's own setup after the template is copied. The
         dialect is what makes the reset honest on the second dialect: a SQLite
         chapter's workspace is a file, and resetting PostgreSQL under it would
         answer `ok` for a restore that did not happen. */
      request('POST', environmentResetPath(dataset, dialect, course)).pipe(
        Effect.map(asEnvironmentReset),
        Effect.mapError(asGatewayError),
      ),
    /* The gateway does not serve chapter content yet, so use the authored
       markdown that ships with the client. */
    getContent: (moduleId) => Effect.succeed(BUNDLED_CONTENT[moduleId] ?? []),
    getProgress: (courseId) =>
      request('GET', `/progress/${courseId ?? 'selected'}`).pipe(
        Effect.map((json) => {
          const j = (json ?? {}) as { snapshot?: unknown };
          return { ok: true, snapshot: j.snapshot };
        }),
        Effect.catch((e) => Effect.succeed({ ok: false, snapshot: undefined, ...errorMeta(e) })),
      ),
    saveProgress: (snapshot, courseId) =>
      request('PUT', `/progress/${courseId ?? 'selected'}`, snapshot).pipe(
        Effect.map(() => ({ ok: true })),
        Effect.catch((e) => Effect.succeed({ ok: false, ...errorMeta(e) })),
      ),
    logEvent: (evt) =>
      request('POST', '/events', evt).pipe(
        Effect.map((json) => ({ ok: true, id: (json as { id?: string } | null)?.id })),
        Effect.catch((e) => Effect.succeed({ ok: false, ...errorMeta(e) })),
      ),
    /* A draft is a client-side authoring record, not a server operation.
       `generateCourse`/`generateChallenge` (the AI seam) are what talk to the
       gateway; sending a draft here would run the real §11.1 pipeline as a side
       effect of the offline evidence run and leave an artifact behind that the
       learner never asked for. So the gateway adapter drafts locally, exactly
       like the offline ones. */
    draftCourse: (req) =>
      Effect.sync(() => ({
        title: req.title,
        chapters: (req.steps ?? []).length,
        exercises: req.exercises,
        datasetVersion: 1,
      })),
    draftChallenge: () => Effect.succeed({ ok: true }),
  };
  return Layer.succeed(ExecutionService, service);
}

/* ---------- helpers ---------- */

function errorMeta(e: unknown): { error?: { code: string; message: string } } {
  if (e instanceof GatewayError) return { error: { code: e.code, message: e.message } };
  return { error: { code: 'ENVIRONMENT_UNAVAILABLE', message: 'Gateway unavailable.' } };
}

/* The gateway reports failures as { ok:false, error:{ code, message } }. These
   pull the stable code/message out of an already-parsed body, tolerating any
   shape, so a transport-level 401 can be classified without guessing. */
function errorCode(body: unknown): string | null {
  const err = (body as { error?: { code?: unknown } } | null)?.error;
  return typeof err?.code === 'string' ? err.code : null;
}

function errorMessage(body: unknown): string | null {
  const err = (body as { error?: { message?: unknown } } | null)?.error;
  return typeof err?.message === 'string' ? err.message : null;
}

/* The environment endpoints answer `{ ok, environment }` (reset adds
   `duration_ms`). There is no contracts/*.schema.json for them yet, so the
   parse is deliberately tolerant: an unknown shape reads as "no environment"
   rather than throwing inside a reset the learner has already confirmed.
   Errors never reach here — a 4xx fails earlier in `request` with the
   gateway's own code and message, which the UI shows verbatim. */
function asEnvironment(value: unknown): EnvironmentInfo | null {
  if (typeof value !== 'object' || value === null) return null;
  const e = value as Record<string, unknown>;
  const str = (k: string): string | undefined => (typeof e[k] === 'string' ? (e[k] as string) : undefined);
  const num = (k: string): number | undefined => (typeof e[k] === 'number' ? (e[k] as number) : undefined);
  return {
    account: str('account'),
    database: str('database'),
    exists: e.exists === true,
    writable: e.writable === true,
    tables: num('tables'),
    created_at: str('created_at'),
    reset_at: str('reset_at'),
    idle_until: str('idle_until'),
  };
}

function environmentFrom(json: unknown): EnvironmentInfo | null {
  return asEnvironment((json as { environment?: unknown } | null)?.environment);
}

function asEnvironmentReset(json: unknown): EnvironmentReset {
  const body = (json ?? {}) as { ok?: unknown; duration_ms?: unknown; setup_statements?: unknown; environment?: unknown };
  return {
    ok: body.ok === true,
    duration_ms: typeof body.duration_ms === 'number' ? body.duration_ms : 0,
    /* A gateway that answered without the count applied none; reporting the
       missing field as a number the adapter never saw would be a guess. */
    setup_statements: typeof body.setup_statements === 'number' ? body.setup_statements : 0,
    environment: asEnvironment(body.environment) ?? undefined,
  };
}

/** The reset route's path, with the workspace selectors that name it. */
function environmentResetPath(dataset: DatasetId, dialect: Dialect = DEFAULT_DIALECT, course?: Course | null): string {
  const params = new URLSearchParams();
  if (dataset !== DEFAULT_DATASET) params.set('dataset', dataset);
  /* The dialect is always sent, including the default: an omitted dialect means
     PostgreSQL by omission, but sending it explicitly keeps the request honest
     about which workspace it means even if the default ever changes. */
  params.set('dialect', dialect);
  if (course?.id) params.set('course', course.id);
  const query = params.toString();
  return query ? `/environment/reset?${query}` : '/environment/reset';
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

function failedResult(e: unknown, dialect: Dialect = DEFAULT_DIALECT): QueryResult {
  const code = e instanceof GatewayError ? e.code : 'ENVIRONMENT_UNAVAILABLE';
  const message = e instanceof GatewayError ? e.message : 'The execution environment is unavailable.';
  const detail = e instanceof GatewayError ? e.detail ?? null : String(e);
  return {
    status: 'failed',
    columns: [],
    rows: [],
    row_count: 0,
    truncated: false,
    duration_ms: 0,
    error: { code, message, detail },
    dialect,
  };
}

/* ---------- Promise-shaped facade used by React ---------- */

export function createExecutionApi(opts: {
  mode: 'mock' | 'pglite' | 'http';
  baseUrl?: string;
  /** Live engine string reported by the gateway (e.g. "PostgreSQL 18.3"). */
  engine?: string | null;
  onLog?: (log: ApiLogEntry[], count: number) => void;
  /** Fired when the gateway reports AUTH_REQUIRED, so the app can drop its
      session and return to sign-in. Ignored by the offline adapters. */
  onUnauthorized?: () => void;
}): ExecutionApi {
  const log: ApiLogEntry[] = [];
  let count = 0;
  const note: NoteFn = (method, path, status, ms) => {
    count++;
    log.unshift({ method, path, status, ms: Math.round(ms), at: Date.now() });
    if (log.length > 20) log.pop();
    opts.onLog?.(log, count);
  };

  const config: ApiConfig =
    opts.mode === 'http'
      ? {
          baseUrl: (opts.baseUrl || 'http://localhost:8080').replace(/\/$/, ''),
          engine: opts.engine?.trim() || 'PostgreSQL (server)',
          dialect: 'postgresql',
          latency: true,
        }
      : opts.mode === 'pglite'
        ? PGLITE_CONFIG
        : MOCK_CONFIG;
  const layer =
    opts.mode === 'http'
      ? httpLayer({ baseUrl: config.baseUrl, note, onUnauthorized: opts.onUnauthorized })
      : opts.mode === 'pglite'
        ? pgliteLayer(config, note)
        : mockLayer(config, note);

  /* The first query against a workspace pays for the WASM download, initdb and
     the seed, so start the default one now. `plans` starts its own when the
     course opens (see the store), because warming the 10 000-row fixture for a
     learner who is on `foundations` would be pure waste. */
  if (opts.mode === 'pglite') warmUpDataset();

  const run = <A>(effect: Effect.Effect<A, unknown, ExecutionService>): Promise<A> =>
    Effect.runPromise(Effect.provide(effect, layer));
  const call = <A>(f: (svc: ExecutionServiceShape) => Effect.Effect<A, unknown, never>): Promise<A> =>
    run(
      Effect.gen(function* () {
        const svc = yield* ExecutionService;
        return yield* f(svc);
      }),
    );

  /* Availability is read once per signed-in session — the token identifies the
     session — and remembered, the way the store already caches engine
     capabilities, so a screen can render its controls on the first frame
     instead of flashing "unavailable". `aiAvailability()` resolves, never
     rejects: an unreadable probe is an unavailable answer. */
  let availabilityKey: string | null = null;
  let availabilityPromise: Promise<AiAvailability> | null = null;
  const aiAvailability = (): Promise<AiAvailability> => {
    const key = readAuthToken() ?? '';
    if (availabilityPromise && availabilityKey === key) return availabilityPromise;
    availabilityKey = key;
    availabilityPromise = call((svc) => svc.aiAvailability()).catch(() => unavailableAvailability());
    return availabilityPromise;
  };

  return {
    mode: opts.mode,
    baseUrl: config.baseUrl,
    config,
    log,
    get count() {
      return count;
    },

    signup: (input) => call((svc) => svc.signup(input)),
    login: (input) => call((svc) => svc.login(input)),
    logout: () => call((svc) => svc.logout()),
    me: () => call((svc) => svc.me()),

    capabilities: () => call((svc) => svc.capabilities()),
    adminPasswordEmpty: () => call((svc) => svc.adminPasswordEmpty()),

    runQuery: (sql, context) => call((svc) => svc.runQuery(sql, context)),
    checkAnswer: (sql, cell) => call((svc) => svc.checkAnswer(sql, cell)),
    explain: (req) => call((svc) => svc.explain(req)),
    getTables: (dataset, dialect) => call((svc) => svc.getTables(dataset, dialect)),
    getEnvironment: () => call((svc) => svc.getEnvironment()),
    resetEnvironment: (dataset, dialect, course) => call((svc) => svc.resetEnvironment(dataset, dialect, course)),
    warmUp(dataset, dialect) {
      /* Fire and forget: nothing waits on a warm-up, and a rejection is already
         reported by the first real query. */
      void call((svc) => svc.warmUp(dataset, dialect)).catch(() => undefined);
    },
    getContent: (moduleId) => call((svc) => svc.getContent(moduleId)),
    getProgress: (courseId) => call((svc) => svc.getProgress(courseId)),
    saveProgress: (snapshot, courseId) => call((svc) => svc.saveProgress(snapshot, courseId)),
    logEvent: (evt) => call((svc) => svc.logEvent(evt)),

    saveDraft(cellId, sql) {
      /* The localStorage mirror keeps the draft and the synchronous export path
         working even when IndexedDB is blocked; the `drafts` store is the 8.1
         copy, so a future import/export no longer has to scrape localStorage. */
      try {
        localStorage.setItem('qc.draft.' + cellId, sql);
      } catch {
        /* storage disabled */
      }
      return idbPut(STORE_DRAFTS, cellId, sql)
        .then(() => ({ ok: true }))
        .catch(() => ({ ok: false }));
    },
    getDraft(cellId) {
      const fromLocal = (): string | null => {
        try {
          return localStorage.getItem('qc.draft.' + cellId);
        } catch {
          return null;
        }
      };
      return idbGet<string>(STORE_DRAFTS, cellId)
        .then((stored) => (stored != null ? stored : fromLocal()))
        .catch(fromLocal);
    },

    /* Raw draft calls. The screens pace their own progress and then ask the API
       to record the artifact, so there is no client-side cadence hidden in the
       data layer for a caller to mistake for real work. */
    draftCourse: (req) => call((svc) => svc.draftCourse(req)),
    draftChallenge: (req) => call((svc) => svc.draftChallenge(req)),

    /* AI operations (design §11). Generation resolves to an outcome for every
       adapter; the rest keep the typed error channel. */
    aiAvailability,
    generateChallenge: (req) => call((svc) => svc.generateChallenge(req)),
    generateCourse: (req) => call((svc) => svc.generateCourse(req)),
    listChallenges: () => call((svc) => svc.listChallenges()),
    getChallenge: (id) => call((svc) => svc.getChallenge(id)),
    resetChallenge: (id) => call((svc) => svc.resetChallenge(id)),
    listCourses: () => call((svc) => svc.listCourses()),
    getCourse: (id) => call((svc) => svc.getCourse(id)),
    deleteCourse: (id) => call((svc) => svc.deleteCourse(id)),
    approveCourse: (id) => call((svc) => svc.approveCourse(id)),
    submitGeneration: (req, kind) => call((svc) => svc.submitGeneration(req, kind)),
    generationJob: (id) => call((svc) => svc.generationJob(id)),
    tutor: (req) => call((svc) => svc.tutor(req)),
    critique: (challengeId, req) => call((svc) => svc.critique(challengeId, req)),
    reportDefect: (req) => call((svc) => svc.reportDefect(req)),
    decidePatch: (patchId, approved) => call((svc) => svc.decidePatch(patchId, approved)),
  };
}
