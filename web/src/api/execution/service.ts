import { Effect } from 'effect';
import { unavailableAvailability } from '../ai';
import type { AiAvailability } from '../aiContract';
import { STORE_DRAFTS, idbGet, idbPut } from '../idb';
import type { ApiConfig, ApiLogEntry, ExecutionApi } from '../../types';
import { ExecutionService, readAuthUser } from './shared';
import type { ExecutionServiceShape, NoteFn } from './shared';
import { PGLITE_CONFIG, pgliteLayer } from './browser';
import { httpLayer } from './http';

/* The promise facade the screens use.

   `createExecutionApi` picks the layer for the mode, wires the API log note
   through, and exposes one promise per service method. The Effect service
   stays the contract; this module is only the React-facing edge. */

/* ---------- Promise-shaped facade used by React ---------- */

export function createExecutionApi(opts: {
  mode: 'pglite' | 'http';
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
      : PGLITE_CONFIG;
  const layer =
    opts.mode === 'http'
      ? httpLayer({ baseUrl: config.baseUrl, note, onUnauthorized: opts.onUnauthorized })
      : pgliteLayer(config, note);

  /* Nothing is warmed here. The selected course's dialect decides which engine
     to start, and the store owns that decision (`warmUpCourse`). Booting PGlite
     on every pglite-mode page was spending the 6 MB Postgres download on a
     SQLite chapter that would never use it. */

  const run = <A>(effect: Effect.Effect<A, unknown, ExecutionService>): Promise<A> =>
    Effect.runPromise(Effect.provide(effect, layer));
  const call = <A>(f: (svc: ExecutionServiceShape) => Effect.Effect<A, unknown, never>): Promise<A> =>
    run(
      Effect.gen(function* () {
        const svc = yield* ExecutionService;
        return yield* f(svc);
      }),
    );

  /* Availability is read once per signed-in account — the stored account id is
     the local marker for "a session is attached" — and remembered, the way the
     store already caches engine capabilities, so a screen can render its
     controls on the first frame instead of flashing "unavailable".
     `aiAvailability()` resolves, never rejects: an unreadable probe is an
     unavailable answer. */
  let availabilityKey: string | null = null;
  let availabilityPromise: Promise<AiAvailability> | null = null;
  const aiAvailability = (): Promise<AiAvailability> => {
    const key = readAuthUser() ?? '';
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
    prepareEnvironment: (courseId, dialect) => call((svc) => svc.prepareEnvironment(courseId, dialect)),
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
    adminCourses: () => call((svc) => svc.adminCourses()),
    adminAI: () => call((svc) => svc.adminAI()),
    adminAIModels: () => call((svc) => svc.adminAIModels()),
    preferences: () => call((svc) => svc.preferences()),
    savePreferences: (prefs) => call((svc) => svc.savePreferences(prefs)),
    saveAdminAI: (update) => call((svc) => svc.saveAdminAI(update)),
    diagnostics: () => call((svc) => svc.diagnostics()),
    getCourse: (id) => call((svc) => svc.getCourse(id)),
    deleteCourse: (id) => call((svc) => svc.deleteCourse(id)),
    updateCourse: (id, edits) => call((svc) => svc.updateCourse(id, edits)),
    approveCourse: (id) => call((svc) => svc.approveCourse(id)),
    submitGeneration: (req, kind) => call((svc) => svc.submitGeneration(req, kind)),
    generationJob: (id) => call((svc) => svc.generationJob(id)),
    tutor: (req) => call((svc) => svc.tutor(req)),
    critique: (challengeId, req) => call((svc) => svc.critique(challengeId, req)),
    reportDefect: (req) => call((svc) => svc.reportDefect(req)),
    decidePatch: (patchId, approved) => call((svc) => svc.decidePatch(patchId, approved)),
    listRuns: () => call((svc) => svc.listRuns()),
    getRun: (id) => call((svc) => svc.getRun(id)),
  };
}

