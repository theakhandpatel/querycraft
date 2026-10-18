import { allModules, exerciseIdsOfModule } from './model';
import type { Progress, ProgressEvent } from '../types';

/** Shape version of the stored snapshot. Older snapshots are discarded whole,
    never half-read: a stale field is worse than a fresh start. */
export const PROGRESS_VERSION = 5;

/** Design §7.3: the content versions an attempt was made against. */
interface ContentVersions {
  datasetVersion: number;
  exerciseVersion: number;
}

/** Version stamp for a fresh attempt. An exercise is version 1 until its
    prompt, contract or reference solution changes, so the stamp is only
    meaningful when the author bumps it. */
export function stampFor(block: { version?: number } | null | undefined, datasetVersion: number): ContentVersions {
  const exerciseVersion = typeof block?.version === 'number' && block.version > 0 ? block.version : 1;
  return { datasetVersion, exerciseVersion };
}

/** A client request id for one attempt (design §8.2). The exercise id is the
    same for every attempt at that exercise, so storage needs a value that is
    unique per attempt; this is it. */
export function newAttemptId(): string {
  const c = typeof crypto === 'undefined' ? undefined : crypto;
  if (c && typeof c.randomUUID === 'function') return 'att_' + c.randomUUID();
  return 'att_' + Date.now().toString(36) + Math.random().toString(36).slice(2, 10);
}

/** True when an attempt was made against content that has since changed, so the
    UI can say "solved on an earlier version" instead of quietly re-grading it.
    Attempts from before version stamps existed report false: nothing is known,
    and guessing would be worse than staying quiet. */
export function isEarlierVersion(evt: ProgressEvent, current: ContentVersions): boolean {
  if (typeof evt.datasetVersion !== 'number' || typeof evt.exerciseVersion !== 'number') return false;
  return evt.datasetVersion !== current.datasetVersion || evt.exerciseVersion !== current.exerciseVersion;
}

/**
 * Storage identity for one attempt.
 *
 * The exercise id is the same for every attempt at that exercise, so keying a
 * store on it would collapse a learner's history onto a single row. Documents
 * written before request ids existed fall back to a composite that is still
 * unique per attempt.
 */
export function attemptKey(evt: ProgressEvent): string {
  const explicit = typeof evt.attemptId === 'string' ? evt.attemptId.trim() : '';
  if (explicit) return explicit;
  return `${evt.course}:${evt.id}:${evt.ts}`;
}

export function emptyProgress(): Progress {
  return {
    version: PROGRESS_VERSION,
    seeded: false,
    events: [],
    passed: {},
    accepted: {},
    firstTry: {},
    done: {},
    notes: [],
    saved: [],
    attempts: 0,
    attemptsByDay: [0, 0, 0, 0, 0, 0, 0],
    timeMs: {},
  };
}

/**
 * Fold one timer sample into the pending bucket.
 *
 * `elapsedMs` is clamped to a minute so a suspended laptop is not charged the
 * whole sleep, and hidden time is not study time at all: reading a chapter in a
 * background tab should not inflate the total. Kept pure and exported so the
 * sampling rule is covered by `make audit-progress` rather than by eyeballing a
 * timer that a background tab deliberately does not run.
 */
export function accrueStudyTime(pendingMs: number, elapsedMs: number, hidden: boolean): number {
  if (hidden) return Math.max(pendingMs, 0);
  return Math.max(pendingMs, 0) + Math.min(Math.max(elapsedMs, 0), 60000);
}

/**
 * Render a measured duration the way a learner reads it.
 *
 * Progress used to show `attempts * 3 + 6` minutes, which was invented: a brand
 * new workspace reported six minutes of study it had never seen. This formats
 * the real accumulated value instead, and says so plainly when nothing has been
 * measured yet.
 */
export function formatDuration(ms: number): string {
  if (!ms || ms < 60000) return ms && ms > 0 ? '<1 min' : '—';
  const minutes = Math.round(ms / 60000);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} h` : `${hours} h ${rest} min`;
}

/**
 * Bring a stored snapshot up to the current shape when the change is additive.
 *
 * A version bump used to discard the snapshot whole, which is right when the
 * meaning of a field changed (the v1 exercise ids no longer exist) but wrong
 * when a bump only *adds* a field: it silently threw away a learner's real
 * passes. v4 -> v5 added `timeMs`, so it is upgraded instead of dropped.
 * Returns null when the snapshot is too old or not a snapshot at all.
 */
export function migrateProgress(raw: unknown): Progress | null {
  if (!raw || typeof raw !== 'object') return null;
  const p = raw as Progress;
  if (p.version === PROGRESS_VERSION) return p;
  if (p.version === 4) {
    return { ...p, version: PROGRESS_VERSION, timeMs: (p as Partial<Progress>).timeMs || {} };
  }
  return null;
}

/**
 * True when a snapshot came from the sample-data generator rather than from a
 * learner. Sample snapshots are never adopted: they used to be seeded on first
 * run, which made a real gateway look like a canned mock.
 */
export function isSampleProgress(p: unknown): boolean {
  return !!p && typeof p === 'object' && (p as Progress).seeded === true;
}

export function attemptsByDay(events: ProgressEvent[]): number[] {
  const midnight = new Date();
  midnight.setHours(0, 0, 0, 0);
  const base = midnight.getTime();
  const out: number[] = [];
  for (let i = 6; i >= 0; i--) {
    const start = base - i * 86400000;
    const end = start + 86400000;
    out.push(events.filter((e) => e.ts >= start && e.ts < end && (e.kind === 'passed' || e.kind === 'failed')).length);
  }
  return out;
}

export function rebuildProgressFromEvents(p: Progress): Progress {
  /* timeMs is measured directly, not derived from events, so a rebuild must
     carry it across rather than reset it. */
  p.timeMs = p.timeMs || {};
  p.passed = {};
  p.accepted = {};
  p.firstTry = {};
  p.done = {};
  const seen: Record<string, boolean> = {};
  const passedIds: Record<string, boolean> = {};
  const acceptedIds: Record<string, boolean> = {};
  p.events
    .slice()
    .sort((a, b) => a.ts - b.ts)
    .forEach((e) => {
      if (e.kind === 'passed' || e.kind === 'failed') {
        if (!seen[e.id]) {
          seen[e.id] = true;
          p.firstTry[e.id] = e.kind === 'passed';
        }
      }
      if (e.kind === 'passed') passedIds[e.id] = true;
      if (e.kind === 'accepted') acceptedIds[e.id] = true;
    });
  Object.keys(passedIds).forEach((id) => {
    p.passed[id] = true;
    delete acceptedIds[id];
  });
  Object.keys(acceptedIds).forEach((id) => {
    p.accepted[id] = true;
  });
  p.attempts = p.events.filter((e) => e.kind === 'passed' || e.kind === 'failed').length;
  /* `manuallyDone` carries the learner's explicit "mark chapter complete"
     across the rebuild. Without it, recomputing `done` from passed exercises
     wholesale dropped that signal on the next graded attempt anywhere, and a
     chapter with no exercises could never read as done. The two are unioned:
     done = every exercise passed OR the learner said so. */
  const manual = p.manuallyDone || {};
  allModules().forEach((m) => {
    const exs = exerciseIdsOfModule(m);
    p.done[m.id] = Boolean(manual[m.id]) || (exs.length > 0 && exs.every((id) => passedIds[id]));
  });
  p.attemptsByDay = attemptsByDay(p.events);
  return p;
}

export function seedProgress(): Progress {
  const now = Date.now();
  const DAY = 86400000;
  function ev(dAgo: number, hm: string, id: string, kind: ProgressEvent['kind'], title: string, course?: string): ProgressEvent {
    const d = new Date(now - dAgo * DAY);
    const parts = hm.split(':');
    d.setHours(+parts[0], +parts[1], 0, 0);
    return { ts: d.getTime(), id, kind, title, course: course || 'sql-analytics', lesson: null };
  }
  const events: ProgressEvent[] = [
    ev(6, '09:12', 'ex.1.1', 'passed', 'Products in Electronics by price'),
    ev(6, '09:26', 'ex.2.1', 'failed', 'Line items per order'),
    ev(6, '09:39', 'ex.2.1', 'passed', 'Line items per order'),
    ev(4, '19:02', 'ex.3.1', 'passed', 'Net revenue per month'),
    ev(4, '19:20', 'ex.4.1', 'passed', 'Customers with 3+ completed orders'),
    ev(3, '20:15', 'ex.j1.1', 'passed', 'Orders with customer names', 'joins'),
    ev(2, '08:45', 'ex.5.1', 'passed', 'Rank products within category'),
    ev(2, '09:10', 'ex.5.2', 'failed', 'Top 2 categories by month'),
    ev(1, '18:40', 'ex.j2.1', 'failed', 'Orders per customer, including zero', 'joins'),
    ev(0, '07:58', 'ex.5.2', 'failed', 'Top 2 categories by month'),
    ev(0, '08:09', 'ex.5.2', 'accepted', 'Top 2 categories by month'),
  ];
  const p = emptyProgress();
  p.seeded = true;
  p.events = events;
  /* Sample finish times, so the demo workspace shows the same *kind* of number
     a real learner accumulates. Clearly sample data, requested by name. */
  p.timeMs = { 'sql-analytics': 96 * 60000, joins: 42 * 60000 };
  p.notes = [
    { course: 'sql-analytics', lesson: 3, kind: 'note', ts: now - 4 * DAY, text: 'Discount is per order, not per line — allocate it before comparing months.' },
    { course: 'sql-analytics', lesson: 5, kind: 'note', ts: now - 2 * DAY, text: 'Check grain before ranking: aggregate first, then RANK().' },
    { course: 'sql-analytics', lesson: 6, kind: 'bookmark', ts: now - 1 * DAY, text: 'Cohort retention by first-purchase month' },
    { course: 'joins', lesson: 2, kind: 'note', ts: now - 3 * DAY, text: 'LEFT JOIN + COUNT(o.id): unmatched rows count as 0, which is what you want.' },
  ];
  p.saved = [
    { course: 'sql-analytics', lesson: 3, name: 'monthly_revenue.sql' },
    { course: 'sql-analytics', lesson: 4, name: 'completed_per_customer.sql' },
    { course: 'sql-analytics', lesson: 5, name: 'category_rank.sql' },
    { course: 'joins', lesson: 2, name: 'orders_per_customer.sql' },
  ];
  rebuildProgressFromEvents(p);
  return p;
}

function dayKey(ts: number): string {
  const d = new Date(ts);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

export function currentStreak(events: ProgressEvent[]): number {
  const days: Record<string, boolean> = {};
  (events || []).forEach((e) => {
    if (e.kind === 'passed' || e.kind === 'failed') days[dayKey(e.ts)] = true;
  });
  let n = 0;
  const d = new Date();
  for (let i = 0; i < 400; i++) {
    if (days[dayKey(d.getTime())]) {
      n++;
      d.setDate(d.getDate() - 1);
    } else break;
  }
  return n;
}

export function whenLabel(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const day = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diff = Math.round((today - day) / 86400000);
  const hm = `${('0' + d.getHours()).slice(-2)}:${('0' + d.getMinutes()).slice(-2)}`;
  if (diff <= 0) return `Today ${hm}`;
  if (diff === 1) return `Yesterday ${hm}`;
  return `${diff}d ago`;
}

export const WEEKDAY_LABELS: string[] = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];
