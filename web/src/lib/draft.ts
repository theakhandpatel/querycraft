/**
 * M7 — turning a verified course into the app's own model, and the build's
 * measured facts into the report the Review screen renders.
 *
 * The rule this module exists to keep: **nothing here invents a fact.** A
 * `Course` is built from a plan the gateway only returns when every exercise in
 * it was executed and cross-checked, so its exercises carry the engine's own
 * expected rows. The report is built from what the run measured — the gateway's
 * per-exercise verification report, or (when no provider is configured) the
 * reference queries this browser actually ran. When a number was not measured
 * there is no number, and the screens say so rather than printing a
 * plausible one.
 */

import type { AiCoursePlan, AiExerciseResult, AiOutcome } from '../api/aiContract';
import type { Block, BlockType, Course, CourseModule, RowValue } from '../types';

/** One chapter's worth of measured verification. */
export interface DraftChapterReport {
  id: string;
  title: string;
  sections: number;
  exercises: number;
  verified: number;
  rows: number;
  crossCheckRows: number;
  fixturesRejected: number;
  fixturesTotal: number;
  ms: number;
  /** Exercises that did not verify, with the stage that refused. */
  failures: DraftFailure[];
}

export interface DraftFailure {
  id: string;
  title: string;
  stage: string;
  message: string;
  detail?: string | null;
}

/**
 * What a build actually measured. `source` is the field that decides what the
 * UI may claim: `provider` means a model wrote the course and the gateway
 * verified it by execution; `authored` means the built-in curriculum was used
 * and this browser ran its references. They are different facts and the screens
 * never blur them.
 */
export interface DraftReport {
  source: 'provider' | 'authored';
  goal: string;
  /** ISO timestamp of when the run finished. */
  at: string;
  outcome: 'active' | 'rejected' | 'unavailable' | 'failed' | 'awaiting_approval';
  provider?: string;
  model?: string;
  pipelineVersion?: string;
  datasetVersion?: number;
  /** Bounded repairs the pipeline spent. Zero means none were needed. */
  repairs: number;
  chapters: DraftChapterReport[];
  totals: {
    chapters: number;
    sections: number;
    exercises: number;
    verified: number;
    rows: number;
    ms: number;
  };
  error?: { code: string; stage?: string; message: string; detail?: string | null } | null;
  /** The gateway's own stage list, verbatim, so the Review screen can show the
      pipeline that ran instead of describing one. */
  stages: { name: string; state: string; detail?: string; ms?: number }[];
}

/* ---------- plan -> course ---------- */

/** A generated exercise's expected rows, in the shape the grid and the grader
    use. Values arrive from the driver already normalized to their wire shape;
    anything else is stringified rather than dropped, so a row never silently
    loses a cell. */
function toRows(rows: unknown[][]): RowValue[][] {
  return rows.map((row) =>
    row.map((v): RowValue => {
      if (v === null || v === undefined) return null;
      const t = typeof v;
      if (t === 'string' || t === 'number' || t === 'boolean') return v as RowValue;
      return String(v);
    }),
  );
}

/**
 * Materialise a verified plan as a Course, so a generated course runs through
 * the same lesson page, evaluator, progress store and attempts history as an
 * authored one. Sections become markdown prose blocks (the lesson page renders
 * them with the same renderer the authored chapters use, Mermaid included);
 * exercises carry the plan's reference solution, contract and measured rows.
 *
 * `checks` are deliberately not carried across. The gateway verified each
 * exercise by executing its reference and cross-check and by rejecting
 * adversarial fixtures — all against the result contract. A provider-authored
 * `checks[].pattern` was never part of that verification, so forwarding it as a
 * grading rule would let an unverified regex fail a correct query. Grading is
 * the contract, exactly as the Review screen has always said.
 */
export function courseFromPlan(plan: AiCoursePlan, goal: string): Course {
  const modules: CourseModule[] = plan.chapters.map((ch, i) => ({
    id: ch.id,
    n: ch.n && ch.n > 0 ? ch.n : i + 1,
    title: ch.title,
    kind: ch.kind && ch.kind.trim() ? ch.kind : 'Generated',
    minutes: ch.minutes && ch.minutes > 0 ? ch.minutes : 12,
    summary: ch.summary,
    objectives: ch.objectives,
    blocks: [
      ...ch.sections.map((s): Block => ({
        type: 'prose' as BlockType,
        html: `## ${s.title}\n\n${s.body}`,
        markdown: true,
        section: s.title,
      })),
      ...ch.exercises.map((ex): Block => ({
        type: 'exercise' as BlockType,
        id: ex.id,
        prompt: ex.prompt,
        solution: ex.reference_solution,
        hints: ex.hints,
        contract: {
          columns: ex.result_contract.columns,
          column_order: ex.result_contract.column_order,
          row_order: ex.result_contract.row_order,
          duplicates: ex.result_contract.duplicates,
          numeric_tolerance: ex.result_contract.numeric_tolerance,
        },
        result: ex.expected ? { columns: ex.expected.columns, rows: toRows(ex.expected.rows) } : undefined,
      })),
    ],
  }));

  const minutes = modules.reduce((total, m) => total + (m.minutes || 0), 0);
  return {
    id: plan.id,
    title: plan.title,
    subtitle: plan.subtitle,
    level: plan.level,
    source: 'generated',
    enrolled: true,
    built: true,
    dialect: plan.dialect || 'postgresql',
    /* M9. The record's own setup plan travels with the course so "restore my
       data" can rebuild the workspace the exercises were verified in; it is the
       stored value, never one this function derived. */
    setup: plan.setup,
    minutes,
    modules,
    blurb: plan.subtitle,
    goal,
    provenance: plan.provenance,
  } as Course;
}

/* ---------- outcome -> report ---------- */

function sum(result: AiExerciseResult): number {
  return typeof result.reference_rows === 'number' ? result.reference_rows : 0;
}

/**
 * The gateway's outcome as the report the screens render. Every field is read
 * off the envelope; nothing is defaulted to a flattering value. A course that
 * was rejected reports `verified < exercises` and carries the failures.
 */
export function reportFromOutcome(outcome: AiOutcome, goal: string): DraftReport {
  const plan = outcome.plan ?? null;
  const reported = outcome.chapters ?? [];

  const chapters: DraftChapterReport[] = reported.map((ch, i) => {
    const planChapter = plan?.chapters?.[i];
    let verified = 0;
    let rows = 0;
    let crossCheckRows = 0;
    let fixturesRejected = 0;
    let fixturesTotal = 0;
    let ms = 0;
    const failures: DraftFailure[] = [];
    for (const ex of ch.exercises ?? []) {
      if (ex.status === 'verified') verified += 1;
      rows += sum(ex);
      crossCheckRows += typeof ex.cross_check_rows === 'number' ? ex.cross_check_rows : 0;
      fixturesRejected += typeof ex.fixtures_rejected === 'number' ? ex.fixtures_rejected : 0;
      fixturesTotal += typeof ex.fixtures_total === 'number' ? ex.fixtures_total : 0;
      ms += typeof ex.ms === 'number' ? ex.ms : 0;
      if (ex.status !== 'verified') {
        failures.push({
          id: ex.id,
          title: ex.title,
          stage: ex.error?.stage || 'verify',
          message: ex.error?.message || 'the exercise did not verify',
          detail: ex.error?.detail ?? null,
        });
      }
    }
    return {
      id: ch.id,
      title: ch.title,
      sections: planChapter?.sections?.length ?? 0,
      exercises: (ch.exercises ?? []).length || (planChapter?.exercises?.length ?? 0),
      verified,
      rows,
      crossCheckRows,
      fixturesRejected,
      fixturesTotal,
      ms,
      failures,
    };
  });

  return {
    source: 'provider',
    goal,
    at: new Date().toISOString(),
    outcome: (outcome.status as DraftReport['outcome']) ?? 'failed',
    provider: plan?.provenance?.provider,
    model: plan?.provenance?.model,
    pipelineVersion: plan?.provenance?.pipeline_version,
    datasetVersion: plan?.provenance?.dataset_version,
    repairs: typeof plan?.provenance?.repairs === 'number' ? plan.provenance.repairs : 0,
    chapters,
    totals: {
      chapters: plan?.chapters?.length ?? reported.length,
      sections: plan?.chapters?.reduce((n, c) => n + (c.sections?.length ?? 0), 0) ?? 0,
      exercises: plan?.chapters?.reduce((n, c) => n + (c.exercises?.length ?? 0), 0) ?? 0,
      verified: chapters.reduce((n, c) => n + c.verified, 0),
      rows: chapters.reduce((n, c) => n + c.rows, 0),
      ms: chapters.reduce((n, c) => n + c.ms, 0),
    },
    error: outcome.error
      ? { code: outcome.error.code, stage: outcome.error.stage, message: outcome.error.message, detail: outcome.error.detail ?? null }
      : null,
    stages: (outcome.stages ?? []).map((s) => ({ name: s.name, state: s.state, detail: s.detail, ms: s.ms })),
  };
}

/**
 * The report for a build that ran on the built-in curriculum because no
 * provider is configured. It records what this browser measured — the same
 * numbers the Building screen printed — and it says `authored`, so no screen
 * can present it as a generated course.
 */
export function authoredReport(
  goal: string,
  measured: { ran: number; failed: number; rows: number; ms: number },
  course: Course,
): DraftReport {
  const modules = course.modules ?? [];
  return {
    source: 'authored',
    goal,
    at: new Date().toISOString(),
    outcome: measured.failed > 0 ? 'failed' : 'active',
    repairs: 0,
    chapters: modules.map((m) => {
      const exercises = (m.blocks || []).filter((b) => b.type === 'exercise');
      return {
        id: m.id,
        title: m.title,
        sections: (m.blocks || []).filter((b) => b.type === 'prose').length,
        exercises: exercises.length,
        // The authored draft's exercises were not each run in this pass: the
        // build runs every reference it can find and reports the totals, so a
        // per-chapter split would be an invented one. Zero here means "not
        // measured per chapter", which is the truth.
        verified: 0,
        rows: 0,
        crossCheckRows: 0,
        fixturesRejected: 0,
        fixturesTotal: 0,
        ms: 0,
        failures: [],
      };
    }),
    totals: {
      chapters: modules.length,
      sections: modules.reduce((n, m) => n + (m.blocks || []).filter((b) => b.type === 'prose').length, 0),
      exercises: modules.reduce((n, m) => n + (m.blocks || []).filter((b) => b.type === 'exercise').length, 0),
      verified: measured.ran,
      rows: measured.rows,
      ms: measured.ms,
    },
    error: measured.failed > 0
      ? { code: 'AUTHORED_REFERENCE_FAILED', message: `${measured.failed} of the built-in course's reference queries failed on this engine.`, detail: null }
      : null,
    stages: [],
  };
}
