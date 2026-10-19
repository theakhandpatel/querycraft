/*
 * The two ways Querycraft ships from one codebase.
 *
 * Simple is the public browser build: the whole built-in library, SQLite and
 * PGlite in the page, progress in this browser, no server. It is the GitHub
 * Pages sample.
 *
 * Full is the hosted build: the Go gateway, accounts, AI course generation and
 * tutoring, stored courses, generation runs, and administration.
 *
 * The mode is a build-time choice, not a runtime switch. The static build sets
 * VITE_APP_MODE=simple together with VITE_API_MODE=pglite. The gateway build
 * leaves it unset and gets Full. Both builds share the courses, the grading
 * seam, the workbook, the themes, and the export format.
 */
export type AppMode = 'simple' | 'full';

const requested = import.meta.env.VITE_APP_MODE;

export const APP_MODE: AppMode = requested === 'simple' ? 'simple' : 'full';

/** True in the browser-only sample. Used to hide every hosted surface. */
export const SIMPLE_MODE = APP_MODE === 'simple';
