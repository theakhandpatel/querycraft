/*
 * The versioned JSON export (technical design §8.3).
 *
 * One self-contained document: a format version, the content versions the data
 * was produced against, and every workbook entity the learner owns. It never
 * contains credentials — there is no token, session or password field, and the
 * progress snapshot it wraps is the same payload the gateway stores, which has
 * none either.
 *
 * The format is the migration path between browser and server mode, so the
 * version is explicit and additive changes bump it.
 */
import type { Progress } from '../types';

/** Bump when the shape below changes in a way an older reader cannot accept. */
export const EXPORT_FORMAT_VERSION = 1;

/** localStorage prefix for per-cell editor drafts. */
const DRAFT_PREFIX = 'qc.draft.';

export interface ExportBundle {
  format_version: number;
  exported_at: string;
  /** The content the data was produced against (design §7.3). */
  content_versions: {
    dataset_version: number;
    progress_version: number;
  };
  /** The learner's own record: attempts, passes, notes, saved queries, time. */
  progress: Progress;
  /** Editor text per cell id, so an in-progress answer is not lost. */
  drafts: Record<string, string>;
}

/** Every non-empty editor draft currently held in storage. */
export function collectDrafts(storage: Storage | null = safeStorage()): Record<string, string> {
  const drafts: Record<string, string> = {};
  if (!storage) return drafts;
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (!key || !key.startsWith(DRAFT_PREFIX)) continue;
    const value = storage.getItem(key);
    if (value != null && value !== '') drafts[key.slice(DRAFT_PREFIX.length)] = value;
  }
  return drafts;
}

export function buildExportBundle(
  progress: Progress,
  drafts: Record<string, string>,
  datasetVersion: number,
  now: Date = new Date(),
): ExportBundle {
  return {
    format_version: EXPORT_FORMAT_VERSION,
    exported_at: now.toISOString(),
    content_versions: {
      dataset_version: datasetVersion,
      progress_version: progress.version,
    },
    progress,
    drafts,
  };
}

/** A stable, sortable filename: querycraft-export-2026-10-14.json */
export function exportFilename(now: Date = new Date()): string {
  const iso = now.toISOString().slice(0, 10);
  return `querycraft-export-${iso}.json`;
}

/**
 * Serialize and hand the bundle to the browser as a download. Returns the
 * filename so a caller can report what it wrote.
 */
export function downloadExport(bundle: ExportBundle, filename = exportFilename()): string {
  const blob = new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
  // Revoke on the next tick: Safari needs the URL alive for the click to land.
  setTimeout(() => URL.revokeObjectURL(url), 0);
  return filename;
}

function safeStorage(): Storage | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage;
  } catch {
    return null;
  }
}
