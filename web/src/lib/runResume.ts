/*
 * The breadcrumb for a background generation this tab was watching.
 *
 * A run lives on the gateway: it keeps going whether or not a tab is open, and
 * `GET /generation/{id}` is the only thing that says what it is doing. What a
 * reload cannot recover by itself is *which* run this learner was watching — a
 * fresh page holds no handle, and the gateway has no "my runs" route to ask.
 * This stores that id and nothing else about the run: every fact about its
 * state is read back from the gateway, so a stale breadcrumb can cost one read
 * but can never produce a wrong answer.
 *
 * It is scoped to the account that started the run. That scoping is a local
 * courtesy, not the security boundary — the gateway already answers about
 * somebody else's run exactly as it answers about one that never existed — but
 * it keeps a second learner on the same browser from being shown a progress
 * view for work that is not theirs.
 */

import { Option, Schema } from 'effect';

interface RunBreadcrumb {
  /** The id the gateway returned when the run was submitted. */
  jobId: string;
  /** The account the run belongs to. A breadcrumb from another account is dropped. */
  ownerId: string;
  /** The goal, kept because a resumed page has no form left to read it from. */
  goal: string;
  /** When the run was submitted, so an old breadcrumb can be recognised as old. */
  startedAt: number;
}

const KEY = 'qc.run';

/* The stored breadcrumb as JSON. Every field is optional so a partial or older
   record still decodes, and the caller decides what a missing field reads as. */
interface StoredRun {
  jobId?: string;
  ownerId?: string;
  goal?: string;
  startedAt?: number;
}

const StoredRunSchema = Schema.Struct({
  jobId: Schema.optional(Schema.String),
  ownerId: Schema.optional(Schema.String),
  goal: Schema.optional(Schema.String),
  startedAt: Schema.optional(Schema.Number),
});
const storedRunFromString = Schema.fromJsonString(StoredRunSchema);

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    /* Storage can be denied outright. The run still continues on the gateway;
       this tab just cannot offer to re-attach to it after a reload. */
    return null;
  }
}

function readRaw(): StoredRun | null {
  const store = storage();
  if (!store) return null;
  let raw: string | null = null;
  try {
    raw = store.getItem(KEY);
  } catch {
    return null;
  }
  if (!raw) return null;
  return Option.getOrNull(Schema.decodeUnknownOption(storedRunFromString)(raw));
}

/** The run this account was watching, or null when there is none to resume.
    A breadcrumb that belongs to somebody else, or that cannot be read at all,
    is removed rather than reported. */
export function readRun(ownerId: string | null): RunBreadcrumb | null {
  if (!ownerId) return null;
  const record = readRaw();
  if (!record) {
    clearRun();
    return null;
  }
  const jobId = record.jobId ?? '';
  if (!jobId) {
    clearRun();
    return null;
  }
  if (record.ownerId !== ownerId) {
    clearRun();
    return null;
  }
  return {
    jobId,
    ownerId,
    goal: record.goal ?? '',
    startedAt: record.startedAt ?? 0,
  };
}

export function writeRun(run: RunBreadcrumb): void {
  const store = storage();
  if (!store) return;
  try {
    store.setItem(KEY, JSON.stringify(run));
  } catch {
    /* A full or refused store is not a failed run. */
  }
}

/** Forget the breadcrumb. Passing the id makes it conditional: a late clear
    from a run that has already been replaced must not wipe the newer one. */
export function clearRun(jobId?: string): void {
  if (jobId !== undefined) {
    const record = readRaw();
    if (record && record.jobId !== undefined && record.jobId !== jobId) return;
  }
  const store = storage();
  if (!store) return;
  try {
    store.removeItem(KEY);
  } catch {
    /* Nothing to do about it. */
  }
}
