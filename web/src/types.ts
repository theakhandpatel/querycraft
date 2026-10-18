/* Shared domain types for Querycraft. These describe the shape of the course
   content, learner progress and the ExecutionService contract that both the
   in-browser engine and the Go gateway satisfy. */

import type { ExplainRequest, PerfContract, PlanBundle } from './api/planContract';
import type {
  AiAvailability,
  AiCourseBuildAnswer,
  AdminAIConfig,
  AdminAIModelEntry,
  AdminAIUpdate,
  AiCourseRecord,
  AiCritiqueResponse,
  AiDefectReport,
  AiDefectVerdict,
  AiGenerationRequest,
  AiJob,
  AiJobHandle,
  AiOutcome,
  AiRecord,
  AiSetupPlan,
  AiTutorRequest,
  AiTutorResponse,
} from './api/aiContract';

/* ---------- course content ---------- */

export type BlockType = 'prose' | 'syntax' | 'example' | 'exercise';

export type RowValue = string | number | boolean | null;

export interface ResultSet {
  columns: string[];
  rows: RowValue[][];
}

export interface ResultContract {
  columns: string[];
  column_order?: 'exact' | 'ignored';
  row_order?: 'exact' | 'ignored';
  duplicates?: 'preserved' | 'ignored';
  numeric_tolerance?: number;
}

export interface Block {
  type: BlockType;
  /** Stable id, present on exercises (e.g. "ex.1.1"). */
  id?: string;
  /** Prose/section HTML. */
  html?: string;
  /** True when `html` holds markdown from the content store, not HTML. */
  markdown?: boolean;
  /** Optional section heading that starts a new outline group. */
  section?: string;
  /** Syntax/example SQL. */
  sql?: string;
  note?: string;
  /** Exercise fields. */
  prompt?: string;
  starter?: string;
  solution?: string;
  hints?: string[];
  contract?: ResultContract;
  /** M5: a structural plan contract. When present the exercise is graded on the
      plan its query produces, alongside the result contract above. */
  perf?: PerfContract;
  /** Design §7.3: bump when the prompt, contract or reference solution changes,
      so an earlier pass reads as "solved on an earlier version". Defaults to 1. */
  version?: number;
  /** M5: the dataset this block runs against. Undefined is the course's
      dataset, which is `DEFAULT_DATASET` (commerce) when the course is silent
      too. A `plans` exercise carries `perf`; every other exercise is silent. */
  dataset?: DatasetId;
  /** M6: the dialect this block runs against. Same precedence as `dataset` —
      block, then module, then course, then `DEFAULT_DIALECT`. Authoring it on
      the block is the escape hatch for a single exercise that steps outside its
      chapter's engine; a chapter that mixes the two is two chapters (D15). */
  dialect?: Dialect;
  /** The authored expected rows. The browser adapter grades against a live
      reference run and falls back to these rows when a block has no solution. */
  result?: ResultSet;
}

export interface CourseModule {
  id: string;
  n: number;
  title: string;
  kind: string;
  minutes: number;
  summary: string;
  objectives: string[];
  blocks: Block[];
  /** Design 14 (M2 exit gate): a lesson with no graded exercise must say why it
      is reading-only, and that reason is shown to the learner in the outline. */
  readingOnly?: string;
  /** M6 step 1 (D15): every chapter carries exactly one dialect, and it is
      authoritative — a cross-dialect comparison is a separate chapter, never a
      dialect-neutral one. Undefined is the course default, which is
      `DEFAULT_DIALECT`: every chapter authored before M6 is PostgreSQL, and
      saying so in one place beats repeating it on 40 modules. */
  dialect?: Dialect;
}

export interface Course {
  id: string;
  title: string;
  subtitle?: string;
  level?: string;
  built?: boolean;
  enrolled?: boolean;
  modules?: CourseModule[] | null;
  /** The default dialect for this course's chapters (M6 step 1). A chapter
      that teaches another engine overrides it. */
  dialect?: Dialect;
  /** The dataset this course's chapters run against (M5). A block that names
      another dataset overrides it. */
  dataset?: DatasetId;
  /** M8. True when the gateway stores this course as the learner's own record
      rather than it shipping with the app. It is the one field that says where a
      course came from, so a built-in course never carries it and a stored course
      always does. */
  gateway?: boolean;
  /** M8. The stored record's lifecycle: `active` for a finished course,
      `building` for one the pipeline is still writing a chapter at a time. Only
      present on a gateway course. */
  recordStatus?: string;
  /** M9. The stored record's setup plan, kept so "restore my data" can rebuild
      the workspace the exercises were verified in. A built-in course has none:
      its workspace is the sealed dataset. */
  setup?: AiSetupPlan;
  /** Where the course came from: a shipped library course, a draft opened from
      the reference curriculum, or one the gateway generated. The library's
      "Created by you" filter reads it. */
  source?: string;
  /** A one-line summary shown on the course cards. */
  blurb?: string;
  /** The subject area, shown beside the estimated time. */
  domain?: string;
  /** The whole course's estimated reading and practice minutes. */
  minutes?: number;
  /** True when the reference curriculum was opened as a draft rather than
      generated, so no screen presents it as a model-built course. */
  authored?: boolean;
}

export interface TableDef {
  name: string;
  rows: number;
  cols: [string, string, string][];
  preview: string[][];
}

/* ---------- learner progress ---------- */

/* Design §6.2: hint and solution views are recorded as events, alongside the
   attempt outcomes, so the history shows what help was used and when. */
export type EventKind = 'passed' | 'failed' | 'accepted' | 'error' | 'solution' | 'hint' | 'bookmark';

export interface ProgressEvent {
  ts: number;
  id: string;
  /** Design §8.2: the client request id. One value per attempt, so two
      attempts at the same exercise never collide in storage. Optional because
      seeded and older events predate it. */
  attemptId?: string;
  kind: EventKind;
  title: string;
  course: string;
  lesson: number | null;
  /** Design §7.3: every attempt stores the content versions it was made
      against, so a later change to the dataset or the exercise never silently
      invalidates a pass. Absent on attempts recorded before this existed. */
  datasetVersion?: number;
  exerciseVersion?: number;
}

export interface NoteItem {
  course: string;
  lesson: number;
  kind: 'note' | 'bookmark';
  ts: number;
  text: string;
  /** Design §10: a bookmark points at one section of a chapter, so the
      workbook can send the learner back to it. */
  section?: number;
}

export interface SavedItem {
  course: string;
  lesson: number;
  name: string;
  /** The SQL the learner kept, so the workbook can re-run it through the seam. */
  sql?: string;
  /** The exercise it was kept from, when it came from one. */
  exercise?: string;
  /** Contract snapshot at save time, so a kept query knows what it was for. */
  contract?: ResultContract;
  datasetVersion?: number;
  exerciseVersion?: number;
  ts?: number;
}

export interface Progress {
  version: number;
  seeded: boolean;
  events: ProgressEvent[];
  passed: Record<string, boolean>;
  accepted: Record<string, boolean>;
  firstTry: Record<string, boolean>;
  done: Record<string, boolean>;
  /** Chapters the learner marked complete by hand. `done` is recomputed from
      graded attempts on every event, which used to silently erase an explicit
      "Mark chapter complete" (and made a chapter with no exercises impossible to
      complete). This set is the learner's own signal; `rebuildProgressFromEvents`
      preserves it, and `markLessonDone` toggles it. */
  manuallyDone?: Record<string, boolean>;
  notes: NoteItem[];
  saved: SavedItem[];
  attempts: number;
  attemptsByDay: number[];
  /** Active reading/practice time per course id, in milliseconds. Measured on
      the client while a chapter is open and the tab is visible; never guessed. */
  timeMs: Record<string, number>;
}

/* ---------- ExecutionService contract ---------- */

/** The closed error-code set, in contract order, from
    contracts/query-result.schema.json. The type derives from these values and
    a runtime check reads them, so the set is stated once. */
export const QUERY_ERROR_CODES = [
  'EMPTY_QUERY',
  'QUERY_SYNTAX_ERROR',
  'QUERY_RUNTIME_ERROR',
  'QUERY_TIMEOUT',
  'RESULT_LIMIT_EXCEEDED',
  'ENVIRONMENT_UNAVAILABLE',
  /** A product refusal, not an engine fault: the chapter was written for the
      other dialect. It is a closed code so the UI can name the fix. */
  'DIALECT_MISMATCH',
  'PERSISTENCE_FAILED',
  'EVALUATION_ERROR',
] as const;

export type QueryErrorCode = (typeof QUERY_ERROR_CODES)[number];

/** Narrows a code that arrived as data, from a gateway body or the SQLite
    dialect profile, to the closed set. A code outside the contract must not
    pass as one. */
export function isQueryErrorCode(code: string): code is QueryErrorCode {
  return QUERY_ERROR_CODES.some((known) => known === code);
}

/** The closed AI error-code set, in contract order, from
    contracts/ai.schema.json. These are not QueryResult codes: the AI envelope
    has its own vocabulary. The type derives from these values and the runtime
    check reads them, so the set is stated once. */
export const AI_ERROR_CODES = [
  'AI_UNAVAILABLE',
  'AI_INVALID_REQUEST',
  'AI_INVALID_ARTIFACT',
  'AI_REFERENCE_FAILED',
  'AI_CROSS_CHECK_FAILED',
  'AI_REPAIR_EXHAUSTED',
  'AI_NOT_FOUND',
  'AI_PATCH_UNVERIFIED',
  'AI_PATCH_UNAPPROVED',
  'ENVIRONMENT_UNAVAILABLE',
] as const;

export type AiErrorCode = (typeof AI_ERROR_CODES)[number];

/** Narrows an AI code that arrived as data to the closed set. */
export function isAiErrorCode(code: string): code is AiErrorCode {
  return AI_ERROR_CODES.some((known) => known === code);
}

/** The dialects the app can run, in contract order (contracts/query-result.schema.json). */
export type Dialect = 'postgresql' | 'sqlite';

/** The default dialect. PostgreSQL is first among equals (ADR 0004): a module
    that does not declare one is PostgreSQL, which is what every existing
    chapter was authored and graded against. */
export const DEFAULT_DIALECT: Dialect = 'postgresql';

/** The datasets the app can run against (M5 / M6 step 1).
 *
 * `commerce` is the small 30/20/80/160-row fixture every lesson starts on;
 * `perf` is the larger 2000/500/10000/20000-row fixture the `plans` course
 * teaches access paths on. The set is closed and lives in the contract order
 * the Go gateway serves, exactly like `Dialect`. */
export type DatasetId = 'commerce' | 'perf';

/** The default dataset. A chapter that does not name one runs on the same
    small commerce fixture every lesson authored before M5 was written on, so
    the existing courses keep meaning what they meant. */
export const DEFAULT_DATASET: DatasetId = 'commerce';

export interface QueryErrorShape {
  code: QueryErrorCode;
  message: string;
  detail?: string | null;
}

export interface QueryResult {
  status: 'succeeded' | 'failed';
  columns: { name: string; type: string }[];
  rows: RowValue[][];
  row_count: number;
  truncated: boolean;
  duration_ms: number;
  error: QueryErrorShape | null;
  request_id?: string | null;
  engine?: string | null;
  /** The machine-readable dialect of the engine that produced this result. It
      is what a chapter is compared against, and it is required by the contract:
      a result that cannot say which engine produced it cannot be graded. */
  dialect: Dialect;
}

export interface CheckResult {
  label: string;
  code: string;
  ok: boolean;
}

export interface Evaluation {
  qr: QueryResult;
  checks: CheckResult[];
  passed: boolean;
  /** Milliseconds, formatted as a string by every adapter (the gateway emits
      "8.4"). It was a number|string union, which only papered over the browser
      adapters disagreeing with the gateway; both now emit the same string. */
  duration_ms: string;
}

export interface TablesResponse {
  dataset: string;
  version: number;
  tables: TableDef[];
  rows: number;
}

/** One markdown section of a chapter's reading content, as stored in the DB. */
export interface ContentSection {
  section: string;
  markdown: string;
}

export interface ApiLogEntry {
  method: string;
  path: string;
  status: number;
  ms: number;
  at: number;
}

export interface ApiConfig {
  baseUrl: string;
  engine: string;
  /** The dialect this adapter runs. Each adapter declares its own, once, so a
      result never has to guess from `engine`'s free text. */
  dialect: Dialect;
  latency: boolean;
  latencyMin?: number;
  latencyMax?: number;
}

/** The execution capability surface (design §5.3). The one that matters to a
    learner is `hardTimeout`: a Web Worker or a server can be told to stop, a
    main-thread WASM engine cannot, and the UI must not claim otherwise. */
/** The gateway's own readiness report (GET /diagnostics). Field names mirror
    the gateway's JSON; a field the gateway does not send is simply absent, and
    the decoder drops a body that cannot be read rather than inventing values. */
export interface DiagnosticsReport {
  build: string;
  engine: {
    name: string;
    ready: boolean;
    reason: string;
    version: string;
    limits: { max_rows: number; max_bytes: number; timeout_ms: number; max_concurrent: number };
  };
  store: { kind: string; persistent: boolean };
  dataset: { schema: string; version: number };
  progress: { version: number };
  learner?: Record<string, unknown>;
}

export interface EngineCapabilities {
  placement: 'worker' | 'main' | 'server' | 'none';
  hardTimeout: boolean;
  timeoutMs: number;
  /** Whether learner SQL may change data. True for the PGlite workspace and for
      a signed-in gateway environment (ADR 0003). */
  writable: boolean;
  /** Whether the engine can produce a plan at all (M5). An engine that cannot
      plan leaves the action disabled with a reason shown, rather than
      pretending, exactly like an unavailable AI lab. */
  explain: boolean;
  /** The dialect this engine runs. The honest source for "what am I about to
      run on" on a screen that has no chapter in hand yet. */
  dialect: Dialect;
  /** The dialects a *gateway* can serve, in stable order. Absent for a single-
      engine adapter, where `dialect` already answers. A chapter whose dialect
      is missing from this list is refused with the one action that fixes it. */
  dialects?: Dialect[];
  /** The dialects this engine can capture a plan for (M6 step 6). Separate from
      `explain` because the answer is per dialect, not per adapter: the browser
      adapter runs both engines, and PostgreSQL can be planned while SQLite
      cannot (`contracts/dialects/sqlite.json` says `plan: false`). Absent means
      "the same answer as `explain` for every dialect", which is what a
      single-dialect adapter means. */
  explainDialects?: Dialect[];
}

/** The learner's writable workspace, as reported by the active adapter
    (design §5.4 / ADR 0003). The gateway owns a per-account database copied
    from the sealed template; the offline adapters own the browser's PGlite
    instance. `getEnvironment()` returns null when the adapter has no
    environment at all (canned results). */
export interface EnvironmentInfo {
  account?: string;
  database?: string;
  exists: boolean;
  writable: boolean;
  tables?: number;
  created_at?: string;
  reset_at?: string;
  idle_until?: string;
}

/** The result of restoring the original dataset. `duration_ms` is what the
    adapter measured end to end; `environment` describes the fresh workspace
    when the adapter can produce one. */
export interface EnvironmentReset {
  ok: boolean;
  duration_ms: number;
  /** How many of a named course's setup statements were applied to the restored
      workspace. Zero when the request named no course; never a guess. */
  setup_statements: number;
  environment?: EnvironmentInfo;
}

export type PipelineEvent =
  | { type: 'step'; index: number; state: 'running' | 'done' }
  | { type: 'log'; t: string; html: string }
  | { type: 'all-done' };

export interface GenerationRequest {
  title?: string;
  steps?: string[];
  lines?: [string, string][];
  exercises?: number;
  [key: string]: unknown;
}

export interface CourseGeneration {
  title?: string;
  chapters: number;
  exercises?: number;
  datasetVersion: number;
}

export interface Cell {
  item?: Block;
  module?: CourseModule;
  course?: Course;
  /** The dataset to grade against, when the caller already resolved it. Left
      undefined, the adapter resolves it from `item`/`course` itself, so a
      screen that knows nothing about datasets still runs on the right one. */
  dataset?: DatasetId;
  /** The dialect to grade against (M6), resolved by the adapter from the cell's
      own module/course when the caller leaves it out — the same rule the
      dataset follows, for the same reason. */
  dialect?: Dialect;
}

/** What a caller knows about the workspace a statement belongs to.
 *
 * It is deliberately a *description of the place the SQL came from*, not a set
 * of unrelated options: every field is something a screen already has in hand
 * (the exercise it is showing, the chapter, the course), and the adapter turns
 * that into the one dataset to run on. `dataset` is the escape hatch for the
 * caller that has already resolved it and must not have it derived twice. */
export interface QueryContext {
  block?: Block;
  module?: CourseModule;
  course?: Course;
  dataset?: DatasetId;
  /** The dialect the SQL is written in (M6). Left undefined, the adapter reads
      it from the module, then the course, then the app default — a screen that
      has never heard of dialects still runs on the chapter's own engine. */
  dialect?: Dialect;
}

/* ---------- accounts ---------- */

export type UserRole = 'admin' | 'learner';

/** An authenticated account, as returned by the gateway's auth endpoints. */
export interface User {
  id: string;
  email: string;
  name: string;
  role: UserRole;
  createdAt: string;
}

/** A successful signup/login: the account plus its bearer token. */
export interface AuthSession {
  user: User;
  token: string;
  expiresAt: string;
}

export interface SignupInput {
  email: string;
  name: string;
  password: string;
}

export interface LoginInput {
  email: string;
  password: string;
}

/** The single seam every screen depends on. Promise-shaped for React. */
export interface ExecutionApi {
  mode: 'pglite' | 'http';
  baseUrl: string;
  config: ApiConfig;
  log: ApiLogEntry[];
  readonly count: number;

  /** Where SQL actually runs and whether a runaway statement can be stopped by
      the host (design §5.2/§5.3). `null` until the engine has booted. */
  capabilities(): Promise<EngineCapabilities | null>;

  /** True only when the gateway's seeded default administrator can be signed in
      with an empty password — a loopback-only local-development install. The
      sign-in screen shows its local-admin hint for this boolean and no other
      reason, so a release that set QC_ADMIN_PASSWORD never advertises a blank
      password. The offline adapters report false: they have no server account. */
  adminPasswordEmpty(): Promise<boolean>;

  /* Auth. Progress and events are owner-scoped server-side, so the token these
     return is the only credential the client ever sends. */
  signup(input: SignupInput): Promise<AuthSession>;
  login(input: LoginInput): Promise<AuthSession>;
  logout(): Promise<void>;
  /** The current account, or null when no valid session exists. */
  me(): Promise<User | null>;

  /** M5. The second argument describes where the SQL came from — the exercise,
      the chapter, the course — and the adapter picks the one dataset to run on
      from it. A screen that has never heard of datasets still runs on the right
      one, because the course it is rendering carries the dataset. */
  runQuery(sql: string, context?: QueryContext): Promise<QueryResult>;
  checkAnswer(sql: string, cell: Cell): Promise<Evaluation>;
  /** Capture a plan (M5). Resolves to a bundle for every adapter: a failure —
      no engine, a refused ANALYZE, a bad statement — is reported inside it with
      `timing_precision: 'none'`, never invented. */
  explain(req: ExplainRequest): Promise<PlanBundle>;
  /** The second argument names the engine whose tables to report (M6): the two
      dialects hold different schemas, so a SQLite chapter that listed the
      PostgreSQL relations would be describing a database it is not running on. */
  getTables(dataset?: DatasetId, dialect?: Dialect): Promise<TablesResponse>;
  /** The learner's writable workspace, or null when the adapter has none. */
  getEnvironment(): Promise<EnvironmentInfo | null>;
  /** Restore the original dataset and discard the learner's own changes and
      tables. Destructive, so the caller confirms first. The dataset names which
      workspace: the challenge database, or the plan lab — they are separate
      databases, so a plan-lesson reset must not restore the other one. A course
      may be named as a third argument when the lesson is a stored one: the
      adapter then rebuilds the course's own setup in the restored workspace, so
      the learner's database is the one the exercises were verified in. */
  resetEnvironment(dataset?: DatasetId, dialect?: Dialect, course?: Course | null): Promise<EnvironmentReset>;
  /** Start building a workspace before the learner's first Run needs it. Fire
      and forget by design: nothing waits on it and it never reports. */
  warmUp(dataset?: DatasetId, dialect?: Dialect): void;
  /** Chapter reading content, stored as markdown. */
  getContent(moduleId: string): Promise<ContentSection[]>;
  getProgress(courseId?: string): Promise<{ ok: boolean; snapshot?: unknown }>;
  saveProgress(snapshot: Progress, courseId?: string): Promise<{ ok: boolean }>;
  logEvent(evt: ProgressEvent): Promise<{ ok: boolean; id?: string }>;
  saveDraft(cellId: string, sql: string): Promise<{ ok: boolean }>;
  getDraft(cellId: string): Promise<string | null>;
  /** Record a course/challenge draft through the API. The screens pace their
      own progress and call this once real work has produced something to save. */
  draftCourse(req: GenerationRequest): Promise<CourseGeneration>;
  draftChallenge(req: GenerationRequest): Promise<{ ok: boolean }>;

  /* AI generation (design §11). Availability resolves, never rejects. The
     generate calls always resolve to an outcome — `rejected`, `failed` and
     `unavailable` included — so a screen renders what the service reported
     instead of inventing a result. The browser adapters report the lab
     unavailable rather than imitating a pipeline. */
  aiAvailability(): Promise<AiAvailability>;
  generateChallenge(req: AiGenerationRequest): Promise<AiOutcome>;
  generateCourse(req: AiGenerationRequest): Promise<AiOutcome>;
  listChallenges(): Promise<AiRecord[]>;
  getChallenge(id: string): Promise<AiRecord | null>;
  resetChallenge(id: string): Promise<{ ok: boolean }>;
  /** M8. The learner's own generated courses, read from the gateway so a reload
      (or a second tab, or a deep link) finds them. The offline adapters refuse:
      they have no course store to read. */
  listCourses(): Promise<AiCourseRecord[]>;
  /** Admin. Every stored course, every owner, with owner ids and statuses.
      The gateway refuses a learner with 403 ADMIN_REQUIRED; the offline
      adapters refuse with AI_UNAVAILABLE, so call it only in gateway mode. */
  adminCourses(): Promise<AiCourseRecord[]>;
  /** Admin. The live AI configuration; the key is a boolean, never a value.
      The offline adapters refuse, so call it only in gateway mode. */
  adminAI(): Promise<AdminAIConfig>;
  /** Admin. Every chat model in the gateway's embedded catalog — the picker
      behind the pi-ai provider's model field. Offline adapters answer empty. */
  adminAIModels(): Promise<AdminAIModelEntry[]>;
  /** The caller's client preferences (theme, custom themes), or null when the
      account has none. Offline adapters answer null honestly. */
  preferences(): Promise<Record<string, unknown> | null>;
  /** Replace the caller's preference document wholesale. */
  savePreferences(prefs: Record<string, unknown>): Promise<Record<string, unknown>>;
  /** Admin. Save and apply an AI configuration while the gateway runs; it
      survives a restart because the boot path applies the stored setting. */
  saveAdminAI(update: AdminAIUpdate): Promise<AdminAIConfig>;
  /** Admin health: the gateway's measured readiness report, or null when
      there is no gateway to ask (the offline adapters answer null, never a
      fabricated report). */
  diagnostics(): Promise<DiagnosticsReport | null>;
  getCourse(id: string): Promise<AiCourseRecord | null>;
  deleteCourse(id: string): Promise<{ ok: boolean }>;
  /** M10. Approve a paused course: continue its stored outline into the chapter
      phase. Answers with a job to poll when the gateway ran it in the
      background, or with the blocking outcome. */
  approveCourse(id: string): Promise<AiCourseBuildAnswer>;
  /** M8. A generation that answers with a job instead of holding the request
      open for minutes. The handle is null when the gateway answered with the
      blocking envelope, so the caller falls back rather than guessing. */
  submitGeneration(req: AiGenerationRequest, kind: 'challenge' | 'course'): Promise<AiJobHandle | null>;
  /** One read of a job; the caller owns the pacing. A job that is gone resolves
      to null, the same way a course that is gone does. */
  generationJob(id: string): Promise<AiJob | null>;
  tutor(req: AiTutorRequest): Promise<AiTutorResponse>;
  critique(challengeId: string, req: { sql: string; objective?: string }): Promise<AiCritiqueResponse>;
  reportDefect(req: AiDefectReport): Promise<AiDefectVerdict>;
  decidePatch(patchId: string, approved: boolean): Promise<{ applied: boolean }>;
}

/* ---------- UI ---------- */

export type Screen =
  | 'home'
  | 'course'
  | 'library'
  | 'create'
  | 'building'
  | 'review'
  | 'manage'
  | 'lesson'
  | 'workbook'
  | 'workbook-progress'
  | 'challenge-lab'
  | 'appearance-settings';

export type ModalName = 'editCourse' | 'editLesson' | 'extend' | 'addLesson' | null;

/** A request to show the in-app prompt dialog. */
export interface PromptRequest {
  /** Dialog heading. */
  title: string;
  /** Label tied to the text input. */
  label: string;
  defaultValue?: string;
  placeholder?: string;
  submitLabel?: string;
  /** When true (the default), the primary CTA stays disabled until the value is non-empty. */
  required?: boolean;
  onSubmit: (value: string) => void;
}

export type ThemeName =
  | 'light'
  | 'dark'
  | 'paper'
  | 'ocean-light'
  | 'contrast-light'
  | 'midnight'
  | 'ember'
  | 'ocean-dark'
  | 'terminal'
  | 'contrast-dark'
  | 'system';

/** The theme preference is any preset id, a user theme id ('user-…'), or
    'system'. It is a string in the store because user themes are dynamic. */
export type ThemePreference = ThemeName | `user-${string}`;

export interface UiState {
  selectedCourseId: string;
  libraryFilter: string;
  [key: string]: unknown;
}
