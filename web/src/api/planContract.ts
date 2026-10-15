/**
 * The M5 plan contract, mirroring `contracts/plan.schema.json` and
 * `contracts/perf-contract.schema.json`.
 *
 * This module is the one JavaScript vocabulary for a captured plan. Both the
 * browser adapter (`./pglite`) and the gateway adapter (`./execution`) hand
 * whatever the engine produced through `buildPlanBundle`, so the two can never
 * describe the same plan with two different `summary` shapes. The Go engine
 * writes the same shape in `backend/internal/perf/plan.go`; the JSON Schemas in
 * `contracts/` are what keeps the two languages honest, and
 * `web/scripts/plan-conformance-audit.mjs` runs those schemas against every
 * fixture.
 *
 * Two rules are structural, not stylistic:
 *
 *   - `plan` is untrusted text from an engine. It is reported verbatim and is
 *     never graded. `PlanView` renders it as escaped text, never as HTML.
 *   - No graded assertion reads a timing. `gradePlan` walks `summary` only;
 *     `actual_time_ms` and `duration_ms` are for the learner to look at, not for
 *     a verdict to depend on.
 */

import { Schema } from 'effect';
import type { DatasetId } from '../types';

/* ---------- bundle ---------- */

export type PlanStatus = 'succeeded' | 'failed';

/** What the numbers beside a plan are worth. `analyze`: the statement ran, so
    per-node timings are measured. `estimate`: the planner's cost model only, no
    timings exist. `none`: the request failed, so there is nothing to show. */
export type TimingPrecision = 'analyze' | 'estimate' | 'none';

export type PlanEngine = 'postgresql' | (string & {});

/** One node of the flat, depth-first walk the viewer renders. `actual_*` are
    null unless `timing_precision` is `analyze`. */
export interface PlanNode {
  depth: number;
  node_type: string;
  relation: string | null;
  alias: string | null;
  index: string | null;
  join_type: string | null;
  parent_relationship: string | null;
  estimated_rows: number | null;
  estimated_cost: number | null;
  actual_rows: number | null;
  actual_time_ms: number | null;
  loops: number | null;
  buffers_read: number | null;
  filter: string | null;
  index_condition: string | null;
  sort_key: string[] | null;
  warnings: string[];
}

export interface PlanSeqScan {
  relation: string;
  plan_rows: number;
}

export interface PlanIndexScan {
  relation: string;
  index: string;
  node_type: string;
}

/** The deterministic, gradeable facts. Everything `gradePlan` reads lives here. */
export interface PlanSummary {
  node_count: number;
  node_types: string[];
  relations: string[];
  indexes_used: string[];
  seq_scans: PlanSeqScan[];
  index_scans: PlanIndexScan[];
  max_estimated_rows: number;
  plan_rows: number;
  total_cost: number;
  nodes: PlanNode[];
}

export interface PlanIndex {
  relation: string;
  name: string;
  definition?: string | null;
}

/** What the plan was produced against: a number is never reported without the
    scale and settings that produced it (M5 exit gate). */
export interface PlanEnvironment {
  engine: string;
  engine_version: string;
  scale_factor: number;
  dataset?: string | null;
  row_counts: Record<string, number>;
  indexes: PlanIndex[];
  settings: Record<string, string>;
}

export type PlanWarningCode =
  | 'PLAN_SEQ_SCAN_LARGE_TABLE'
  | 'PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE'
  | 'PLAN_NODE_CAP_REACHED'
  | 'PLAN_ROW_CAP_REACHED'
  | 'PLAN_SETTINGS_NON_DEFAULT'
  | 'PLAN_ANALYZE_SKIPPED_NO_ENVIRONMENT';

export interface PlanWarning {
  code: PlanWarningCode;
  message: string;
  relation?: string | null;
  node_type?: string | null;
}

/** The same envelope as `QueryResult`, so one error taxonomy serves both. */
export interface PlanErrorShape {
  code: string;
  message: string;
  detail?: string | null;
}

/** The raw engine plan node, verbatim and untrusted. */
export type PlanRawNode = Record<string, unknown>;

export interface PlanBundle {
  status: PlanStatus;
  /** True when the statement was executed (EXPLAIN ANALYZE), false when only
      planned. */
  analyze: boolean;
  timing_precision: TimingPrecision;
  engine: PlanEngine;
  /** The root plan node exactly as the engine emitted it. Null on failure. */
  plan: PlanRawNode | null;
  summary: PlanSummary;
  environment: PlanEnvironment;
  warnings: PlanWarning[];
  duration_ms: number;
  statement?: string | null;
  request_id?: string | null;
  error: PlanErrorShape | null;
}

/* ---------- the perf contract ---------- */

export interface PerfRequireNode {
  node_type: string;
  relation?: string | null;
  index?: string | null;
  label?: string | null;
}

export interface PerfForbidNode {
  node_type: string;
  relation: string;
  above_plan_rows?: number;
  label?: string | null;
}

/** An exercise's structural expectations about the plan a query produces.
    Structural facts only — there is deliberately no timing field. */
export interface PerfContract {
  engine?: 'postgresql';
  require_nodes?: PerfRequireNode[];
  forbid_nodes?: PerfForbidNode[];
  require_indexes?: string[];
  max_plan_rows?: number | null;
  min_index_scans?: number | null;
}

/** The `POST /explain` request. `analyze` executes the statement, so the
    gateway only honours it in a writable environment. */
export interface ExplainRequest {
  sql: string;
  analyze?: boolean;
  /** M5. The fixture to plan against. Left undefined, the dataset resolver
      reads `exercise.perf` (a perf contract is its own evidence of scale) and
      falls back to commerce, which is exactly what the Go gateway's
      `resolveDataset` does — so a plan request means one database on both
      adapters. */
  dataset?: DatasetId;
  exercise?: {
    id?: string;
    version?: number;
    lesson_id?: string;
    perf?: PerfContract;
  };
}

/* ---------- validation ---------- */

/** Thrown by `validatePlanBundle` when a body is not a `PlanBundle`. Typed, so
    a caller can tell "the gateway is broken" apart from "the gateway is down". */
export class PlanContractError extends Error {
  readonly code = 'PLAN_CONTRACT_INVALID';
  readonly issues: string[];
  constructor(issues: string[]) {
    super(`The plan bundle does not satisfy contracts/plan.schema.json: ${issues.join('; ')}`);
    this.name = 'PlanContractError';
    this.issues = issues;
  }
}

const PlanWarningCodeSchema = Schema.Literals([
  'PLAN_SEQ_SCAN_LARGE_TABLE',
  'PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE',
  'PLAN_NODE_CAP_REACHED',
  'PLAN_ROW_CAP_REACHED',
  'PLAN_SETTINGS_NON_DEFAULT',
  'PLAN_ANALYZE_SKIPPED_NO_ENVIRONMENT',
]);

const PlanNodeSchema = Schema.Struct({
  depth: Schema.Number,
  node_type: Schema.String,
  relation: Schema.optional(Schema.NullOr(Schema.String)),
  alias: Schema.optional(Schema.NullOr(Schema.String)),
  index: Schema.optional(Schema.NullOr(Schema.String)),
  join_type: Schema.optional(Schema.NullOr(Schema.String)),
  parent_relationship: Schema.optional(Schema.NullOr(Schema.String)),
  estimated_rows: Schema.optional(Schema.NullOr(Schema.Number)),
  estimated_cost: Schema.optional(Schema.NullOr(Schema.Number)),
  actual_rows: Schema.optional(Schema.NullOr(Schema.Number)),
  actual_time_ms: Schema.optional(Schema.NullOr(Schema.Number)),
  loops: Schema.optional(Schema.NullOr(Schema.Number)),
  buffers_read: Schema.optional(Schema.NullOr(Schema.Number)),
  filter: Schema.optional(Schema.NullOr(Schema.String)),
  index_condition: Schema.optional(Schema.NullOr(Schema.String)),
  sort_key: Schema.optional(Schema.NullOr(Schema.Array(Schema.String))),
  warnings: Schema.optional(Schema.Array(Schema.String)),
});

const PlanSummarySchema = Schema.Struct({
  node_count: Schema.Number,
  node_types: Schema.Array(Schema.String),
  relations: Schema.Array(Schema.String),
  indexes_used: Schema.Array(Schema.String),
  seq_scans: Schema.Array(
    Schema.Struct({ relation: Schema.String, plan_rows: Schema.Number }),
  ),
  index_scans: Schema.Array(
    Schema.Struct({ relation: Schema.String, index: Schema.String, node_type: Schema.String }),
  ),
  max_estimated_rows: Schema.Number,
  plan_rows: Schema.Number,
  total_cost: Schema.Number,
  nodes: Schema.Array(PlanNodeSchema),
});

const PlanEnvironmentSchema = Schema.Struct({
  engine: Schema.String,
  engine_version: Schema.String,
  scale_factor: Schema.Number,
  dataset: Schema.optional(Schema.NullOr(Schema.String)),
  row_counts: Schema.Record(Schema.String, Schema.Number),
  indexes: Schema.Array(
    Schema.Struct({
      relation: Schema.String,
      name: Schema.String,
      definition: Schema.optional(Schema.NullOr(Schema.String)),
    }),
  ),
  settings: Schema.Record(Schema.String, Schema.String),
});

const PlanWarningSchema = Schema.Struct({
  code: PlanWarningCodeSchema,
  message: Schema.String,
  relation: Schema.optional(Schema.NullOr(Schema.String)),
  node_type: Schema.optional(Schema.NullOr(Schema.String)),
});

const PlanErrorSchema = Schema.Struct({
  code: Schema.String,
  message: Schema.String,
  detail: Schema.optional(Schema.NullOr(Schema.String)),
});

export const PlanBundleSchema = Schema.Struct({
  status: Schema.Literals(['succeeded', 'failed']),
  analyze: Schema.Boolean,
  timing_precision: Schema.Literals(['analyze', 'estimate', 'none']),
  engine: Schema.String,
  plan: Schema.NullOr(Schema.Record(Schema.String, Schema.Unknown)),
  summary: PlanSummarySchema,
  environment: PlanEnvironmentSchema,
  warnings: Schema.Array(PlanWarningSchema),
  duration_ms: Schema.Number,
  statement: Schema.optional(Schema.NullOr(Schema.String)),
  request_id: Schema.optional(Schema.NullOr(Schema.String)),
  error: Schema.NullOr(PlanErrorSchema),
});

/** Parse a plan bundle against the same shape `contracts/plan.schema.json`
    describes, throwing a `PlanContractError` rather than returning a half-read
    object. A malformed gateway body must fail loudly, not render emptiness. */
export function validatePlanBundle(value: unknown): PlanBundle {
  try {
    return Schema.decodeUnknownSync(PlanBundleSchema)(value) as unknown as PlanBundle;
  } catch (e) {
    throw new PlanContractError(schemaIssues(e));
  }
}

/** Flatten an Effect `SchemaError` into readable `path: message` strings. */
function schemaIssues(e: unknown): string[] {
  const issue = (e as { issue?: unknown } | null)?.issue;
  const leaves: string[] = [];
  const visit = (node: unknown, path: string[]): void => {
    if (!isRecord(node)) return;
    const nextPath = Array.isArray(node.path) ? path.concat(node.path.map(String)) : path;
    const nested = Array.isArray(node.issues) ? node.issues : null;
    if (nested) {
      for (const child of nested) visit(child, nextPath);
      return;
    }
    const message = typeof node.message === 'string' ? node.message : null;
    if (message) leaves.push(nextPath.length ? `${nextPath.join('.')}: ${message}` : message);
  };
  visit(issue, []);
  if (leaves.length) return [...new Set(leaves)];
  return [e instanceof Error ? e.message : String(e)];
}

/* ---------- building a bundle from a raw engine plan ---------- */

/** How many planner-estimated rows make a sequential scan worth flagging. The
    same threshold the Go engine uses for `PLAN_SEQ_SCAN_LARGE_TABLE`; a Seq
    Scan over a twelve-row dimension table is correct and stays quiet. */
export const SEQ_SCAN_LARGE_ROWS = 1000;

/** The ratio at/above which an estimate is called off by orders of magnitude. */
export const ESTIMATE_OFF_RATIO = 100;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function num(node: Record<string, unknown>, ...keys: string[]): number | null {
  for (const key of keys) {
    const v = node[key];
    if (typeof v === 'number' && Number.isFinite(v)) return v;
  }
  return null;
}

function str(node: Record<string, unknown>, ...keys: string[]): string | null {
  for (const key of keys) {
    const v = node[key];
    if (typeof v === 'string') return v;
  }
  return null;
}

/**
 * PostgreSQL's `EXPLAIN (FORMAT JSON)` document, reduced to its root node.
 *
 * The engine wraps the plan as `[{"Plan": {...}}]`; the root node is
 * `doc.Plan`. Anything that is already the node (`{"Node Type": ...}`) is taken
 * as-is, so a caller that stored the unwrapped node still works. Anything else
 * is not a plan and reads as null rather than inventing one.
 */
export function planRootOf(raw: unknown): PlanRawNode | null {
  let doc: unknown = raw;
  if (Array.isArray(doc)) doc = doc[0];
  if (!isRecord(doc)) return null;
  const plan = doc.Plan;
  if (isRecord(plan)) return plan;
  return isRecord(doc) && typeof doc['Node Type'] === 'string' ? (doc as PlanRawNode) : null;
}

/** Parse the engine's JSON when it arrived as text (a `json` column that was not
    auto-decoded, or a body read as a string). */
export function parsePlanJson(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

function blockReads(node: Record<string, unknown>): number | null {
  const read = num(node, 'Shared Read Blocks', 'Read Blocks', 'Local Read Blocks', 'Temp Read Blocks');
  if (read !== null) {
    const local = num(node, 'Local Read Blocks');
    const temp = num(node, 'Temp Read Blocks');
    const shared = num(node, 'Shared Read Blocks', 'Read Blocks');
    const total = (shared ?? 0) + (local ?? 0) + (temp ?? 0);
    return total > 0 ? total : read;
  }
  return null;
}

function walk(root: PlanRawNode, depth: number, out: PlanNode[], parentRelation: string | null): void {
  const nodeType = str(root, 'Node Type');
  if (!nodeType) return;
  const estimated = num(root, 'Plan Rows');
  const actual = num(root, 'Actual Rows');
  /* A Bitmap Index Scan names the index but not the relation: PostgreSQL
     prints the relation on the Bitmap Heap Scan above it and leaves it off the
     scan underneath, so the child is read back against its parent's relation.
     Without this, `index_scans` and `relations` silently drop every bitmap
     access — the common shape for a selective predicate — and an exercise that
     states "two index scans" is graded against a summary that counts one. */
  const relation = str(root, 'Relation Name') ?? parentRelation;
  const node: PlanNode = {
    depth,
    node_type: nodeType,
    relation,
    alias: str(root, 'Alias'),
    index: str(root, 'Index Name'),
    join_type: str(root, 'Join Type'),
    parent_relationship: str(root, 'Parent Relationship'),
    estimated_rows: estimated,
    estimated_cost: num(root, 'Total Cost'),
    actual_rows: actual,
    actual_time_ms: num(root, 'Actual Total Time'),
    loops: num(root, 'Actual Loops'),
    buffers_read: blockReads(root),
    filter: str(root, 'Filter'),
    index_condition: str(root, 'Index Cond'),
    sort_key: Array.isArray(root['Sort Key']) ? (root['Sort Key'] as unknown[]).map(String) : null,
    warnings: [],
  };
  /* A per-node estimate flag, so the viewer can mark the row without recomputing
     the rule (and so a bundle exported by the Go side carries the same note). */
  if (
    estimated !== null &&
    actual !== null &&
    ((estimated > 0 && actual / estimated >= ESTIMATE_OFF_RATIO) ||
      (actual > 0 && estimated / actual >= ESTIMATE_OFF_RATIO))
  ) {
    node.warnings.push('PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE');
  }
  out.push(node);
  const children = root.Plans;
  if (Array.isArray(children)) {
    for (const child of children) {
      if (isRecord(child)) walk(child as PlanRawNode, depth + 1, out, relation);
    }
  }
}

/** The identical depth-first walk the Go engine performs. Given the raw plan
    node tree it returns the deterministic facts `gradePlan` reads. */
export function summarizePlan(planRoot: unknown): PlanSummary {
  const root = planRootOf(planRoot);
  const nodes: PlanNode[] = [];
  if (root) walk(root, 0, nodes, null);

  const nodeTypes: string[] = [];
  const relations: string[] = [];
  const indexesUsed: string[] = [];
  const seqScans: PlanSeqScan[] = [];
  const indexScans: PlanIndexScan[] = [];
  for (const n of nodes) {
    if (!nodeTypes.includes(n.node_type)) nodeTypes.push(n.node_type);
    if (n.relation && !relations.includes(n.relation)) relations.push(n.relation);
    if (n.index && !indexesUsed.includes(n.index)) indexesUsed.push(n.index);
    if (n.node_type === 'Seq Scan' && n.relation) {
      seqScans.push({ relation: n.relation, plan_rows: n.estimated_rows ?? 0 });
    }
    if (n.index && n.relation && /index/i.test(n.node_type)) {
      indexScans.push({ relation: n.relation, index: n.index, node_type: n.node_type });
    }
  }

  const estimated = nodes.map((n) => n.estimated_rows).filter((n): n is number => n !== null);
  const rootEstimate = root ? num(root, 'Plan Rows') : null;
  return {
    node_count: nodes.length,
    node_types: nodeTypes,
    relations,
    indexes_used: indexesUsed,
    seq_scans: seqScans,
    index_scans: indexScans,
    max_estimated_rows: estimated.length ? Math.max(...estimated) : 0,
    plan_rows: rootEstimate ?? 0,
    total_cost: root ? num(root, 'Total Cost') ?? 0 : 0,
    nodes,
  };
}

/** The warnings the summary itself justifies, computed the same way on both
    engines. Callers may add engine-specific ones, but never remove these. */
export function deriveWarnings(summary: PlanSummary, precision: TimingPrecision): PlanWarning[] {
  const warnings: PlanWarning[] = [];
  const large = summary.seq_scans.filter((s) => s.plan_rows >= SEQ_SCAN_LARGE_ROWS);
  if (large.length) {
    warnings.push({
      code: 'PLAN_SEQ_SCAN_LARGE_TABLE',
      message: `${large.map((s) => s.relation).join(', ')} ${large.length === 1 ? 'is' : 'are'} read by a sequential scan over ${SEQ_SCAN_LARGE_ROWS}+ planned rows.`,
    });
  }
  if (precision === 'analyze' && summary.nodes.some((n) => n.warnings.includes('PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE'))) {
    warnings.push({
      code: 'PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE',
      message: `At least one node's actual rows differ from the planner's estimate by ${ESTIMATE_OFF_RATIO}x or more.`,
    });
  }
  return warnings;
}

export interface PlanCaptureInput {
  /** The engine's EXPLAIN output, either raw JSON or the JSON text of it. */
  plan: unknown;
  analyze: boolean;
  timing_precision: TimingPrecision;
  engine: string;
  environment: PlanEnvironment;
  duration_ms: number;
  statement?: string | null;
  request_id?: string | null;
  error?: PlanErrorShape | null;
  /** Extra engine warnings, merged with the ones the summary justifies. */
  warnings?: PlanWarning[];
}

/** Turn one engine EXPLAIN result into the bundle shape. The single place a
    plan becomes a `PlanBundle`, so the browser and the gateway cannot drift. */
export function buildPlanBundle(input: PlanCaptureInput): PlanBundle {
  const root = planRootOf(parsePlanJson(input.plan));
  const summary = summarizePlan(root);
  const warnings = [...deriveWarnings(summary, input.timing_precision), ...(input.warnings ?? [])];
  return {
    status: input.error ? 'failed' : 'succeeded',
    analyze: input.analyze,
    timing_precision: input.timing_precision,
    engine: input.engine,
    plan: root,
    summary,
    environment: input.environment,
    warnings,
    duration_ms: input.duration_ms,
    statement: input.statement ?? null,
    request_id: input.request_id ?? null,
    error: input.error ?? null,
  };
}

/** An honest refusal: no plan was produced, so no numbers are invented. */
export function failedPlanBundle(
  engine: string,
  code: string,
  message: string,
  detail?: string | null,
  environment?: PlanEnvironment,
): PlanBundle {
  return {
    status: 'failed',
    analyze: false,
    timing_precision: 'none',
    engine,
    plan: null,
    summary: summarizePlan(null),
    environment: environment ?? emptyEnvironment(engine),
    warnings: [],
    duration_ms: 0,
    statement: null,
    request_id: null,
    error: { code, message, detail: detail ?? null },
  };
}

/** An empty environment block for a refusal. It says nothing was measured
    rather than reporting a scale the engine never saw. */
export function emptyEnvironment(engine: string): PlanEnvironment {
  return {
    engine,
    engine_version: '',
    scale_factor: 0,
    dataset: null,
    row_counts: {},
    indexes: [],
    settings: {},
  };
}

/* ---------- grading (structural only) ---------- */

/** The stable codes the Go grader emits (`backend/internal/perf/grade.go`).
    One vocabulary for both languages, like the result-contract codes. */
export type PerfCode =
  | 'PERF_NODE_MISSING'
  | 'PERF_NODE_FORBIDDEN'
  | 'PERF_INDEX_NOT_USED'
  | 'PERF_PLAN_ROWS_EXCEEDED'
  | 'PERF_DIALECT_MISMATCH';

/** Structurally identical to `CheckResult` in ../types (which cannot be imported
    here without a cycle: types imports this module). */
export interface PlanCheck {
  label: string;
  code: string;
  ok: boolean;
}

export interface PlanVerdict {
  passed: boolean;
  checks: PlanCheck[];
}

function nodeMatches(node: PlanNode, want: PerfRequireNode): boolean {
  if (node.node_type !== want.node_type) return false;
  if (want.relation != null && node.relation !== want.relation) return false;
  if (want.index != null && node.index !== want.index) return false;
  return true;
}

/**
 * Grade a captured plan against an exercise's perf contract.
 *
 * Every assertion reads `summary` — node types, relations, indexes and the
 * planner's row estimate. Nothing here reads `actual_time_ms`, `duration_ms` or
 * any buffer count, because a timing is not reproducible on a shared machine;
 * M5 reports timings honestly and ungraded.
 */
export function gradePlan(bundle: PlanBundle, contract: PerfContract): PlanVerdict {
  if (bundle.status !== 'succeeded' || bundle.plan === null) {
    return {
      passed: false,
      checks: [
        {
          label: 'The plan could not be captured',
          code: bundle.error?.code ?? 'ENVIRONMENT_UNAVAILABLE',
          ok: false,
        },
      ],
    };
  }

  const checks: PlanCheck[] = [];
  const summary = bundle.summary;

  // The bundle's own top-level `engine` is the dialect that produced the plan;
  // `environment.engine` describes the environment, which may be absent. Both
  // sides of the seam compare the top-level field so the Go grader
  // (backend/internal/perf/grade.go) and this one cannot disagree.
  const contractEngine = contract.engine;
  if (contractEngine && contractEngine !== bundle.engine) {
    checks.push({
      label: `Runs on ${contractEngine} (got ${bundle.engine || 'an unknown engine'})`,
      code: 'PERF_DIALECT_MISMATCH',
      ok: false,
    });
  }

  for (const want of contract.require_nodes ?? []) {
    checks.push({
      label: want.label ?? `Plan contains a ${want.node_type} node${want.relation ? ` on ${want.relation}` : ''}`,
      code: 'PERF_NODE_MISSING',
      ok: summary.nodes.some((n) => nodeMatches(n, want)),
    });
  }

  for (const forbid of contract.forbid_nodes ?? []) {
    const threshold = forbid.above_plan_rows ?? 0;
    /* The threshold is exclusive, exactly as backend/internal/perf/grade.go
       reads it: the contract forbids the node *above* a row threshold, so a
       relation the planner expects to read at or below it stays legal. An
       inclusive comparison here fails a plan the server grades as correct —
       pinned by contracts/fixtures/perf-forbid-at-threshold-is-legal.json. */
    const present = summary.nodes.some(
      (n) =>
        n.node_type === forbid.node_type &&
        n.relation === forbid.relation &&
        (n.estimated_rows ?? 0) > threshold,
    );
    checks.push({
      label:
        forbid.label ??
        `No ${forbid.node_type} on ${forbid.relation}${threshold > 0 ? ` above ${threshold} estimated rows` : ''}`,
      code: 'PERF_NODE_FORBIDDEN',
      ok: !present,
    });
  }

  for (const index of contract.require_indexes ?? []) {
    checks.push({
      label: `Uses index ${index}`,
      code: 'PERF_INDEX_NOT_USED',
      ok: summary.indexes_used.includes(index),
    });
  }

  const minIndexScans = contract.min_index_scans;
  if (minIndexScans != null) {
    checks.push({
      label: `Uses at least ${minIndexScans} index scan${minIndexScans === 1 ? '' : 's'}`,
      code: 'PERF_INDEX_NOT_USED',
      ok: summary.index_scans.length >= minIndexScans,
    });
  }

  const maxPlanRows = contract.max_plan_rows;
  if (maxPlanRows != null) {
    checks.push({
      label: `Planner expects at most ${maxPlanRows} rows`,
      code: 'PERF_PLAN_ROWS_EXCEEDED',
      ok: summary.plan_rows <= maxPlanRows,
    });
  }

  return { passed: checks.every((c) => c.ok), checks };
}
