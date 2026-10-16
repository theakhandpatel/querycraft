import type { ExecutionApi } from '../types';
import type { AiArtifact, AiProvenance } from '../api/aiContract';

/* What the UI may honestly claim about where a result came from.
 *
 * SQL execution, grading, the dataset and saved progress are real in every
 * adapter: the gateway + PostgreSQL on http, PGlite in the page on pglite.
 * Generation works the same way the rest of the app
 * does: the gateway owns the provider and the pipeline, the screens render the
 * stages it reports, and nothing here claims model-generated content that the
 * service did not produce. */

export function generationProvenance(api: ExecutionApi): string {
  switch (api.mode) {
    case 'http':
      return `references graded on ${api.baseUrl} · draft recorded by the gateway`;
    case 'pglite':
      return 'references graded in-browser · draft recorded locally';
  }
}

/** One-line description of the execution engine actually behind Run/Check. */
export function executionEngineLabel(api: ExecutionApi): string {
  return api.config.engine;
}

/**
 * The provider and model that produced an AI artifact, read from the artifact's
 * own provenance (design §11.2: the service records every version). A quiet
 * label, not a badge: empty when there is no artifact to describe.
 */
export function artifactProvenanceLabel(artifact: AiArtifact | null | undefined): string {
  return provenanceLabel(artifact?.provenance);
}

/**
 * The same label for anything else that carries provenance — a generated
 * course plan does, and it is the same fact: which provider, which model, which
 * pipeline, how many repairs.
 */
export function provenanceLabel(p: AiProvenance | null | undefined): string {
  if (!p) return '';
  const model = p.model ? ` ${p.model}` : '';
  const pipeline = p.pipeline_version ? ` · pipeline ${p.pipeline_version}` : '';
  const repairs = p.repairs ? ` · ${p.repairs} repair${p.repairs === 1 ? '' : 's'}` : '';
  return `${p.provider}${model}${pipeline}${repairs}`.trim();
}
