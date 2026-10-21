/*
 * The hash router as a hook. `lib/route.ts` owns the pure URL ⇄ state mapping;
 * this module binds it to the store: the initial fragment, the two effects that
 * write the address bar and read it back, the `hashchange`/`popstate` listener,
 * the re-resolve once the role is known, and `showScreen`.
 *
 * The state the router moves and the refs it reads stay in `StoreProvider`,
 * because other concerns share them: auth and the run recovery set the screen,
 * the course resolver fills `pendingCourse`, and progress reads `activeLesson`.
 * The provider passes them in and keeps them in `StoreValue`, so the public
 * shape and every member's behaviour are unchanged.
 */

import { useCallback, useEffect } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import { courseById, nextChapterIndex as nextChapterIndexOf } from '../lib/model';
import { HOME_HASH, hashForState, isRouteHash, routeTarget, screenAllowed } from '../lib/route';
import type { RouteTarget } from '../lib/route';
import type { Course, Progress, Screen, UiState, UserRole } from '../types';

/** A course id a fragment named that the catalog does not have. It may be a
    stored course the gateway owns, so the provider fetches it before the screen
    paints; the router only hands the id over. */
export interface PendingCourse {
  id: string;
  chapterId: string | null;
}

/** The fragment this page opened on. A deep link, a reload or a paste into the
    address bar has to land on the screen it names, so the first render reads
    the URL instead of always starting at Home and correcting afterwards. */
export function initialRoute(): RouteTarget {
  return routeTarget(typeof window === 'undefined' ? HOME_HASH : window.location.hash);
}

/** The store state the router reads and writes. Every field is owned by the
    provider; the router neither holds nor replaces any of it. */
interface HashRouterOptions {
  screen: Screen;
  challengeId: string | null;
  activeLesson: number;
  /** The selected course, so state → URL can name it and its chapter. */
  course: Course;
  /** Read to gate the authoring screens, the same rule App renders behind. */
  role: UserRole;
  /** False until the session is restored; the fragment is re-resolved then. */
  authReady: boolean;
  pendingCourse: PendingCourse | null;
  uiState: UiState;
  uiStateRef: MutableRefObject<UiState>;
  progressRef: MutableRefObject<Progress>;
  saveUiState: (next: UiState) => void;
  setScreen: Dispatch<SetStateAction<Screen>>;
  setChallengeId: Dispatch<SetStateAction<string | null>>;
  setActiveLesson: Dispatch<SetStateAction<number>>;
  setPendingCourse: Dispatch<SetStateAction<PendingCourse | null>>;
}

interface HashRouter {
  /** Select a screen and return the workspace to the top. */
  showScreen: (id: Screen) => void;
}

export function useHashRouter({
  screen,
  challengeId,
  activeLesson,
  course,
  role,
  authReady,
  pendingCourse,
  uiState,
  uiStateRef,
  progressRef,
  saveUiState,
  setScreen,
  setChallengeId,
  setActiveLesson,
  setPendingCourse,
}: HashRouterOptions): HashRouter {
  /* The fragment is this app's history. Navigating inside the app writes the
     fragment with `pushState` — which fires no event, so the two directions
     cannot chase each other — and a `hashchange` (back, forward, a pasted link)
     writes the state back. Both directions are guarded by the same comparison,
     so a change that produces the fragment already in the address bar is a
     no-op: a state change never re-emits the route it came from, and a
     `hashchange` that matches the current screen changes nothing. */
  useEffect(() => {
    uiStateRef.current = uiState;
  }, [uiState, uiStateRef]);

  /* Authoring is the admin surface; App turns a learner away from those
     screens, and the fragment follows the same rule so it never names a screen
     nobody is looking at. */
  const gated = useCallback(
    (target: RouteTarget): RouteTarget => (screenAllowed(target.screen, role) ? target : routeTarget(HOME_HASH)),
    [role]
  );

  /* state → URL.
     While a course the fragment named is still being fetched, the selected
     course is a placeholder and writing the fragment now would replace the
     learner's link with the placeholder's id. The effect waits for the real
     course: `pendingCourse` clears in the same commit that puts it in the
     catalog, and then this writes the fragment the learner already has. */
  useEffect(() => {
    if (pendingCourse !== null) return;
    const next = hashForState({ screen, courseId: course.id, chapterIndex: activeLesson, challengeId });
    const current = window.location.hash;
    if (current === next) return;
    /* A fragment that is not canonical (`#/nope`, a bare URL, `#/create` for a
       learner) is rewritten in place; only a real navigation adds an entry, so
       the back stack stays the learner's own. */
    if (gated(routeTarget(current)).hash === current) window.history.pushState(null, '', next);
    else window.history.replaceState(null, '', next);
  }, [screen, course.id, activeLesson, challengeId, gated, pendingCourse]);

  /* URL → state */
  const applyHash = useCallback(() => {
    const raw = window.location.hash;
    /* The shell's skip link moves the fragment to `#main`. That is an in-page
       anchor, not a route, so it is left to the browser. */
    if (!isRouteHash(raw)) return;
    const target = gated(routeTarget(raw));
    if (target.hash !== raw) window.history.replaceState(null, '', target.hash);
    if (target.courseId) {
      const linked = courseById(target.courseId);
      if (linked) linked.enrolled = true;
      const ids = new Set(uiStateRef.current.enrolledCourseIds ?? []);
      if (linked) ids.add(linked.id);
      const nextState = {
        ...uiStateRef.current,
        selectedCourseId: target.courseId,
        enrolledCourseIds: [...ids],
      };
      uiStateRef.current = nextState;
      saveUiState(nextState);
    }
    if (target.chapterIndex != null) setActiveLesson(target.chapterIndex);
    else if (target.courseId) {
      const linked = courseById(target.courseId);
      if (linked) setActiveLesson(nextChapterIndexOf(linked, progressRef.current));
    }
    setChallengeId(target.challengeId);
    setScreen(target.screen);
    /* A fragment that names a course the catalog does not have keeps its id
       pending, so the gateway can answer for it before the router gives up. A
       link typed into the address bar is a `hashchange`, not a fresh load, so
       this is the only place that sees it. */
    /* The same link is re-resolved when the session is restored, so this only
       writes when the value actually changed: a fresh object holding the same id
       would be a new identity to React, and the resolver would fetch the course
       a second time for a link that never moved. */
    const pending = target.courseUnresolved && target.courseId ? { id: target.courseId, chapterId: target.chapterId ?? null } : null;
    setPendingCourse((prev) =>
      prev && pending && prev.id === pending.id && prev.chapterId === pending.chapterId ? prev : pending
    );
    const ws = document.querySelector('.workspace');
    if (ws) ws.scrollTop = 0;
  }, [gated, saveUiState, uiStateRef, progressRef, setActiveLesson, setChallengeId, setScreen, setPendingCourse]);

  useEffect(() => {
    window.addEventListener('hashchange', applyHash);
    window.addEventListener('popstate', applyHash);
    return () => {
      window.removeEventListener('hashchange', applyHash);
      window.removeEventListener('popstate', applyHash);
    };
  }, [applyHash]);

  /* The role is not known until the session is restored, so the fragment is
     resolved once more when it is: an authoring link that a learner may not open
     is turned away in place, and a link a signed-in learner opened still lands
     where it pointed. This is URL → state, not data handed to a parent: the
     fragment is the source of truth and this effect is the only reader of it. */
  useEffect(() => {
    if (!authReady) return;
    applyHash();
  }, [authReady, applyHash]);

  const showScreen = useCallback(
    (id: Screen) => {
      setScreen(id);
      const ws = document.querySelector('.workspace');
      if (ws) ws.scrollTop = 0;
    },
    [setScreen]
  );

  return { showScreen };
}
