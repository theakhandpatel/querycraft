/*
 * Splitting one progress snapshot across the design 8.1 stores.
 *
 * The UI keeps working with one in-memory snapshot (`web/src/lib/progress.ts`),
 * and `progress` stores it whole so the next boot is a single read. This module
 * derives the per-entity rows the other stores hold, so the workbook, search
 * and progress views can address a bookmark, a note, a saved query or an
 * attempt without loading everything.
 *
 * Pure and storage-free on purpose: the adapter writes what this returns and
 * `scripts/storage-audit.mjs` reads it back, so the split is checked without a
 * browser. Version metadata is passed in rather than imported, because the
 * dataset version lives in raw SQL the audit has no loader for.
 */
import {
  STORE_ATTEMPTS,
  STORE_BOOKMARKS,
  STORE_EVALUATIONS,
  STORE_META,
  STORE_NOTES,
  STORE_PROGRESS,
  STORE_SAVED_QUERIES,
} from '../api/idb';
import { attemptKey } from './progress';
import type { EventKind, Progress, ProgressEvent } from '../types';

interface OfflineRow {
  store: string;
  key: string;
  value: unknown;
}

/** The single `meta` record: what the workspace was written against. */
const META_KEY = 'workspace';

interface WorkspaceMeta {
  dataset_version: number;
  export_format_version: number;
  updated_at: string;
  counts: {
    attempts: number;
    evaluations: number;
    notes: number;
    bookmarks: number;
    saved_queries: number;
  };
}

/** Attempts already carry the outcome; these are the ones grading produced. */
const GRADED_KINDS: ReadonlySet<EventKind> = new Set<EventKind>(['passed', 'failed', 'accepted', 'error']);

interface AttemptRecord {
  id: string;
  course: string;
  lesson: number | null;
  kind: EventKind;
  title: string;
  ts: number;
  dataset_version?: number;
  exercise_version?: number;
}

interface EvaluationRecord {
  /** The attempt this evaluates (attempt id), so the two stores line up. */
  attempt_id: string;
  exercise_id: string;
  course: string;
  lesson: number | null;
  kind: EventKind;
  passed: boolean;
  ts: number;
  dataset_version?: number;
  exercise_version?: number;
}

interface WorkbookRecord {
  course: string;
  lesson: number;
  ts: number;
  text: string;
  /** Position in the snapshot array, so two notes in the same millisecond still
      get distinct keys. */
  index: number;
}

interface SavedQueryRecord {
  course: string;
  lesson: number;
  name: string;
  index: number;
}

function attemptRow(evt: ProgressEvent): OfflineRow {
  const record: AttemptRecord = {
    id: evt.id,
    course: evt.course,
    lesson: evt.lesson ?? null,
    kind: evt.kind,
    title: evt.title,
    ts: evt.ts,
  };
  if (evt.datasetVersion !== undefined) record.dataset_version = evt.datasetVersion;
  if (evt.exerciseVersion !== undefined) record.exercise_version = evt.exerciseVersion;
  return { store: STORE_ATTEMPTS, key: attemptKey(evt), value: record };
}

function evaluationRow(evt: ProgressEvent): OfflineRow | null {
  if (!GRADED_KINDS.has(evt.kind)) return null;
  const record: EvaluationRecord = {
    attempt_id: attemptKey(evt),
    exercise_id: evt.id,
    course: evt.course,
    lesson: evt.lesson ?? null,
    kind: evt.kind,
    passed: evt.kind === 'passed' || evt.kind === 'accepted',
    ts: evt.ts,
  };
  if (evt.datasetVersion !== undefined) record.dataset_version = evt.datasetVersion;
  if (evt.exerciseVersion !== undefined) record.exercise_version = evt.exerciseVersion;
  return { store: STORE_EVALUATIONS, key: attemptKey(evt), value: record };
}

export function bundleFromProgress(
  progress: Progress,
  opts: { courseId: string; datasetVersion: number; exportFormatVersion: number; now?: Date },
): OfflineRow[] {
  const rows: OfflineRow[] = [{ store: STORE_PROGRESS, key: opts.courseId, value: progress }];

  const attempts = progress.events ?? [];
  let evaluations = 0;
  for (const evt of attempts) {
    rows.push(attemptRow(evt));
    const evaluation = evaluationRow(evt);
    if (evaluation) {
      rows.push(evaluation);
      evaluations++;
    }
  }

  let notes = 0;
  let bookmarks = 0;
  (progress.notes ?? []).forEach((item, index) => {
    const isBookmark = item.kind === 'bookmark';
    const record: WorkbookRecord = { course: item.course, lesson: item.lesson, ts: item.ts, text: item.text, index };
    rows.push({
      store: isBookmark ? STORE_BOOKMARKS : STORE_NOTES,
      key: `${isBookmark ? 'bookmark' : 'note'}:${item.course}:${index}`,
      value: record,
    });
    if (isBookmark) bookmarks++;
    else notes++;
  });

  const saved = progress.saved ?? [];
  saved.forEach((item, index) => {
    const record: SavedQueryRecord = { course: item.course, lesson: item.lesson, name: item.name, index };
    rows.push({ store: STORE_SAVED_QUERIES, key: `saved:${item.course}:${index}`, value: record });
  });

  const meta: WorkspaceMeta = {
    dataset_version: opts.datasetVersion,
    export_format_version: opts.exportFormatVersion,
    updated_at: (opts.now ?? new Date()).toISOString(),
    counts: {
      attempts: attempts.length,
      evaluations,
      notes,
      bookmarks,
      saved_queries: saved.length,
    },
  };
  rows.push({ store: STORE_META, key: META_KEY, value: meta });

  return rows;
}
