/* URL ⇄ app state.
 *
 * Querycraft is one shell over one set of screens, so where the learner is
 * belongs in the address bar: a chapter can be bookmarked, shared and reloaded,
 * and the browser's back button walks the history the learner actually built
 * instead of staying on whatever the app happened to show last.
 *
 * This module owns that mapping in both directions and nothing else. It is pure
 * — no DOM, no React, no dependency on the store — so the store can bind
 * state → fragment and fragment → state without either side guessing what the
 * other means. The fragments it produces read like ordinary product URLs:
 *
 *   #/                            home / dashboard
 *   #/library                     the course library
 *   #/course/:courseId            a course overview
 *   #/course/:courseId/:chapterId a chapter (chapterId is the module id)
 *   #/workbook                    the workbook
 *   #/progress                    the progress screen
 *   #/lab                         the challenge lab
 *   #/lab/:challengeId            the lab, on one registered challenge
 *   #/appearance                  appearance settings
 *   #/create                      the course-authoring flow (admin surface)
 *
 * Anything else — a fragment that is not one of ours, or one that names a
 * course or chapter the catalog does not have — resolves to Home. `unknown` on
 * the resolved target lets the caller rewrite the fragment in place with
 * `replaceState`, so a stale bookmark cannot strand the learner *and* the back
 * stack is left alone.
 */

import { activeModules, courseById } from './model';
import type { Course, Screen } from '../types';

/** The routes, as a fragment spells them. */
export type Route =
  | { name: 'home' }
  | { name: 'library' }
  | { name: 'course'; courseId: string }
  | { name: 'lesson'; courseId: string; chapterId: string }
  | { name: 'lab'; challengeId: string | null }
  | { name: 'workbook' }
  | { name: 'progress' }
  | { name: 'appearance' }
  | { name: 'create' };

/** The slice of app state the fragment is bound to. */
export interface RouteState {
  screen: Screen;
  /** The selected course; every screen keeps one, so it is always known. */
  courseId: string;
  /** Index of the open chapter within the selected course's modules. */
  chapterIndex: number;
  /** The challenge the lab was opened on, when it was opened on one. */
  challengeId: string | null;
}

/** A fragment resolved against the catalog: the screen to show and the ids it
    names, already turned into the numbers the store holds. */
export interface RouteTarget {
  /** Canonical fragment for this target, always prefixed with `#/`. */
  hash: string;
  screen: Screen;
  /** The course the fragment names, or null when it names none. */
  courseId: string | null;
  /** Module index for a chapter fragment, or null. */
  chapterIndex: number | null;
  /** Challenge id for a lab fragment, or null. */
  challengeId: string | null;
  /** True when the fragment named no destination that exists. */
  unknown: boolean;
  /** The chapter id a lesson fragment named, kept even when the course itself
      could not be resolved. The store turns it into an index once the gateway
      returns the course. */
  chapterId?: string | null;
  /** True when the fragment named a course the catalog does not have. The id
      may still be a real stored course — the gateway owns courses this tab has
      never seen — so the store resolves it against the gateway before it gives
      up. Only a course that is also not there is Home. */
  courseUnresolved?: boolean;
}

/** The home fragment, and the one every unknown fragment normalizes to. */
export const HOME_HASH = '#/';

/** Screens that belong to the admin authoring flow rather than to learning.
    Kept here so the store's router and App's render gate agree on one list. */
export const AUTHORING_SCREENS: Screen[] = ['create', 'building', 'review'];

function segment(raw: string): string {
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

/** Parse a fragment into a route, or null when it names nothing the app has.
    `#/course/joins/m2`, `course/joins`, `#/` and `` all read. */
export function parseRoute(hash: string): Route | null {
  const raw = (hash.charAt(0) === '#' ? hash.slice(1) : hash).split('?')[0];
  const parts = raw.split('/').filter((p) => p.length > 0).map(segment);
  if (parts.length === 0) return { name: 'home' };
  const [head, second, third] = parts;
  switch (head) {
    case 'library':
      return parts.length === 1 ? { name: 'library' } : null;
    case 'workbook':
      return parts.length === 1 ? { name: 'workbook' } : null;
    case 'progress':
      return parts.length === 1 ? { name: 'progress' } : null;
    case 'appearance':
      return parts.length === 1 ? { name: 'appearance' } : null;
    case 'create':
      return parts.length === 1 ? { name: 'create' } : null;
    case 'lab':
      if (parts.length === 1) return { name: 'lab', challengeId: null };
      if (parts.length === 2 && second) return { name: 'lab', challengeId: second };
      return null;
    case 'course':
      if (parts.length === 2 && second) return { name: 'course', courseId: second };
      if (parts.length === 3 && second && third) return { name: 'lesson', courseId: second, chapterId: third };
      return null;
    default:
      return null;
  }
}

/** A course the app can actually open. This mirrors the existing navigation
    (`selectCourse`/`openLessonIn`), which only ever opens a course the catalog
    has built: a deep link must resolve the same way, not invent an enrolment. */
function openableCourse(id: string): Course | null {
  const course = courseById(id);
  return course && course.built ? course : null;
}

function homeTarget(unknown: boolean): RouteTarget {
  return { hash: HOME_HASH, screen: 'home', courseId: null, chapterIndex: null, challengeId: null, unknown };
}

/** Resolve a fragment to the screen and ids it names. Unknown fragments come
    back as Home with `unknown` set. */
export function routeTarget(hash: string): RouteTarget {
  const route = parseRoute(hash);
  if (!route) return homeTarget(true);
  switch (route.name) {
    case 'home':
      return homeTarget(false);
    case 'library':
      return { hash: '#/library', screen: 'library', courseId: null, chapterIndex: null, challengeId: null, unknown: false };
    case 'workbook':
      return { hash: '#/workbook', screen: 'workbook', courseId: null, chapterIndex: null, challengeId: null, unknown: false };
    case 'progress':
      return { hash: '#/progress', screen: 'workbook-progress', courseId: null, chapterIndex: null, challengeId: null, unknown: false };
    case 'appearance':
      return { hash: '#/appearance', screen: 'appearance-settings', courseId: null, chapterIndex: null, challengeId: null, unknown: false };
    case 'create':
      return { hash: '#/create', screen: 'create', courseId: null, chapterIndex: null, challengeId: null, unknown: false };
    case 'lab':
      return {
        hash: route.challengeId ? `#/lab/${encodeURIComponent(route.challengeId)}` : '#/lab',
        screen: 'challenge-lab',
        courseId: null,
        chapterIndex: null,
        challengeId: route.challengeId,
        unknown: false,
      };
    case 'course': {
      const course = openableCourse(route.courseId);
      /* A catalog course the app cannot open (an unbuilt preview) is Home. A
         course the catalog has never heard of is not: the gateway may own it,
         so the route is handed to the store whole and resolved there. */
      if (!course && courseById(route.courseId)) return homeTarget(true);
      if (course) {
        return {
          hash: `#/course/${encodeURIComponent(course.id)}`,
          screen: 'course',
          courseId: course.id,
          chapterIndex: null,
          challengeId: null,
          unknown: false,
        };
      }
      return {
        hash: `#/course/${encodeURIComponent(route.courseId)}`,
        screen: 'course',
        courseId: route.courseId,
        chapterIndex: null,
        challengeId: null,
        unknown: false,
        courseUnresolved: true,
      };
    }
    case 'lesson': {
      const course = openableCourse(route.courseId);
      if (!course && courseById(route.courseId)) return homeTarget(true);
      if (course) {
        const index = activeModules(course).findIndex((m) => m.id === route.chapterId);
        if (index < 0) return homeTarget(true);
        return {
          hash: `#/course/${encodeURIComponent(course.id)}/${encodeURIComponent(route.chapterId)}`,
          screen: 'lesson',
          courseId: course.id,
          chapterIndex: index,
          challengeId: null,
          unknown: false,
        };
      }
      /* The chapter id travels with the route so the store can find the index
         once the course arrives; without it a stored course's chapter link
         would open the course at chapter 1. */
      return {
        hash: `#/course/${encodeURIComponent(route.courseId)}/${encodeURIComponent(route.chapterId)}`,
        screen: 'lesson',
        courseId: route.courseId,
        chapterIndex: null,
        chapterId: route.chapterId,
        challengeId: null,
        unknown: false,
        courseUnresolved: true,
      };
    }
  }
}

/** The canonical fragment for a slice of app state — the state → URL direction.
    Every screen has one, so a screen change and a fragment change cannot
    disagree about where the learner is. */
export function hashForState(state: RouteState): string {
  switch (state.screen) {
    case 'home':
      return HOME_HASH;
    case 'library':
      return '#/library';
    case 'workbook':
      return '#/workbook';
    case 'workbook-progress':
      return '#/progress';
    case 'appearance-settings':
      return '#/appearance';
    /* Creating a course is one flow across three screens, and it is one URL: a
       reload during the build lands at the start of the flow, not mid-wizard on
       whatever draft happened to be in memory. */
    case 'create':
    case 'building':
    case 'review':
      return '#/create';
    case 'challenge-lab':
      return state.challengeId ? `#/lab/${encodeURIComponent(state.challengeId)}` : '#/lab';
    case 'course':
      return `#/course/${encodeURIComponent(state.courseId)}`;
    case 'lesson': {
      const modules = activeModules(courseById(state.courseId));
      const module = modules[state.chapterIndex] || modules[0];
      if (!module) return HOME_HASH;
      return `#/course/${encodeURIComponent(state.courseId)}/${encodeURIComponent(module.id)}`;
    }
  }
}

/** True for a fragment the router owns (`#/…`, or none at all), false for an
    in-page anchor such as the shell's `#main` skip link, which must be left for
    the browser to handle. */
export function isRouteHash(hash: string): boolean {
  return hash === '' || hash === '#' || hash.startsWith('#/');
}
