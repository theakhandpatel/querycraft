import { Effect, Option, Schema } from 'effect';
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
  AiCourseEdits,
  AiCourseRecord,
  AiCourseChapter,
  AiCourseExercise,
  AiCoursePlan,
  AiCourseSection,
  AiCritiqueResponse,
  AiDefectReport,
  AiDefectVerdict,
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
  AiRunEntry,
  AiRunSummary,
  AiRunTranscript,
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
  /* Edit a stored course's authored fields. Only the title, the subtitle and a
     chapter's title and summary can change; the gateway refuses an unknown
     chapter id and a body that changes nothing. Answers with the stored record. */
  updateCourse(id: string, edits: AiCourseEdits): Effect.Effect<AiCourseRecord, GatewayError>;
  /* Approve a course that stopped awaiting the outline's approval: continue the
     stored outline into its chapter phase. Answers with a job to poll when the
     gateway ran it in the background, or with the blocking outcome. */
  approveCourse(id: string): Effect.Effect<AiCourseBuildAnswer, GatewayError>;
  tutor(req: AiTutorRequest): Effect.Effect<AiTutorResponse, GatewayError>;
  critique(challengeId: string, req: { sql: string; objective?: string }): Effect.Effect<AiCritiqueResponse, GatewayError>;
  reportDefect(req: AiDefectReport): Effect.Effect<AiDefectVerdict, GatewayError>;
  decidePatch(patchId: string, approved: boolean): Effect.Effect<{ applied: boolean }, GatewayError>;
  /* M12. The agent-run audit trail: the caller's own runs, newest first, and
     one run's whole transcript. The gateway scopes both to the session, so
     another account's run is invisible (the read answers null, like every other
     owner-scoped route). A list that cannot be read fails rather than answering
     an empty list, because "you have no runs" is a claim the client cannot make
     when it could not reach the store. */
  listRuns(): Effect.Effect<AiRunSummary[], GatewayError>;
  getRun(id: string): Effect.Effect<AiRunTranscript | null, GatewayError>;
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
    updateCourse: () => Effect.fail(unavailable),
    approveCourse: () => Effect.fail(unavailable),
    tutor: () => Effect.fail(unavailable),
    critique: () => Effect.fail(unavailable),
    reportDefect: () => Effect.fail(unavailable),
    decidePatch: () => Effect.fail(unavailable),
    /* A run is a gateway record: the page-local engine never runs the
       generation pipeline, so nobody generated a run here. An empty list is
       the true answer and a named run does not exist to read; neither invents
       a transcript. */
    listRuns: () => Effect.succeed([]),
    getRun: () => Effect.succeed(null),
  };
}

/* ---------- gateway (http) adapter ---------- */

/** One gateway round trip as the AI seam needs it: the status *and* the body,
    so a non-2xx can be turned into an outcome instead of throwing. */
export interface AiPerformResult {
  status: number;
  body: Schema.Json | null;
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

/** The outgoing body of one gateway round trip. The transport only serialises
    it with `JSON.stringify` and never reads a field, so the parameter is
    generic over the caller's own object type. Each caller's domain schema owns
    the shape; the transport states only that it is an object. */
type AiPerform = <Body extends object>(
  method: string,
  path: string,
  body?: Body,
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

/** A JSON object as the gateway's bodies carry one. The predicate names the
    shape every decoder below reads a member out of. */
function isRecord(value: Schema.Json): value is Schema.JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function statusWord(status: number): string {
  return status === 0 ? 'unreachable' : `HTTP ${status}`;
}

/* ---------- shared Schema leaves ---------- */

/* Optional fields the gateway may omit or mis-type. The contract names them
   optional, so a wrong type reads as absent rather than failing the envelope
   that carries it. */
const OptionalString = Schema.optional(Schema.String).pipe(
  Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
);
const OptionalNumber = Schema.optional(Schema.Number).pipe(
  Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
);
const OptionalBoolean = Schema.optional(Schema.Boolean).pipe(
  Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
);

/* Required fields the gateway may omit or mis-type, each carrying the default
   the old hand-written decoder supplied rather than failing the whole shape.
   The default covers a missing key; the catch covers a wrong type. */
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

/* The shared leaves, one per $defs entry in contracts/ai.schema.json. The
   artifact, the course plan and the exercise all decode against these, so a
   shape is defined once instead of re-checked field by field in each decoder. */
const AiResultContractSchema = Schema.Struct({
  columns: Schema.Array(Schema.String),
  column_order: Schema.Literals(['exact', 'ignored']),
  row_order: Schema.Literals(['exact', 'ignored']),
  duplicates: Schema.Literals(['preserved', 'ignored']),
  numeric_tolerance: Schema.Number,
});

const AiSetupPlanSchema = Schema.Struct({
  dataset: Schema.String,
  schema_version: Schema.Number,
  seed_version: Schema.Number,
  statements: Schema.Array(Schema.String),
});

const AiCheckSchema = Schema.Struct({
  label: Schema.String,
  code: Schema.String,
  pattern: OptionalString,
});

/* The gateway's attribution for a reply. Optional on the response, so a body
   that carries no readable one reads as absent rather than half-typed. */
const AiProvenanceSchema = Schema.Struct({
  provider: Schema.String,
  model: Schema.String,
  provider_version: Schema.String,
  pipeline_version: Schema.String,
  evaluator_version: Schema.String,
  dataset_version: Schema.Number,
  created_at: Schema.String,
  repairs: Schema.optional(Schema.Number),
});

/* The error envelope a non-2xx body carries. Every field is optional and read
   through the same optional leaf as the rest, so a body that names no error, or
   names one with a mistyped field, reads as absent rather than failing. */
const EnvelopeErrorSchema = Schema.Struct({
  code: OptionalString,
  message: OptionalString,
  detail: OptionalString,
});

const EnvelopeSchema = Schema.Struct({
  error: Schema.optional(EnvelopeErrorSchema).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
});

function envelopeError(body: Schema.Json): { code?: string; message?: string; detail?: string } | undefined {
  const decoded = Schema.decodeUnknownOption(EnvelopeSchema)(body);
  return Option.isSome(decoded) ? decoded.value.error : undefined;
}

function envelopeCode(body: Schema.Json): string | null {
  return envelopeError(body)?.code ?? null;
}

function envelopeMessage(body: Schema.Json): string | null {
  return envelopeError(body)?.message ?? null;
}

function envelopeDetail(body: Schema.Json): string | null {
  return envelopeError(body)?.detail ?? null;
}

/* ---------- M7: a generated course ---------- */

/* A plan is present only on a course that was activated, and everything in it
   is either the pipeline's verified structure or the engine's own measured rows
   — so this parser is the one place that decides the client can render a course
   at all. It is strict for that reason: a plan it cannot read in full is
   reported as unreadable, never as a half-course. The measured `expected` rows
   are carried through verbatim and are never synthesised: a missing answer
   stays missing, so the exercise is not graded against a guess. */

/** Every string in an array of the gateway's, dropping anything else. A prose
    field is a list of strings; one wrong element must not fail the whole list. */
function stringsOf(value: Schema.Json): string[] {
  if (!Array.isArray(value)) return [];
  const out: string[] = [];
  for (const raw of value) {
    const decoded = Schema.decodeUnknownOption(Schema.String)(raw);
    if (Option.isSome(decoded)) out.push(decoded.value);
  }
  return out;
}

/* One measured row of the engine's own answer. A row that is not a list is
   dropped rather than failing the whole answer, which is what the gateway
   reports. */
const CourseRowSchema = Schema.Array(Schema.Unknown);

const CourseExpectedSchema = Schema.Struct({
  columns: Schema.Array(Schema.String),
  rows: Schema.Array(Schema.Unknown),
});

/** The engine's own answer for an exercise's reference solution. Both halves
    are required: columns without rows would render as a table that returned
    nothing, which is a claim the engine never made. */
function asExpectedResult(value: Schema.Json): { columns: string[]; rows: unknown[][] } | null {
  const decoded = Schema.decodeUnknownOption(CourseExpectedSchema)(value);
  if (Option.isNone(decoded)) return null;
  const rows: unknown[][] = [];
  for (const raw of decoded.value.rows) {
    const row = Schema.decodeUnknownOption(CourseRowSchema)(raw);
    if (Option.isSome(row)) rows.push([...row.value]);
  }
  return { columns: [...decoded.value.columns], rows };
}

const AiCourseSectionSchema = Schema.Struct({
  title: Schema.String,
  body: Schema.String,
});

function asCourseSection(value: Schema.Json): AiCourseSection | null {
  const decoded = Schema.decodeUnknownOption(AiCourseSectionSchema)(value);
  return Option.isSome(decoded) ? decoded.value : null;
}

/* A course exercise. `result_contract` is what the grader compares against, so
   a plan whose contract will not read whole is not a plan this client can grade
   with. `expected` stays separate from the struct so an unmeasured exercise
   keeps none rather than failing the whole course. */
const AiCourseExerciseSchema = Schema.Struct({
  id: Schema.String,
  title: Schema.String,
  prompt: Schema.String,
  difficulty: Schema.Literals(['beginner', 'intermediate', 'advanced']),
  type: OptionalString,
  reference_solution: Schema.String,
  cross_check_solution: stringOr(''),
  result_contract: AiResultContractSchema,
  checks: Schema.optional(Schema.Array(AiCheckSchema)).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
});

function asCourseExercise(value: Schema.Json): AiCourseExercise | null {
  const decoded = Schema.decodeUnknownOption(AiCourseExerciseSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  const exercise = decoded.value;
  return {
    id: exercise.id,
    title: exercise.title,
    prompt: exercise.prompt,
    difficulty: exercise.difficulty,
    type: exercise.type,
    objectives: stringsOf(raw.objectives),
    hints: stringsOf(raw.hints),
    reference_solution: exercise.reference_solution,
    cross_check_solution: exercise.cross_check_solution,
    result_contract: { ...exercise.result_contract, columns: [...exercise.result_contract.columns] },
    checks: exercise.checks ? exercise.checks.map((check) => ({ ...check })) : undefined,
    expected: asExpectedResult(raw.expected) ?? undefined,
  };
}

const AiCourseChapterSchema = Schema.Struct({
  id: Schema.String,
  n: OptionalNumber,
  title: Schema.String,
  summary: stringOr(''),
  kind: OptionalString,
  minutes: OptionalNumber,
});

function asCourseChapter(value: Schema.Json): AiCourseChapter | null {
  const decoded = Schema.decodeUnknownOption(AiCourseChapterSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  const sections = Array.isArray(raw.sections) ? raw.sections.map(asCourseSection) : [];
  const exercises = Array.isArray(raw.exercises) ? raw.exercises.map(asCourseExercise) : [];
  if (sections.some((section) => section === null) || exercises.some((exercise) => exercise === null)) return null;
  const chapter = decoded.value;
  return {
    id: chapter.id,
    n: chapter.n,
    title: chapter.title,
    summary: chapter.summary,
    kind: chapter.kind,
    minutes: chapter.minutes,
    objectives: stringsOf(raw.objectives),
    sections: sections.flatMap((section): AiCourseSection[] => (section === null ? [] : [section])),
    exercises: exercises.flatMap((exercise): AiCourseExercise[] => (exercise === null ? [] : [exercise])),
  };
}

const AiCoursePlanSchema = Schema.Struct({
  id: Schema.String,
  version: Schema.Number,
  title: Schema.String,
  subtitle: stringOr(''),
  level: stringOr(''),
  dialect: Schema.optional(Schema.Literals(['postgresql'])).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
  setup: AiSetupPlanSchema,
  provenance: AiProvenanceSchema,
});

/** The activated course plan, or null when the body does not carry a whole one.
    A chapter with no readable exercises is not a chapter this client will show,
    because a course whose exercises were not verified is exactly what the
    pipeline refuses to activate. */
function asCoursePlan(value: Schema.Json): AiCoursePlan | null {
  const decoded = Schema.decodeUnknownOption(AiCoursePlanSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  if (!Array.isArray(raw.chapters)) return null;
  const chapters = raw.chapters.map(asCourseChapter);
  if (chapters.length === 0 || chapters.some((chapter) => chapter === null)) return null;
  const plan = decoded.value;
  return {
    id: plan.id,
    version: plan.version,
    title: plan.title,
    subtitle: plan.subtitle,
    level: plan.level,
    dialect: plan.dialect,
    objectives: stringsOf(raw.objectives),
    setup: { ...plan.setup, statements: [...plan.setup.statements] },
    chapters: chapters.flatMap((chapter): AiCourseChapter[] => (chapter === null ? [] : [chapter])),
    provenance: { ...plan.provenance },
  };
}

const AiExerciseResultSchema = Schema.Struct({
  id: Schema.String,
  title: stringOr(''),
  status: Schema.String,
  reference_rows: OptionalNumber,
  cross_check_rows: OptionalNumber,
  fixtures_rejected: OptionalNumber,
  fixtures_total: OptionalNumber,
  ms: OptionalNumber,
});

function asExerciseResult(value: Schema.Json): AiExerciseResult | null {
  const decoded = Schema.decodeUnknownOption(AiExerciseResultSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  const result = decoded.value;
  return {
    id: result.id,
    title: result.title,
    status: result.status,
    reference_rows: result.reference_rows,
    cross_check_rows: result.cross_check_rows,
    fixtures_rejected: result.fixtures_rejected,
    fixtures_total: result.fixtures_total,
    ms: result.ms,
    error: asAiError(raw.error) ?? undefined,
  };
}

const AiChapterResultSchema = Schema.Struct({
  id: Schema.String,
  title: stringOr(''),
});

/** The per-chapter verification report. It exists on a refusal too — "which
    exercise failed and what the engine said" is the useful half of a rejection
    — so an unreadable chapter is skipped rather than failing the whole report. */
function asChapterResults(value: Schema.Json): AiChapterResult[] {
  if (!Array.isArray(value)) return [];
  const out: AiChapterResult[] = [];
  for (const raw of value) {
    const decoded = Schema.decodeUnknownOption(AiChapterResultSchema)(raw);
    if (Option.isNone(decoded)) continue;
    const source = isRecord(raw) ? raw : {};
    const exercises = Array.isArray(source.exercises)
      ? source.exercises.flatMap((entry): AiExerciseResult[] => {
          const exercise = asExerciseResult(entry);
          return exercise === null ? [] : [exercise];
        })
      : [];
    out.push({ id: decoded.value.id, title: decoded.value.title, exercises });
  }
  return out;
}

/* ---------- M8: the learner's stored courses ---------- */

/** The array a list route carries: the envelope's own member when the gateway
    wraps it (`{ok, courses: [...]}`), or the body when it is already the list. */
function listOf(body: Schema.Json, key: string): readonly Schema.Json[] {
  if (Array.isArray(body)) return body;
  if (isRecord(body) && Array.isArray(body[key])) return body[key];
  return [];
}

/** The record a single-item route carries: the envelope's member when it wraps
    one (`{ok, course: {...}}`), or the body when it is already the record. */
function memberOf(body: Schema.Json, key: string): Schema.JsonObject | null {
  if (!isRecord(body)) return null;
  const member = body[key];
  return isRecord(member) ? member : body;
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
const AiCourseRecordSchema = Schema.Struct({
  status: stringOr('active'),
  owner_id: OptionalString,
  created_at: stringOr(''),
});

export function asCourseRecord(body: Schema.Json): AiCourseRecord | null {
  const value = memberOf(body, 'course');
  if (!value) return null;
  const decoded = Schema.decodeUnknownOption(AiCourseRecordSchema)(value);
  if (Option.isNone(decoded)) return null;
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
  const record = decoded.value;
  return {
    plan,
    outcome,
    status: record.status.trim() ? record.status : 'active',
    owner_id: record.owner_id,
    created_at: record.created_at,
  };
}

/** Every readable course in a list body. A record that will not parse is
    dropped; the rest are kept, so one corrupt row cannot hide the others. */
export function asCourseRecords(body: Schema.Json): AiCourseRecord[] {
  const out: AiCourseRecord[] = [];
  for (const raw of listOf(body, 'courses')) {
    const record = asCourseRecord(raw);
    if (record) out.push(record);
  }
  return out;
}

/* ---------- M12: the agent-run audit trail ---------- */

/* The transcript's leaves mirror contracts/run.schema.json. A run's identity
   (id, kind, status) is required: a row without one names nothing, so it is
   dropped rather than shown as a half-row. The numbers and flags are the same
   forgiving leaves the rest of this file uses, so a mis-typed optional field
   reads as its default rather than failing the row that carries it. */
const AiRunEntrySchema = Schema.Struct({
  seq: numberOr(0),
  kind: Schema.Literals(['message', 'tool_call', 'tool_result', 'error']),
  role: OptionalString,
  content: OptionalString,
  tool_call_id: OptionalString,
  tool_name: OptionalString,
  at: stringOr(''),
  ms: OptionalNumber,
  truncated: OptionalBoolean,
  redacted: OptionalBoolean,
});

const AiRunSummarySchema = Schema.Struct({
  id: Schema.NonEmptyString,
  kind: Schema.Literals(['challenge', 'course']),
  status: Schema.NonEmptyString,
  entry_count: numberOr(0),
  bytes: numberOr(0),
  truncated: booleanOr(false),
  started_at: stringOr(''),
  finished_at: stringOr(''),
});

const AiRunTranscriptSchema = Schema.Struct({
  id: Schema.NonEmptyString,
  kind: Schema.Literals(['challenge', 'course']),
  status: Schema.NonEmptyString,
  entry_count: numberOr(0),
  bytes: numberOr(0),
  truncated: booleanOr(false),
  started_at: stringOr(''),
  finished_at: stringOr(''),
});

/** One ordered entry, or null when it names no kind this build can read. A
    single unreadable turn is skipped rather than failing the whole run. */
function asRunEntry(value: Schema.Json): AiRunEntry | null {
  const decoded = Schema.decodeUnknownOption(AiRunEntrySchema)(value);
  return Option.isSome(decoded) ? { ...decoded.value } : null;
}

/** Every readable entry of a transcript, in the order the run wrote them. */
function asRunEntries(value: Schema.Json): AiRunEntry[] {
  if (!Array.isArray(value)) return [];
  const out: AiRunEntry[] = [];
  for (const raw of value) {
    const entry = asRunEntry(raw);
    if (entry) out.push(entry);
  }
  return out;
}

/** One run summary, or null when it names no run. */
function asRunSummary(value: Schema.Json): AiRunSummary | null {
  const decoded = Schema.decodeUnknownOption(AiRunSummarySchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  return { ...decoded.value, error: asAiError(raw.error) ?? undefined };
}

/** Every readable summary in a list body. A row that will not parse is
    dropped; the rest are kept, so one corrupt row cannot hide the others. A
    body the gateway did not wrap, or wrapped without a list, is empty. */
export function asRunSummaries(body: Schema.Json): AiRunSummary[] {
  const out: AiRunSummary[] = [];
  for (const raw of listOf(body, 'runs')) {
    const summary = asRunSummary(raw);
    if (summary) out.push(summary);
  }
  return out;
}

/** One run's whole transcript, or null when the body does not carry a readable
    one. `entries` is required by the contract; a body without a list is not a
    transcript this client will render with an invented empty conversation. */
export function asRunTranscript(body: Schema.Json): AiRunTranscript | null {
  const value = memberOf(body, 'run');
  if (!value || !Array.isArray(value.entries)) return null;
  const decoded = Schema.decodeUnknownOption(AiRunTranscriptSchema)(value);
  if (Option.isNone(decoded)) return null;
  return {
    ...decoded.value,
    entries: asRunEntries(value.entries),
    error: asAiError(value.error) ?? undefined,
  };
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

const JobTerminalStatusSchema = Schema.Literals(JOB_TERMINAL_STATUSES);

type JobTerminalStatus = (typeof JOB_TERMINAL_STATUSES)[number];

function isTerminalJobStatus(status: string): status is JobTerminalStatus {
  return Schema.is(JobTerminalStatusSchema)(status);
}

export function jobIsRunning(job: { status: string }): boolean {
  return !isTerminalJobStatus(job.status);
}

/* The words a job's own report uses for one stage, as `AiJobStage.state` fixes
   them. Stated once so the literal schema and the contract type cannot drift. */
const JOB_STAGE_STATES = ['queued', 'running', 'done', 'failed', 'skipped'] as const;

const JobStageStateSchema = Schema.Literals(JOB_STAGE_STATES);

/* One stage of a live job view. `name` is required and non-empty: a stage
   without a name cannot be matched to the step it belongs to. An unknown state
   reads as `queued`, so a word from a newer gateway keeps the step open. */
const AiJobStageSchema = Schema.Struct({
  name: Schema.NonEmptyString,
  state: JobStageStateSchema.pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some('queued'))),
    Schema.withDecodingDefaultType(Effect.succeed('queued' as const)),
  ),
  detail: OptionalString,
  ms: OptionalNumber,
});

function asJobStage(value: Schema.Json): AiJobStage | null {
  const decoded = Schema.decodeUnknownOption(AiJobStageSchema)(value);
  return Option.isSome(decoded) ? decoded.value : null;
}

/* The handle a submission answers with. `kind` defaults to `course` and
   `status` to `running`, the same defaults the hand-written decoder used. */
const AiJobHandleSchema = Schema.Struct({
  id: Schema.NonEmptyString,
  kind: Schema.Literals(['challenge', 'course']).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some('course'))),
    Schema.withDecodingDefaultType(Effect.succeed('course' as const)),
  ),
  status: stringOr('running'),
});

function asJobHandle(body: Schema.Json): AiJobHandle | null {
  const job = isRecord(body) ? body.job : undefined;
  const decoded = Schema.decodeUnknownOption(AiJobHandleSchema)(job);
  return Option.isSome(decoded) ? decoded.value : null;
}

const AiJobAttemptSchema = Schema.Struct({
  number: Schema.Number,
  stage: Schema.String,
  detail: Schema.String,
});

const AiJobSchema = Schema.Struct({
  id: Schema.NonEmptyString,
  kind: Schema.Literals(['challenge', 'course']).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some('course'))),
    Schema.withDecodingDefaultType(Effect.succeed('course' as const)),
  ),
  status: stringOr('running'),
  chapters_planned: OptionalNumber,
  chapters_verified: OptionalNumber,
  current_chapter: OptionalString,
  course_id: OptionalString,
  challenge_id: OptionalString,
  created_at: OptionalString,
  finished_at: OptionalString,
});

/**
 * A job view, or null when the body carries none.
 *
 * The job is a report about a run, not the run's artifact: nothing here is
 * promoted to a course or an outcome. `chapters_verified` is left undefined
 * when the gateway omits it, because "no chapters verified yet" and "this body
 * does not say" are different facts, and a screen that prints a count should
 * print the one it was given.
 */
function asJob(body: Schema.Json): AiJob | null {
  const job = memberOf(body, 'job');
  const decoded = Schema.decodeUnknownOption(AiJobSchema)(job);
  if (Option.isNone(decoded)) return null;
  const value = job ?? {};
  const stages: AiJobStage[] = [];
  for (const raw of Array.isArray(value.stages) ? value.stages : []) {
    const stage = asJobStage(raw);
    if (stage) stages.push(stage);
  }
  const attempts: AiAttempt[] = [];
  for (const raw of Array.isArray(value.attempts) ? value.attempts : []) {
    const attempt = Schema.decodeUnknownOption(AiJobAttemptSchema)(raw);
    if (Option.isSome(attempt)) attempts.push({ ...attempt.value });
  }
  const error = asAiError(value.error);
  const report = decoded.value;
  return {
    id: report.id,
    kind: report.kind,
    status: report.status,
    stages,
    attempts,
    chapters: Array.isArray(value.chapters) ? asChapterResults(value.chapters) : undefined,
    chapters_planned: report.chapters_planned,
    chapters_verified: report.chapters_verified,
    current_chapter: report.current_chapter,
    error: error ?? null,
    course_id: report.course_id ? report.course_id : undefined,
    challenge_id: report.challenge_id ? report.challenge_id : undefined,
    created_at: report.created_at,
    finished_at: report.finished_at,
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
function outcomeFromResponse(status: number, body: Schema.Json): AiOutcome {
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

/** A transport-level failure (no response, timeout, unreadable body). The
    transport's error channel is already `GatewayError`, so this is the typed
    failure itself, not an arbitrary thrown value. */
function outcomeFromError(err: GatewayError): AiOutcome {
  const code = asAiErrorCode(err.code);
  return {
    status: code === 'AI_UNAVAILABLE' ? 'unavailable' : 'failed',
    artifact: null,
    stages: [],
    attempts: [],
    error: { code, message: err.message, detail: err.detail ?? null },
  };
}

function failureFor(status: number, body: Schema.Json, label: string): GatewayError {
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

  const noteSessionGone = (status: number, body: Schema.Json): void => {
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
  const requireOk = <Body extends object>(method: string, path: string, body: Body | undefined, label: string, provider = false) =>
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
        Effect.map(asAdminAIModels),
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
    /* An edit is a write, not a provider call, so it keeps the transport
       deadline. The gateway answers with the stored record, which this decoder
       reads whole: a body whose plan will not parse is an unreadable course, not
       an edit that silently failed. */
    updateCourse: (id, edits) =>
      requireOk('PUT', `/courses/${encodeURIComponent(id)}`, edits, 'course update').pipe(
        Effect.flatMap((body) => {
          const course = asCourseRecord(body);
          return course ? Effect.succeed(course) : Effect.fail(malformed('course'));
        }),
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
    /* M12. The audit trail. A list is a read of the caller's own runs, so it
       keeps the transport deadline and retry; a failed read fails rather than
       reporting none. A run that is gone, or belongs to another account, is a
       404 and reads as null, the same rule every owner-scoped detail route
       follows. */
    listRuns: () =>
      requireOk('GET', '/runs', undefined, 'run list').pipe(Effect.map(asRunSummaries)),
    getRun: (id) =>
      requireOk('GET', `/runs/${encodeURIComponent(id)}`, undefined, 'run').pipe(
        Effect.catch((e) => {
          const err = asGatewayError(e);
          if (err.code === 'AI_NOT_FOUND' || err.code === 'NOT_FOUND' || err.status === 404) return Effect.succeed(null);
          return Effect.fail(err);
        }),
        Effect.map((body) => (body === null ? null : asRunTranscript(body))),
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

/* ---------- group 1: effect/Schema decoders ---------- */

/* The model catalog (GET /admin/ai/models). One entry per chat model in the
   gateway's embedded catalog. A row without a provider and a model names
   nothing, so it is dropped rather than shown as a half row; a missing display
   name is the empty string, which is what a models.dev entry without one
   carries. */
const AdminAIModelEntrySchema = Schema.Struct({
  provider: Schema.String,
  model: Schema.String,
  name: Schema.String.pipe(Schema.withDecodingDefaultType(Effect.succeed(''))),
});

/** The catalog, decoded one row at a time: a body that carries no `models`
    list is empty rather than an error, and one unreadable row does not hide the
    rest. The manage surface renders an empty picker from either. */
function asAdminAIModels(body: Schema.Json): AdminAIModelEntry[] {
  if (!isRecord(body) || !Array.isArray(body.models)) return [];
  const entries: AdminAIModelEntry[] = [];
  for (const raw of body.models) {
    const decoded = Schema.decodeUnknownOption(AdminAIModelEntrySchema)(raw);
    if (Option.isSome(decoded)) entries.push(decoded.value);
  }
  return entries;
}

/* What the tutor saw, summarised by the gateway. Every field is required by
   contracts/ai.schema.json, so a block that will not read whole is undefined,
   never an invention. */
const AiTutorContextSchema = Schema.Struct({
  exercise_id: Schema.String,
  dataset: Schema.String,
  dialect: Schema.String,
  tables: Schema.Array(Schema.String),
  tables_truncated: Schema.Boolean,
  expected: Schema.Literals(['included', 'columns_only', 'unavailable']),
  expected_columns: Schema.Number,
  expected_rows: Schema.Number,
  learner_result: Schema.Literals(['ran', 'error', 'not_run', 'unavailable']),
});

/* The response envelope. `message`, `hint_level` and `reveals_solution` are the
   contract's required fields, so a body without them fails decoding here and
   the caller answers with the typed `malformed('tutor response')` it always
   did. `provenance` and `context` are optional: an older gateway that predates
   the context still parses. */
const AiTutorResponseSchema = Schema.Struct({
  message: Schema.String,
  hint_level: Schema.Number,
  reveals_solution: Schema.Boolean,
});

function asProvenance(value: Schema.Json): AiProvenance | undefined {
  return Option.getOrUndefined(Schema.decodeUnknownOption(AiProvenanceSchema)(value));
}

function asTutorContext(value: Schema.Json): AiTutorResponse['context'] {
  const decoded = Schema.decodeUnknownOption(AiTutorContextSchema)(value);
  if (Option.isNone(decoded)) return undefined;
  const context = decoded.value;
  return {
    exercise_id: context.exercise_id,
    dataset: context.dataset,
    dialect: context.dialect,
    tables: [...context.tables],
    tables_truncated: context.tables_truncated,
    expected: context.expected,
    expected_columns: context.expected_columns,
    expected_rows: context.expected_rows,
    learner_result: context.learner_result,
  };
}

function asTutorResponse(value: Schema.Json): AiTutorResponse | null {
  const decoded = Schema.decodeUnknownOption(AiTutorResponseSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  return {
    message: decoded.value.message,
    hint_level: decoded.value.hint_level,
    reveals_solution: decoded.value.reveals_solution,
    provenance: asProvenance(raw.provenance),
    context: asTutorContext(raw.context),
  };
}

const AiCritiqueResponseSchema = Schema.Struct({ message: Schema.String });

/* `notes` is a list of prose strings and `provenance` is the gateway's
   attribution, so each reads through the same leaf the tutor does: a bad note is
   dropped and a bad provenance is absent, never a half-typed object. */
function asCritiqueResponse(value: Schema.Json): AiCritiqueResponse | null {
  const decoded = Schema.decodeUnknownOption(AiCritiqueResponseSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  return {
    message: decoded.value.message,
    notes: stringsOf(raw.notes),
    provenance: asProvenance(raw.provenance),
  };
}

const AiPatchChangeSchema = Schema.Struct({
  field: Schema.String,
  from: Schema.String,
  to: Schema.String,
});

const AiPatchProposalSchema = Schema.Struct({
  id: Schema.String,
  challenge_id: Schema.String,
  summary: Schema.String,
  changes: Schema.Array(AiPatchChangeSchema),
});

/* `verified` and `applied` are only ever true when the gateway says so, so a
   missing or mistyped flag reads as false rather than failing the verdict. A
   patch that will not read whole reads as no patch, because it is evidence a
   learner acts on, not decoration. */
const AiDefectVerdictSchema = Schema.Struct({
  verified: booleanOr(false),
  applied: booleanOr(false),
  summary: Schema.String,
});

function asDefectVerdict(value: Schema.Json): AiDefectVerdict | null {
  const decoded = Schema.decodeUnknownOption(AiDefectVerdictSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  const patch = Schema.decodeUnknownOption(AiPatchProposalSchema)(raw.patch);
  return {
    verified: decoded.value.verified,
    applied: decoded.value.applied,
    summary: decoded.value.summary,
    patch: Option.isSome(patch)
      ? {
          id: patch.value.id,
          challenge_id: patch.value.challenge_id,
          summary: patch.value.summary,
          changes: patch.value.changes.map((change) => ({ ...change })),
        }
      : null,
    evidence: asEvidence(raw.evidence),
    error: asAiError(raw.error),
  };
}

/* ---------- group 2: the outcome and artifact tree ---------- */

/* One artifact, whole. Every field the contract marks required is required
   here, so a half-valid artifact reads as absent instead of being spread
   through as an `AiArtifact` it never proved it was. */
const AiArtifactSchema = Schema.Struct({
  id: Schema.String,
  version: Schema.Number,
  lesson_id: OptionalString,
  dialect: Schema.optional(Schema.Literals(['postgresql'])).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
  type: OptionalString,
  title: Schema.String,
  prompt: Schema.String,
  difficulty: Schema.Literals(['beginner', 'intermediate', 'advanced']),
  objectives: Schema.Array(Schema.String),
  hints: Schema.Array(Schema.String),
  setup: AiSetupPlanSchema,
  reference_solution: Schema.String,
  cross_check_solution: OptionalString,
  result_contract: AiResultContractSchema,
  checks: Schema.optional(Schema.Array(AiCheckSchema)).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
  provenance: AiProvenanceSchema,
});

/** An artifact only counts when it carries everything the contract marks
    required. Anything less reads as absent, so a half-valid artifact is never
    rendered as if it had passed the pipeline. */
function asAiArtifact(value: Schema.Json): AiArtifact | null {
  const decoded = Schema.decodeUnknownOption(AiArtifactSchema)(value);
  if (Option.isNone(decoded)) return null;
  const artifact = decoded.value;
  /* The decoder's arrays are readonly; the contract's are not, so each is
     copied rather than cast. */
  return {
    id: artifact.id,
    version: artifact.version,
    lesson_id: artifact.lesson_id,
    dialect: artifact.dialect,
    type: artifact.type,
    title: artifact.title,
    prompt: artifact.prompt,
    difficulty: artifact.difficulty,
    objectives: [...artifact.objectives],
    hints: [...artifact.hints],
    setup: { ...artifact.setup, statements: [...artifact.setup.statements] },
    reference_solution: artifact.reference_solution,
    cross_check_solution: artifact.cross_check_solution,
    result_contract: { ...artifact.result_contract, columns: [...artifact.result_contract.columns] },
    checks: artifact.checks ? artifact.checks.map((check) => ({ ...check })) : undefined,
    provenance: { ...artifact.provenance },
  };
}

const AiStageSchema = Schema.Struct({
  name: Schema.String,
  state: Schema.Literals(['done', 'failed', 'skipped']),
  detail: OptionalString,
  ms: OptionalNumber,
});

/** One stage of the finished pipeline report per readable entry; a stage with
    no name or an unknown state is dropped rather than shown as an unnamed or
    invented step. */
function asStages(value: Schema.Json): AiStage[] {
  if (!Array.isArray(value)) return [];
  const stages: AiStage[] = [];
  for (const raw of value) {
    const decoded = Schema.decodeUnknownOption(AiStageSchema)(raw);
    if (Option.isSome(decoded)) stages.push(decoded.value);
  }
  return stages;
}

const AiAttemptSchema = Schema.Struct({
  number: numberOr(0),
  stage: stringOr(''),
  detail: Schema.String,
});

function asAttempts(value: Schema.Json): AiAttempt[] {
  if (!Array.isArray(value)) return [];
  const attempts: AiAttempt[] = [];
  for (const raw of value) {
    const decoded = Schema.decodeUnknownOption(AiAttemptSchema)(raw);
    if (Option.isSome(decoded)) attempts.push(decoded.value);
  }
  return attempts;
}

const AiEvidenceSchema = Schema.Struct({
  reference_rows: OptionalNumber,
  cross_check_rows: OptionalNumber,
  fixtures_rejected: OptionalNumber,
  fixtures_total: OptionalNumber,
  repairs: OptionalNumber,
  scratch_database: OptionalString,
});

/** The measured facts of a run. A body with no evidence at all reads as
    absent; a record with none of the numbers set reads as an empty measure
    rather than an error. */
function asEvidence(value: Schema.Json): AiEvidence | undefined {
  return Option.getOrUndefined(Schema.decodeUnknownOption(AiEvidenceSchema)(value));
}

const AiErrorSchema = Schema.Struct({
  code: Schema.NonEmptyString,
  stage: OptionalString,
  message: Schema.String,
  detail: Schema.optional(Schema.NullOr(Schema.String)).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
});

function asAiError(value: Schema.Json): AiError | null {
  const decoded = Schema.decodeUnknownOption(AiErrorSchema)(value);
  if (Option.isNone(decoded)) return null;
  return {
    code: asAiErrorCode(decoded.value.code),
    stage: decoded.value.stage,
    message: decoded.value.message,
    detail: decoded.value.detail ?? null,
  };
}

const AiOutcomeSchema = Schema.Struct({
  status: Schema.Literals(['active', 'rejected', 'unavailable', 'failed', 'awaiting_approval']),
  kind: Schema.optional(Schema.Literals(['challenge', 'course'])).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
});

/** A generation outcome, honoured only when it names a status the contract
    defines. Malformed bodies are rejected so the caller builds an honest
    failure instead of rendering a half-parsed artifact. */
function asAiOutcome(value: Schema.Json): AiOutcome | null {
  const decoded = Schema.decodeUnknownOption(AiOutcomeSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  return {
    status: decoded.value.status,
    kind: decoded.value.kind,
    artifact: asAiArtifact(raw.artifact),
    /* A course carries a plan instead of an artifact, and a refusal carries the
       per-exercise report. Both are read here rather than dropped: a parser
       that ignored `plan` turned an activated, verified, stored course into
       "the course was rejected" in front of the learner. */
    plan: asCoursePlan(raw.plan),
    chapters: asChapterResults(raw.chapters),
    stages: asStages(raw.stages),
    attempts: asAttempts(raw.attempts),
    evidence: asEvidence(raw.evidence),
    error: asAiError(raw.error),
  };
}

const AiRecordSchema = Schema.Struct({
  status: Schema.Literals(['rejected', 'active']).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some('active'))),
    Schema.withDecodingDefaultType(Effect.succeed('active' as const)),
  ),
  owner_id: OptionalString,
  created_at: stringOr(''),
});

function asAiRecord(value: Schema.Json): AiRecord | null {
  const decoded = Schema.decodeUnknownOption(AiRecordSchema)(value);
  if (Option.isNone(decoded)) return null;
  const raw = isRecord(value) ? value : {};
  const artifact = asAiArtifact(raw.artifact);
  if (!artifact) return null;
  return {
    artifact,
    outcome: asAiOutcome(raw.outcome) ?? unavailableOutcome('challenge'),
    status: decoded.value.status,
    owner_id: decoded.value.owner_id,
    created_at: decoded.value.created_at,
  };
}

/** Every readable record in a list body. A record with no readable artifact
    is dropped; the rest are kept, so one corrupt row cannot hide the others. */
function asAiRecords(value: Schema.Json): AiRecord[] {
  if (!Array.isArray(value)) return [];
  const out: AiRecord[] = [];
  for (const raw of value) {
    const record = asAiRecord(raw);
    if (record) out.push(record);
  }
  return out;
}

const AiAvailabilitySchema = Schema.Struct({
  configured: Schema.Boolean,
  available: Schema.Boolean,
  provider: stringOr(''),
  model: OptionalString,
  deterministic: OptionalBoolean,
  reason: stringOr(''),
  timeout_ms: Schema.optional(Schema.Number.check(Schema.isGreaterThan(0))).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
});

/** GET /capabilities > ai, or null when the body is not one this build can
    read: `configured` and `available` are the contract's required booleans, so
    without them the caller reports the honest fallback rather than a guess. */
function asAiAvailability(value: Schema.Json): AiAvailability | null {
  const decoded = Schema.decodeUnknownOption(AiAvailabilitySchema)(value);
  return Option.isSome(decoded) ? decoded.value : null;
}

/** The `ai` object of GET /capabilities. A gateway that does not report one is
    honestly described as not offering AI rather than assumed available. */
export function availabilityFromCapabilities(body: Schema.Json): AiAvailability {
  const advertised = isRecord(body) ? asAiAvailability(body.ai) : null;
  return advertised ?? unavailableAvailability('The gateway did not report an AI capability.');
}

const AdminAIConfigSchema = Schema.Struct({
  ok: booleanOr(false),
  provider: Schema.Literals(['off', 'fake', 'openai-compatible', 'pi-ai']).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some('off'))),
    Schema.withDecodingDefaultType(Effect.succeed('off' as const)),
  ),
  base_url: OptionalString,
  model: OptionalString,
  key_set: booleanOr(false),
  timeout_ms: numberOr(0),
  source: Schema.Literals(['database', 'boot']).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some('boot'))),
    Schema.withDecodingDefaultType(Effect.succeed('boot' as const)),
  ),
});

/** The live AI configuration. An unreadable body answers with the config that
    names no provider, never with a half-parsed one. */
function asAdminAIConfig(body: Schema.Json): AdminAIConfig {
  const decoded = Schema.decodeUnknownOption(AdminAIConfigSchema)(body);
  return Option.isSome(decoded)
    ? decoded.value
    : { ok: false, provider: 'off', key_set: false, timeout_ms: 0, source: 'boot' };
}
