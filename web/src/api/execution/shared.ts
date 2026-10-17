import { Context, Effect } from 'effect';
import { GatewayError } from '../errors';
import type { AiServiceShape } from '../ai';
import type { ExplainRequest, PlanBundle } from '../planContract';
import type {
  ApiConfig,
  AuthSession,
  Cell,
  ContentSection,
  Course,
  CourseGeneration,
  DatasetId,
  DiagnosticsReport,
  Dialect,
  EngineCapabilities,
  EnvironmentInfo,
  EnvironmentReset,
  Evaluation,
  GenerationRequest,
  LoginInput,
  Progress,
  ProgressEvent,
  QueryContext,
  QueryResult,
  SignupInput,
  TablesResponse,
  User,
} from '../../types';

/* The seam that every transport shares.

   `ExecutionService` is the Effect service key. `ExecutionServiceShape` is the
   contract each layer satisfies. The bearer token store lives here because the
   gateway layer attaches the token and both layers clear it on logout. */

/* The typed failure every adapter can raise now lives in ./errors so the AI
   seam can raise and inspect it without importing the browser engine. It is
   re-exported here because that is the name callers already import. */
export { GatewayError };

/** The capability surface, expressed as Effects with a typed error channel. */
export interface ExecutionServiceShape extends AiServiceShape {
  /* The gateway's measured readiness report. Null on the offline adapters:
     there is no gateway to ask, and a lookalike would be a lie. */
  diagnostics(): Effect.Effect<DiagnosticsReport | null>;
  /* Per-account client preferences. The gateway stores the document without
     interpreting it; the offline adapters answer null honestly and their
     saves resolve to the stored value they cannot keep. */
  preferences(): Effect.Effect<Record<string, unknown> | null>;
  savePreferences(prefs: Record<string, unknown>): Effect.Effect<Record<string, unknown>>;
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

export type NoteFn = (method: string, path: string, status: number, ms: number) => void;

export const nowMs = (): number => (typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now());

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

