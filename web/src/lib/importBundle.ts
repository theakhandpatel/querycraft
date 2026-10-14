/*
 * Import: validate the design 8.3 document, then merge it into the workbook.
 *
 * Import is the other half of the migration path, so it is written the same way
 * as the export: one pure, storage-free module that the audit can drive without
 * a browser, and one schema that is the single source of truth for the shape.
 *
 * Two rules shape everything here:
 *
 *   1. Validate before writing. A document is checked against
 *      `contracts/export.schema.json` in full before a byte of it is applied, and
 *      a `format_version` this build does not implement is refused rather than
 *      half-read.
 *   2. Merge, never replace. Importing unions the incoming record into the
 *      existing one: an attempt with a request id already present is skipped,
 *      a note that already exists is not duplicated, and nothing the learner
 *      already has is deleted. Re-importing the same file is a no-op.
 *
 * Because of (2), importing an export into an empty workspace reproduces that
 * export exactly — which is what `scripts/import-audit.mjs` proves.
 */
import Ajv2020 from 'ajv/dist/2020';
import exportSchema from '../../../contracts/export.schema.json';
import type { EventKind, NoteItem, Progress, ProgressEvent, SavedItem } from '../types';
import { EXPORT_FORMAT_VERSION, type ExportBundle } from './exportBundle';
import { PROGRESS_VERSION, attemptKey, emptyProgress } from './progress';

/* ---------- validation ---------- */

export type ImportErrorCode =
  | 'INVALID_JSON'
  | 'NOT_A_DOCUMENT'
  | 'UNKNOWN_FORMAT_VERSION'
  | 'UNSUPPORTED_PROGRESS_VERSION'
  | 'MALFORMED_DOCUMENT';

export interface ImportFailure {
  ok: false;
  code: ImportErrorCode;
  /** Written for a person: says what was wrong and what this build accepts. */
  message: string;
}

export interface ImportCounts {
  attempts: number;
  notes: number;
  bookmarks: number;
  saved: number;
  drafts: number;
  /** Entries the document repeated, so the report says "skipped", not "lost". */
  alreadyPresent: number;
}

export interface ImportSuccess {
  ok: true;
  progress: Progress;
  drafts: Record<string, string>;
  counts: ImportCounts;
  /** The document's own version, so the report can name what it read. */
  formatVersion: number;
  exportedAt: string;
}

export type ImportOutcome = ImportSuccess | ImportFailure;

const ajv = new Ajv2020({ allErrors: true, strict: true });
const validateAgainstSchema = ajv.compile(exportSchema as Record<string, unknown>);

function fail(code: ImportErrorCode, message: string): ImportFailure {
  return { ok: false, code, message };
}

/** The first schema complaint, as a path a person can act on. */
function describeSchemaError(): string {
  const first = validateAgainstSchema.errors?.[0];
  if (!first) return 'the document does not match the export schema';
  const path = first.instancePath ? first.instancePath.replace(/^\//, '').replace(/\//g, '.') : 'the document';
  if (first.keyword === 'additionalProperties') {
    const extra = (first.params as { additionalProperty?: string }).additionalProperty;
    return `${path} has a property this format does not define${extra ? ` ("${extra}")` : ''}`;
  }
  return `${path}: ${first.message ?? 'is invalid'}`;
}

/**
 * Check a parsed document without writing anything. Refuses a newer document
 * rather than guessing at it, and refuses one that merely claims to be an
 * export: the schema is exact, so a stray credential field is a rejection, not
 * something quietly kept.
 */
export function validateBundle(raw: unknown): { ok: true; bundle: ExportBundle } | ImportFailure {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return fail('NOT_A_DOCUMENT', 'This file is not a Querycraft export. Expected a single JSON object.');
  }
  const claimed = (raw as { format_version?: unknown }).format_version;
  if (!Number.isInteger(claimed)) {
    return fail('NOT_A_DOCUMENT', 'This file has no format_version, so it is not a Querycraft export.');
  }
  if (claimed !== EXPORT_FORMAT_VERSION) {
    return fail(
      'UNKNOWN_FORMAT_VERSION',
      `This file is format version ${String(claimed)}; this build reads version ${EXPORT_FORMAT_VERSION}.`,
    );
  }
  if (!validateAgainstSchema(raw)) {
    return fail('MALFORMED_DOCUMENT', `This export is not readable — ${describeSchemaError()}.`);
  }
  const bundle = raw as ExportBundle;
  /* A snapshot layout from the future cannot be interpreted, only guessed at. */
  if (bundle.content_versions.progress_version > PROGRESS_VERSION) {
    return fail(
      'UNSUPPORTED_PROGRESS_VERSION',
      `This file was written against progress layout ${bundle.content_versions.progress_version}; this build understands ${PROGRESS_VERSION}.`,
    );
  }
  return { ok: true, bundle };
}

/** Parse the file's text, then validate it. Never throws. */
export function readImport(text: string): { ok: true; bundle: ExportBundle } | ImportFailure {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (e) {
    return fail('INVALID_JSON', `This file is not valid JSON — ${e instanceof Error ? e.message : String(e)}.`);
  }
  return validateBundle(raw);
}

/* ---------- merge ---------- */

/** Attempt outcomes, the kinds that carry a request id and a verdict. */
const ATTEMPT_KINDS: ReadonlySet<EventKind> = new Set<EventKind>(['passed', 'failed', 'accepted', 'error']);

/** Nothing the learner produced: the merge of an import into it is the import. */
export function isEmptyProgress(p: Progress): boolean {
  return (
    p.events.length === 0 &&
    p.notes.length === 0 &&
    p.saved.length === 0 &&
    Object.keys(p.passed).length === 0 &&
    Object.keys(p.accepted).length === 0 &&
    Object.keys(p.firstTry).length === 0 &&
    Object.keys(p.done).length === 0 &&
    Object.keys(p.timeMs).length === 0
  );
}

/* Identity for a workbook item. The timestamp alone is not enough: two notes can
   be written in the same millisecond, and a note and a bookmark can share one. */
function noteKey(n: { kind: string; course: string; lesson: number; ts: number; text: string }): string {
  return `${n.kind}:${n.course}:${n.lesson}:${n.ts}:${n.text}`;
}

function savedKey(s: { course: string; lesson: number; name: string; sql?: string }): string {
  return `${s.course}:${s.lesson}:${s.name}:${s.sql ?? ''}`;
}

/** True wins on a union, so importing can never un-pass or un-complete anything. */
function unionFlags(a: Record<string, boolean>, b: Record<string, boolean>): Record<string, boolean> {
  const out: Record<string, boolean> = { ...a };
  for (const [k, v] of Object.entries(b)) out[k] = !!out[k] || !!v;
  return out;
}

/** Study time is only ever added to, and re-importing the same export must not
    double it, so the merge takes the larger measured total per course. */
function unionTime(a: Record<string, number>, b: Record<string, number>): Record<string, number> {
  if (Object.keys(a).length === 0) return { ...b };
  const out: Record<string, number> = { ...a };
  for (const [k, v] of Object.entries(b)) out[k] = Math.max(out[k] ?? 0, v);
  return out;
}

function unionDays(a: number[], b: number[]): number[] {
  if (a.length === 0) return b.slice();
  const out = a.slice();
  for (let i = 0; i < b.length; i++) out[i] = Math.max(out[i] ?? 0, b[i] ?? 0);
  return out;
}

export interface MergeResult {
  progress: Progress;
  counts: ImportCounts;
}

/**
 * Fold an incoming snapshot into the current one without losing either side.
 *
 * Identity is per attempt (`attemptKey`: the client request id, falling back to
 * course+exercise+timestamp for documents written before request ids existed),
 * per workbook item and per saved query. Nothing is overwritten and nothing is
 * removed, so the operation is commutative in effect and idempotent in practice.
 */
export function mergeProgress(current: Progress, incoming: Progress): MergeResult {
  const fresh = isEmptyProgress(current);

  const seenEvents = new Set(current.events.map(attemptKey));
  const events: ProgressEvent[] = current.events.slice();
  let attempts = 0;
  let alreadyPresent = 0;
  for (const evt of incoming.events) {
    const key = attemptKey(evt);
    if (seenEvents.has(key)) {
      alreadyPresent++;
      continue;
    }
    seenEvents.add(key);
    events.push(evt);
    if (ATTEMPT_KINDS.has(evt.kind)) attempts++;
  }

  const seenNotes = new Set(current.notes.map((n) => noteKey(n)));
  const addedNotes: NoteItem[] = [];
  let notes = 0;
  let bookmarks = 0;
  for (const item of incoming.notes) {
    const key = noteKey(item);
    if (seenNotes.has(key)) {
      alreadyPresent++;
      continue;
    }
    seenNotes.add(key);
    addedNotes.push(item);
    if (item.kind === 'bookmark') bookmarks++;
    else notes++;
  }

  const seenSaved = new Set(current.saved.map((s) => savedKey(s)));
  const addedSaved: SavedItem[] = [];
  let saved = 0;
  for (const item of incoming.saved) {
    const key = savedKey(item);
    if (seenSaved.has(key)) {
      alreadyPresent++;
      continue;
    }
    seenSaved.add(key);
    addedSaved.push(item);
    saved++;
  }

  const progress: Progress = {
    version: PROGRESS_VERSION,
    /* A workspace stays "sample data" only while both sides are sample data; an
       import of real work into it — or of it into real work — is real work. */
    seeded: fresh ? incoming.seeded : current.seeded && incoming.seeded,
    events,
    passed: unionFlags(current.passed, incoming.passed),
    accepted: unionFlags(current.accepted, incoming.accepted),
    firstTry: unionFlags(current.firstTry, incoming.firstTry),
    done: unionFlags(current.done, incoming.done),
    notes: current.notes.concat(addedNotes),
    saved: current.saved.concat(addedSaved),
    attempts: fresh ? incoming.attempts : Math.max(current.attempts, incoming.attempts),
    attemptsByDay: fresh ? incoming.attemptsByDay.slice() : unionDays(current.attemptsByDay, incoming.attemptsByDay),
    timeMs: unionTime(current.timeMs, incoming.timeMs),
  };

  return { progress, counts: { attempts, notes, bookmarks, saved, drafts: 0, alreadyPresent } };
}

/**
 * Drafts merge the same way, except the in-progress editor wins: the text on
 * screen right now is the learner's latest, so an import only fills cells that
 * have no draft yet.
 */
export function mergeDrafts(
  current: Record<string, string>,
  incoming: Record<string, string>,
): { drafts: Record<string, string>; filled: number } {
  const drafts: Record<string, string> = { ...incoming, ...current };
  const filled = Object.keys(incoming).filter((k) => incoming[k] !== '' && (current[k] === undefined || current[k] === '')).length;
  return { drafts, filled };
}

/** The whole import as one pure step: nothing is written, nothing is thrown. */
export function applyImport(current: Progress, bundle: ExportBundle, currentDrafts: Record<string, string> = {}): ImportSuccess {
  const { progress, counts } = mergeProgress(current, bundle.progress);
  const merged = mergeDrafts(currentDrafts, bundle.drafts);
  return {
    ok: true,
    progress,
    drafts: merged.drafts,
    counts: { ...counts, drafts: merged.filled },
    formatVersion: bundle.format_version,
    exportedAt: bundle.exported_at,
  };
}

/** The sentence the toast and the workbook both show, so the count is one fact. */
export function describeImport(result: ImportSuccess): string {
  const parts: string[] = [];
  if (result.counts.attempts) parts.push(`${result.counts.attempts} attempt${result.counts.attempts === 1 ? '' : 's'}`);
  if (result.counts.notes) parts.push(`${result.counts.notes} note${result.counts.notes === 1 ? '' : 's'}`);
  if (result.counts.bookmarks) parts.push(`${result.counts.bookmarks} bookmark${result.counts.bookmarks === 1 ? '' : 's'}`);
  if (result.counts.saved) parts.push(`${result.counts.saved} saved quer${result.counts.saved === 1 ? 'y' : 'ies'}`);
  if (result.counts.drafts) parts.push(`${result.counts.drafts} draft${result.counts.drafts === 1 ? '' : 's'}`);
  if (parts.length === 0) {
    return result.counts.alreadyPresent > 0
      ? 'Nothing new — every entry in that export is already in your workbook.'
      : 'Nothing to import — that export has no workbook entries.';
  }
  const skipped = result.counts.alreadyPresent ? `, ${result.counts.alreadyPresent} already present` : '';
  return `Imported ${parts.join(', ')}${skipped}. Nothing was overwritten.`;
}

/** The workbook's empty-state text, kept next to the counts it describes. */
export function emptyImportState(): Progress {
  return emptyProgress();
}
