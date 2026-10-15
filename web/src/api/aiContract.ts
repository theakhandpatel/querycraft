/**
 * The M4 AI contract, mirroring `contracts/ai.schema.json`.
 *
 * This file is types only — the seam every AI screen compiles against. The
 * model is an untrusted content generator and the Go service is the policy, so
 * nothing here may compute a verdict: a status of `active` means the service
 * verified the artifact, and `ai` in `EngineCapabilities` says whether the lab
 * can run at all. When it cannot, the screens hide the lab rather than
 * pretending.
 */

export type AiDifficulty = 'beginner' | 'intermediate' | 'advanced';

/** AI-envelope codes. These are not QueryResult codes (that enum is closed). */
export type AiErrorCode =
  | 'AI_UNAVAILABLE'
  | 'AI_INVALID_REQUEST'
  | 'AI_INVALID_ARTIFACT'
  | 'AI_REFERENCE_FAILED'
  | 'AI_CROSS_CHECK_FAILED'
  | 'AI_REPAIR_EXHAUSTED'
  | 'AI_NOT_FOUND'
  | 'AI_PATCH_UNVERIFIED'
  | 'AI_PATCH_UNAPPROVED'
  | 'ENVIRONMENT_UNAVAILABLE';

export interface AiError {
  code: AiErrorCode;
  stage?: string;
  message: string;
  detail?: string | null;
}

export interface AiResultContract {
  columns: string[];
  column_order: 'exact' | 'ignored';
  row_order: 'exact' | 'ignored';
  duplicates: 'preserved' | 'ignored';
  numeric_tolerance: number;
}

export interface AiSetupPlan {
  dataset: string;
  schema_version: number;
  seed_version: number;
  statements: string[];
}

export interface AiProvenance {
  provider: string;
  model: string;
  provider_version: string;
  pipeline_version: string;
  evaluator_version: string;
  dataset_version: number;
  created_at: string;
  repairs?: number;
}

export interface AiCheck {
  label: string;
  code: string;
  pattern?: string;
}

/** A challenge the service has allowed to exist. */
export interface AiArtifact {
  id: string;
  version: number;
  lesson_id?: string;
  dialect?: 'postgresql';
  type?: string;
  title: string;
  prompt: string;
  difficulty: AiDifficulty;
  objectives: string[];
  hints: string[];
  setup: AiSetupPlan;
  reference_solution: string;
  cross_check_solution?: string;
  result_contract: AiResultContract;
  checks?: AiCheck[];
  provenance: AiProvenance;
}

/** The §11.1 stage names, in pipeline order. */
export type AiStageName =
  | 'validate_request'
  | 'request_bundle'
  | 'validate_artifact'
  | 'scratch_environment'
  | 'reference_contract'
  | 'cross_check'
  | 'adversarial_fixtures'
  | 'repair'
  | 'activate';

export interface AiStage {
  name: AiStageName | string;
  state: 'done' | 'failed' | 'skipped';
  detail?: string;
  ms?: number;
}

export interface AiAttempt {
  number: number;
  stage: string;
  detail: string;
}

/**
 * One stage of a run as a *live* job view reports it.
 *
 * `AiStage` above is the pipeline's final report and only ever carries the
 * settled states. A poller sees the run while it is still going, so a job stage
 * also carries `queued` and `running`. `done` / `failed` / `skipped` are final.
 */
export interface AiJobStage {
  name: string;
  state: 'queued' | 'running' | 'done' | 'failed' | 'skipped';
  detail?: string;
  ms?: number;
}

/** What a submission with `Prefer: respond-async` (or `async=1`) answers with:
    the id to poll, and what the run is. */
export interface AiJobHandle {
  id: string;
  kind: 'challenge' | 'course';
  status: string;
}

/**
 * A generation in flight, as `GET /generation/{id}` reports it.
 *
 * The job is a *view* of the run, not the durable record: `course_id` is named
 * as soon as the outline is stored, and the course it names already answers
 * `GET /courses/{id}` with the chapters that have verified so far. The stored
 * record, not the job, is what a learner keeps.
 */
export interface AiJob {
  id: string;
  kind: 'challenge' | 'course';
  /** `running` while the run is going; otherwise one of the terminal states a
      job shares with an outcome envelope (`active` / `rejected` /
      `unavailable` / `failed`), plus `awaiting_approval` (a paused course run)
      and `discarded` (a paused run whose course was removed). Read through
      `jobIsRunning` rather than compared by hand, so an unrecognised status
      keeps the poller waiting instead of being mistaken for a finish. */
  status: string;
  stages: AiJobStage[];
  attempts: AiAttempt[];
  /** The per-chapter verification report in outline order, once chapters have
      been planned. A course job only. */
  chapters?: AiChapterResult[];
  chapters_planned?: number;
  chapters_verified?: number;
  /** The chapter the gateway is verifying now. It keeps its name until the next
      chapter is handed in, so it says which chapter the run is on. */
  current_chapter?: string;
  error?: AiError | null;
  /** Present once a course was stored — a `building` one while the run is still
      writing chapters. Absent on a refused run, which stored nothing. */
  course_id?: string;
  challenge_id?: string;
  created_at?: string;
  finished_at?: string;
}

export interface AiEvidence {
  reference_rows?: number;
  cross_check_rows?: number;
  fixtures_rejected?: number;
  fixtures_total?: number;
  repairs?: number;
  scratch_database?: string;
}

/** M7 — a generated course. The service returns a plan only when every one of
    its exercises was executed and cross-checked, so a plan's presence is the
    activation, not a draft. `expected` on each exercise is the engine's own
    answer, measured during verification: it is what a learner's rows are
    compared against, and it is never something the model supplied. */
export interface AiCourseSection {
  title: string;
  /** Markdown, rendered with the same renderer the authored chapters use. */
  body: string;
}

export interface AiCourseExercise {
  id: string;
  title: string;
  prompt: string;
  difficulty: AiDifficulty;
  type?: string;
  objectives: string[];
  hints: string[];
  reference_solution: string;
  cross_check_solution: string;
  result_contract: AiResultContract;
  checks?: AiCheck[];
  expected?: { columns: string[]; rows: unknown[][] };
}

export interface AiCourseChapter {
  id: string;
  n?: number;
  title: string;
  summary: string;
  kind?: string;
  minutes?: number;
  objectives: string[];
  sections: AiCourseSection[];
  exercises: AiCourseExercise[];
}

export interface AiCoursePlan {
  id: string;
  version: number;
  title: string;
  subtitle: string;
  level: string;
  dialect?: 'postgresql';
  objectives: string[];
  setup: AiSetupPlan;
  chapters: AiCourseChapter[];
  provenance: AiProvenance;
}

export type AiExerciseStatus = 'verified' | 'failed';

/** What verification measured for one exercise. Reported for a rejection too:
    "which exercise failed, and what did the engine say" is the useful part. */
export interface AiExerciseResult {
  id: string;
  title: string;
  status: AiExerciseStatus | string;
  reference_rows?: number;
  cross_check_rows?: number;
  fixtures_rejected?: number;
  fixtures_total?: number;
  ms?: number;
  error?: AiError;
}

export interface AiChapterResult {
  id: string;
  title: string;
  exercises: AiExerciseResult[];
}

export interface AiOutcome {
  /** `status` is the run's own verdict. `active` is the only one that puts a
      course in front of learners; `awaiting_approval` is a course run that
      stopped after storing its outline and is waiting for the learner to
      approve it (`approveCourse`). */
  status: 'active' | 'rejected' | 'unavailable' | 'failed' | 'awaiting_approval';
  kind?: 'challenge' | 'course';
  artifact: AiArtifact | null;
  /** Present for a course that was activated, and for one that stopped at
      `awaiting_approval` (the outline being approved). */
  plan?: AiCoursePlan | null;
  /** Per-chapter verification report; present on a rejection as well. */
  chapters?: AiChapterResult[];
  stages: AiStage[];
  attempts: AiAttempt[];
  evidence?: AiEvidence;
  error: AiError | null;
}

export interface AiGenerationRequest {
  kind?: 'challenge' | 'course';
  title?: string;
  topics?: string[];
  difficulty?: AiDifficulty;
  scenario?: string;
  constraints?: string[];
  exercise_count?: number;
  steps?: string[];
  /** Course only. Ask the run to stop after the outline is validated and
      stored, so the learner can approve the index before the chapters are
      written. The response status is then `awaiting_approval` and
      `approveCourse` continues it. Absent (false) is today's one-request run:
      outline straight into chapters. */
  require_approval?: boolean;
}

/** The answer to an approval (`POST /courses/{id}/build`): either a job to
    poll (when the client asked for one) or the blocking envelope. Exactly one
    half is set, so a caller can follow a job or read an outcome and never
    mistake one for the other. */
export interface AiCourseBuildAnswer {
  job: AiJobHandle | null;
  outcome: AiOutcome | null;
}

export interface AiAvailability {
  configured: boolean;
  available: boolean;
  provider: string;
  model?: string;
  deterministic?: boolean;
  reason: string;
  /** The gateway's run budget in milliseconds: how long a generation may take
      before the pipeline answers with its own failure. The UI states it, and
      the transport waits at least this long, so "still working" is never shown
      as "unreachable". */
  timeout_ms?: number;
}

export interface AiTutorRequest {
  challenge_id?: string;
  sql: string;
  objective: string;
  error_code?: string;
  error_message?: string;
  hint_level?: number;
  result_summary?: string;
}

export interface AiTutorResponse {
  message: string;
  hint_level: number;
  reveals_solution: boolean;
  provenance?: AiProvenance;
}

export interface AiCritiqueResponse {
  message: string;
  notes: string[];
  provenance?: AiProvenance;
}

export interface AiDefectReport {
  challenge_id: string;
  description: string;
  sql?: string;
  expected?: string;
  observed?: string;
}

export interface AiPatchProposal {
  id: string;
  challenge_id: string;
  summary: string;
  changes: { field: string; from: string; to: string }[];
}

export interface AiDefectVerdict {
  verified: boolean;
  applied: boolean;
  summary: string;
  patch: AiPatchProposal | null;
  evidence?: AiEvidence;
  error: AiError | null;
}

/** A stored challenge plus the evidence captured when it was allowed in. */
export interface AiRecord {
  artifact: AiArtifact;
  outcome: AiOutcome;
  status: 'active' | 'rejected';
  owner_id?: string;
  created_at: string;
}

/** M8 — a generated course the learner owns, as the gateway stores it.
 *
 * This is the record the Library and a deep link read, so it is the thing that
 * makes a generated course survive a reload: the plan is the whole verified
 * course, and `outcome` is the run that produced it (its stage list and
 * per-chapter report, which is where the verified counts come from).
 *
 * `status` is the record's own lifecycle, and it is deliberately an open string
 * rather than a closed union. `active` is a finished course; `building` is one
 * the pipeline is still writing a chapter at a time, carrying the chapters that
 * already verified. A client that could only read `active` would have to throw
 * away a course that is half finished and real, which is the opposite of what a
 * learner watching a build wants.
 *
 * `owner_id` names the account that owns the record. The gateway scopes every
 * read to the session, so this is only ever the caller's own id; it travels as
 * `undefined` when the route withheld it.
 */
export interface AiCourseRecord {
  plan: AiCoursePlan;
  outcome: AiOutcome;
  status: string;
  owner_id?: string;
  created_at: string;
}
