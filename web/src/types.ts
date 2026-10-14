/* Shared domain types for Querycraft. These describe the shape of the course
   content, learner progress and the ExecutionService contract that both the
   in-browser mock and the Go gateway satisfy. */

/* ---------- course content ---------- */

export type BlockType = 'prose' | 'syntax' | 'example' | 'exercise';

export type RowValue = string | number | boolean | null;

export interface ResultSet {
  columns: string[];
  rows: RowValue[][];
}

export interface ExerciseCheck {
  label: string;
  code: string;
  /** Reference-implementation check over the learner's SQL text (mock only). */
  test: (sql: string) => boolean;
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
  checks?: ExerciseCheck[];
  /** Design §7.3: bump when the prompt, contract or reference solution changes,
      so an earlier pass reads as "solved on an earlier version". Defaults to 1. */
  version?: number;
  /** Reference result the mock engine returns for correct SQL. */
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
}

export interface Course {
  id: string;
  title: string;
  subtitle?: string;
  level?: string;
  tracks?: string[];
  built?: boolean;
  enrolled?: boolean;
  modules?: CourseModule[] | null;
  [key: string]: unknown;
}

export interface TableColumnDef {
  name: string;
  type: string;
  note: string;
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
  notes: NoteItem[];
  saved: SavedItem[];
  attempts: number;
  attemptsByDay: number[];
  /** Active reading/practice time per course id, in milliseconds. Measured on
      the client while a chapter is open and the tab is visible; never guessed. */
  timeMs: Record<string, number>;
}

/* ---------- ExecutionService contract ---------- */

export type QueryErrorCode =
  | 'EMPTY_QUERY'
  | 'QUERY_SYNTAX_ERROR'
  | 'QUERY_RUNTIME_ERROR'
  | 'QUERY_TIMEOUT'
  | 'RESULT_LIMIT_EXCEEDED'
  | 'ENVIRONMENT_UNAVAILABLE'
  | 'PERSISTENCE_FAILED'
  | 'EVALUATION_ERROR';

export interface QueryErrorShape {
  code: QueryErrorCode | string;
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
  duration_ms: number | string;
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
  latency: boolean;
  latencyMin?: number;
  latencyMax?: number;
}

/** The execution capability surface (design §5.3). The one that matters to a
    learner is `hardTimeout`: a Web Worker or a server can be told to stop, a
    main-thread WASM engine cannot, and the UI must not claim otherwise. */
export interface EngineCapabilities {
  placement: 'worker' | 'main' | 'server' | 'none';
  hardTimeout: boolean;
  timeoutMs: number;
  /** Whether learner SQL may change data. True for the PGlite workspace and for
      a signed-in gateway environment (ADR 0003); false for canned results. */
  writable: boolean;
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
  mode: 'mock' | 'pglite' | 'http';
  baseUrl: string;
  config: ApiConfig;
  log: ApiLogEntry[];
  readonly count: number;

  /** Where SQL actually runs and whether a runaway statement can be stopped by
      the host (design §5.2/§5.3). `null` until the engine has booted. */
  capabilities(): Promise<EngineCapabilities | null>;

  /* Auth. Progress and events are owner-scoped server-side, so the token these
     return is the only credential the client ever sends. */
  signup(input: SignupInput): Promise<AuthSession>;
  login(input: LoginInput): Promise<AuthSession>;
  logout(): Promise<void>;
  /** The current account, or null when no valid session exists. */
  me(): Promise<User | null>;

  runQuery(sql: string, expect?: Block): Promise<QueryResult>;
  checkAnswer(sql: string, cell: Cell): Promise<Evaluation>;
  getTables(): Promise<TablesResponse>;
  /** The learner's writable workspace, or null when the adapter has none. */
  getEnvironment(): Promise<EnvironmentInfo | null>;
  /** Restore the original dataset and discard the learner's own changes and
      tables. Destructive, so the caller confirms first. */
  resetEnvironment(): Promise<EnvironmentReset>;
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
}

/* ---------- UI ---------- */

export type Screen =
  | 'home'
  | 'course'
  | 'library'
  | 'create'
  | 'building'
  | 'review'
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

export type ThemeName = 'light' | 'dark' | 'system';

export interface UiState {
  selectedCourseId: string;
  libraryFilter: string;
  [key: string]: unknown;
}
