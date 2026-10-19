import type { DatasetId, Dialect, EnvironmentPrepare } from '../types';

/* The workspace warm-up for the course that is open.
 *
 * Two things happen when the selected course changes. The engine for the
 * course's dataset and dialect starts, so the learner's first Run does not pay
 * for the WASM download. And a stored course's own setup is applied to the
 * learner's workspace, because its exercises were verified in a workspace that
 * had it. Without the second, the first exercise of a stored course dies with
 * "relation does not exist" until the learner resets by hand.
 *
 * The warm-up never waits and never throws: a refusal is reported through
 * `onRefusal` and the course opens either way. The gateway makes prepare
 * idempotent per workspace, so a re-run is a cheap no-op rather than a second
 * `CREATE TABLE`.
 */

/** The slice of the execution seam the warm-up needs. */
export interface CourseWarmupApi {
  warmUp(dataset?: DatasetId, dialect?: Dialect): void;
  prepareEnvironment(courseId: string, dialect?: Dialect): Promise<EnvironmentPrepare>;
}

/** Start the course's engine and prepare a stored course's workspace.
 *
 * `storedCourseId` is the id of a course the gateway stores, or null for a
 * built-in course. A built-in course has no stored setup, so it is warmed and
 * not prepared. One call per course change is what the caller's effect
 * guarantees; the gateway turns a repeated prepare into a no-op, so calling
 * this again for the same course is safe.
 */
export function warmUpCourse(
  api: CourseWarmupApi,
  dataset: DatasetId,
  dialect: Dialect,
  storedCourseId: string | null,
  onRefusal: (message: string) => void,
): void {
  api.warmUp(dataset, dialect);
  if (storedCourseId === null) return;
  void api.prepareEnvironment(storedCourseId, dialect).catch(
    // oxlint-disable-next-line anti-slop/no-unknown-parameters -- a rejected prepare call is untrusted; this reads its refusal message.
    (e: unknown) => {
      onRefusal(refusalMessage(e));
    },
  );
}

/** The message a prepare refusal shows. The gateway's own words are preferred:
    it knows why (no workspace, an expired session, a busy server) far better
    than a string invented here. */
// oxlint-disable-next-line anti-slop/no-unknown-parameters -- a rejected prepare call is untrusted; this reads its refusal message.
function refusalMessage(e: unknown): string {
  const message = e instanceof Error && e.message ? e.message : '';
  return message || 'The course workspace could not be prepared.';
}
