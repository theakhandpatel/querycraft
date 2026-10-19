/*
 * The browser's half of the SQLite dialect profile.
 *
 * M6's rule is that the per-dialect execution facts live in one language-neutral
 * file — `contracts/dialects/sqlite.json` — and each engine *reads* it instead
 * of keeping its own copy that can drift. The Go gateway embeds a byte-identical
 * mirror (`backend/internal/dialect/sqlite.json`, guarded by
 * `TestEmbeddedProfileMatchesContract`, which cannot escape a package directory
 * with a go:embed pattern). The browser can import the contract directly, so it
 * does: one file, two readers, no mirror at all.
 *
 * This module owns three things and nothing else:
 *
 *   - `classifySqliteMessage` — the profile's 30 ordered message rules, first
 *     match wins, exactly as `dialect.Profile.ClassifyError` applies them. The
 *     rules describe *SQLite's* own message text (what sqlite3_errmsg returns),
 *     so this is a **pure matcher**: it is handed the engine's sentence and
 *     nothing else. Removing the framing a driver wraps around that sentence is
 *     the *driver's* business, and it happens beside the driver — the gateway's
 *     raw driver (`backend/internal/engine/sqlite_raw.go`) reports
 *     sqlite3_errmsg verbatim, so the Go executor hands the sentence straight to
 *     the profile, while the browser's wasm build frames its errors as
 *     `SQLITE_ERROR: sqlite3 result code 1: incomplete input`, which
 *     `sqliteUnframe` in `web/src/api/sqliteFraming.ts` strips before the rules
 *     run. Keeping the strip out of here is why the same file can serve two
 *     drivers: without it the `equals` and `prefix` rules can never fire
 *     and a syntax error reaches the learner as a runtime error.
 *   - `encodeSqliteValue` — the storage-class → JSON-value map. SQLite reports
 *     the class of a value, never the declared type of its column, so a text
 *     value in an INTEGER-affinity column is reported as text. That is the
 *     dialect's central lesson (`docs/sqlite-dialect.md` §2), and re-applying
 *     the declared affinity here would report a value the engine never stored.
 *   - `sqliteCapabilities` — the capability block the UI reads. It is derived
 *     from the same file, so a screen cannot advertise a feature the profile
 *     says the engine does not have.
 */

import contract from '../../../contracts/dialects/sqlite.json';
import { isQueryErrorCode } from '../types';
import type { EngineCapabilities, QueryErrorCode, RowValue } from '../types';

/** The engine name a result row carries. The Go side reports the same string
    shape (`SQLite 3.50.3`) from the driver's own `sqlite_version()`. */
export const SQLITE_ENGINE_LABEL = contract.engine_label;

/** The profile's match kinds, as a closed set — the same closure the Go side
    asserts in `TestErrorClassificationShape`. A rule naming anything else is a
    contract bug, so it throws at module load rather than matching nothing. */
const MATCH_KINDS = ['equals', 'prefix', 'contains'] as const;
type MatchKind = (typeof MATCH_KINDS)[number];

interface MessageRule {
  match: MatchKind;
  pattern: string;
  code: QueryErrorCode;
}

function isMatchKind(value: string): value is MatchKind {
  return (MATCH_KINDS as readonly string[]).includes(value);
}

/* Decoded once, at import, so a malformed contract fails loudly and early. */
const RULES: readonly MessageRule[] = (contract.error_classification as readonly { match: string; pattern: string; code: string }[])
  .map((rule, i) => {
    if (!isMatchKind(rule.match)) {
      throw new Error(`contracts/dialects/sqlite.json error_classification[${i}] has match "${rule.match}", which is not one of ${MATCH_KINDS.join(', ')}`);
    }
    /* The profile is contract data, not a closed set TypeScript can see. A
       rule code outside query-result.schema.json would compile and then crash
       the UI's own branching, so it fails here, at import. */
    if (!isQueryErrorCode(rule.code)) {
      throw new Error(`contracts/dialects/sqlite.json error_classification[${i}] has code "${rule.code}", which is outside contracts/query-result.schema.json`);
    }
    return { match: rule.match, pattern: rule.pattern, code: rule.code };
  });

/** The rule set, in order, for a caller that wants to show it (a diagnostics
    screen, or the audit that keeps this reader honest). */
export function sqliteErrorRules(): readonly MessageRule[] {
  return RULES;
}

/** The closed `QueryResult` error code for one engine message.
 *
 * `message` is the engine's own sentence, already stripped of the driver's
 * framing by the caller that owns the driver (`sqliteUnframe` in
 * `web/src/api/sqliteFraming.ts`; the Go raw driver supplies it unframed). This
 * function matches and nothing else — see the module header. Ordered, first match wins,
 * and the profile's last rule is the catch-all, so this always returns a code —
 * the same guarantee `TestClassifyErrorNeverEmitsExecutorCodes` pins on the Go
 * side. */
export function classifySqliteMessage(message: string): QueryErrorCode {
  const msg = message.trim();
  for (const rule of RULES) {
    if (rule.match === 'equals' && msg === rule.pattern) return rule.code;
    if (rule.match === 'prefix' && msg.startsWith(rule.pattern)) return rule.code;
    if (rule.match === 'contains' && msg.includes(rule.pattern)) return rule.code;
  }
  /* Unreachable while the profile's catch-all is last, but a rule set edited
     without one must not return undefined. */
  return 'QUERY_RUNTIME_ERROR';
}

/**
 * The profile's affinity of a declared type, by SQLite's own ordered rules
 * (`types.affinity_rules`, from sqlite.org/datatype3.html §3.1). Order is the
 * whole rule: `VARCHAR` contains both "CHAR" and nothing else, `BIGINT` contains
 * "INT", and a declared type matching no rule gets NUMERIC — while a column with
 * *no* declared type gets BLOB, which is a different answer and easy to lose.
 *
 * The profile states each rule as prose ("the declared type contains \"INT\""),
 * so this reads the quoted tokens the prose names rather than restating the
 * affinity table here: the tokens and the order stay the profile's.
 */
export function affinityFor(declaredType?: string | null): string {
  const declared = (declaredType ?? '').trim().toUpperCase();
  for (const rule of contract.types.affinity_rules) {
    const tokens = [...rule.when.matchAll(/"([^"]+)"/g)].map((m) => m[1]);
    /* A rule with no quoted token is the profile's catch-all. */
    if (tokens.length === 0) return rule.affinity;
    if (tokens.some((token) => declared.includes(token))) return rule.affinity;
    if (declared === '' && /no declared type/.test(rule.when)) return rule.affinity;
  }
  return 'NUMERIC';
}

/** 2^53-1, read from the profile: the largest integer a JSON number holds. */
const MAX_SAFE_INTEGER = Number(contract.value_encoding.integer.max_safe_integer ?? 9007199254740991);

/**
 * The label a result column carries, from the profile's `column_type_label`
 * rule: the declared type verbatim when the column has one, otherwise the
 * profile's own fallback word.
 *
 * The declared type is *not* the value's type — a `DATE` column labels `DATE`
 * while every value in it is TEXT — and the fallback is read from the contract
 * rather than written here, so the two adapters and the profile cannot disagree
 * about what an expression is called.
 */
export function sqliteColumnTypeLabel(declared: string | null | undefined): string {
  const fallback = contract.value_encoding.column_type_label?.fallback ?? 'expression';
  const label = (declared ?? '').trim();
  return label === '' ? fallback : label;
}

const BLOB_PREFIX = contract.value_encoding.blob.prefix ?? '\\x';

/**
 * One value the engine handed back, as the JSON value a `QueryResult` row
 * carries. The storage class decides; the declared affinity of the column is
 * deliberately not a parameter, because applying it would report a value the
 * engine never stored.
 */
// oxlint-disable-next-line anti-slop/no-unknown-parameters -- a raw engine value is untrusted; this is the storage-class encode boundary.
export function encodeSqliteValue(raw: unknown): RowValue {
  if (raw === null || raw === undefined) return null;
  // oxlint-disable-next-line anti-slop/no-runtime-typeof -- the engine's storage classes are runtime JS values; this switch is their decode boundary.
  switch (typeof raw) {
    case 'boolean':
      /* No boolean class: TRUE/FALSE are 1/0 and the domain is unenforced, so
         reporting a boolean would be an invention. */
      return raw ? 1 : 0;
    case 'bigint':
      return withinSafeRange(raw) ? Number(raw) : raw.toString();
    case 'number':
      if (!Number.isFinite(raw)) {
        /* JSON has no non-finite numbers; the profile names the rule. */
        if (Number.isNaN(raw)) return null;
        return raw > 0 ? 'Infinity' : '-Infinity';
      }
      return raw;
    case 'string':
      /* Verbatim, never parsed: a stored date stays the same text. */
      return raw;
    case 'object':
      if (raw instanceof Uint8Array) return BLOB_PREFIX + hexLower(raw);
      if (raw instanceof Date) {
        /* A driver that materialised a Date has already lost the stored text;
           report the instant as ISO text rather than a host Date object, which
           is what would reintroduce the timezone shift the PostgreSQL adapter
           had to fix. */
        return raw.toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, '');
      }
      return String(raw);
    default:
      return String(raw);
  }
}

function withinSafeRange(v: bigint): boolean {
  return v >= BigInt(-MAX_SAFE_INTEGER) && v <= BigInt(MAX_SAFE_INTEGER);
}

function hexLower(bytes: Uint8Array): string {
  let out = '';
  for (const b of bytes) out += b.toString(16).padStart(2, '0');
  return out;
}

/**
 * The capability block for the SQLite adapter, derived from the profile so the
 * UI cannot advertise what the engine does not have. `over` carries the two
 * facts only the running host knows — where the engine ended up, and how long
 * its deadline is. `hardTimeout` is the caller's honest answer to "can this host
 * stop a statement mid-run", never a guess: the profile's `interrupt` capability
 * says the engine *offers* one, not that this deployment can reach it.
 */
export function sqliteCapabilities(over: {
  placement: EngineCapabilities['placement'];
  hardTimeout: boolean;
  timeoutMs: number;
  writable: boolean;
}): EngineCapabilities {
  return {
    placement: over.placement,
    hardTimeout: over.hardTimeout,
    timeoutMs: over.timeoutMs,
    writable: over.writable,
    explain: false,
    dialect: 'sqlite',
  };
}

/** The profile's own answer to "can this dialect plan", for a caller that wants
    to disable the plan action with the reason the profile gives. */
export function sqlitePlanRefusal(): string | null {
  const plan = contract.capabilities.plan;
  if (plan) return null;
  return contract.capability_notes.plan ?? 'SQLite has no EXPLAIN (ANALYZE), so a plan cannot be captured for this dialect.';
}
