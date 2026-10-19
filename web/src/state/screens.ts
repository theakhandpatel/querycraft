/*
 * Screen metadata that is not a component. `store.tsx` owns the provider, so a
 * non-component export there would break Fast Refresh for the whole file; these
 * two maps live on their own instead.
 *
 * `SCREEN_CRUMBS` is the human label for a screen. `SCREEN_NAV` maps a screen to
 * the primary nav destination that should read as current in the top bar.
 */
import type { Screen } from '../types';

export const SCREEN_CRUMBS: Record<Screen, string> = {
  home: 'Home',
  course: 'My learning',
  library: 'Library',
  create: 'Create a course',
  building: 'Building course',
  review: 'Draft studio',
  manage: 'Manage',
  runs: 'Generation runs',
  lesson: 'Chapter',
  workbook: 'Workbook',
  'workbook-progress': 'Progress',
  'challenge-lab': 'Challenge lab',
  'appearance-settings': 'Appearance',
};

export const SCREEN_NAV: Record<Screen, string> = {
  home: 'home',
  course: 'course',
  library: 'library',
  create: 'create',
  building: 'create',
  review: 'create',
  manage: 'manage',
  runs: 'manage',
  lesson: 'course',
  workbook: 'workbook',
  'workbook-progress': 'progress',
  'challenge-lab': 'lab',
  'appearance-settings': 'appearance',
};
