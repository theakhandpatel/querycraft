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

export interface AiEvidence {
  reference_rows?: number;
  cross_check_rows?: number;
  fixtures_rejected?: number;
  fixtures_total?: number;
  repairs?: number;
  scratch_database?: string;
}

export interface AiOutcome {
  status: 'active' | 'rejected' | 'unavailable' | 'failed';
  kind?: 'challenge' | 'course';
  artifact: AiArtifact | null;
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
}

export interface AiAvailability {
  configured: boolean;
  available: boolean;
  provider: string;
  model?: string;
  deterministic?: boolean;
  reason: string;
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
