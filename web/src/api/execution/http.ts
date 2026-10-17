import { Effect, Layer, Schedule } from 'effect';
import { GatewayError, asGatewayError, authFailure } from '../errors';
import { gatewayAiMethods } from '../ai';
import type { AiPerformOptions, AiPerformResult } from '../ai';
import { advertisedRunBudgetMs, providerDeadlineMs } from '../aiDeadline';
import { decodeAuthSession, decodeEvaluation, decodeQueryResult, decodeUser } from '../contract';
import { failedPlanBundle, validatePlanBundle } from '../planContract';
import { datasetFor } from '../../lib/dataset';
import { dialectFor } from '../../lib/dialect';
import { BUNDLED_CONTENT } from '../content';
import { DEFAULT_DATASET, DEFAULT_DIALECT, isQueryErrorCode } from '../../types';
import type {
  ApiConfig,
  Course,
  DatasetId,
  DiagnosticsReport,
  Dialect,
  EngineCapabilities,
  EnvironmentInfo,
  EnvironmentReset,
  QueryResult,
  TablesResponse,
  User,
} from '../../types';
import {
  ExecutionService,
  clearAuthToken,
  nowMs,
  readAuthToken,
  writeAuthToken,
  writeAuthUser,
} from './shared';
import type { ExecutionServiceShape, NoteFn } from './shared';

/* The gateway layer: every call goes to the Go HTTP API.

   `perform` is the one round trip. It keeps the status and the parsed body so
   a caller can read a non-2xx body, and it owns the bearer token, the retry on
   idempotent reads and the timeout. `request` turns the status into the typed
   `GatewayError` channel. */

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

    const isRecordBody = (v: unknown): v is Record<string, unknown> =>
    typeof v === "object" && v !== null && !Array.isArray(v);

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
    diagnostics: () =>
      request('GET', '/diagnostics').pipe(
        Effect.map((json): DiagnosticsReport | null => {
          const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null;
          if (!isRecord(json)) return null;
          const build = typeof json.build === 'string' ? json.build : '';
          const engine = isRecord(json.engine) ? json.engine : null;
          const store = isRecord(json.store) ? json.store : null;
          const dataset = isRecord(json.dataset) ? json.dataset : null;
          const progress = isRecord(json.progress) ? json.progress : null;
          if (!engine || !store || !dataset || !progress) return null;
          const limits = isRecord(engine.limits) ? engine.limits : {};
          return {
            build,
            engine: {
              name: typeof engine.name === 'string' ? engine.name : '',
              ready: engine.ready === true,
              reason: typeof engine.reason === 'string' ? engine.reason : '',
              version: typeof engine.version === 'string' ? engine.version : '',
              limits: {
                max_rows: typeof limits.max_rows === 'number' ? limits.max_rows : 0,
                max_bytes: typeof limits.max_bytes === 'number' ? limits.max_bytes : 0,
                timeout_ms: typeof limits.timeout_ms === 'number' ? limits.timeout_ms : 0,
                max_concurrent: typeof limits.max_concurrent === 'number' ? limits.max_concurrent : 0,
              },
            },
            store: {
              kind: typeof store.kind === 'string' ? store.kind : '',
              persistent: store.persistent === true,
            },
            dataset: {
              schema: typeof dataset.schema === 'string' ? dataset.schema : '',
              version: typeof dataset.version === 'number' ? dataset.version : 0,
            },
            progress: { version: typeof progress.version === 'number' ? progress.version : 0 },
          };
        }),
        Effect.catch(() => Effect.succeed<DiagnosticsReport | null>(null)),
      ),
    preferences: () =>
      request('GET', '/preferences').pipe(
        Effect.catch(() => Effect.succeed(null)),
        Effect.map((json): Record<string, unknown> | null => {
          if (!isRecordBody(json)) return null;
          const prefs = (json as { preferences?: unknown }).preferences;
          return prefs && typeof prefs === 'object' && !Array.isArray(prefs)
            ? (prefs as Record<string, unknown>)
            : null;
        }),
      ),
    savePreferences: (prefs) =>
      request('PUT', '/preferences', prefs).pipe(
        Effect.catch(() => Effect.succeed(prefs)),
        Effect.map((json): Record<string, unknown> => {
          const parsed = json && typeof json === 'object' && !Array.isArray(json)
            ? (json as { preferences?: unknown }).preferences
            : null;
          return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
            ? (parsed as Record<string, unknown>)
            : prefs;
        }),
      ),
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
          const capabilities: EngineCapabilities = {
            placement: 'server',
            hardTimeout,
            timeoutMs: 5000,
            writable,
            explain,
            dialect: 'postgresql',
          };
          /* A gateway that advertises no dialects keeps the field absent. An
             empty list would read as "no dialect works", which is not what an
             older gateway means. */
          if (dialects.length > 0) capabilities.dialects = dialects;
          return capabilities;
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

function failedResult(e: unknown, dialect: Dialect = DEFAULT_DIALECT): QueryResult {
  /* A QueryResult error may only carry a code from the closed query enum. The
     gateway's code space is wider (auth, rate limit, bad request), so a code
     from outside the enum becomes ENVIRONMENT_UNAVAILABLE; the message still
     names what the gateway said. */
  const code = e instanceof GatewayError && isQueryErrorCode(e.code) ? e.code : 'ENVIRONMENT_UNAVAILABLE';
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

