/*
 * The progress concern as a hook. `lib/progress.ts` owns the pure snapshot
 * model; this module binds it to the store: the per-owner load and migration,
 * the snapshot's owner-scoped persistence, the gateway sync with its one
 * failure notice, and every mutation the screens call (events, notes, saved
 * queries, bookmarks, study time, chapter completion, sample data, and the
 * export/import documents).
 *
 * The state and refs it moves stay in `StoreProvider`, because other concerns
 * share them: navigation starts from `progress`, the hash router reads
 * `progressRef`, and sign-in/logout reset the hydration guards. The provider
 * passes them in and keeps every member in `StoreValue`, so the public shape and
 * each member's behaviour are unchanged. This is the same reason the router's
 * own state stayed behind in `useHashRouter`'s provider.
 *
 * Every mutation takes the same three steps through `commitProgress`: put the
 * snapshot in React state, write it to this browser, and push it to the
 * gateway. One helper, so the three can never drift apart.
 */

import { useCallback, useEffect, useLayoutEffect, useRef } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import { datasetVersion } from '../api/dataset';
import { buildExportBundle, collectDrafts, downloadExport } from '../lib/exportBundle';
import { applyImport, describeImport, readImport } from '../lib/importBundle';
import type { ImportOutcome } from '../lib/importBundle';
import { eventTitle, findExercise } from '../lib/model';
import {
  emptyProgress,
  isSampleProgress,
  migrateProgress,
  newAttemptId,
  rebuildProgressFromEvents,
  seedProgress,
  stampFor,
} from '../lib/progress';
import { loadJSON, saveJSON } from './jsonStore';
import type {
  AuthSession,
  Block,
  Course,
  CourseModule,
  EventKind,
  ExecutionApi,
  Progress,
  ProgressEvent,
} from '../types';

/* Progress storage is owner-scoped: the key carries the account id, so two
   learners signed in on one browser can never read each other's workspace out
   of localStorage. A single shared `qc.progress` key was exactly that leak. */
const PROG_KEY = 'qc.progress';
/** The key an anonymous browser — the offline local workspace, or a signed-out
    gateway tab — writes under. The local workspace's session id is `local`, so
    the two agree. */
export const LOCAL_PROGRESS_ID = 'local';
function progressKeyFor(userId: string | null | undefined): string {
  return `${PROG_KEY}.${userId || LOCAL_PROGRESS_ID}`;
}
/** A snapshot the gateway actually sent, before `migrateProgress` reads it. */
function isObjectValue(value: unknown): value is object {
  return !!value && typeof value === 'object';
}

/** Per-course ceiling on kept queries, so the workbook stays a workbook. */
const SAVED_PER_COURSE = 50;

/**
 * First-run state is empty, full stop.
 *
 * This used to call seedProgress(), which fabricated weeks of history — passed
 * exercises, notes, bookmarks, a streak — for every new workspace. With a real
 * gateway behind the API that made a live app look like a mock: checkmarks and
 * "60%" nobody earned. Sample data is now opt-in from Appearance.
 */
export function initialProgress(userId: string | null | undefined): Progress {
  const key = progressKeyFor(userId);
  const raw = loadJSON<unknown>(key, null);
  if (raw && !isSampleProgress(raw)) {
    const migrated = migrateProgress(raw);
    if (migrated) return migrated;
  }
  /* Nothing usable on disk: a missing blob, generated sample data, or a
     snapshot too old to migrate. Start empty and overwrite the stale row so
     every boot does not have to decide to ignore it again. */
  const fresh = emptyProgress();
  saveJSON(key, fresh);
  return fresh;
}

/** The provider state and refs the hook reads and writes. Every field is owned
    by the provider; the hook neither holds nor replaces any of it. */
interface ProgressStoreOptions {
  api: ExecutionApi;
  /** The snapshot as this render sees it; each mutation derives its next value. */
  progress: Progress;
  setProgress: Dispatch<SetStateAction<Progress>>;
  /** The stable read: the study timer commits between renders through this. */
  progressRef: MutableRefObject<Progress>;
  /** The account whose snapshot is loaded: `session.user.id`, or the local id. */
  ownerId: string;
  /** Null when signed out; the gateway pull runs only for a real account. */
  session: AuthSession | null;
  /** False until the session is restored; the owner swap waits for it. */
  authReady: boolean;
  course: Course;
  modules: CourseModule[];
  activeLesson: number;
  /** The selected course, the default owner of a snapshot write. */
  selectedCourseId: string;
  toast: (msg: string) => void;
  /** Hydration guards, reset by signup/login/logout and the owner swap. */
  hydratedRef: MutableRefObject<boolean>;
  dirtyRef: MutableRefObject<boolean>;
  /** The owner the on-disk key was loaded for. */
  progressOwnerRef: MutableRefObject<string>;
}

/** The progress mutations and documents the provider spreads into `StoreValue`. */
interface ProgressStore {
  recordEvent: (id: string, kind: EventKind, label?: string) => void;
  addNote: (text: string) => void;
  saveQuery: (sql: string, block?: Block) => void;
  removeSaved: (ts: number) => void;
  toggleBookmark: (title: string, lesson: number, section: number) => void;
  removeWorkbookItem: (ts: number) => void;
  isBookmarked: (lesson: number, section: number) => boolean;
  addStudyTime: (courseId: string, ms: number) => void;
  markLessonDone: () => void;
  loadSampleProgress: () => void;
  clearProgress: () => void;
  exportData: () => string;
  importData: (text: string) => ImportOutcome;
}

export function useProgressStore({
  api,
  progress,
  setProgress,
  progressRef,
  ownerId,
  session,
  authReady,
  course,
  modules,
  activeLesson,
  selectedCourseId,
  toast,
  hydratedRef,
  dirtyRef,
  progressOwnerRef,
}: ProgressStoreOptions): ProgressStore {
  /* Read through a ref so the mutation callbacks stay identity-stable. The
     study timer commits on an interval, and a callback that changed on every
     progress write would restart the timer effect (and its cleanup flush) on
     every tick. */
  useEffect(() => {
    progressRef.current = progress;
  }, [progress, progressRef]);

  /* Progress is owner-scoped. The owner changes on signup, login, logout, a
     401, and session restore, and the in-memory snapshot has to change with it
     — otherwise a learner could see the previous account's passes for a frame
     and the next write would upload them into the new account's row. This is a
     layout effect, not a passive one: it runs before the browser paints, so the
     new owner's snapshot is in place for the first frame of the new session.
     `authReady` gates it so the initial render's snapshot (loaded from the
     stored account id) is not swapped away before the session has resolved. */
  useLayoutEffect(() => {
    if (!authReady || progressOwnerRef.current === ownerId) return;
    progressOwnerRef.current = ownerId;
    hydratedRef.current = false;
    dirtyRef.current = false;
    const next = initialProgress(ownerId);
    progressRef.current = next;
    setProgress(next);
  }, [authReady, ownerId, hydratedRef, dirtyRef, progressOwnerRef, progressRef, setProgress]);

  const persistProgress = useCallback((next?: Progress) => {
    const p = next || progressRef.current;
    dirtyRef.current = true;
    saveJSON(progressKeyFor(progressOwnerRef.current), p);
    return p;
  }, [progressRef, dirtyRef, progressOwnerRef]);

  /* A durable write that fails has to be visible (M1: "a failed write must be
     surfaced"). The localStorage copy already holds the snapshot, so the
     learner keeps working — but the server/IndexedDB copy is now behind, and
     staying quiet about that would be the "it's a mock" tell all over again.
     One notice per session, so a browser that never allows storage cannot spam
     the toast on every keystroke. */
  const persistFailureNoticed = useRef(false);
  const reportPersistFailure = useCallback(
    // oxlint-disable-next-line anti-slop/no-unknown-parameters -- a failed durable write is untrusted; this reads its typed code.
    (e: unknown) => {
      if (persistFailureNoticed.current) return;
      persistFailureNoticed.current = true;
      const code = (e as { code?: string } | null)?.code;
      toast(
        code === 'PERSISTENCE_FAILED'
          ? 'This browser blocked local storage, so your work is kept in this tab only.'
          : 'Could not sync your progress to the account. It is still saved in this browser.'
      );
    },
    [toast]
  );
  const syncProgress = useCallback(
    (p: Progress, courseId?: string) => {
      api.saveProgress(p, courseId ?? selectedCourseId).catch(reportPersistFailure);
    },
    [api, reportPersistFailure, selectedCourseId]
  );
  const syncEvent = useCallback(
    (evt: ProgressEvent) => {
      api.logEvent(evt).catch(reportPersistFailure);
    },
    [api, reportPersistFailure]
  );
  /* Every progress mutation takes the same three steps: put the snapshot in
     React state, write it to this browser, and push it to the gateway. One
     helper, so the three can never drift apart. Returns the snapshot so the
     caller can keep using the value it passed.
     The hydration path below does not use this: it restores a snapshot rather
     than recording a change, so it must not mark the workspace dirty. */
  const commitProgress = useCallback(
    (next: Progress, courseId?: string): Progress => {
      setProgress(next);
      persistProgress(next);
      syncProgress(next, courseId);
      return next;
    },
    [setProgress, persistProgress, syncProgress]
  );

  /* Learner data lives on the server, so pull it once per owner.
   *
   * Without this the UI was localStorage-only: the gateway recorded progress
   * that no client ever read back, so a second browser (or a cleared cache)
   * showed an empty workspace next to a full server row — the classic
   * "it's a mock" tell. The server snapshot now wins, but only if the learner
   * has not already touched something locally while the request was in flight.
   *
   * An absent snapshot on the gateway is authoritative, not "no opinion": it
   * means this account has never saved anything, so any local copy is cleared
   * rather than kept. Keeping it was how one learner's history bled into the
   * next account on a shared browser. The offline adapters are the opposite —
   * their store *is* the local one — so an empty read there leaves the local
   * workspace snapshot alone and it still survives a reload. */
  useEffect(() => {
    /* Progress and events are owner-scoped by the bearer token, so there is
       nothing to pull until the session is restored and a user is present. */
    if (!authReady || !session) return;
    if (hydratedRef.current) return;
    hydratedRef.current = true;
    let alive = true;
    const owner = session.user.id;
    api
      .getProgress(selectedCourseId)
      .then(({ ok, snapshot }) => {
        if (!alive || !ok) return;
        const hasSnapshot = isObjectValue(snapshot);
        if (!hasSnapshot) {
          /* The gateway has no row for this account: clear the local snapshot
             so a shared browser never shows the previous account's work. A
             learner who changed something while the read was in flight keeps
             their edits; the server row is empty, not conflicting. */
          if (api.mode === 'http' && !dirtyRef.current) {
            const fresh = emptyProgress();
            progressRef.current = fresh;
            setProgress(fresh);
            saveJSON(progressKeyFor(owner), fresh);
          }
          return;
        }
        const remote = snapshot as Progress;
        /* A server row that only holds generated sample data is legacy residue
           from the old first-run seed; drop it instead of showing it as real. */
        if (isSampleProgress(remote)) {
          const fresh = emptyProgress();
          progressRef.current = fresh;
          setProgress(fresh);
          saveJSON(progressKeyFor(owner), fresh);
          syncProgress(fresh, selectedCourseId);
          return;
        }
        const migrated = migrateProgress(remote);
        if (!migrated || dirtyRef.current) return;
        const remoteProgress = migrated;
        progressRef.current = remoteProgress;
        setProgress(remoteProgress);
        saveJSON(progressKeyFor(owner), remoteProgress);
        /* A snapshot that needed upgrading must be written back, or the next
           boot would migrate it again from the same stale row. */
        if (remoteProgress !== remote) syncProgress(remoteProgress, selectedCourseId);
      })
      .catch(() => {
        /* offline or degraded gateway: keep the local snapshot */
      });
    return () => {
      alive = false;
    };
  }, [api, syncProgress, selectedCourseId, authReady, session, hydratedRef, dirtyRef, progressRef, setProgress]);

  /* ---------- progress mutations ---------- */
  const recordEvent = useCallback(
    (id: string, kind: EventKind, label?: string) => {
      const f = findExercise(id);
      const evt = {
        ts: Date.now(),
        id,
        /* §8.2: one id per attempt, so two tries at one exercise are two rows. */
        attemptId: newAttemptId(),
        kind,
        title: label || eventTitle(id),
        course: f ? f.course.id : selectedCourseId,
        lesson: f ? f.index : activeLesson,
        /* §7.3: the attempt carries the versions it was made against. */
        ...stampFor(f?.block, datasetVersion),
      };
      const next = { ...progress, events: [...(progress.events || []), evt] };
      rebuildProgressFromEvents(next);
      commitProgress(next, evt.course);
      syncEvent(evt);
    },
    [progress, activeLesson, selectedCourseId, commitProgress, syncEvent]
  );

  /* Measured study time. The lesson screen accumulates visible time and hands
     it over in chunks; this adds it to the running total for the course and
     syncs the snapshot so the gateway has the same number as the UI. */
  const addStudyTime = useCallback(
    (courseId: string, ms: number) => {
      if (!courseId || !(ms > 0)) return;
      const prev = progressRef.current;
      const total = (prev.timeMs?.[courseId] || 0) + ms;
      const next: Progress = { ...prev, timeMs: { ...prev.timeMs, [courseId]: total } };
      progressRef.current = next;
      commitProgress(next, courseId);
    },
    [commitProgress, progressRef]
  );

  const markLessonDone = useCallback(() => {
    const m = modules[activeLesson];
    if (!m) return;
    /* The learner's explicit signal lives in `manuallyDone`, not `done`: a
       rebuild of `done` from graded attempts runs on every event and would
       otherwise erase this. `done` is the union (see rebuildProgressFromEvents). */
    const manual = { ...progress.manuallyDone };
    const nowDone = !(progress.done[m.id] || manual[m.id]);
    if (nowDone) manual[m.id] = true;
    else delete manual[m.id];
    const next = {
      ...progress,
      manuallyDone: manual,
      done: { ...progress.done, [m.id]: nowDone },
    };
    /* Chapter completion is progress like any other mutation, so it must reach
       the gateway too — otherwise the server snapshot disagrees with the UI and
       the signal is lost on a different browser or a cleared cache. */
    commitProgress(next, selectedCourseId);
    toast(nowDone ? 'Chapter marked complete.' : 'Chapter marked incomplete.');
  }, [modules, activeLesson, progress, commitProgress, selectedCourseId, toast]);

  const addNote = useCallback(
    (text: string) => {
      if (!text) return;
      const c = course;
      const next: Progress = {
        ...progress,
        notes: [...(progress.notes || []), { course: c.id, lesson: activeLesson + 1, kind: 'note', ts: Date.now(), text }],
      };
      commitProgress(next, c.id);
      toast('Note saved to your workbook.');
    },
    [course, progress, activeLesson, commitProgress, toast]
  );

  /* Design §10: a bookmark is a note that points at one section, plus a history
     event so the learner's own record shows when they flagged it. It is written
     as one snapshot so the note and the event cannot disagree. */
  const toggleBookmark = useCallback(
    (title: string, lesson: number, section: number) => {
      const c = course;
      const existing = (progress.notes || []).find(
        (n) => n.course === c.id && n.kind === 'bookmark' && n.lesson === lesson && n.section === section,
      );
      if (existing) {
        const next: Progress = {
          ...progress,
          notes: (progress.notes || []).filter((n) => n !== existing),
        };
        commitProgress(next, c.id);
        toast('Bookmark removed.');
        return;
      }
      const ts = Date.now();
      const event: ProgressEvent = {
        ts,
        id: `bookmark.${c.id}.${lesson}.${section}`,
        attemptId: newAttemptId(),
        kind: 'bookmark',
        title: title || `Chapter ${lesson}`,
        course: c.id,
        lesson,
      };
      const next: Progress = {
        ...progress,
        notes: [...(progress.notes || []), { course: c.id, lesson, section, kind: 'bookmark', ts, text: title }],
        events: [...(progress.events || []), event],
      };
      commitProgress(next, c.id);
      syncEvent(event);
      toast('Bookmarked for your workbook.');
    },
    [course, progress, commitProgress, syncEvent, toast]
  );

  /* Design §10: a kept query stores the SQL plus the versions it was written
     against, deduped on (course, exercise, sql) so pressing Keep twice is not
     two rows. The cap is stated before it bites rather than silently truncating. */
  const saveQuery = useCallback(
    (sql: string, block?: Block) => {
      const text = String(sql || '').trim();
      if (!text) {
        toast('Write a query first, then keep it.');
        return;
      }
      const c = course;
      /* Only a real exercise gets an exercise id; an example's id is a demo
         label, and treating it as an exercise would put a fake one in the
         workbook's dedupe key. */
      const exercise = block?.type === 'exercise' ? block.id : undefined;
      const saved = progress.saved || [];
      if (saved.some((q) => q.course === c.id && q.exercise === exercise && q.sql === text)) {
        toast('That query is already in your workbook.');
        return;
      }
      if (saved.filter((q) => q.course === c.id).length >= SAVED_PER_COURSE) {
        toast(`Your workbook keeps ${SAVED_PER_COURSE} queries per course. Remove one first.`);
        return;
      }
      const vers = stampFor(block, datasetVersion);
      const next: Progress = {
        ...progress,
        saved: [
          ...saved,
          {
            course: c.id,
            lesson: activeLesson + 1,
            name: (exercise ? eventTitle(exercise) : block?.note ? String(block.note) : text.split('\n')[0]).slice(0, 60),
            sql: text,
            exercise,
            contract: block?.contract,
            datasetVersion: vers.datasetVersion,
            exerciseVersion: vers.exerciseVersion,
            ts: Date.now(),
          },
        ],
      };
      commitProgress(next, c.id);
      toast('Query kept in your workbook.');
    },
    [course, progress, activeLesson, commitProgress, toast]
  );

  const removeSaved = useCallback(
    (ts: number) => {
      const c = course;
      const next: Progress = {
        ...progress,
        saved: (progress.saved || []).filter((q) => !(q.course === c.id && q.ts === ts)),
      };
      commitProgress(next, c.id);
      toast('Saved query removed.');
    },
    [course, progress, commitProgress, toast]
  );

  const removeWorkbookItem = useCallback(
    (ts: number) => {
      const c = course;
      const next: Progress = {
        ...progress,
        notes: (progress.notes || []).filter((n) => !(n.course === c.id && n.ts === ts)),
      };
      commitProgress(next, c.id);
      toast('Removed from your workbook.');
    },
    [course, progress, commitProgress, toast]
  );

  const isBookmarked = useCallback(
    (lesson: number, section: number) =>
      (progress.notes || []).some(
        (n) => n.course === course.id && n.kind === 'bookmark' && n.lesson === lesson && n.section === section,
      ),
    [progress.notes, course.id]
  );

  const clearProgress = useCallback(() => {
    commitProgress(emptyProgress(), selectedCourseId);
    toast('Your data was cleared on this account.');
  }, [commitProgress, selectedCourseId, toast]);

  /* Sample data is a demo, not a default. It has to be asked for by name so the
     workspace never looks pre-populated with work nobody did. */
  const loadSampleProgress = useCallback(() => {
    commitProgress(seedProgress(), selectedCourseId);
    toast('Sample data loaded. Clear it any time from Appearance.');
  }, [commitProgress, selectedCourseId, toast]);

  /* The export is built from the same Progress the server holds, plus the
     local editor drafts, so it is complete and credential-free. */
  const exportData = useCallback(() => {
    const filename = downloadExport(buildExportBundle(progress, collectDrafts(), datasetVersion));
    toast(`Exported to ${filename}`);
    return filename;
  }, [progress, toast]);

  /* The other half of the same document. Validation happens before anything is
     written, the merge never removes what is already here, and the adapter is
     whichever one is live — the merge itself is adapter-independent. */
  const importData = useCallback(
    (text: string): ImportOutcome => {
      const parsed = readImport(text);
      if (!parsed.ok) {
        toast(parsed.message);
        return parsed;
      }
      const result = applyImport(progress, parsed.bundle, collectDrafts());
      commitProgress(result.progress, selectedCourseId);
      for (const [cellId, sql] of Object.entries(result.drafts)) api.saveDraft(cellId, sql);
      toast(describeImport(result));
      return result;
    },
    [api, progress, commitProgress, selectedCourseId, toast],
  );

  return {
    recordEvent,
    addNote,
    saveQuery,
    removeSaved,
    toggleBookmark,
    removeWorkbookItem,
    isBookmarked,
    addStudyTime,
    markLessonDone,
    loadSampleProgress,
    clearProgress,
    exportData,
    importData,
  };
}
