import { Effect } from 'effect';
import { GatewayError, asGatewayError } from './errors';
import { readAuthUser } from './authStorage';
import { isAiErrorCode } from '../types';
import type {
  AdminAIConfig,
  AdminAIModelEntry,
  AdminAIUpdate,
  AiArtifact,
  AiAttempt,
  AiAvailability,
  AiChapterResult,
  AiCourseBuildAnswer,
  AiCourseRecord,
  AiCheck,
  AiCourseChapter,
  AiCourseExercise,
  AiCoursePlan,
  AiCourseSection,
  AiCritiqueResponse,
  AiDefectReport,
  AiDefectVerdict,
  AiDifficulty,
  AiError,
  AiErrorCode,
  AiEvidence,
  AiExerciseResult,
  AiGenerationRequest,
  AiJob,
  AiJobHandle,
  AiJobStage,
  AiOutcome,
  AiProvenance,
  AiRecord,
  AiResultContract,
  AiSetupPlan,
  AiStage,
  AiTutorRequest,
  AiTutorResponse,
} from './aiContract';

/*
 * The M4 AI operations seam (design §11).
 *
 * The Go gateway is the policy: it decides whether a provider is configured,
 * runs the §11.1 pipeline and is the only thing allowed to mark an artifact
 * active. Nothing here computes a verdict, and the browser adapters do not
 * imitate one: when there is no gateway they report the lab unavailable and
 * return an `unavailable` outcome with no artifact, rather than a canned
 * challenge that never passed the pipeline.
 *
 * Keeping the AI surface in its own module (it only needs `effect`, the
 * contract types, the typed error and the account-id reader in `./authStorage`)
 * is what lets the audit scripts exercise it in node, away from the browser
 * engine that `./execution` also owns.
 */

const AI_UNAVAILABLE_REASON = 'AI generation runs on the gateway; start the app with the Go backend.';

/** The AI slice of the ExecutionService contract. */
export interface AiServiceShape {
  /* Never fails: an unavailable lab is a value, not an error. */
  aiAvailability(): Effect.Effect<AiAvailability>;
  generateChallenge(req: AiGenerationRequest): Effect.Effect<AiOutcome, GatewayError>;
  generateCourse(req: AiGenerationRequest): Effect.Effect<AiOutcome, GatewayError>;
  listChallenges(): Effect.Effect<AiRecord[], GatewayError>;
  getChallenge(id: string): Effect.Effect<AiRecord | null, GatewayError>;
  resetChallenge(id: string): Effect.Effect<{ ok: boolean }, GatewayError>;
  /* A generation that answers with a job instead of waiting for the model. The
     handle is null when the gateway ignored the async ask and answered with the
     blocking envelope, so a caller can fall back rather than guess. */
  submitGeneration(req: AiGenerationRequest, kind: 'challenge' | 'course'): Effect.Effect<AiJobHandle | null, GatewayError>;
  /* One read of a job, so the caller owns the pacing. A job that is gone (or is
     somebody else's, which the gateway reports the same way) resolves to null. */
  generationJob(id: string): Effect.Effect<AiJob | null, GatewayError>;
  /* The courses the learner owns. These never return an empty list on a
     failure: an empty list is the claim "you have no courses", and a client
     that cannot reach the store does not know that. */
  listCourses(): Effect.Effect<AiCourseRecord[], GatewayError>;
  /* Every stored course, every owner: the administrator's inventory
     (GET /admin/courses). The gateway refuses a learner with 403. */
  adminCourses(): Effect.Effect<AiCourseRecord[], GatewayError>;
  /* The live AI configuration (GET /admin/ai). The key is never in a body. */
  adminAI(): Effect.Effect<AdminAIConfig, GatewayError>;
  /* Save and apply one: persists under the store's 'ai' key and swaps the
     running configuration. */
  saveAdminAI(update: AdminAIUpdate): Effect.Effect<AdminAIConfig, GatewayError>;
  /* The embedded catalog, for the manage surface's model picker. */
  adminAIModels(): Effect.Effect<AdminAIModelEntry[], GatewayError>;
  getCourse(id: string): Effect.Effect<AiCourseRecord | null, GatewayError>;
  deleteCourse(id: string): Effect.Effect<{ ok: boolean }, GatewayError>;
  /* Approve a course that stopped awaiting the outline's approval: continue the
     stored outline into its chapter phase. Answers with a job to poll when the
     gateway ran it in the background, or with the blocking outcome. */
  approveCourse(id: string): Effect.Effect<AiCourseBuildAnswer, GatewayError>;
  tutor(req: AiTutorRequest): Effect.Effect<AiTutorResponse, GatewayError>;
  critique(challengeId: string, req: { sql: string; objective?: string }): Effect.Effect<AiCritiqueResponse, GatewayError>;
  reportDefect(req: AiDefectReport): Effect.Effect<AiDefectVerdict, GatewayError>;
  decidePatch(patchId: string, approved: boolean): Effect.Effect<{ applied: boolean }, GatewayError>;
}

/* ---------- browser adapters: honest unavailability ---------- */

export function unavailableAvailability(reason: string = AI_UNAVAILABLE_REASON): AiAvailability {
  return { configured: false, available: false, provider: '', reason };
}

function unavailableOutcome(kind: 'challenge' | 'course', reason: string = AI_UNAVAILABLE_REASON): AiOutcome {
  return {
    status: 'unavailable',
    kind,
    artifact: null,
    stages: [],
    attempts: [],
    error: { code: 'AI_UNAVAILABLE', message: reason },
  };
}

/**
 * The AI methods for the browser-only adapter (pglite).
 *
 * Generation *policy* lives on the gateway, so there is no honest artifact to
 * return here and these adapters do not synthesise one. The generate calls
 * resolve to an `unavailable` outcome (the screens render its stage list, which
 * is empty, as "nothing ran"); the non-outcome calls fail with a typed
 * AI_UNAVAILABLE, because a fabricated tutor hint or critique would be a claim
 * the engine cannot back.
 */
export function browserAiMethods(reason: string = AI_UNAVAILABLE_REASON): AiServiceShape {
  const unavailable = new GatewayError({ code: 'AI_UNAVAILABLE', message: reason });
  return {
    aiAvailability: () => Effect.succeed(unavailableAvailability(reason)),
    generateChallenge: () => Effect.succeed(unavailableOutcome('challenge', reason)),
    generateCourse: () => Effect.succeed(unavailableOutcome('course', reason)),
    listChallenges: () => Effect.fail(unavailable),
    getChallenge: () => Effect.fail(unavailable),
    /* No gateway, no inventory: there is nothing an administrator could act
       on in a page-local engine. */
    adminCourses: () => Effect.fail(unavailable),
    adminAI: () => Effect.fail(unavailable),
    saveAdminAI: () => Effect.fail(unavailable),
    adminAIModels: () => Effect.fail(unavailable),
    resetChallenge: () => Effect.fail(unavailable),
    /* There is no background run to submit to without a gateway, and no job
       store to poll: a fabricated handle would send the screen chasing a run
       that does not exist. */
    submitGeneration: () => Effect.fail(unavailable),
    generationJob: () => Effect.fail(unavailable),
    /* The offline adapters have no course store of their own: nothing a learner
       generated in a tab is anywhere a second tab could read it, so the honest
       answer is a typed refusal rather than a list that looks like "none". */
    listCourses: () => Effect.fail(unavailable),
    getCourse: () => Effect.fail(unavailable),
    deleteCourse: () => Effect.fail(unavailable),
    approveCourse: () => Effect.fail(unavailable),
    tutor: () => Effect.fail(unavailable),
    critique: () => Effect.fail(unavailable),
    reportDefect: () => Effect.fail(unavailable),
    decidePatch: () => Effect.fail(unavailable),
  };
}

/* ---------- gateway (http) adapter ---------- */

/** One gateway round trip as the AI seam needs it: the status *and* the body,
    so a non-2xx can be turned into an outcome instead of throwing. */
export interface AiPerformResult {
  status: number;
  body: unknown;
}

/** Per-call transport overrides. A provider-backed call runs for as long as
    the gateway allows it to, which is minutes, so it cannot share the query
    path's deadline. */
export interface AiPerformOptions {
  /** Deadline for this one round trip. Defaults to the transport's own. */
  timeoutMs?: number;
  /** Attempts after the first. */
  retries?: number;
}

type AiPerform = (
  method: string,
  path: string,
  body?: unknown,
  opts?: AiPerformOptions,
) => Effect.Effect<AiPerformResult, GatewayError>;

interface AiGatewayOptions {
  perform: AiPerform;
  /** Fired when the gateway reports the session is gone, so the token is
      cleared and the app returns to sign-in. */
  onUnauthorized?: () => void;
  /** The deadline a provider-backed call may take: the gateway's advertised run
      budget plus slack. A transport that does not advertise one leaves this
      undefined and the call keeps the transport's own deadline. */
  providerDeadlineMs?: () => number | undefined;
}

function asAiErrorCode(code: string | null | undefined): AiErrorCode {
  return code && isAiErrorCode(code) ? code : 'ENVIRONMENT_UNAVAILABLE';
}

function asAdminAIConfig(body: unknown): AdminAIConfig {
  const raw = isRecord(body) ? body : {};
  const provider = raw.provider;
  return {
    ok: raw.ok === true,
    provider: provider === 'fake' || provider === 'openai-compatible' || provider === 'pi-ai' ? provider : 'off',
    base_url: typeof raw.base_url === 'string' ? raw.base_url : undefined,
    model: typeof raw.model === 'string' ? raw.model : undefined,
    key_set: raw.key_set === true,
    timeout_ms: typeof raw.timeout_ms === 'number' ? raw.timeout_ms : 0,
    source: raw.source === 'database' ? 'database' : 'boot',
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function envelopeCode(body: unknown): string | null {
  const err = (body as { error?: { code?: unknown } } | null)?.error;
  return typeof err?.code === 'string' ? err.code : null;
}

function envelopeMessage(body: unknown): string | null {
  const err = (body as { error?: { message?: unknown } } | null)?.error;
  return typeof err?.message === 'string' ? err.message : null;
}

function envelopeDetail(body: unknown): string | null {
  const err = (body as { error?: { detail?: unknown } } | null)?.error;
  return typeof err?.detail === 'string' ? err.detail : null;
}

function statusWord(status: number): string {
  return status === 0 ? 'unreachable' : `HTTP ${status}`;
}

/* ---------- parsing the contract (tolerant, never inventing) ---------- */

function asAiAvailability(value: unknown): AiAvailability | null {
  if (!isRecord(value)) return null;
  if (typeof value.configured !== 'boolean' || typeof value.available !== 'boolean') return null;
  return {
    configured: value.configured,
    available: value.available,
    provider: typeof value.provider === 'string' ? value.provider : '',
    model: typeof value.model === 'string' ? value.model : undefined,
    deterministic: typeof value.deterministic === 'boolean' ? value.deterministic : undefined,
    reason: typeof value.reason === 'string' ? value.reason : '',
    timeout_ms:
      typeof value.timeout_ms === 'number' && Number.isFinite(value.timeout_ms) && value.timeout_ms > 0
        ? value.timeout_ms
        : undefined,
  };
}

/** The `ai` object of GET /capabilities. A gateway that does not report one is
    honestly described as not offering AI rather than assumed available. */
export function availabilityFromCapabilities(body: unknown): AiAvailability {
  const advertised = isRecord(body) ? asAiAvailability((body as { ai?: unknown }).ai) : null;
  return advertised ?? unavailableAvailability('The gateway did not report an AI capability.');
}

function asStages(value: unknown): AiStage[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((raw): AiStage[] => {
    if (!isRecord(raw) || typeof raw.name !== 'string') return [];
    const state = raw.state;
    if (state !== 'done' && state !== 'failed' && state !== 'skipped') return [];
    return [
      {
        name: raw.name,
        state,
        detail: typeof raw.detail === 'string' ? raw.detail : undefined,
        ms: typeof raw.ms === 'number' ? raw.ms : undefined,
      },
    ];
  });
}

function asAttempts(value: unknown): AiAttempt[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((raw): AiAttempt[] => {
    if (!isRecord(raw) || typeof raw.detail !== 'string') return [];
    const number = typeof raw.number === 'number' ? raw.number : 0;
    return [{ number, stage: typeof raw.stage === 'string' ? raw.stage : '', detail: raw.detail }];
  });
}

function asEvidence(value: unknown): AiEvidence | undefined {
  if (!isRecord(value)) return undefined;
  const num = (k: string): number | undefined => (typeof value[k] === 'number' ? (value[k] as number) : undefined);
  return {
    reference_rows: num('reference_rows'),
    cross_check_rows: num('cross_check_rows'),
    fixtures_rejected: num('fixtures_rejected'),
    fixtures_total: num('fixtures_total'),
    repairs: num('repairs'),
    scratch_database: typeof value.scratch_database === 'string' ? value.scratch_database : undefined,
  };
}

/** An artifact only counts when it carries everything the contract marks
    required — an id, a title, a difficulty, a reference solution, a result
    contract and a provenance. Anything less reads as absent, so a half-valid
    artifact is never rendered as if it had passed the pipeline. */
function asAiArtifact(value: unknown): AiArtifact | null {
  if (!isRecord(value)) return null;
  if (typeof value.id !== 'string' || typeof value.title !== 'string') return null;
  if (typeof value.prompt !== 'string' || typeof value.reference_solution !== 'string') return null;
  if (typeof value.version !== 'number') return null;
  const difficulty = value.difficulty;
  if (difficulty !== 'beginner' && difficulty !== 'intermediate' && difficulty !== 'advanced') return null;
  const contract = value.result_contract;
  if (!isRecord(contract) || !Array.isArray(contract.columns)) return null;
  if (!isRecord(value.provenance)) return null;
  const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
  return {
    ...(value as unknown as AiArtifact),
    difficulty,
    objectives: strings(value.objectives),
    hints: strings(value.hints),
  };
}

function asAiError(value: unknown): AiError | null {
  if (!isRecord(value)) return null;
  const code = typeof value.code === 'string' ? value.code : null;
  if (!code || typeof value.message !== 'string') return null;
  return {
    code: asAiErrorCode(code),
    stage: typeof value.stage === 'string' ? value.stage : undefined,
    message: value.message,
    detail: typeof value.detail === 'string' ? value.detail : null,
  };
}

/** A generation outcome, honoured only when it names a status the contract
    defines. Malformed bodies are rejected so the caller builds an honest
    failure instead of rendering a half-parsed artifact. */
function asAiOutcome(value: unknown): AiOutcome | null {
  if (!isRecord(value)) return null;
  const status = value.status;
  if (
    status !== 'active' &&
    status !== 'rejected' &&
    status !== 'unavailable' &&
    status !== 'failed' &&
    status !== 'awaiting_approval'
  ) {
    return null;
  }
  return {
    status,
    kind: value.kind === 'challenge' || value.kind === 'course' ? value.kind : undefined,
    artifact: asAiArtifact(value.artifact),
    /* A course carries a plan instead of an artifact, and a refusal carries the
       per-exercise report. Both are read here rather than dropped: a parser
       that ignored `plan` turned an activated, verified, stored course into
       "the course was rejected" in front of the learner. */
    plan: asCoursePlan(value.plan),
    chapters: asChapterResults(value.chapters),
    stages: asStages(value.stages),
    attempts: asAttempts(value.attempts),
    evidence: asEvidence(value.evidence),
    error: asAiError(value.error),
  };
}

/* ---------- M7: a generated course ---------- */

/* A plan is present only on a course that was activated, and everything in it
   is either the pipeline's verified structure or the engine's own measured rows
   — so this parser is the one place that decides the client can render a course
   at all. It is strict for that reason: a plan it cannot read in full is
   reported as unreadable, never as a half-course. The measured `expected` rows
   are carried through verbatim and are never synthesised: a missing answer
   stays missing, so the exercise is not graded against a guess. */

const stringsOf = (value: unknown): string[] =>
  Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : [];

const difficultyOf = (value: unknown): AiDifficulty | null =>
  value === 'beginner' || value === 'intermediate' || value === 'advanced' ? value : null;

/** The engine's own answer for an exercise's reference solution. Both halves
    are required: columns without rows would render as a table that returned
    nothing, which is a claim the engine never made. */
function asExpectedResult(value: unknown): { columns: string[]; rows: unknown[][] } | null {
  if (!isRecord(value) || !Array.isArray(value.columns) || !Array.isArray(value.rows)) return null;
  const columns = stringsOf(value.columns);
  if (columns.length !== value.columns.length) return null;
  return { columns, rows: value.rows.filter((row): row is unknown[] => Array.isArray(row)) };
}

function asCourseSection(value: unknown): AiCourseSection | null {
  if (!isRecord(value) || typeof value.title !== 'string' || typeof value.body !== 'string') return null;
  return { title: value.title, body: value.body };
}

function asCourseExercise(value: unknown): AiCourseExercise | null {
  if (!isRecord(value)) return null;
  if (typeof value.id !== 'string' || typeof value.title !== 'string' || typeof value.prompt !== 'string') return null;
  if (typeof value.reference_solution !== 'string') return null;
  const difficulty = difficultyOf(value.difficulty);
  if (!difficulty) return null;
  const contract = value.result_contract;
  if (!isRecord(contract) || !Array.isArray(contract.columns)) return null;
  const expected = asExpectedResult(value.expected);
  return {
    id: value.id,
    title: value.title,
    prompt: value.prompt,
    difficulty,
    type: typeof value.type === 'string' ? value.type : undefined,
    objectives: stringsOf(value.objectives),
    hints: stringsOf(value.hints),
    reference_solution: value.reference_solution,
    cross_check_solution: typeof value.cross_check_solution === 'string' ? value.cross_check_solution : '',
    result_contract: contract as unknown as AiResultContract,
    checks: Array.isArray(value.checks) ? (value.checks as AiCheck[]) : undefined,
    expected: expected ?? undefined,
  };
}

function asCourseChapter(value: unknown): AiCourseChapter | null {
  if (!isRecord(value) || typeof value.id !== 'string' || typeof value.title !== 'string') return null;
  const sections = Array.isArray(value.sections) ? value.sections.map(asCourseSection) : [];
  const exercises = Array.isArray(value.exercises) ? value.exercises.map(asCourseExercise) : [];
  if (sections.some((section) => section === null) || exercises.some((exercise) => exercise === null)) return null;
  return {
    id: value.id,
    n: typeof value.n === 'number' ? value.n : undefined,
    title: value.title,
    summary: typeof value.summary === 'string' ? value.summary : '',
    kind: typeof value.kind === 'string' ? value.kind : undefined,
    minutes: typeof value.minutes === 'number' ? value.minutes : undefined,
    objectives: stringsOf(value.objectives),
    sections: sections as AiCourseSection[],
    exercises: exercises as AiCourseExercise[],
  };
}

/** The activated course plan, or null when the body does not carry a whole one.
    A chapter with no readable exercises is not a chapter this client will show,
    because a course whose exercises were not verified is exactly what the
    pipeline refuses to activate. */
function asCoursePlan(value: unknown): AiCoursePlan | null {
  if (!isRecord(value)) return null;
  if (typeof value.id !== 'string' || typeof value.title !== 'string' || typeof value.version !== 'number') return null;
  if (!isRecord(value.setup) || !isRecord(value.provenance)) return null;
  if (!Array.isArray(value.chapters)) return null;
  const chapters = value.chapters.map(asCourseChapter);
  if (chapters.length === 0 || chapters.some((chapter) => chapter === null)) return null;
  return {
    id: value.id,
    version: value.version,
    title: value.title,
    subtitle: typeof value.subtitle === 'string' ? value.subtitle : '',
    level: typeof value.level === 'string' ? value.level : '',
    dialect: value.dialect === 'postgresql' ? 'postgresql' : undefined,
    objectives: stringsOf(value.objectives),
    setup: value.setup as unknown as AiSetupPlan,
    chapters: chapters as AiCourseChapter[],
    provenance: value.provenance as unknown as AiProvenance,
  };
}

function asExerciseResult(value: unknown): AiExerciseResult | null {
  if (!isRecord(value) || typeof value.id !== 'string' || typeof value.status !== 'string') return null;
  return {
    id: value.id,
    title: typeof value.title === 'string' ? value.title : '',
    status: value.status,
    reference_rows: typeof value.reference_rows === 'number' ? value.reference_rows : undefined,
    cross_check_rows: typeof value.cross_check_rows === 'number' ? value.cross_check_rows : undefined,
    fixtures_rejected: typeof value.fixtures_rejected === 'number' ? value.fixtures_rejected : undefined,
    fixtures_total: typeof value.fixtures_total === 'number' ? value.fixtures_total : undefined,
    ms: typeof value.ms === 'number' ? value.ms : undefined,
    error: asAiError(value.error) ?? undefined,
  };
}

/** The per-chapter verification report. It exists on a refusal too — "which
    exercise failed and what the engine said" is the useful half of a rejection
    — so an unreadable chapter is skipped rather than failing the whole report. */
function asChapterResults(value: unknown): AiChapterResult[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((raw): AiChapterResult[] => {
    if (!isRecord(raw) || typeof raw.id !== 'string') return [];
    const exercises = Array.isArray(raw.exercises)
      ? raw.exercises.map(asExerciseResult).filter((exercise): exercise is AiExerciseResult => exercise !== null)
      : [];
    return [{ id: raw.id, title: typeof raw.title === 'string' ? raw.title : '', exercises }];
  });
}

function asAiRecord(value: unknown): AiRecord | null {
  if (!isRecord(value)) return null;
  const artifact = asAiArtifact(value.artifact);
  if (!artifact) return null;
  const status = value.status === 'rejected' ? 'rejected' : 'active';
  return {
    artifact,
    outcome: asAiOutcome(value.outcome) ?? unavailableOutcome('challenge'),
    status,
    owner_id: typeof value.owner_id === 'string' ? value.owner_id : undefined,
    created_at: typeof value.created_at === 'string' ? value.created_at : '',
  };
}

function asAiRecords(value: unknown): AiRecord[] {
  if (!Array.isArray(value)) return [];
  const out: AiRecord[] = [];
  for (const raw of value) {
    const record = asAiRecord(raw);
    if (record) out.push(record);
  }
  return out;
}

/* ---------- M8: the learner's stored courses ---------- */

/** The array a list route carries: the envelope's own member when the gateway
    wraps it (`{ok, courses: [...]}`), or the body when it is already the list. */
function listOf(body: unknown, key: string): unknown[] {
  if (Array.isArray(body)) return body;
  if (isRecord(body) && Array.isArray(body[key])) return body[key] as unknown[];
  return [];
}

/** The record a single-item route carries: the envelope's member when it wraps
    one (`{ok, course: {...}}`), or the body when it is already the record. */
function memberOf(body: unknown, key: string): unknown {
  if (isRecord(body) && isRecord(body[key])) return body[key];
  return body;
}

/**
 * One stored course, or null when its plan will not parse.
 *
 * The plan is what the lesson page renders and what the grader compares a
 * learner's rows against, so a record whose plan cannot be read in full is
 * skipped rather than rendered with missing chapters: a half-course is worse
 * than no course, because nothing on screen would say which half was missing.
 *
 * `status` is carried through verbatim, because it is what separates a finished
 * course from one the pipeline is still writing chapter by chapter. A
 * `building` record is readable on purpose — its plan carries the chapters that
 * already verified, and its `outcome.chapters` says which ones those are.
 */
export function asCourseRecord(body: unknown): AiCourseRecord | null {
  const value = memberOf(body, 'course');
  if (!isRecord(value)) return null;
  const plan = asCoursePlan(value.plan);
  if (!plan) return null;
  /* The run report is evidence, not decoration: a record whose report is
     unreadable keeps its plan and is reported as an unreadable report, never as
     a verification and never as "the lab is off". */
  const outcome: AiOutcome =
    asAiOutcome(value.outcome) ?? {
      status: 'failed',
      kind: 'course',
      artifact: null,
      stages: [],
      attempts: [],
      error: { code: 'AI_INVALID_ARTIFACT', message: 'The stored course carried no readable run report.' },
    };
  return {
    plan,
    outcome,
    status: typeof value.status === 'string' && value.status.trim() ? value.status : 'active',
    owner_id: typeof value.owner_id === 'string' ? value.owner_id : undefined,
    created_at: typeof value.created_at === 'string' ? value.created_at : '',
  };
}

/** Every readable course in a list body. A record that will not parse is
    dropped; the rest are kept, so one corrupt row cannot hide the others. */
export function asCourseRecords(body: unknown): AiCourseRecord[] {
  const out: AiCourseRecord[] = [];
  for (const raw of listOf(body, 'courses')) {
    const record = asCourseRecord(raw);
    if (record) out.push(record);
  }
  return out;
}

/* Terminal job states are the ones a job shares with an outcome envelope, plus
   the two course-run states that end a run without activating a course:
   `awaiting_approval` (paused on its outline) and `discarded` (the paused
   course was removed). Any other value — including one this build has never
   heard of — is read as "still going", so an unknown word from a newer gateway
   keeps the poller polling rather than being mistaken for a finish. */
const JOB_TERMINAL_STATUSES = [
  'active',
  'rejected',
  'unavailable',
  'failed',
  'awaiting_approval',
  'discarded',
] as const;

type JobTerminalStatus = (typeof JOB_TERMINAL_STATUSES)[number];

function isTerminalJobStatus(status: string): status is JobTerminalStatus {
  return JOB_TERMINAL_STATUSES.some((known) => known === status);
}

export function jobIsRunning(job: { status: string }): boolean {
  return !isTerminalJobStatus(job.status);
}

/* The words a job's own report uses for one stage, as `AiJobStage.state` fixes
   them. Stated once so the guard and the contract type cannot drift apart. */
const JOB_STAGE_STATES = ['queued', 'running', 'done', 'failed', 'skipped'] as const;

type JobStageState = (typeof JOB_STAGE_STATES)[number];

function isJobStageState(state: string): state is JobStageState {
  return JOB_STAGE_STATES.some((known) => known === state);
}

/** One stage of a live job view, or null when it carries no name. A stage
    without a name cannot be matched to the step it belongs to, so it is
    dropped rather than shown as an unnamed step. */
function asJobStage(value: unknown): AiJobStage | null {
  if (!isRecord(value)) return null;
  const name = typeof value.name === 'string' ? value.name : '';
  if (!name) return null;
  const state = typeof value.state === 'string' ? value.state : 'queued';
  return {
    name,
    state: isJobStageState(state) ? state : 'queued',
    detail: typeof value.detail === 'string' ? value.detail : undefined,
    ms: typeof value.ms === 'number' ? value.ms : undefined,
  };
}

/** The handle a submission answers with, or null when the gateway answered with
    something else — the blocking envelope, which has no job to poll. */
function asJobHandle(body: unknown): AiJobHandle | null {
  const job = isRecord(body) ? body.job : undefined;
  if (!isRecord(job)) return null;
  const id = typeof job.id === 'string' ? job.id : '';
  if (!id) return null;
  const kind = job.kind === 'challenge' ? 'challenge' : 'course';
  return { id, kind, status: typeof job.status === 'string' ? job.status : 'running' };
}

/**
 * A job view, or null when the body carries none.
 *
 * The job is a report about a run, not the run's artifact: nothing here is
 * promoted to a course or an outcome. `chapters_verified` is left undefined
 * when the gateway omits it, because "no chapters verified yet" and "this body
 * does not say" are different facts, and a screen that prints a count should
 * print the one it was given.
 */
function asJob(body: unknown): AiJob | null {
  const job = memberOf(body, 'job');
  if (!isRecord(job)) return null;
  const id = typeof job.id === 'string' ? job.id : '';
  if (!id) return null;
  const stages: AiJobStage[] = [];
  for (const raw of Array.isArray(job.stages) ? job.stages : []) {
    const stage = asJobStage(raw);
    if (stage) stages.push(stage);
  }
  const attempts: AiAttempt[] = [];
  for (const raw of Array.isArray(job.attempts) ? job.attempts : []) {
    if (isRecord(raw) && typeof raw.number === 'number' && typeof raw.stage === 'string' && typeof raw.detail === 'string') {
      attempts.push({ number: raw.number, stage: raw.stage, detail: raw.detail });
    }
  }
  const error = asAiError(job.error);
  return {
    id,
    kind: job.kind === 'challenge' ? 'challenge' : 'course',
    status: typeof job.status === 'string' ? job.status : 'running',
    stages,
    attempts,
    chapters: Array.isArray(job.chapters) ? asChapterResults(job.chapters) : undefined,
    chapters_planned: typeof job.chapters_planned === 'number' ? job.chapters_planned : undefined,
    chapters_verified: typeof job.chapters_verified === 'number' ? job.chapters_verified : undefined,
    current_chapter: typeof job.current_chapter === 'string' ? job.current_chapter : undefined,
    error: error ?? null,
    course_id: typeof job.course_id === 'string' && job.course_id ? job.course_id : undefined,
    challenge_id: typeof job.challenge_id === 'string' && job.challenge_id ? job.challenge_id : undefined,
    created_at: typeof job.created_at === 'string' ? job.created_at : undefined,
    finished_at: typeof job.finished_at === 'string' ? job.finished_at : undefined,
  };
}

/** The settled states a finished job shares with an outcome envelope, plus the
    two course-run states that stopped the run without activating a course.
    Derived from the one terminal tuple; `discarded` is handled separately in
    `outcomeFromJob`, which reads it as a refusal. */
const SETTLED_JOB_STATES: readonly string[] = JOB_TERMINAL_STATUSES.filter((s) => s !== 'discarded');

/**
 * Assemble the outcome an adoption path already understands from a finished job
 * and the course it stored.
 *
 * A job is a view of a run, so it carries no plan; the plan is in the stored
 * record the job names. Nothing here is inferred — a run that stored no course
 * yields an outcome with no plan, and the screens read that as the refusal it
 * is. The stored record is the durable fact, so an active record makes the
 * outcome active even if the job's last word for the run was something else.
 */
export function outcomeFromJob(job: AiJob, record: AiCourseRecord | null): AiOutcome {
  const stored = record?.outcome ?? null;
  const status: AiOutcome['status'] =
    record && record.status === 'active' && record.plan
      ? 'active'
      : /* A discarded run is a course the learner removed: nothing was
           activated, and the honest outcome is a refusal, not an unknown. */
        job.status === 'discarded'
        ? 'rejected'
        : SETTLED_JOB_STATES.includes(job.status)
          ? (job.status as AiOutcome['status'])
          : 'failed';
  return {
    status,
    kind: 'course',
    artifact: stored?.artifact ?? null,
    plan: record?.plan ?? null,
    chapters: job.chapters ?? stored?.chapters,
    /* Only settled stages belong in an outcome. A stage still sitting at
       `queued` or `running` in a finished run's report was never reported on,
       and naming a state for it here would be a claim this tab cannot back.
       Dropping it leaves the checklist where it honestly is. */
    stages: job.stages.flatMap((s) =>
      s.state === 'queued' || s.state === 'running' ? [] : [{ name: s.name, state: s.state, detail: s.detail, ms: s.ms }]
    ),
    attempts: job.attempts,
    evidence: stored?.evidence,
    error: job.error ?? stored?.error ?? null,
  };
}

/**
 * Turn a gateway response into an outcome without ever inventing an artifact.
 *
 * A body that already parses as an outcome is used verbatim — the gateway
 * reports a rejected or unavailable generation that way, including on a non-2xx
 * that still carries the envelope. Otherwise the envelope's own code and message
 * are surfaced, with `unavailable` reserved for AI_UNAVAILABLE.
 */
function outcomeFromResponse(status: number, body: unknown): AiOutcome {
  const parsed = asAiOutcome(body);
  if (parsed) return parsed;

  const code = status >= 400 ? asAiErrorCode(envelopeCode(body)) : 'AI_INVALID_ARTIFACT';
  const message =
    envelopeMessage(body) ??
    (status >= 400
      ? `The gateway rejected the generation (${statusWord(status)}).`
      : 'The gateway did not return a generation outcome.');
  const error: AiError = { code, message, detail: envelopeDetail(body) };
  return {
    status: code === 'AI_UNAVAILABLE' ? 'unavailable' : 'failed',
    artifact: null,
    stages: [],
    attempts: [],
    error,
  };
}

/** A transport-level failure (no response, timeout, unreadable body). */
function outcomeFromError(e: unknown): AiOutcome {
  const err = e instanceof GatewayError ? e : asGatewayError(e);
  const code = asAiErrorCode(err.code);
  return {
    status: code === 'AI_UNAVAILABLE' ? 'unavailable' : 'failed',
    artifact: null,
    stages: [],
    attempts: [],
    error: { code, message: err.message, detail: err.detail ?? null },
  };
}

function failureFor(status: number, body: unknown, label: string): GatewayError {
  return new GatewayError({
    code: envelopeCode(body) ?? 'ENVIRONMENT_UNAVAILABLE',
    message: envelopeMessage(body) ?? `The gateway rejected the ${label} (${statusWord(status)}).`,
    detail: envelopeDetail(body),
    status,
  });
}

function malformed(label: string): GatewayError {
  return new GatewayError({ code: 'AI_INVALID_ARTIFACT', message: `The gateway returned an unreadable ${label}.` });
}

/**
 * The gateway implementation of the AI operations.
 *
 * Every call rides the shared transport, so the bearer token, retry and timeout
 * behave exactly as they do for `/query`. Generation always resolves to an
 * outcome — a rejected or unreachable provider is data the screen renders, not
 * an exception — while the non-outcome calls keep the typed error channel.
 */
export function gatewayAiMethods(opts: AiGatewayOptions): AiServiceShape {
  const { perform } = opts;
  const onUnauthorized = opts.onUnauthorized ?? (() => {});
  const providerDeadline = opts.providerDeadlineMs ?? (() => undefined);

  /* A provider-backed call gets the gateway's own run budget and exactly one
     attempt. Retrying used to be free because the timeout was short; now that
     a call legitimately runs for minutes, a retry would ask the model to
     generate a whole second course after the first one already answered. */
  const longCall = (): AiPerformOptions => ({ timeoutMs: providerDeadline(), retries: 0 });

  const noteSessionGone = (status: number, body: unknown): void => {
    if (status !== 401) return;
    if ((envelopeCode(body) ?? 'AUTH_REQUIRED') === 'AUTH_REQUIRED') onUnauthorized();
  };

  const generate = (path: string, req: AiGenerationRequest, kind: 'challenge' | 'course') =>
    perform('POST', path, { ...req, kind }, longCall()).pipe(
      Effect.tap((r) => Effect.sync(() => noteSessionGone(r.status, r.body))),
      Effect.map((r) => outcomeFromResponse(r.status, r.body)),
      Effect.catch((e) => Effect.succeed(outcomeFromError(e))),
    );

  /* `provider` marks the calls that wait on a model rather than on a table: the
     tutor, the critique and the defect report each make a provider call, so
     they share the run budget instead of the query deadline. */
  const requireOk = (method: string, path: string, body: unknown, label: string, provider = false) =>
    perform(method, path, body, provider ? longCall() : undefined).pipe(
      Effect.tap((r) => Effect.sync(() => noteSessionGone(r.status, r.body))),
      Effect.flatMap((r) => (r.status >= 400 ? Effect.fail(failureFor(r.status, r.body, label)) : Effect.succeed(r.body))),
    );

  return {
    aiAvailability: () =>
      perform('GET', '/capabilities').pipe(
        Effect.map((r) => (r.status >= 400 ? unavailableAvailability('The gateway did not report an AI capability.') : availabilityFromCapabilities(r.body))),
        Effect.catch((e) =>
          Effect.succeed(unavailableAvailability(`The gateway could not be reached: ${e instanceof Error ? e.message : String(e)}`)),
        ),
      ),
    generateChallenge: (req) => generate('/challenges/generate', req, 'challenge'),
    generateCourse: (req) => generate('/courses/generate', req, 'course'),
    listChallenges: () =>
      requireOk('GET', '/challenges', undefined, 'challenge list').pipe(Effect.map(asAiRecords)),
    getChallenge: (id) =>
      requireOk('GET', `/challenges/${encodeURIComponent(id)}`, undefined, 'challenge').pipe(
        Effect.catch((e) => {
          const err = asGatewayError(e);
          if (err.code === 'AI_NOT_FOUND' || err.status === 404) return Effect.succeed(null);
          return Effect.fail(err);
        }),
        /* The {ok, challenge} envelope, matching GET /courses/{id}. */
        Effect.map((body) => {
          if (body === null) return null;
          const inner = isRecord(body) && 'challenge' in body ? body.challenge : body;
          return asAiRecord(inner);
        }),
      ),
    resetChallenge: (id) =>
      requireOk('POST', `/challenges/${encodeURIComponent(id)}/reset`, {}, 'challenge reset').pipe(
        Effect.map((body) => ({ ok: isRecord(body) && body.ok === true })),
      ),
    /* A submission returns as soon as the run is accepted, so it keeps the
       transport deadline rather than the provider's run budget: the request the
       gateway holds open is the *blocking* one, and this is not that. */
    submitGeneration: (req, kind) =>
      requireOk('POST', `${kind === 'course' ? '/courses' : '/challenges'}/generate?async=1`, { ...req, kind }, 'generation submission').pipe(
        Effect.map((body) => asJobHandle(body)),
      ),
    generationJob: (id) =>
      requireOk('GET', `/generation/${encodeURIComponent(id)}`, undefined, 'generation job').pipe(
        Effect.catch((e) => {
          const err = asGatewayError(e);
          if (err.code === 'AI_NOT_FOUND' || err.status === 404) return Effect.succeed(null);
          return Effect.fail(err);
        }),
        Effect.map((body) => (body === null ? null : asJob(body))),
      ),
    /* Stored courses. A read is not a provider call, so it keeps the transport
       deadline; a record that will not parse is dropped by the decoder rather
       than surfaced as a course with pieces missing. */
    listCourses: () =>
      requireOk('GET', '/courses', undefined, 'course list').pipe(Effect.map(asCourseRecords)),
    adminCourses: () =>
      requireOk('GET', '/admin/courses', undefined, 'admin course list').pipe(Effect.map(asCourseRecords)),
    adminAI: () =>
      requireOk('GET', '/admin/ai', undefined, 'admin AI read').pipe(
        Effect.map((body) => asAdminAIConfig(body)),
      ),
    saveAdminAI: (update) =>
      requireOk('PUT', '/admin/ai', update, 'admin AI save').pipe(
        Effect.map((body) => asAdminAIConfig(body)),
      ),
    adminAIModels: () =>
      requireOk('GET', '/admin/ai/models', undefined, 'admin model catalog').pipe(
        Effect.map((body) => {
          const raw = isRecord(body) ? body.models : null;
          if (!Array.isArray(raw)) return [];
          return raw.flatMap((e) => {
            if (!isRecord(e) || typeof e.provider !== 'string' || typeof e.model !== 'string') return [];
            return [{ provider: e.provider, model: e.model, name: typeof e.name === 'string' ? e.name : '' }];
          });
        }),
      ),
    getCourse: (id) =>
      requireOk('GET', `/courses/${encodeURIComponent(id)}`, undefined, 'course').pipe(
        Effect.catch((e) => {
          const err = asGatewayError(e);
          if (err.code === 'AI_NOT_FOUND' || err.status === 404) return Effect.succeed(null);
          return Effect.fail(err);
        }),
        Effect.map((body) => (body === null ? null : asCourseRecord(body))),
      ),
    deleteCourse: (id) =>
      requireOk('DELETE', `/courses/${encodeURIComponent(id)}`, undefined, 'course delete').pipe(
        Effect.map((body) => ({ ok: isRecord(body) && body.ok === true })),
      ),
    /* The approval is minutes of work, so it asks for a job first and falls
       back to the blocking envelope, exactly as a submission does. It rides the
       provider deadline, because a gateway that answers the blocking ask holds
       the request for the whole chapter phase. */
    approveCourse: (id) =>
      perform('POST', `/courses/${encodeURIComponent(id)}/build?async=1`, undefined, longCall()).pipe(
        Effect.tap((r) => Effect.sync(() => noteSessionGone(r.status, r.body))),
        Effect.flatMap((r) => {
          const handle = asJobHandle(r.body);
          if (handle) return Effect.succeed<AiCourseBuildAnswer>({ job: handle, outcome: null });
          /* A gateway that answered the blocking ask returns the outcome
             envelope, which is read verbatim (a refusal is data, not an
             exception) — the same rule `generate` follows. Only a body with no
             outcome at all (404 for a course that is gone, 409 for one that is
             not awaiting approval) becomes a typed failure. */
          const outcome = asAiOutcome(r.body);
          if (outcome) return Effect.succeed<AiCourseBuildAnswer>({ job: null, outcome });
          if (r.status >= 400) return Effect.fail(failureFor(r.status, r.body, 'course build'));
          return Effect.succeed<AiCourseBuildAnswer>({
            job: null,
            outcome: outcomeFromResponse(r.status, r.body),
          });
        }),
      ),
    tutor: (req) =>
      requireOk('POST', '/tutor', req, 'tutor response', true).pipe(
        Effect.flatMap((body) => {
          const parsed = asTutorResponse(body);
          return parsed ? Effect.succeed(parsed) : Effect.fail(malformed('tutor response'));
        }),
      ),
    critique: (challengeId, req) =>
      requireOk('POST', `/challenges/${encodeURIComponent(challengeId)}/critique`, req, 'critique', true).pipe(
        Effect.flatMap((body) => {
          const parsed = asCritiqueResponse(body);
          return parsed ? Effect.succeed(parsed) : Effect.fail(malformed('critique'));
        }),
      ),
    reportDefect: (req) =>
      requireOk('POST', `/challenges/${encodeURIComponent(req.challenge_id)}/report`, req, 'defect report', true).pipe(
        Effect.flatMap((body) => {
          const parsed = asDefectVerdict(body);
          return parsed ? Effect.succeed(parsed) : Effect.fail(malformed('defect verdict'));
        }),
      ),
    decidePatch: (patchId, approved) =>
      requireOk('POST', patchPath(patchId), { approved, approved_by: readApprover() }, 'patch decision').pipe(
        Effect.map((body) => ({ applied: isRecord(body) && body.applied === true })),
      ),
  };
}

/* The route is /challenges/{id}/patches/{patchID}; the seam carries only the
   patch id, so a caller that knows the challenge passes "<challengeId>/<patchId>".
   A bare patch id is used for both segments — the gateway resolves the patch by
   its own id, which is unique in its store. */
function patchPath(ref: string): string {
  const slash = ref.indexOf('/');
  const challengeId = slash === -1 ? ref : ref.slice(0, slash);
  const patchId = slash === -1 ? ref : ref.slice(slash + 1);
  return `/challenges/${encodeURIComponent(challengeId)}/patches/${encodeURIComponent(patchId)}`;
}

/* The approver is the signed-in learner. The gateway attributes the approval to
   the bearer token; `approved_by` is the contract's required echo of that. The
   id is kept in local storage at sign-in, and `readAuthUser` carries the
   storage guards. */
function readApprover(): string {
  return readAuthUser() ?? 'learner';
}

function asTutorResponse(value: unknown): AiTutorResponse | null {
  if (!isRecord(value) || typeof value.message !== 'string') return null;
  const level = typeof value.hint_level === 'number' ? value.hint_level : 0;
  return {
    message: value.message,
    hint_level: level,
    reveals_solution: value.reveals_solution === true,
    provenance: isRecord(value.provenance) ? (value.provenance as unknown as AiTutorResponse['provenance']) : undefined,
  };
}

function asCritiqueResponse(value: unknown): AiCritiqueResponse | null {
  if (!isRecord(value) || typeof value.message !== 'string') return null;
  return {
    message: value.message,
    notes: Array.isArray(value.notes) ? value.notes.filter((n): n is string => typeof n === 'string') : [],
    provenance: isRecord(value.provenance) ? (value.provenance as unknown as AiCritiqueResponse['provenance']) : undefined,
  };
}

function asDefectVerdict(value: unknown): AiDefectVerdict | null {
  if (!isRecord(value) || typeof value.summary !== 'string') return null;
  return {
    verified: value.verified === true,
    applied: value.applied === true,
    summary: value.summary,
    patch: isRecord(value.patch) ? (value.patch as unknown as AiDefectVerdict['patch']) : null,
    evidence: asEvidence(value.evidence),
    error: asAiError(value.error),
  };
}
