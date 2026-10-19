/*
 * The catalog concern as a hook. `COURSES`/`MODULES` are the catalog every
 * screen and the router resolve an id against; this module binds the learner's
 * stored courses to it: the gateway list read on sign-in, the in-place mirror
 * into `COURSES`, the resolve of a course a fragment named, deletion, and
 * mid-run adoption. The `catalogVersion` counter is the re-render trigger for
 * those in-place mutations, and `bumpCatalog` is returned so the provider's
 * authoring mutations share the same one.
 *
 * The state and refs it moves stay in `StoreProvider`, because other concerns
 * share them: the hash router hands `pendingCourse` over, auth owns the
 * session, and navigation owns the selected course and the active lesson. The
 * provider passes them in and keeps every member in `StoreValue`, so the public
 * shape and each member's behaviour are unchanged. This is the same reason the
 * router's and progress's own state stayed behind in their hooks' providers.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import { COURSES } from '../data/curriculum';
import { courseFromPlan } from '../lib/draft';
import { HOME_HASH } from '../lib/route';
import type { AiCourseRecord } from '../api/aiContract';
import type { AuthSession, Course, ExecutionApi, Screen, UiState } from '../types';
import type { PendingCourse } from './useHashRouter';

/** The provider state and refs the hook reads and writes. Every field is owned
    by the provider; the hook neither holds nor replaces any of it. */
interface CatalogStoreOptions {
  api: ExecutionApi;
  /** False until the session is restored; the stored-course read waits for it. */
  authReady: boolean;
  /** Null when signed out; the stored-course list belongs to a real account. */
  session: AuthSession | null;
  /** A course id a fragment named that the catalog does not have. */
  pendingCourse: PendingCourse | null;
  setPendingCourse: Dispatch<SetStateAction<PendingCourse | null>>;
  setScreen: Dispatch<SetStateAction<Screen>>;
  setActiveLesson: Dispatch<SetStateAction<number>>;
  /** The selected course, read through the ref so deletion is not a dep. */
  uiStateRef: MutableRefObject<UiState>;
  saveUiState: (next: UiState) => void;
  toast: (msg: string) => void;
}

/** The catalog members the provider spreads into `StoreValue`. */
interface CatalogStore {
  /** The re-render trigger for the in-place catalog mutations, read only in the
      provider value's dependencies. */
  catalogVersion: number;
  /** Bump the catalog version so a mutation editing `COURSES`/`MODULES` in
      place re-renders the provider. */
  bumpCatalog: () => void;
  /** True while a course the fragment named is being fetched from the gateway,
      so the shell can wait for it instead of painting the wrong course. */
  courseResolving: boolean;
  /** Remove one of the learner's own courses through the gateway, and from the
      catalog. Resolves false when the gateway refused and nothing changed. */
  deleteCourse: (id: string) => Promise<boolean>;
  /** Register a course the gateway stored (used while one is still being
      written), so the catalog and every screen see it. */
  adoptStoredCourse: (rec: AiCourseRecord) => Course;
}

/* A course the gateway does not have. Both spellings are the same fact: the
   generic route answers 404 `NOT_FOUND`, the AI contract names the same thing
   `AI_NOT_FOUND`. */
// oxlint-disable-next-line anti-slop/no-unknown-parameters -- a rejected gateway call is untrusted; this reads its typed code.
function isMissingCourse(e: unknown): boolean {
  const err = e as { code?: string; status?: number } | null;
  return err?.status === 404 || err?.code === 'NOT_FOUND' || err?.code === 'AI_NOT_FOUND';
}

export function useCatalog({
  api,
  authReady,
  session,
  pendingCourse,
  setPendingCourse,
  setScreen,
  setActiveLesson,
  uiStateRef,
  saveUiState,
  toast,
}: CatalogStoreOptions): CatalogStore {
  /* The catalog mutations edit `COURSES`/`MODULES` in place, so they cannot
     change an identity the memoized provider value can compare. This counter is
     their re-render trigger and is read only in the value's dependencies. */
  const [catalogVersion, setCatalogVersion] = useState(0);
  /* The learner's own generated courses, as the gateway stores them. A ref, not
     state: the catalog is what every screen reads, so this is only the list a
     later refresh compares against. Read on sign-in and after a generation,
     never invented. */
  const storedRef = useRef<Course[]>([]);

  const bumpCatalog = useCallback(() => setCatalogVersion((v) => v + 1), []);

  /* ---------- M8: the learner's stored courses ----------
   *
   * The gateway stores a generated course; this tab used to keep it only in
   * React state, so a reload threw it away and `#/course/{id}` landed on
   * whatever built-in course the router fell back to. Everything below makes
   * the gateway the source of truth: read the list on sign-in, resolve a course
   * a link names, and delete through the API so the change is the server's.
   *
   * `gateway: true` is the flag that says where a course came from. A built-in
   * course never carries it, so the two can never be confused for one another,
   * and it is what tells a later list refresh which entries are the learner's
   * own records and may be replaced or dropped. */
  const materialise = useCallback(
    (rec: AiCourseRecord): Course =>
      ({
        ...courseFromPlan(rec.plan, rec.plan.subtitle || rec.plan.title),
        gateway: true,
        recordStatus: rec.status,
      }) as Course,
    []
  );

  /* Mirror a list of stored courses into the catalog. The catalog is what every
     screen and the router resolve an id against, so a stored course has to be
     in it to be reachable; a course that is no longer in the list is dropped,
     so a delete is visible everywhere at once. */
  const applyStored = useCallback(
    (next: Course[]) => {
      const keep = new Set(next.map((c) => c.id));
      for (let i = COURSES.length - 1; i >= 0; i--) {
        const c = COURSES[i];
        if (c.gateway === true && !keep.has(c.id)) COURSES.splice(i, 1);
      }
      for (const c of next) {
        const at = COURSES.findIndex((x) => x.id === c.id);
        if (at >= 0) COURSES[at] = c;
        else COURSES.push(c);
      }
      storedRef.current = next;
      bumpCatalog();
    },
    [bumpCatalog]
  );

  /* The learner's courses, read once the session is known. `session` is a
     dependency because the list belongs to the account: signing in as somebody
     else must not keep the previous account's courses on screen. */
  useEffect(() => {
    if (api.mode !== 'http' || !authReady || !session) return;
    let alive = true;
    void api
      .listCourses()
      .then((records) => {
        if (alive) applyStored(records.map(materialise));
      })
      .catch(() => {
        /* The list is unavailable. Nothing is dropped: the catalog keeps what it
           already has rather than presenting a failed read as "no courses". */
      });
    return () => {
      alive = false;
    };
  }, [api, authReady, session, applyStored, materialise]);

  /* A course the fragment named. The gateway may own an id this tab has never
     seen, so the store asks before the router is allowed to give up. */
  useEffect(() => {
    if (!pendingCourse || !authReady) return;
    if (!session) return;
    const goHome = (message: string | null) => {
      setPendingCourse(null);
      setScreen('home');
      window.history.replaceState(null, '', HOME_HASH);
      if (message) toast(message);
    };
    if (api.mode !== 'http') {
      /* The offline adapters keep no course store, so this link is not theirs. */
      goHome(null);
      return;
    }
    let alive = true;
    void api
      .getCourse(pendingCourse.id)
      .then((rec) => {
        if (!alive) return;
        if (!rec) {
          goHome('That course is not in your library.');
          return;
        }
        const course = materialise(rec);
        const others = storedRef.current.filter((c) => c.id !== course.id);
        applyStored([...others, course]);
        if (pendingCourse.chapterId) {
          const index = (course.modules || []).findIndex((m) => m.id === pendingCourse.chapterId);
          if (index >= 0) setActiveLesson(index);
        }
        setPendingCourse(null);
      })
      .catch(() => {
        if (alive) goHome('That course could not be opened.');
      });
    return () => {
      alive = false;
    };
  }, [api, authReady, session, pendingCourse, applyStored, materialise, toast, setPendingCourse, setScreen, setActiveLesson]);

  /* Removing a course is the gateway's decision, so it goes there first and the
     list changes only once it agreed. A course the gateway has already
     forgotten is not a failure: the learner asked for it to be gone and it is. */
  const deleteCourse = useCallback(
    async (id: string): Promise<boolean> => {
      if (api.mode !== 'http') {
        toast('Generated courses live on the gateway; there is nothing to remove offline.');
        return false;
      }
      try {
        await api.deleteCourse(id);
      } catch (e) {
        /* A course the gateway has already forgotten is the outcome the learner
           asked for, so it is not a failure: the route answers 404 `NOT_FOUND`
           (and the AI surface also spells it `AI_NOT_FOUND`). Anything else is a
           real refusal and the list is left exactly as it was. */
        if (!isMissingCourse(e)) {
          toast('The course could not be removed. Try again.');
          return false;
        }
      }
      applyStored(storedRef.current.filter((c) => c.id !== id));
      if (uiStateRef.current.selectedCourseId === id) {
        const next = { ...uiStateRef.current, selectedCourseId: COURSES[0].id };
        uiStateRef.current = next;
        saveUiState(next);
        setActiveLesson(0);
        setScreen('library');
      }
      toast('Course removed.');
      return true;
    },
    [api, applyStored, saveUiState, toast, uiStateRef, setActiveLesson, setScreen]
  );

  /* Register one stored course from the gateway, replacing any copy already in
     the catalog. Used mid-run: a course the pipeline is still writing is real,
     it is the learner's, and reading it as it grows is the whole point of
     generating chapter by chapter. Returns the catalog course so the caller can
     show it without waiting for a render. */
  const adoptStoredCourse = useCallback(
    (rec: AiCourseRecord): Course => {
      const course = materialise(rec);
      applyStored([...storedRef.current.filter((c) => c.id !== course.id), course]);
      return course;
    },
    [applyStored, materialise]
  );

  /* M8. A fragment that names a course we have not resolved yet. The shell waits
     on the splash for it — but only for a learner the resolve can run for:
     signed out, the sign-in screen comes first and the link is picked up after
     the session arrives. */
  const courseResolving = pendingCourse !== null && authReady && session !== null;

  return { catalogVersion, bumpCatalog, courseResolving, deleteCourse, adoptStoredCourse };
}
