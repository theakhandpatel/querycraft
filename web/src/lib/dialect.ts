import { DEFAULT_DIALECT, type Block, type Course, type CourseModule, type Dialect } from '../types';

/* The dialect dimension (M6 step 1, design §15 D15).
 *
 * One chapter teaches exactly one dialect, and that dialect is authoritative: a
 * cross-dialect comparison is a *separate* chapter, never a dialect-neutral one
 * that would have to hedge every claim. The resolution order is chapter →
 * course → the app default, so the 40-odd chapters authored before M6 keep
 * meaning PostgreSQL without repeating it 40 times, and a SQLite chapter says
 * so once where it matters. */

/** The closed set of dialects the app can run, in contract order. */
export const DIALECTS: readonly Dialect[] = ['postgresql', 'sqlite'];

export function isDialect(value: unknown): value is Dialect {
  return value === 'postgresql' || value === 'sqlite';
}

/** Narrow an untrusted value to a dialect, or undefined. Used when reading a
    course out of a bundle, a URL or the database, where the value is data. */
export function asDialect(value: unknown): Dialect | undefined {
  return isDialect(value) ? value : undefined;
}

type DialectSource = Pick<CourseModule, 'dialect'> | null | undefined;
type CourseDialectSource = Pick<Course, 'dialect'> | null | undefined;

/** The one dialect a chapter runs on. */
export function effectiveDialect(mod?: DialectSource, course?: CourseDialectSource): Dialect {
  return asDialect(mod?.dialect) ?? asDialect(course?.dialect) ?? DEFAULT_DIALECT;
}

/** The words a learner reads for a dialect. `engine` strings ("PostgreSQL
    18.3") are for a result row; this is for a chapter badge, where the version
    would be noise. */
export function dialectLabel(dialect: Dialect): string {
  return dialect === 'sqlite' ? 'SQLite' : 'PostgreSQL';
}

/** Whether a chapter's dialect is one the running engine can serve. `null`
    dialects means the engine has not answered yet, so the caller must not use
    this to disable a control before it knows. */
export function dialectSupported(dialect: Dialect, served: readonly Dialect[] | undefined | null): boolean | null {
  if (!served || served.length === 0) return null;
  return served.includes(dialect);
}

/* ---------- which dialect a statement runs on (M6 step 6) ----------
 *
 * The same shape `web/src/lib/dataset.ts` gives the dataset dimension, and for
 * the same reason: a screen hands the adapter *where the SQL came from* and the
 * adapter resolves one answer, so no screen has to know the precedence and no
 * two screens can disagree about it. A caller that has already resolved the
 * dialect passes it explicitly, which is what makes the answer the same when
 * the seam is driven directly (an audit, a retake) as when a lesson runs. */

/** What a caller knows about the engine a statement belongs to. */
export interface DialectRef {
  dialect?: unknown;
  /** A block may name its own dialect in future; read here so a screen that
      passes it gets the documented precedence rather than a silent fallback. */
  block?: Block | null;
  module?: CourseModule | null;
  course?: Course | null;
}

/** Why a statement runs where it runs. `unknown-dialect` is reported rather
    than swallowed: a named dialect nobody serves is a different situation from
    an unset one, and only the first one is a mistake. */
export type DialectReason = 'explicit' | 'block' | 'module' | 'course' | 'unknown-dialect' | 'default';

export interface DialectChoice {
  dialect: Dialect;
  reason: DialectReason;
}

/** The one dialect a statement runs on, with the reason it was chosen. */
export function dialectChoice(ref?: DialectRef | null): DialectChoice {
  const explicit = asDialect(ref?.dialect);
  if (explicit) return { dialect: explicit, reason: 'explicit' };
  const fromBlock = asDialect(ref?.block?.dialect);
  if (fromBlock) return { dialect: fromBlock, reason: 'block' };
  const fromModule = asDialect(ref?.module?.dialect);
  if (fromModule) return { dialect: fromModule, reason: 'module' };
  const fromCourse = asDialect(ref?.course?.dialect);
  if (fromCourse) return { dialect: fromCourse, reason: 'course' };
  const named = ref?.dialect ?? ref?.block?.dialect ?? ref?.module?.dialect ?? ref?.course?.dialect;
  if (named !== undefined && named !== null && named !== '') {
    return { dialect: DEFAULT_DIALECT, reason: 'unknown-dialect' };
  }
  return { dialect: DEFAULT_DIALECT, reason: 'default' };
}

/** The one dialect a statement runs on. */
export function dialectFor(ref?: DialectRef | null): Dialect {
  return dialectChoice(ref).dialect;
}

/** Why `dialectFor` returned what it did. */
export function dialectReason(ref?: DialectRef | null): DialectReason {
  return dialectChoice(ref).reason;
}
