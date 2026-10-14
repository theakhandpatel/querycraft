import type { ExecutionApi } from '../types';

/* What the UI may honestly claim about where a result came from.
 *
 * SQL execution, grading, the dataset and saved progress are real in every
 * adapter: the gateway + PostgreSQL on http, PGlite in the page on pglite, a
 * canned engine on mock. Generation now works the same way the rest of the app
 * does: the screens pace their own progress, run every reference query and
 * adversarial fixture on the engine below, and only then ask the API to record
 * the draft. Nothing here claims model-generated content. */

export function generationProvenance(api: ExecutionApi): string {
  switch (api.mode) {
    case 'http':
      return `references graded on ${api.baseUrl} · draft recorded by the gateway`;
    case 'pglite':
      return 'references graded in-browser · draft recorded locally';
    default:
      return 'references graded on the canned engine (dev only)';
  }
}

/** One-line description of the execution engine actually behind Run/Check. */
export function executionEngineLabel(api: ExecutionApi): string {
  return api.config.engine;
}
