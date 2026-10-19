import { Effect, Layer, Option, Schedule, Schema } from 'effect';
import { GatewayError, asGatewayError, authFailure } from '../errors';
import { gatewayAiMethods } from '../ai';
import type { AiPerformOptions } from '../ai';
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
  EnvironmentPrepare,
  EnvironmentReset,
  QueryResult,
  TablesResponse,
  User,
} from '../../types';
import {
  ExecutionService,
  clearAuthUser,
  nowMs,
  offlineDraft,
  readAuthUser,
  writeAuthUser,
} from './shared';
import type { ExecutionServiceShape, NoteFn } from './shared';

/* The gateway layer: every call goes to the Go HTTP API.

   `perform` is the one round trip. It keeps the status and the parsed body so
   a caller can read a non-2xx body, and it owns the retry on idempotent reads
   and the timeout. Authentication rides the same-origin `qc_session` cookie,
   which the browser attaches itself; the layer never reads or sends a token.
   `request` turns the status into the typed `GatewayError` channel. */

/* ---------- HTTP adapter: the Go gateway (Adapter B) ---------- */


export function httpLayer(opts: {
  baseUrl: string;
  note?: NoteFn;
  timeoutMs?: number;
  /** Called when the gateway rejects a request with AUTH_REQUIRED (a missing or
      expired session), so the app can clear its session and return to sign-in. */
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
     fetch, cookie, retry and timeout plumbing. */
  /* The advertised run budget, learned from the first /capabilities answer.
     The app reads capabilities before it offers generation, so this is warm by
     the time a course is asked for; the fallback covers a caller that asks
     without probing first. */
  let aiRunBudgetMs: number | null = null;

  const perform = <Body extends object>(
    method: string,
    path: string,
    body?: Body,
    opts?: AiPerformOptions,
  ): Effect.Effect<WireResult, GatewayError> =>
    Effect.gen(function* () {
      const t0 = nowMs();
      const headers: Record<string, string> = {};
      if (body !== undefined) headers['content-type'] = 'application/json';
      /* No Authorization header: the browser attaches the same-origin
         `qc_session` cookie itself, and the gateway scopes every write to that
         session. `credentials: 'same-origin'` states the rule the default
         already applies, and a cross-origin page cannot use the cookie. */
      const res = yield* Effect.tryPromise({
        try: (signal) =>
          fetch(root + path, {
            method,
            headers: Object.keys(headers).length ? headers : undefined,
            body: body !== undefined ? JSON.stringify(body) : undefined,
            credentials: 'same-origin',
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
      /* The wire is text; this is where it becomes a named JSON value. A body
         that is not JSON reads as null, exactly as an empty one does, so no
         caller has to guess at a half-parsed response. */
      const parsed = Option.getOrNull(Schema.decodeUnknownOption(JsonBodySchema)(text));
      /* Every capabilities answer refreshes the AI run budget, whichever
         caller asked for it. */
      if (path === '/capabilities') {
        const advertised = advertisedRunBudgetMs(parsed);
        if (advertised !== null) aiRunBudgetMs = advertised;
      }
      return { status: res.status, body: parsed };
    });

  const request = <Body extends object>(method: string, path: string, body?: Body, opts?: { rawErrorBodies?: boolean }) =>
    Effect.gen(function* () {
      const { status, body: parsed } = yield* perform(method, path, body);
      /* A 401 means the session is absent, expired or invalid. AUTH_REQUIRED is
         the session-invalid signal: clear the stored account id and let the app
         bounce to sign-in. Login's own 401 carries AUTH_INVALID_CREDENTIALS and
         must reach the form as a plain error, so it is deliberately not wired
         to the session reset. */
      const failure = envelope(parsed);
      if (status === 401) {
        const code = failure.code ?? 'AUTH_REQUIRED';
        if (code === 'AUTH_REQUIRED') {
          clearAuthUser();
          unauthorized();
        }
        return yield* Effect.fail(authFailure(code, failure.message ?? 'Authentication required.', 401));
      }
      if (status >= 400 && !opts?.rawErrorBodies) {
        return yield* Effect.fail(
          authFailure(
            failure.code ?? 'ENVIRONMENT_UNAVAILABLE',
            failure.message ?? `The gateway rejected the request (HTTP ${status}).`,
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
        clearAuthUser();
        unauthorized();
      },
      providerDeadlineMs: () => providerDeadlineMs(aiRunBudgetMs),
    }),
    /* The gateway enforces its own deadline server-side (QC_TIMEOUT_MS) and
       advertises it on /capabilities. A failed probe reports "unknown" rather
       than claiming a capability that could not be confirmed. */
    diagnostics: () =>
      request('GET', '/diagnostics').pipe(
        Effect.map(decodeDiagnostics),
        Effect.catch(() => Effect.succeed<DiagnosticsReport | null>(null)),
      ),
    preferences: () =>
      request('GET', '/preferences').pipe(
        Effect.map(decodePreferences),
        Effect.catch(() => Effect.succeed<Schema.JsonObject | null>(null)),
      ),
    savePreferences: (prefs) =>
      request('PUT', '/preferences', prefs).pipe(
        Effect.map((json) => decodePreferences(json) ?? prefs),
        Effect.catch(() => Effect.succeed(prefs)),
      ),
    capabilities: () =>
      request('GET', '/capabilities').pipe(
        Effect.map(decodeCapabilities),
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
        /* The gateway sets the `qc_session` cookie on this response; keep the
           account id so a patch approval echoes `approved_by`. The returned
           session still carries the token for an API-shaped caller, but this
           layer stores none of it. */
        Effect.tap((session) => Effect.sync(() => writeAuthUser(session.user.id))),
        Effect.mapError(asGatewayError),
      ),
    login: (input) =>
      request('POST', '/auth/login', input).pipe(
        Effect.flatMap((json) => decodeAuthSession(json)),
        Effect.tap((session) => Effect.sync(() => writeAuthUser(session.user.id))),
        Effect.mapError(asGatewayError),
      ),
    logout: () =>
      Effect.gen(function* () {
        /* GET/POST carry the cookie, so the gateway clears it and revokes the
           server session; the local account marker goes with it. */
        yield* request('POST', '/auth/logout').pipe(Effect.catch(() => Effect.void));
        clearAuthUser();
      }),
    me: () => {
      /* The cookie may name a session even though nothing local holds a token,
         so ask every time: a 401 is "signed out", an answer is the account. */
      return request('GET', '/auth/me').pipe(
        Effect.flatMap((json) => decodeUser(json).pipe(Effect.mapError(asGatewayError))),
        Effect.tap((user) => Effect.sync(() => writeAuthUser(user.id))),
        Effect.map((user): User | null => user),
        Effect.catch((e) => {
          const err = asGatewayError(e);
          if (err.code === 'AUTH_REQUIRED' || err.status === 401) {
            clearAuthUser();
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
            const failure = envelope(json);
            return failedPlanBundle(
              'postgresql',
              failure.code ?? 'EVALUATION_ERROR',
              failure.message ?? 'The gateway returned a plan body this client could not read.',
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
          /* The evaluator reads `result_contract` (backend/internal/eval/eval.go).
             A bare `contract` key decodes as absent, which graded every HTTP
             submission against the zero contract: order and tolerance ignored. */
          result_contract: cell.item?.contract ?? { columns: [], column_order: 'exact', row_order: 'ignored', duplicates: 'preserved', numeric_tolerance: 0 },
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
        Effect.map((json) => decodeTables(json, { dataset, version: 1, tables: [], rows: 0 })),
        Effect.catch(() => Effect.succeed({ dataset, version: 1, tables: [], rows: 0 })),
      ),
    /* ADR 0003. `GET /environment` describes the caller's workspace; an
       anonymous caller has none, so it answers null rather than an error. A 401
       is still handled by the shared request helper, which drops the account
       marker. */
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
    /* M9. A stored course's exercises were verified in a workspace that had the
       course's own setup applied. Preparing it here, when the course opens,
       means the first exercise does not die with "relation does not exist". The
       route is idempotent per workspace, so a second open is a cheap no-op. */
    prepareEnvironment: (courseId, dialect = DEFAULT_DIALECT) =>
      request('POST', environmentPreparePath(courseId, dialect)).pipe(
        Effect.map(asEnvironmentPrepare),
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
    draftCourse: (req) => Effect.sync(() => offlineDraft(req).course),
    draftChallenge: (req) => Effect.succeed(offlineDraft(req).challenge),
  };
  return Layer.succeed(ExecutionService, service);
}

/* ---------- wire decoding ----------

   The gateway speaks JSON. Each response shape is decoded with an Effect
   `Schema` here, the way `src/api/ai.ts` and `src/api/contract.ts` decode
   theirs, so a field the gateway omits or mistypes reads as the fallback the
   old hand-written parser supplied instead of flowing onward untyped. The
   optional leaves mirror `ai.ts`: a field the contract marks optional that
   arrives absent or mistyped reads as absent.

   The outgoing body of `perform` and `request` is generic: a caller hands in
   an already-shaped value and the transport serialises it without reading it,
   so the parameter keeps the caller's own type. Each caller's domain schema
   owns the shape. */

const JsonBodySchema = Schema.fromJsonString(Schema.Json);

interface WireResult {
  status: number;
  body: Schema.Json | null;
}

const optionalString = Schema.optional(Schema.String).pipe(
  Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
);
const optionalNumber = Schema.optional(Schema.Number).pipe(
  Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
);
const optionalBoolean = Schema.optional(Schema.Boolean).pipe(
  Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
);
const stringOr = (fallback: string) =>
  Schema.String.pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(fallback))),
    Schema.withDecodingDefaultType(Effect.succeed(fallback)),
  );
const numberOr = (fallback: number) =>
  Schema.Number.pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(fallback))),
    Schema.withDecodingDefaultType(Effect.succeed(fallback)),
  );
const booleanOr = (fallback: boolean) =>
  Schema.Boolean.pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(fallback))),
    Schema.withDecodingDefaultType(Effect.succeed(fallback)),
  );
const optionalStruct = <S extends Schema.Constraint>(schema: S) =>
  Schema.optional(schema).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  );

/* The failure envelope `{ error: { code, message } }` a non-2xx body carries.
   The shape is tolerant because a transport-level 401 must be classified from
   whatever the gateway sent, even when it is not the full envelope. */
const EnvelopeErrorSchema = Schema.Struct({
  code: optionalString,
  message: optionalString,
});
const EnvelopeSchema = Schema.Struct({ error: optionalStruct(EnvelopeErrorSchema) });

interface EnvelopeFailure {
  code: string | null;
  message: string | null;
}

function envelope(body: Schema.Json | null): EnvelopeFailure {
  const decoded = Schema.decodeUnknownOption(EnvelopeSchema)(body);
  if (Option.isNone(decoded)) return { code: null, message: null };
  return {
    code: decoded.value.error?.code ?? null,
    message: decoded.value.error?.message ?? null,
  };
}

function errorMeta(e: GatewayError) {
  if (e instanceof GatewayError) return { error: { code: e.code, message: e.message } };
  return { error: { code: 'ENVIRONMENT_UNAVAILABLE', message: 'Gateway unavailable.' } };
}

/* GET /diagnostics. The four reports are required: a body without an engine,
   store, dataset or progress report is not the report this client renders, so
   it reads as null rather than as a report of empty strings. */
const DiagnosticsLimitsSchema = Schema.Struct({
  max_rows: numberOr(0),
  max_bytes: numberOr(0),
  timeout_ms: numberOr(0),
  max_concurrent: numberOr(0),
});
const DiagnosticsEngineSchema = Schema.Struct({
  name: stringOr(''),
  ready: booleanOr(false),
  reason: stringOr(''),
  version: stringOr(''),
  limits: optionalStruct(DiagnosticsLimitsSchema),
});
const DiagnosticsStoreSchema = Schema.Struct({
  kind: stringOr(''),
  persistent: booleanOr(false),
});
const DiagnosticsDatasetSchema = Schema.Struct({
  schema: stringOr(''),
  version: numberOr(0),
});
const DiagnosticsProgressSchema = Schema.Struct({ version: numberOr(0) });
const DiagnosticsSchema = Schema.Struct({
  build: stringOr(''),
  engine: DiagnosticsEngineSchema,
  store: DiagnosticsStoreSchema,
  dataset: DiagnosticsDatasetSchema,
  progress: DiagnosticsProgressSchema,
});

function decodeDiagnostics(json: Schema.Json | null): DiagnosticsReport | null {
  const decoded = Schema.decodeUnknownOption(DiagnosticsSchema)(json);
  if (Option.isNone(decoded)) return null;
  const d = decoded.value;
  return {
    build: d.build,
    engine: {
      name: d.engine.name,
      ready: d.engine.ready,
      reason: d.engine.reason,
      version: d.engine.version,
      limits: d.engine.limits ?? { max_rows: 0, max_bytes: 0, timeout_ms: 0, max_concurrent: 0 },
    },
    store: { kind: d.store.kind, persistent: d.store.persistent },
    dataset: { schema: d.dataset.schema, version: d.dataset.version },
    progress: { version: d.progress.version },
  };
}

/* GET/PUT /preferences. The document is arbitrary JSON the gateway stores
   without interpreting it, so the decoder proves only that it is an object;
   that is what lets the method answer the `Record<string, unknown>` seam in
   `shared.ts` without a hand-rolled dictionary type. */
const PreferencesEnvelopeSchema = Schema.Struct({ preferences: optionalStruct(Schema.JsonObject) });

function decodePreferences(json: Schema.Json | null): Schema.JsonObject | null {
  const decoded = Schema.decodeUnknownOption(PreferencesEnvelopeSchema)(json);
  return Option.isSome(decoded) ? (decoded.value.preferences ?? null) : null;
}

/* GET /capabilities. `plan` is either a boolean or a `{ supported }` block, so
   both forms decode to a value the caller reads without a bare typeof. */
const DialectSchema = Schema.Literals(['postgresql', 'sqlite']);
const PlanBlockSchema = Schema.Struct({ supported: optionalBoolean });
const CapabilitiesSchema = Schema.Struct({
  hard_timeout: optionalBoolean,
  explain: optionalBoolean,
  plan: optionalStruct(Schema.Union([Schema.Boolean, PlanBlockSchema])),
  writable: optionalBoolean,
  dialects: optionalStruct(Schema.Array(Schema.Json)),
});
type CapabilitiesBody = Schema.Schema.Type<typeof CapabilitiesSchema>;
const EMPTY_CAPABILITIES: CapabilitiesBody = {};

function decodeCapabilities(json: Schema.Json | null): EngineCapabilities {
  const decoded = Schema.decodeUnknownOption(CapabilitiesSchema)(json);
  const body = Option.isSome(decoded) ? decoded.value : EMPTY_CAPABILITIES;
  const hardTimeout = body.hard_timeout ?? true;
  const plan = body.plan;
  /* The gateway advertises plan support as `explain` (the documented
     capability surface) or as a `plan` block; a gateway that advertises
     neither cannot plan, and the UI disables the action rather than asking for
     a plan and guessing at the refusal. */
  const explain =
    body.explain === true || plan === true || (Schema.is(PlanBlockSchema)(plan) && plan.supported === true);
  /* Writes need an owned environment, which the gateway gives a signed-in
     learner; an anonymous visitor keeps the shared read-only dataset
     (ADR 0003). Two facts have to hold, and the gateway can only answer one of
     them: it says whether it *has* a workspace manager, and this client says
     whether it *has* an account. The account id is the local marker: sign-in
     writes it, `me` writes it on restore, and logout/401 clears it. Asking only
     the gateway was a live bug — the answer was computed once at boot, before
     sign-in, and a signed-in learner was told to sign in. */
  const writable = body.writable !== false && readAuthUser() !== null;
  const dialects = (body.dialects ?? []).filter(Schema.is(DialectSchema));
  const capabilities: EngineCapabilities = {
    placement: 'server',
    hardTimeout,
    timeoutMs: 5000,
    writable,
    explain,
    dialect: 'postgresql',
  };
  /* A gateway that advertises no dialects keeps the field absent. An empty
     list would read as "no dialect works", which is not what an older gateway
     means. */
  if (dialects.length > 0) capabilities.dialects = dialects;
  return capabilities;
}

/* GET /datasets/{id}/tables. The wire shape mirrors backend/internal/dataset:
   a column is [name, type, flags] and a preview is a header row plus up to five
   data rows. The decoded arrays are readonly, so each is copied into the
   mutable contract shape rather than cast. */
const TableDefSchema = Schema.Struct({
  name: Schema.String,
  rows: numberOr(0),
  cols: Schema.Array(Schema.Tuple([Schema.String, Schema.String, Schema.String])),
  preview: Schema.Array(Schema.Array(Schema.String)),
});
const TablesResponseSchema = Schema.Struct({
  dataset: Schema.String,
  version: Schema.Number,
  tables: Schema.Array(TableDefSchema),
  rows: numberOr(0),
});

function decodeTables(json: Schema.Json | null, fallback: TablesResponse): TablesResponse {
  const decoded = Schema.decodeUnknownOption(TablesResponseSchema)(json);
  if (Option.isNone(decoded)) return fallback;
  const d = decoded.value;
  return {
    dataset: d.dataset,
    version: d.version,
    rows: d.rows,
    tables: d.tables.map((t) => ({
      name: t.name,
      rows: t.rows,
      cols: t.cols.map((c) => [...c]),
      preview: t.preview.map((row) => [...row]),
    })),
  };
}

/* The environment endpoints answer `{ ok, environment }` (reset adds
   `duration_ms`). There is no contracts/*.schema.json for them yet, so the
   decode is deliberately tolerant: an unreadable shape reads as "no
   environment" rather than throwing inside a reset the learner has already
   confirmed. Errors never reach here — a 4xx fails earlier in `request` with
   the gateway's own code and message, which the UI shows verbatim. */
const EnvironmentSchema = Schema.Struct({
  account: optionalString,
  database: optionalString,
  exists: booleanOr(false),
  writable: booleanOr(false),
  tables: optionalNumber,
  created_at: optionalString,
  reset_at: optionalString,
  idle_until: optionalString,
});
const EnvironmentEnvelopeSchema = Schema.Struct({ environment: optionalStruct(EnvironmentSchema) });

function environmentFrom(json: Schema.Json | null): EnvironmentInfo | null {
  const decoded = Schema.decodeUnknownOption(EnvironmentEnvelopeSchema)(json);
  if (Option.isNone(decoded)) return null;
  return decoded.value.environment ?? null;
}

const EnvironmentResetSchema = Schema.Struct({
  ok: booleanOr(false),
  duration_ms: numberOr(0),
  setup_statements: numberOr(0),
  environment: optionalStruct(EnvironmentSchema),
});
type EnvironmentResetBody = Schema.Schema.Type<typeof EnvironmentResetSchema>;
const EMPTY_ENVIRONMENT_RESET: EnvironmentResetBody = { ok: false, duration_ms: 0, setup_statements: 0 };

function asEnvironmentReset(json: Schema.Json | null): EnvironmentReset {
  const decoded = Schema.decodeUnknownOption(EnvironmentResetSchema)(json);
  const body = Option.isSome(decoded) ? decoded.value : EMPTY_ENVIRONMENT_RESET;
  return {
    ok: body.ok,
    duration_ms: body.duration_ms,
    /* A gateway that answered without the count applied none; reporting the
       missing field as a number the adapter never saw would be a guess. */
    setup_statements: body.setup_statements,
    environment: body.environment ?? undefined,
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

/** The prepare route's path. The course is required and the dialect travels
    with it for the same reason it travels with a reset: the two workspaces are
    different things, so preparing one while the learner looks at the other
    would prepare the wrong file or database. */
function environmentPreparePath(courseId: string, dialect: Dialect = DEFAULT_DIALECT): string {
  const params = new URLSearchParams();
  params.set('course', courseId);
  params.set('dialect', dialect);
  return `/environment/prepare?${params.toString()}`;
}

const EnvironmentPrepareSchema = Schema.Struct({
  ok: booleanOr(false),
  setup_statements: numberOr(0),
});
type EnvironmentPrepareBody = Schema.Schema.Type<typeof EnvironmentPrepareSchema>;
const EMPTY_ENVIRONMENT_PREPARE: EnvironmentPrepareBody = { ok: false, setup_statements: 0 };

function asEnvironmentPrepare(json: Schema.Json | null): EnvironmentPrepare {
  const decoded = Schema.decodeUnknownOption(EnvironmentPrepareSchema)(json);
  const body = Option.isSome(decoded) ? decoded.value : EMPTY_ENVIRONMENT_PREPARE;
  return { ok: body.ok, setup_statements: body.setup_statements };
}

function failedResult(e: GatewayError | Schema.SchemaError, dialect: Dialect = DEFAULT_DIALECT): QueryResult {
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

