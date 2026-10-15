/*
 * The driver's error framing, in one place, and testable without a browser.
 *
 * `contracts/dialects/sqlite.json` is a pure matcher: its rules are written for
 * SQLite's own sentence (`incomplete input`, `unrecognized token: …`). The
 * browser's SQLite build frames that sentence before handing it over —
 * `SQLITE_ERROR: sqlite3 result code 1: incomplete input` — and the frame is a
 * property of *this* build, not of SQLite. The Go executor strips modernc's
 * different frame (`<errstr>: <msg> (<rc>)`) in the same relative position
 * (`backend/internal/engine/sqlite.go`). Each adapter removes its own driver's
 * framing, and the profile stays the shared, language-neutral rule table.
 *
 * The frame's own numbers and words are what is matched, so SQLite's colons
 * survive: `no such table: nope` and `unrecognized token: "'abc"` are handed on
 * with their sentence intact, and a message this build did not write is returned
 * untouched rather than guessed at.
 *
 * This lives beside the driver rather than in the profile, and beside the
 * adapter rather than inside it, so `scripts/sqlite-error-codes.mjs` can bundle
 * it and pin the exact strings the build produces — a dependency bump that
 * changes the frame then fails an audit instead of silently downgrading every
 * syntax error to a runtime error, which is the defect this file exists for.
 */
import { classifySqliteMessage } from './sqliteProfile';

/* Measured against `@sqlite.org/sqlite-wasm` 3.53.4, every case in
   `/tmp/sqlite-spike/PROBE2.json` and re-measured in the page:
     SQLITE_ERROR: sqlite3 result code 1: incomplete input
     SQLITE_ERROR: sqlite3 result code 1: near "SELEC": syntax error
     SQLITE_ERROR: sqlite3 result code 1: unrecognized token: "'abc"
     SQLITE_ERROR: sqlite3 result code 1: no such table: nope
     SQLITE_CONSTRAINT_UNIQUE: sqlite3 result code 2067: UNIQUE constraint failed: t.id
     SQLITE_INTERRUPT: sqlite3 result code 9: interrupted
   `Error` is included because a JS-level wrapper may re-prefix the engine's own
   text; nothing else is, because nothing else is this build's shape. */
const DRIVER_FRAME = /^(?:SQLITE[A-Z_0-9]*|Error):\s*sqlite3 result code \d+:\s*/;

/** SQLite's own sentence, with this build's framing removed. */
export function sqliteUnframe(framed: string): string {
  const message = String(framed ?? '').trim();
  const match = DRIVER_FRAME.exec(message);
  return match === null ? message : message.slice(match[0].length).trim();
}

/** The closed `QueryResult` code for one driver message: unframe, then match. */
export function sqliteErrorCode(framed: string): string {
  return classifySqliteMessage(sqliteUnframe(framed));
}
