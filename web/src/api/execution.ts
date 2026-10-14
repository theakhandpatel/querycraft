import { Context, Data, Effect, Layer, Schedule } from 'effect';
import { decodeAuthSession, decodeEvaluation, decodeQueryResult, decodeUser } from './contract';
import { engineExecute } from './engine';
import { checksFromComparison, compareColumns, compareResult } from './compare';
import type { Comparison, ExpectedResult } from './compare';
import {
  PGLITE_ENGINE,
  engineCapabilities,
  executeSql,
  fetchModuleContent,
  fetchTables,
  resetEnvironment as resetPgliteEnvironment,
  warmUpDatabase,
} from './pglite';
import { STORE_ATTEMPTS, STORE_DRAFTS, STORE_PROGRESS, idbGet, idbPut, idbPutMany } from './idb';
import { bundleFromProgress } from '../lib/offlineBundle';
import { attemptKey } from '../lib/progress';
import { EXPORT_FORMAT_VERSION } from '../lib/exportBundle';
import { datasetVersion } from './dataset';
import { BUNDLED_CONTENT } from './content';
import { TABLES } from '../data/curriculum';
import type {
  ApiConfig,
  ApiLogEntry,
  AuthSession,
  Block,
  Cell,
  ContentSection,
  CourseGeneration,
  Evaluation,
  EngineCapabilities,
  EnvironmentInfo,
  EnvironmentReset,
  ExecutionApi,
  GenerationRequest,
  LoginInput,
  Progress,
  ProgressEvent,
  QueryResult,
  SignupInput,
  TablesResponse,
  User,
} from '../types';

/* The typed failure every adapter can raise. It carries a stable contract code
   (see contracts/query-result.schema.json) so callers can branch on meaning
   rather than on transport mechanics. */
export class GatewayError extends Data.TaggedError('GatewayError')<{
  readonly code: string;
  readonly message: string;
  readonly detail?: string | null;
  readonly status?: number;
}> {}

/** The capability surface, expressed as Effects with a typed error channel. */
export interface ExecutionServiceShape {
  readonly config: ApiConfig;
  /* Accounts. `me` resolves to null when there is no valid session (including a
     401 from the gateway, which also clears the stored token) so boot restore
     never has to treat "signed out" as an error. */
  signup(input: SignupInput): Effect.Effect<AuthSession, GatewayError>;
  login(input: LoginInput): Effect.Effect<AuthSession, GatewayError>;
  logout(): Effect.Effect<void, GatewayError>;
  me(): Effect.Effect<User | null, GatewayError>;
  runQuery(sql: string, expect?: Block): Effect.Effect<QueryResult, GatewayError>;
  checkAnswer(sql: string, cell: Cell): Effect.Effect<Evaluation, GatewayError>;
  getTables(): Effect.Effect<TablesResponse, GatewayError>;
  /* ADR 0003: the learner's writes live in a workspace that can be restored.
     `getEnvironment()` returns null when the adapter has no such workspace. */
  getEnvironment(): Effect.Effect<EnvironmentInfo | null, GatewayError>;
  resetEnvironment(): Effect.Effect<EnvironmentReset, GatewayError>;
  getProgress(courseId?: string): Effect.Effect<{ ok: boolean; snapshot?: unknown }, GatewayError>;
  saveProgress(snapshot: Progress, courseId?: string): Effect.Effect<{ ok: boolean }, GatewayError>;
  logEvent(evt: ProgressEvent): Effect.Effect<{ ok: boolean; id?: string }, GatewayError>;
  generateCourse(req: GenerationRequest): Effect.Effect<CourseGeneration, GatewayError>;
  generateChallenge(req: GenerationRequest): Effect.Effect<{ ok: boolean }, GatewayError>;
  getContent(moduleId: string): Effect.Effect<ContentSection[], GatewayError>;
  /** Never fails: a missing capability is reported as null, not as an error. */
  capabilities(): Effect.Effect<EngineCapabilities | null>;
}

/** Service key: screens and tests depend on this, never on a concrete adapter. */
export class ExecutionService extends Context.Service<ExecutionService, ExecutionServiceShape>()(
  'Querycraft/ExecutionService',
) {}

type NoteFn = (method: string, path: string, status: number, ms: number) => void;

const nowMs = (): number => (typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now());
const round = (n: number): number => Number(n.toFixed(1));
const rid = (): string => 'rq_' + Math.random().toString(36).slice(2, 8);

/* ---------- bearer token storage ----------

   The gateway's auth surface issues one bearer token; it is the only
   credential the client ever sends. It lives under a single localStorage key so
   `httpLayer` can attach it and `me`/`logout` can clear it. Every access is
   guarded: prerender/SSR (no `window`) and disabled storage both degrade to "no
   token", never a crash. */

export const AUTH_TOKEN_KEY = 'querycraft.token';

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

export function clearAuthToken(): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.removeItem(AUTH_TOKEN_KEY);
  } catch {
    /* storage disabled */
  }
}

/** A failed auth call, normalised to the typed error channel. */
function asGatewayError(e: unknown): GatewayError {
  if (e instanceof GatewayError) return e;
  return new GatewayError({
    code: 'ENVIRONMENT_UNAVAILABLE',
    message: e instanceof Error ? e.message : 'The gateway response could not be read.',
  });
}

function authFailure(code: string, message: string, status?: number): GatewayError {
  return new GatewayError({ code, message, status });
}

export const MOCK_CONFIG: ApiConfig = {
  baseUrl: 'mock://api.querycraft.app/v1',
  engine: 'PostgreSQL 16.3',
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
  const service: ExecutionServiceShape = {
    config,
    ...mockAuthMethods(config, note),
    /* Nothing is executed here — the results are canned — so there is no engine
       to put a deadline on and no capability to advertise. */
    /* Canned results: nothing executes, so there is no deadline to enforce and
       no workspace to write to. */
    capabilities: () =>
      Effect.succeed<EngineCapabilities | null>({ placement: 'none', hardTimeout: false, timeoutMs: 0, writable: false }),
    runQuery: (sql, expect) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        const out = engineExecute(sql, expect);
        const ms = nowMs() - t0;
        note('POST', '/query', out.status === 'succeeded' ? 200 : 400, ms);
        return {
          ...out,
          request_id: rid(),
          engine: config.engine,
          duration_ms: round(ms),
        };
      }),
    checkAnswer: (sql, cell) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
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
          qr: { ...out, request_id: rid(), engine: config.engine, duration_ms: round(ms) },
          checks,
          passed: out.status === 'succeeded' && checks.every((c) => c.ok),
          duration_ms: round(ms),
        };
      }),
    getTables: () =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('GET', '/datasets/commerce/tables', 200, nowMs() - t0);
        const rows = TABLES.reduce((a, t) => a + t.rows, 0);
        return { dataset: 'commerce', version: 1, tables: TABLES, rows };
      }),
    /* The mock adapter holds no database at all, so there is no environment to
       report and nothing a reset could restore. Saying so is the honest result;
       pretending to reset would be the one thing the mock must never do. */
    getEnvironment: () => Effect.succeed<EnvironmentInfo | null>(null),
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
  latency: false,
};

/* Offline learner data lives in IndexedDB (see ./idb) so the mock and PGlite
   layers share one honest store: a snapshot read back on the next boot, and a
   failed write that surfaces as PERSISTENCE_UNAVAILABLE instead of a silent
   { ok: true }. Course generation stays in memory; it is a draft, not data. */
type LocalMethods = Pick<ExecutionServiceShape, 'getProgress' | 'saveProgress' | 'logEvent' | 'generateCourse' | 'generateChallenge' | 'getContent'>;

const READ_FAILURE = 'Could not read your saved progress in this browser (storage unavailable).';
const SAVE_FAILURE = 'Could not save your progress in this browser (storage unavailable or full).';

/** Offline storage failed: carry the contract code so callers can branch on it. */
function persistenceFailure(message: string, cause: unknown): GatewayError {
  return new GatewayError({
    code: 'PERSISTENCE_UNAVAILABLE',
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
    generateCourse: (req) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        yield* Effect.sleep(delayMs(config));
        note('POST', '/courses/generate', 201, nowMs() - t0);
        return { title: req.title, chapters: (req.steps ?? []).length, exercises: req.exercises, datasetVersion: 1 };
      }),
    generateChallenge: () =>
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

export function pgliteLayer(config: ApiConfig = PGLITE_CONFIG, note: NoteFn = () => {}): Layer.Layer<ExecutionService> {
  /* Reference solutions are executed once per exercise and reused. */
  const referenceCache = new Map<string, ExpectedResult | null>();

  const service: ExecutionServiceShape = {
    config,
    ...offlineAuthMethods(),
    /* Reported by the engine itself: 'worker' keeps the deadline real, 'main'
       is the fallback a strict page policy forces (design §5.2). */
    capabilities: () => Effect.sync(() => engineCapabilities()),
    runQuery: (sql) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const out = yield* Effect.tryPromise({ try: () => executeSql(sql), catch: engineFailure });
        const ms = nowMs() - t0;
        note('POST', '/query', out.status === 'succeeded' ? 200 : 400, ms);
        return { ...out, request_id: rid(), engine: config.engine, duration_ms: round(ms) };
      }),
    checkAnswer: (sql, cell) =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const block = cell.item;
        const out = yield* Effect.tryPromise({ try: () => executeSql(sql), catch: engineFailure });
        const ms = nowMs() - t0;
        note('POST', '/answers/evaluate', out.status === 'succeeded' ? 200 : 400, ms);
        const qr = { ...out, request_id: rid(), engine: config.engine, duration_ms: round(ms) };

        if (out.status !== 'succeeded') {
          return { qr, checks: [], passed: false, duration_ms: round(ms) };
        }

        const actual = { columns: out.columns.map((c) => c.name), rows: out.rows };
        const reference = String(block?.solution ?? '').trim();
        const cacheKey = `${block?.id ?? 'anon'}::${reference}`;

        let comparison: Comparison;
        let expected: ExpectedResult | null = null;
        if (reference) {
          if (referenceCache.has(cacheKey)) {
            expected = referenceCache.get(cacheKey) ?? null;
          } else {
            const refOut = yield* Effect.tryPromise({ try: () => executeSql(reference), catch: engineFailure });
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
        return { qr, checks, passed: comparison.ok, duration_ms: round(ms) };
      }),
    getTables: () =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const live = yield* Effect.tryPromise({ try: () => fetchTables(), catch: engineFailure });
        note('GET', '/datasets/commerce/tables', 200, nowMs() - t0);
        return { dataset: 'commerce', version: 1, tables: live.tables, rows: live.rows };
      }),
    /* The browser engine is the learner's own database, created on first use
       and writable, so it always has an environment to describe. */
    getEnvironment: () =>
      Effect.succeed<EnvironmentInfo | null>({ database: config.baseUrl, exists: true, writable: true }),
    resetEnvironment: () =>
      Effect.gen(function* () {
        const t0 = nowMs();
        const out = yield* Effect.tryPromise({ try: () => resetPgliteEnvironment(), catch: engineFailure });
        note('POST', '/environment/reset', 200, nowMs() - t0);
        return { ok: out.ok, duration_ms: out.duration_ms, environment: { database: config.baseUrl, exists: true, writable: true } };
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
  const config: ApiConfig = { baseUrl: root, engine: 'PostgreSQL (server)', latency: true };

  /* rawErrorBodies: /query answers a failure with a 400 whose *body* is the
     QueryResult the UI renders (columns, error.detail, …), so that one call
     wants the parsed body rather than a thrown error. Every other call treats a
     4xx/5xx envelope as a typed failure, which is what lets signup surface
     AUTH_EMAIL_TAKEN instead of dying later in a schema decode. */
  const request = (method: string, path: string, body?: unknown, opts?: { rawErrorBodies?: boolean }) =>
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
      }).pipe(Effect.retry(Schedule.recurs(2)), Effect.timeout(timeoutMs));

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
      if (!text) return null;
      let parsed: unknown = null;
      try {
        parsed = JSON.parse(text) as unknown;
      } catch {
        parsed = null;
      }
      /* A 401 means the token is absent, expired or invalid. AUTH_REQUIRED is
         the session-invalid signal: clear the stored token and let the app
         bounce to sign-in. Login's own 401 carries AUTH_INVALID_CREDENTIALS and
         must reach the form as a plain error, so it is deliberately not wired
         to the session reset. */
      const envelopeCode = errorCode(parsed);
      if (res.status === 401) {
        const code = envelopeCode ?? 'AUTH_REQUIRED';
        if (code === 'AUTH_REQUIRED') {
          clearAuthToken();
          unauthorized();
        }
        return yield* Effect.fail(authFailure(code, errorMessage(parsed) ?? 'Authentication required.', 401));
      }
      if (res.status >= 400 && !opts?.rawErrorBodies) {
        return yield* Effect.fail(
          authFailure(
            envelopeCode ?? 'ENVIRONMENT_UNAVAILABLE',
            errorMessage(parsed) ?? `The gateway rejected the request (HTTP ${res.status}).`,
            res.status,
          ),
        );
      }
      return parsed;
    });

  const service: ExecutionServiceShape = {
    config,
    /* The gateway enforces its own deadline server-side (QC_TIMEOUT_MS) and
       advertises it on /capabilities. A failed probe reports "unknown" rather
       than claiming a capability that could not be confirmed. */
    capabilities: () =>
      request('GET', '/capabilities').pipe(
        Effect.map((json): EngineCapabilities | null => {
          const body = (json ?? {}) as { hard_timeout?: unknown };
          const hardTimeout = typeof body.hard_timeout === 'boolean' ? body.hard_timeout : true;
          /* Writes need an owned environment, which the gateway gives a
             signed-in learner; an anonymous visitor keeps the shared read-only
             dataset (ADR 0003). So the honest answer is "is there a session". */
          return { placement: 'server', hardTimeout, timeoutMs: 5000, writable: readAuthToken() !== null };
        }),
        Effect.catch(() => Effect.succeed<EngineCapabilities | null>(null)),
      ),
    signup: (input) =>
      request('POST', '/auth/signup', input).pipe(
        Effect.flatMap((json) => decodeAuthSession(json)),
        Effect.tap((session) => Effect.sync(() => writeAuthToken(session.token))),
        Effect.mapError(asGatewayError),
      ),
    login: (input) =>
      request('POST', '/auth/login', input).pipe(
        Effect.flatMap((json) => decodeAuthSession(json)),
        Effect.tap((session) => Effect.sync(() => writeAuthToken(session.token))),
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
    runQuery: (sql, _expect) =>
      request('POST', '/query', { sql, limits: {} }, { rawErrorBodies: true }).pipe(
        Effect.flatMap((json) => decodeQueryResult(json)),
        Effect.catch((e) => Effect.succeed(failedResult(e))),
      ),
    checkAnswer: (sql, cell) =>
      request('POST', '/answers/evaluate', {
        sql,
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
    getTables: () =>
      request('GET', '/datasets/commerce/tables').pipe(
        Effect.map((json) => json as TablesResponse),
        Effect.catch(() => Effect.succeed({ dataset: 'commerce', version: 1, tables: [], rows: 0 })),
      ),
    /* ADR 0003. `GET /environment` describes the caller's workspace; an
       anonymous caller has none, so it answers null rather than an error. A 401
       is still handled by the shared request helper, which drops the token. */
    getEnvironment: () =>
      request('GET', '/environment').pipe(
        Effect.map(environmentFrom),
        Effect.catch(() => Effect.succeed<EnvironmentInfo | null>(null)),
      ),
    resetEnvironment: () =>
      request('POST', '/environment/reset').pipe(
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
    generateCourse: (req) =>
      request('POST', '/courses/generate', { title: req.title, exercises: req.exercises, steps: req.steps }).pipe(
        Effect.map((json) => {
          const j = (json ?? {}) as Partial<CourseGeneration>;
          return {
            title: j.title ?? req.title,
            chapters: j.chapters ?? (req.steps ?? []).length,
            exercises: j.exercises ?? req.exercises,
            datasetVersion: j.datasetVersion ?? 1,
          };
        }),
        Effect.catch(() =>
          Effect.succeed({ title: req.title, chapters: (req.steps ?? []).length, exercises: req.exercises, datasetVersion: 1 }),
        ),
      ),
    generateChallenge: () =>
      request('POST', '/challenges/generate', {}).pipe(
        Effect.map(() => ({ ok: true })),
        Effect.catch(() => Effect.succeed({ ok: false })),
      ),
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
  const body = (json ?? {}) as { ok?: unknown; duration_ms?: unknown; environment?: unknown };
  return {
    ok: body.ok === true,
    duration_ms: typeof body.duration_ms === 'number' ? body.duration_ms : 0,
    environment: asEnvironment(body.environment) ?? undefined,
  };
}

function failedResult(e: unknown): QueryResult {
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

  /* First query pays for the WASM download and initdb, so start it now. */
  if (opts.mode === 'pglite') warmUpDatabase();

  const run = <A>(effect: Effect.Effect<A, unknown, ExecutionService>): Promise<A> =>
    Effect.runPromise(Effect.provide(effect, layer));
  const call = <A>(f: (svc: ExecutionServiceShape) => Effect.Effect<A, unknown, never>): Promise<A> =>
    run(
      Effect.gen(function* () {
        const svc = yield* ExecutionService;
        return yield* f(svc);
      }),
    );

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

    runQuery: (sql, expect) => call((svc) => svc.runQuery(sql, expect)),
    checkAnswer: (sql, cell) => call((svc) => svc.checkAnswer(sql, cell)),
    getTables: () => call((svc) => svc.getTables()),
    getEnvironment: () => call((svc) => svc.getEnvironment()),
    resetEnvironment: () => call((svc) => svc.resetEnvironment()),
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
    draftCourse: (req) => call((svc) => svc.generateCourse(req)),
    draftChallenge: (req) => call((svc) => svc.generateChallenge(req)),
  };
}
