/*
 * Comparing a lesson's *authored* rows against the engine's rows.
 *
 * This is not the learner-facing comparator. That one is `web/src/api/compare.ts`
 * and its Go twin `backend/internal/eval/compare.go`, is specified by design
 * §6.1, and is deliberately strict: rule 7 says "no implicit text normalization",
 * so `10:12` does not equal `10:12:00` there, and it must not start doing so —
 * a learner's answer is compared against a reference the *same engine* produced.
 *
 * An authored `result:` row is a different thing: it is the lesson's own claim
 * about its dataset, written for a reader, and it states a wall clock to the
 * minute ("2026-12-28 01:48"). The engine's text depends on the dialect and is
 * not the same characters: PostgreSQL prints a `timestamp` as
 * `2026-12-28 01:48:00` (eight-byte time, seconds always shown), SQLite stores
 * the value as ISO-8601 text and prints `2026-12-28 01:48` verbatim. So a check
 * that "the authored rows are true of the fixture" has to compare the *instant*,
 * not the characters, or it fails on two dialects for two different reasons.
 *
 * `perf-content-audit` and `perf-seam-audit` both ask that question, and they
 * used to answer it with two copies of this rule that had already drifted — the
 * seam audit compared `10:12` to `10:12:00` strictly and reported three false
 * failures. One copy, here, is the fix.
 */

const pad = (n) => String(n).padStart(2, '0');

/** The wall clock a timestamp holds, whatever shape the driver handed back.
    PGlite's default parsers turn the column into a `Date` (the literal read as
    local time), and `String(date)` is a locale string — "Mon Dec 28 2026 …" —
    that can never equal "2026-12-28 01:48", which is what once made every
    authored timestamp row look wrong. */
export function wallClock(v) {
  if (!(v instanceof Date)) return String(v);
  return `${v.getFullYear()}-${pad(v.getMonth() + 1)}-${pad(v.getDate())} ` +
    `${pad(v.getHours())}:${pad(v.getMinutes())}:${pad(v.getSeconds())}`;
}

/** An ISO-8601 date, or date and time, to at least minute precision. */
export const TIMESTAMP = /^\d{4}-\d{2}-\d{2}([ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?)?/;

/** Two authored/engine values as the same fact. Numbers compare numerically,
    booleans case-insensitively, and two timestamps compare as instants to the
    minute — the precision a lesson states. Text is otherwise text. */
export function sameValue(a, b) {
  if (a === null || a === undefined) return b === null || b === undefined;
  if (b === null || b === undefined) return false;
  if (typeof a === 'boolean' || typeof b === 'boolean') return String(a).toLowerCase() === String(b).toLowerCase();
  const as = wallClock(a);
  const bs = wallClock(b);
  if (TIMESTAMP.test(as) && TIMESTAMP.test(bs)) {
    return as.slice(0, 16).replace('T', ' ') === bs.slice(0, 16).replace('T', ' ');
  }
  const na = Number(as);
  const nb = Number(bs);
  if (as.trim() !== '' && bs.trim() !== '' && Number.isFinite(na) && Number.isFinite(nb)) return na === nb;
  return as === bs;
}

/** Row-for-row (`got` may be engine rows holding `Date`s, `want` authored rows). */
export function sameRows(got, want) {
  if (!Array.isArray(got) || !Array.isArray(want) || got.length !== want.length) return false;
  for (let i = 0; i < got.length; i++) {
    const a = got[i];
    const b = want[i];
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    for (let c = 0; c < a.length; c++) if (!sameValue(a[c], b[c])) return false;
  }
  return true;
}

/** The first cell where two row sets disagree, for a failure message that
    points at the row rather than saying "rows differ". */
export function firstDifference(got, want) {
  const rows = Math.max(got?.length ?? 0, want?.length ?? 0);
  for (let r = 0; r < rows; r++) {
    const a = got?.[r];
    const b = want?.[r];
    if (!a || !b) return { row: r, column: null, got: a ?? null, want: b ?? null };
    for (let c = 0; c < Math.max(a.length, b.length); c++) {
      if (!sameValue(a[c], b[c])) return { row: r, column: c, got: a[c] ?? null, want: b[c] ?? null };
    }
  }
  return null;
}

/** JavaScript source for one value, so a captured row is pasteable as authored.
    A timestamp is written to the minute, the precision the lesson states and
    the precision `sameValue` compares. */
export function jsValue(v) {
  if (v === null || v === undefined) return 'null';
  if (typeof v === 'number') return String(v);
  if (typeof v === 'boolean') return String(v);
  const text = v instanceof Date ? wallClock(v).slice(0, 16) : String(v);
  return `'${text.replace(/\\/g, '\\\\').replace(/'/g, "\\'")}'`;
}

/** JavaScript source for a row set, for `--capture`. */
export const jsRows = (rows) => '[' + rows.map((r) => '[' + r.map(jsValue).join(',') + ']').join(',') + ']';

/** The TypeScript literal a `result:` line should carry, for `--capture`. */
export function literal(columns, rows) {
  return `result:{columns:${JSON.stringify(columns)},rows:${JSON.stringify(rows)}}`;
}
