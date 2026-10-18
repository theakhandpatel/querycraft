import { DEFAULT_DATASET, type Block, type Course, type CourseModule, type DatasetId } from '../types';

/* The dataset dimension (M5).
 *
 * A chapter teaches against exactly one fixture, and starting with M5 there is
 * more than one: the small commerce dataset every lesson was authored on, and
 * the larger perf dataset the `plans` course teaches access paths on. Which one
 * a statement runs against is not a UI preference — it is part of the exercise,
 * because a plan lesson only means something at a scale where the planner has a
 * choice.
 *
 * The resolution order is block -> course -> the app default, the same shape as
 * `effectiveDialect`, so a course says it once and a single exercise can still
 * differ. The `perf` dataset is a *heavier* workspace: a resolver that silently
 * fell back to commerce would grade a plan against rows that came from the
 * wrong fixture and call the learner wrong, so an unknown name is reported
 * (`datasetReason` returns 'unknown-dataset') rather than swallowed. */

/** The closed set of datasets the app can run, in contract order. */
export const DATASETS: readonly DatasetId[] = ['commerce', 'perf'];

/** What a caller knows about the workspace a statement belongs to. Same shape
    as `QueryContext` in ../types, restated here so this module has no import
    cycle with the adapter that consumes it. */
interface DatasetRef {
  dataset?: unknown;
  course?: Course | null;
  block?: Block | null;
  /* An exercise carrying a perf contract *says* which fixture it was authored
     against, so the contract itself is evidence of scale. It is read here for
     the same reason the Go gateway reads `exercise.perf` in `resolveDataset`:
     the two halves must resolve a plan request to the same database. */
  exercise?: { perf?: unknown } | null;
  /* A caller naturally has a module in hand and passes it for symmetry with
     `effectiveDialect`; a module does not carry its own dataset today, so it
     contributes nothing to the answer. Declaring it keeps the call sites shaped
     the same way whatever a future course wants. */
  module?: CourseModule | null;
}

export function isDataset(value: unknown): value is DatasetId {
  return value === 'commerce' || value === 'perf';
}

/** Narrow an untrusted value to a dataset, or undefined. Used when reading a
    course out of a bundle, a URL or the database, where the value is data. */
export function asDataset(value: unknown): DatasetId | undefined {
  return isDataset(value) ? value : undefined;
}

/** Why a statement runs where it runs. The reason is part of the answer: a
    caller that asked explicitly and was overridden, or that named a dataset
    nobody serves, has to be able to tell the difference between "you asked for
    this" and "you got the default because I could not read you". */
export type DatasetReason = 'explicit' | 'block' | 'exercise' | 'course' | 'unknown-dataset' | 'default';

interface DatasetChoice {
  dataset: DatasetId;
  reason: DatasetReason;
}

/** The one dataset a statement runs on, with the reason it was chosen. */
function datasetChoice(ref?: DatasetRef | null): DatasetChoice {
  if (isDataset(ref?.dataset)) return { dataset: ref.dataset, reason: 'explicit' };
  if (isDataset(ref?.block?.dataset)) return { dataset: ref.block.dataset, reason: 'block' };
  if (ref?.exercise?.perf) return { dataset: 'perf', reason: 'exercise' };
  if (isDataset(ref?.course?.dataset)) return { dataset: ref.course.dataset, reason: 'course' };
  /* A *named* dataset nobody serves is not the default. It is reported so the
     caller can say which name it could not honour, and then the statement still
     runs somewhere honest (the default) rather than nowhere. */
  const named = ref?.dataset ?? ref?.block?.dataset ?? ref?.course?.dataset;
  if (named !== undefined && named !== null && named !== '') {
    return { dataset: DEFAULT_DATASET, reason: 'unknown-dataset' };
  }
  return { dataset: DEFAULT_DATASET, reason: 'default' };
}

/** The one dataset a statement runs on. */
export function datasetFor(ref?: DatasetRef | null): DatasetId {
  return datasetChoice(ref).dataset;
}

/** Why `datasetFor` returned what it did. Public surface, but no screen reads it
    yet — kept because datasetChoice computes it and the module promises the
    "unknown-name is reported, not swallowed" rule. */
export function datasetReason(ref?: DatasetRef | null): DatasetReason {
  return datasetChoice(ref).reason;
}
