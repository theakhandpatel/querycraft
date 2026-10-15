/*
 * SQLite dialect-profile audit — M6 step 3.
 *
 * contracts/dialects/sqlite.json is the language-neutral profile of the SQLite
 * dialect: its capabilities, the affinity algorithm, the value-encoding rules
 * and the ordered message-to-code table. backend/internal/dialect is the Go
 * reader and dialect_test.go is the Go half of the checks; this script is the
 * language-neutral half. It validates the profile against
 * contracts/dialect.schema.json and then asserts the claims a JSON Schema
 * cannot state — the ones that are relationships between two parts of the file:
 *
 *   - every code the profile can emit is a member of the closed QueryResult
 *     enum in contracts/query-result.schema.json, and the codes it cannot emit
 *     are named with a reason;
 *   - the error rules are ordered first-match-wins and the last rule is the
 *     catch-all (a rule that can never fire is a broken rule);
 *   - every capability key has a non-empty note and every note names a
 *     capability key;
 *   - every SQLite storage class (NULL/INTEGER/REAL/TEXT/BLOB) names a JSON
 *     kind, a rule and a note;
 *   - the affinity table covers the five affinity classes and every declared
 *     type maps into exactly one of them.
 *
 * A failure exits 1 and names the profile file and the broken rule.
 *
 *   node scripts/sqlite-error-codes.mjs
 *   node scripts/sqlite-error-codes.mjs --profile /tmp/perturbed.json
 *   make audit-sqlite-errors
 *
 * No new dependency: it reuses the ajv already under web/node_modules, the same
 * way scripts/ai-audit.mjs and scripts/adapter-parity-audit.mjs do.
 */
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { Reporter, repoRoot } from './lib/gateway.mjs';
import { assertFreshBundle } from './lib/bundle.mjs';

/* web/node_modules holds ajv; the repo root has no node_modules, so a
   createRequire rooted in web/ is how this script resolves it. */
const webRequire = createRequire(path.join(repoRoot, 'web', 'package.json'));

/* The browser reads the same profile (`web/src/api/sqliteProfile.ts`) and strips
   its own driver's framing before the rules run (`web/src/api/sqliteFraming.ts`).
   Both are bundled by `make audit-sqlite-errors`, and this guard refuses to judge
   an older revision — a stale bundle is valid and one revision behind, which is
   the trap the whole audit suite learned once already. */
assertFreshBundle('/tmp/sqlite-framing.cjs', 'audit-sqlite-errors', [
  'web/src/api/sqliteFraming.ts',
  'web/src/api/sqliteProfile.ts',
  'contracts/dialects/sqlite.json',
]);
assertFreshBundle('/tmp/sqlite-profile.cjs', 'audit-sqlite-errors', [
  'web/src/api/sqliteProfile.ts',
  'contracts/dialects/sqlite.json',
]);
/* Two bundles, because the two modules answer two questions. `framing` is the
   driver's own wrapper (what this build writes before SQLite's sentence) and
   knows nothing about the profile; `profile` is the shared rule table. Bundling
   them separately keeps the strip module from importing the whole profile just
   to be auditable, which is the coupling its header argues against. */
const browser = webRequire('/tmp/sqlite-framing.cjs');
const browserProfile = webRequire('/tmp/sqlite-profile.cjs');

const argv = process.argv.slice(2);
function flagValue(name, fallback) {
  const i = argv.indexOf(name);
  if (i === -1) return fallback;
  const value = argv[i + 1];
  if (!value || value.startsWith('--')) {
    console.error(`sqlite-error-codes: ${name} needs a path`);
    process.exit(1);
  }
  return path.resolve(value);
}

const CONTRACTS = path.join(repoRoot, 'contracts');
const schemaPath = path.join(CONTRACTS, 'dialect.schema.json');
const resultSchemaPath = path.join(CONTRACTS, 'query-result.schema.json');
const profilePath = flagValue('--profile', path.join(CONTRACTS, 'dialects', 'sqlite.json'));

/** display renders a path relative to the repo when it is inside it. */
function display(p) {
  const rel = path.relative(repoRoot, p);
  return rel.startsWith('..') ? p : rel;
}

const readJSON = (p) => JSON.parse(fs.readFileSync(p, 'utf8'));

let profile;
try {
  profile = readJSON(profilePath);
} catch (err) {
  console.error(`sqlite-error-codes: cannot read ${display(profilePath)}: ${err.message}`);
  process.exit(1);
}
const schema = readJSON(schemaPath);
const resultSchema = readJSON(resultSchemaPath);

const P = display(profilePath);
const r = new Reporter('sqlite-error-codes');
console.log(`profile: ${P}`);
console.log(`schema:  ${display(schemaPath)}`);

/* ---------- the closed enum, from its one source of truth ---------- */

/* The QueryResult error enum is the object arm of properties.error.oneOf. */
const closedCodes = resultSchema.properties?.error?.oneOf
  ?.find((arm) => Array.isArray(arm.properties?.code?.enum))?.properties.code.enum;
if (!Array.isArray(closedCodes) || closedCodes.length === 0) {
  console.error(`sqlite-error-codes: ${display(resultSchemaPath)}: no properties.error code enum found`);
  process.exit(1);
}

const MATCH_KINDS = ['prefix', 'contains', 'equals'];
const JSON_KINDS = ['null', 'boolean', 'number', 'string', 'array', 'object'];
const AFFINITIES = ['INTEGER', 'TEXT', 'BLOB', 'REAL', 'NUMERIC'];
/* The five SQLite storage classes and the profile key each lives under. */
const STORAGE_CLASSES = [
  { key: 'null', storage: 'NULL' },
  { key: 'integer', storage: 'INTEGER' },
  { key: 'real', storage: 'REAL' },
  { key: 'text', storage: 'TEXT' },
  { key: 'blob', storage: 'BLOB' },
];

/* ---------- schema: the profile's shape, and the schema's own edges ---------- */

r.section('schema');

const schemaCodeEnum = schema.$defs?.queryResultErrorCode?.enum;
r.check(
  `contracts/dialect.schema.json's error enum is the closed enum in query-result.schema.json (${closedCodes.length} codes)`,
  Array.isArray(schemaCodeEnum) && schemaCodeEnum.length === closedCodes.length
    && schemaCodeEnum.every((code, i) => code === closedCodes[i]),
  schemaCodeEnum,
);

/* `dialect`/`engine` must be the set of dialects that actually have a profile. */
const dialectDir = path.join(CONTRACTS, 'dialects');
const present = fs.readdirSync(dialectDir).filter((f) => f.endsWith('.json')).map((f) => f.slice(0, -'.json'.length));
const declaredByThemselves = [];
for (const file of present) {
  try {
    const doc = readJSON(path.join(dialectDir, file));
    if (doc.dialect) declaredByThemselves.push(doc.dialect);
  } catch {
    /* A profile that does not parse is caught by its own audit; here its
       absence from the set is the point. */
  }
}
const sameSet = (a, b) => JSON.stringify([...a].sort()) === JSON.stringify([...b].sort());
const dialectEnum = schema.properties?.dialect?.enum ?? [];
const engineEnum = schema.properties?.engine?.enum ?? [];
r.check(
  `dialect/engine are the closed set of profiles in contracts/dialects/ (${[...new Set(declaredByThemselves)].sort().join(', ')})`,
  sameSet(dialectEnum, declaredByThemselves) && sameSet(engineEnum, declaredByThemselves),
  { dialect: dialectEnum, engine: engineEnum, present },
);

let validate;
try {
  const Ajv2020 = webRequire('ajv/dist/2020').default;
  validate = new Ajv2020({ allErrors: true, strict: false }).compile(schema);
} catch (err) {
  console.error(`sqlite-error-codes: ${display(schemaPath)} does not compile: ${err.message}`);
  process.exit(1);
}
const schemaOK = validate(profile);
r.check(
  `${P} validates against ${display(schemaPath)}`,
  schemaOK,
  schemaOK ? undefined : (validate.errors ?? []).slice(0, 6).map((e) => `${e.instancePath || '/'} ${e.message}`),
);

/* ---------- error classification: order, closure and reachability ---------- */

const rules = Array.isArray(profile.error_classification) ? profile.error_classification : [];

function ruleMatches(rule, msg) {
  const pattern = String(rule?.pattern ?? '');
  switch (rule?.match) {
    case 'equals': return msg === pattern;
    case 'prefix': return msg.startsWith(pattern);
    default: return msg.includes(pattern);
  }
}

r.section('error classification');

const listedCodes = new Set(Array.isArray(schemaCodeEnum) ? schemaCodeEnum : closedCodes);
const badShape = [];
rules.forEach((rule, i) => {
  if (!MATCH_KINDS.includes(rule.match)) badShape.push(`rule ${i}: match ${JSON.stringify(rule.match)}`);
  if (typeof rule.pattern !== 'string') badShape.push(`rule ${i}: pattern is not a string`);
  if (typeof rule.note !== 'string' || rule.note.trim() === '') badShape.push(`rule ${i}: empty note`);
  if (!listedCodes.has(rule.code)) badShape.push(`rule ${i}: code ${JSON.stringify(rule.code)} is not in the closed enum`);
});
r.check(
  `${P}: every rule has a closed match kind, a string pattern, a contract code and a note (${rules.length} rules)`,
  rules.length > 0 && badShape.length === 0,
  badShape,
);

/* First match wins, so a later rule an earlier one always beats is dead. A
   rule is provably dead when an earlier rule subsumes it: an earlier
   `contains p` shadows any later rule whose own pattern contains p (and the
   empty contains is universal), and an earlier `prefix p` shadows a later
   `prefix q` with q starting with p. A genuinely broader later rule — say
   `contains "locked"` after `contains "is locked"` — is not flagged: it still
   fires on messages the earlier rule misses. */
const shadowed = [];
rules.forEach((rule, i) => {
  for (let j = 0; j < i; j++) {
    const earlier = rules[j];
    const dead =
      (earlier.match === rule.match && earlier.pattern === rule.pattern)
      || (earlier.match === 'contains' && (earlier.pattern === '' || String(rule.pattern).includes(earlier.pattern)))
      || (earlier.match === 'prefix' && rule.match === 'prefix' && String(rule.pattern).startsWith(earlier.pattern));
    if (dead) {
      shadowed.push(`rule ${i} (${JSON.stringify(rule.pattern)}, ${rule.match}) is dead behind rule ${j} (${JSON.stringify(earlier.pattern)}, ${earlier.match})`);
      break;
    }
  }
});
r.check(
  `${P}: the rules are ordered first-match-wins and no rule is dead behind an earlier, more general one`,
  shadowed.length === 0,
  shadowed,
);

const last = rules[rules.length - 1];
const lastPattern = typeof last?.pattern === 'string' ? last.pattern : null;
r.check(
  `${P}: the last rule is the catch-all (contains "")`,
  last?.match === 'contains' && lastPattern === '',
  last && `rule ${rules.length - 1}: ${last.match} ${JSON.stringify(last.pattern)}`,
);

const emitted = new Set(rules.map((rule) => rule.code));
const outsideEnum = [...emitted].filter((code) => !closedCodes.includes(code));
r.check(
  `${P}: every code the profile can emit is in the closed enum (${emitted.size} emitted of ${closedCodes.length})`,
  outsideEnum.length === 0,
  outsideEnum,
);

/* The codes a profile must NOT be able to emit, with the reason each is out of
   reach. They are not engine errors; a profile that named one would be
   claiming the engine produced something the executor or the grader produced. */
const UNREACHABLE = {
  EMPTY_QUERY: "the executor's blank-SQL check, before any statement reaches the engine",
  DIALECT_MISMATCH:
    'the dialect gate refuses the request before any statement reaches the engine; it is a product refusal, not an engine message',
  PERSISTENCE_FAILED: 'the store failed to persist a result; no engine message maps to it',
  EVALUATION_ERROR: 'the evaluator failed, not the engine',
};
const unreachable = closedCodes.filter((code) => !emitted.has(code));
const unnamed = unreachable.filter((code) => !UNREACHABLE[code]);
r.check(
  `${P}: every closed code is either emitted by a rule or named unreachable with a reason`,
  unnamed.length === 0,
  unnamed,
);

/* ---------- printed tables ---------- */

console.log('\nreachability (the closed QueryResult enum)');
const reachableWhy = {
  QUERY_SYNTAX_ERROR: 'parse-time SQLITE_ERROR messages (the syntax-error families)',
  QUERY_RUNTIME_ERROR: 'everything SQLite raises after parsing, including the catch-all',
  QUERY_TIMEOUT: 'SQLITE_BUSY / SQLITE_LOCKED / SQLITE_INTERRUPT — contention and the host deadline',
  RESULT_LIMIT_EXCEEDED: 'SQLITE_TOOBIG; the host row/byte caps share the code',
  ENVIRONMENT_UNAVAILABLE: 'the file, the disk or the host failed, not the learner',
};
const codeWidth = Math.max(...closedCodes.map((c) => c.length), 'code'.length);
const reachWidth = 'reachable'.length;
console.log(`  ${'code'.padEnd(codeWidth)}  ${'reachable'.padEnd(reachWidth)}  rules  why`);
for (const code of closedCodes) {
  const count = rules.filter((rule) => rule.code === code).length;
  const reachable = count > 0;
  const why = reachable ? reachableWhy[code] ?? '' : UNREACHABLE[code] ?? '';
  console.log(`  ${code.padEnd(codeWidth)}  ${(reachable ? 'yes' : 'NO').padEnd(reachWidth)}  ${String(count).padStart(5)}  ${why}`);
}

r.section('capabilities');
const capabilities = profile.capabilities && typeof profile.capabilities === 'object' ? profile.capabilities : {};
const capability_notes = profile.capability_notes && typeof profile.capability_notes === 'object' ? profile.capability_notes : {};
const capKeys = Object.keys(capabilities);
const noteKeys = Object.keys(capability_notes);
r.check(
  `${P}: every capability is a boolean (${capKeys.length} capabilities)`,
  capKeys.length > 0 && capKeys.every((k) => typeof capabilities[k] === 'boolean'),
  capKeys.filter((k) => typeof capabilities[k] !== 'boolean'),
);
r.check(
  `${P}: every capability key has a note (${capKeys.length} capabilities, ${noteKeys.length} notes)`,
  capKeys.every((k) => k in capability_notes),
  capKeys.filter((k) => !(k in capability_notes)),
);
r.check(
  `${P}: every note names a capability key`,
  noteKeys.every((k) => k in capabilities),
  noteKeys.filter((k) => !(k in capabilities)),
);
r.check(
  `${P}: every note is non-empty`,
  noteKeys.every((k) => typeof capability_notes[k] === 'string' && capability_notes[k].trim() !== ''),
  noteKeys.filter((k) => typeof capability_notes[k] !== 'string' || capability_notes[k].trim() === ''),
);

r.section('value encoding');
const ve = profile.value_encoding && typeof profile.value_encoding === 'object' ? profile.value_encoding : {};
const veProblems = [];
for (const { key, storage } of STORAGE_CLASSES) {
  const rule = ve[key];
  if (!rule || typeof rule !== 'object') { veProblems.push(`${storage}: no rule`); continue; }
  if (!JSON_KINDS.includes(rule.json)) veProblems.push(`${storage}: json kind ${JSON.stringify(rule.json)}`);
  if (typeof rule.rule !== 'string' || rule.rule.trim() === '') veProblems.push(`${storage}: no rule`);
  if (typeof rule.note !== 'string' || rule.note.trim() === '') veProblems.push(`${storage}: no note`);
}
r.check(
  `${P}: every storage class names a JSON kind, a rule and a note (${STORAGE_CLASSES.length} classes)`,
  veProblems.length === 0,
  veProblems,
);
console.log(`\n  ${'storage class'.padEnd(13)}  ${'json'.padEnd(6)}  rule`);
for (const { key, storage } of STORAGE_CLASSES) {
  const rule = ve[key] ?? {};
  console.log(`  ${storage.padEnd(13)}  ${String(rule.json ?? '?').padEnd(6)}  ${rule.rule ?? '(missing)'}`);
}

r.section('affinity');
const types = profile.types && typeof profile.types === 'object' ? profile.types : {};
const affinityRules = Array.isArray(types.affinity_rules) ? types.affinity_rules : [];
const ruleClasses = affinityRules.map((rule) => rule.affinity);
const coverageProblems = [];
for (const cls of AFFINITIES) {
  const count = ruleClasses.filter((c) => c === cls).length;
  if (count !== 1) coverageProblems.push(`${cls}: named by ${count} rules`);
}
for (const cls of ruleClasses) if (!AFFINITIES.includes(cls)) coverageProblems.push(`${JSON.stringify(cls)}: not an affinity class`);
r.check(
  `${P}: the affinity rules name each of the five classes exactly once (${affinityRules.length} rules)`,
  coverageProblems.length === 0,
  coverageProblems,
);
const declared = types.declared && typeof types.declared === 'object' ? types.declared : {};
const declaredKeys = Object.keys(declared);
const declaredProblems = declaredKeys.filter((k) => !AFFINITIES.includes(declared[k]));
r.check(
  `${P}: every declared type maps to exactly one affinity class (${declaredKeys.length} declared types)`,
  declaredKeys.length > 0 && declaredProblems.length === 0,
  declaredProblems.map((k) => `${k} -> ${JSON.stringify(declared[k])}`),
);
console.log(`\n  ${'affinity'.padEnd(8)}  selected when the declared type`);
for (const rule of affinityRules) {
  console.log(`  ${String(rule.affinity ?? '?').padEnd(8)}  ${rule.when ?? '(missing)'}`);
}
console.log(`\n  declared: ${declaredKeys.map((k) => `${k}=${declared[k]}`).join('  ')}`);

/* ---------- the browser reader ---------- */

r.section('the browser reader (src/api/sqliteProfile.ts + src/api/sqliteFraming.ts)');

/* The frames the shipped build produces, verbatim — measured across every error
   case in the M6 spike (`/tmp/sqlite-spike/PROBE2.json`) and re-measured in the
   page after the adapter landed. Each row is [driver message, SQLite's own
   sentence, the code the profile must return].

   The middle column is the whole point of `sqliteUnframe`: the profile's rules
   are written for the sentence, not for the driver's rendering of it. If a
   dependency bump changes the frame, `incomplete input` and
   `unrecognized token:` stop matching and every syntax error is reported as a
   runtime error — which is a defect this suite caught once, in the Go half,
   before either adapter shipped. */
const FRAMES = [
  ['SQLITE_ERROR: sqlite3 result code 1: incomplete input', 'incomplete input', 'QUERY_SYNTAX_ERROR'],
  ['SQLITE_ERROR: sqlite3 result code 1: near "SELEC": syntax error', 'near "SELEC": syntax error', 'QUERY_SYNTAX_ERROR'],
  ['SQLITE_ERROR: sqlite3 result code 1: unrecognized token: "\'abc"', 'unrecognized token: "\'abc"', 'QUERY_SYNTAX_ERROR'],
  ['SQLITE_ERROR: sqlite3 result code 1: no such table: nope', 'no such table: nope', 'QUERY_RUNTIME_ERROR'],
  ['SQLITE_ERROR: sqlite3 result code 1: no such column: nope', 'no such column: nope', 'QUERY_RUNTIME_ERROR'],
  ['SQLITE_CONSTRAINT_UNIQUE: sqlite3 result code 2067: UNIQUE constraint failed: t.id', 'UNIQUE constraint failed: t.id', 'QUERY_RUNTIME_ERROR'],
  ['SQLITE_CONSTRAINT_NOTNULL: sqlite3 result code 1299: NOT NULL constraint failed: t.name', 'NOT NULL constraint failed: t.name', 'QUERY_RUNTIME_ERROR'],
  ['SQLITE_READONLY: sqlite3 result code 8: attempt to write a readonly database', 'attempt to write a readonly database', 'QUERY_RUNTIME_ERROR'],
  ['SQLITE_INTERRUPT: sqlite3 result code 9: interrupted', 'interrupted', 'QUERY_TIMEOUT'],
  ['SQLITE_BUSY: sqlite3 result code 5: database is locked', 'database is locked', 'QUERY_TIMEOUT'],
  ['SQLITE_IOERR: sqlite3 result code 10: disk I/O error', 'disk I/O error', 'ENVIRONMENT_UNAVAILABLE'],
  ['SQLITE_TOOBIG: sqlite3 result code 18: string or blob too big', 'string or blob too big', 'RESULT_LIMIT_EXCEEDED'],
];

const unframeProblems = [];
const codeProblems = [];
for (const [framed, sentence, code] of FRAMES) {
  const got = browser.sqliteUnframe(framed);
  if (got !== sentence) unframeProblems.push(`${framed}\n      -> ${JSON.stringify(got)}, want ${JSON.stringify(sentence)}`);
  const gotCode = browser.sqliteErrorCode(framed);
  if (gotCode !== code) codeProblems.push(`${framed} -> ${gotCode}, want ${code}`);
}
r.check(
  `the adapter strips this build's frame and keeps SQLite's own sentence (${FRAMES.length} measured frames)`,
  unframeProblems.length === 0,
  unframeProblems,
);
r.check(
  `every framed message classifies to the profile's code (${FRAMES.length} cases)`,
  codeProblems.length === 0,
  codeProblems,
);

/* A message the driver did not frame is handed on untouched: reporting the
   driver's own words beats reporting a sentence the adapter guessed at. */
const UNFRAMED = ['no such table: nope', 'incomplete input', 'interrupted', "near \"x\": syntax error"];
r.check(
  'a message this build did not write is returned unchanged',
  UNFRAMED.every((m) => browser.sqliteUnframe(m) === m),
  UNFRAMED.map((m) => `${m} -> ${browser.sqliteUnframe(m)}`).filter((_, i) => browser.sqliteUnframe(UNFRAMED[i]) !== UNFRAMED[i]),
);

/* The reader reads the profile's rules in order rather than keeping a copy: a
   rule added to the contract and not seen here is a reader that has drifted. */
const readerRules = browserProfile.sqliteErrorRules();
const profileRules = Array.isArray(profile.error_classification) ? profile.error_classification : [];
const drifted = profileRules.filter((rule, i) => {
  const seen = readerRules[i];
  return !seen || seen.match !== rule.match || seen.pattern !== rule.pattern || seen.code !== rule.code;
});
r.check(
  `the browser reader applies the profile's rules in order (${readerRules.length} rules)`,
  readerRules.length === profileRules.length && drifted.length === 0,
  drifted.map((rule) => `${rule.match} ${JSON.stringify(rule.pattern)}`),
);

/* The column label rule: the declared type verbatim, and the profile's own word
   when the engine has no declared type (an expression). The label is what the
   parity audit compares against the gateway's DatabaseTypeName, so it is pinned
   against the profile rather than against a literal here. */
const fallbackLabel = profile.value_encoding?.column_type_label?.fallback;
const labelProblems = [];
if (browserProfile.sqliteColumnTypeLabel(null) !== fallbackLabel) labelProblems.push(`null -> ${browserProfile.sqliteColumnTypeLabel(null)}, want ${fallbackLabel}`);
if (browserProfile.sqliteColumnTypeLabel('') !== fallbackLabel) labelProblems.push(`empty -> ${browserProfile.sqliteColumnTypeLabel('')}, want ${fallbackLabel}`);
for (const declared of ['INTEGER', 'TEXT', 'REAL', 'NUMERIC', 'DATE', 'TIMESTAMP', 'BOOLEAN']) {
  if (browserProfile.sqliteColumnTypeLabel(declared) !== declared) labelProblems.push(`${declared} -> ${browserProfile.sqliteColumnTypeLabel(declared)}`);
}
r.check(
  `the declared type is reported verbatim and an expression is ${JSON.stringify(fallbackLabel)}`,
  labelProblems.length === 0,
  labelProblems,
);

/* Value encoding, by storage class. The declared affinity of the column is
   deliberately not an input: a TEXT value in an INTEGER-affinity column is
   reported as text, because that is what the engine stored. */
const ENCODING = [
  ['null', null, null],
  ['integer', 42, 42],
  ['integer past 2^53-1', 2n ** 60n, '1152921504606846976'],
  ['real', 2.5, 2.5],
  ['real NaN (stored as NULL)', Number.NaN, null],
  ['real +Infinity', Number.POSITIVE_INFINITY, 'Infinity'],
  ['text that looks numeric', '007', '007'],
  ['blob', new Uint8Array([0, 255]), '\\x00ff'],
  ['boolean keyword', true, 1],
];
const encodingProblems = [];
for (const [what, input, want] of ENCODING) {
  const got = browserProfile.encodeSqliteValue(input);
  if (got !== want) encodingProblems.push(`${what}: ${JSON.stringify(got)} (${typeof got}), want ${JSON.stringify(want)}`);
}
r.check(
  `every storage class encodes by the profile's rule (${ENCODING.length} cases)`,
  encodingProblems.length === 0,
  encodingProblems,
);

/* Affinity, from the profile's ordered rules — including the two that are easy
   to lose: a declared type matching no rule is NUMERIC, and a column with *no*
   declared type is BLOB, which is a different answer. */
const AFFINITY_CASES = [
  ['INTEGER', 'INTEGER'],
  ['BIGINT', 'INTEGER'],
  ['VARCHAR(20)', 'TEXT'],
  ['CLOB', 'TEXT'],
  ['BLOB', 'BLOB'],
  ['', 'BLOB'],
  ['REAL', 'REAL'],
  ['DOUBLE PRECISION', 'REAL'],
  /* The trap in the ordered rules: "FLOATING POINT" contains "INT" (in POINT),
     so the INTEGER rule fires before the REAL rule and the column has INTEGER
     affinity. Measured on the wasm build and on Python's SQLite 3.31: a value
     written as 1.0 into a `FLOATING POINT` column reads back as the integer 1.
     A reader that matched REAL first would report a value the engine never
     stored, so this case exists to keep the order pinned. */
  ['FLOATING POINT', 'INTEGER'],
  ['NUMERIC', 'NUMERIC'],
  ['DECIMAL(10,2)', 'NUMERIC'],
  ['DATE', 'NUMERIC'],
  ['TIMESTAMP', 'NUMERIC'],
  ['BOOLEAN', 'NUMERIC'],
];
const affinityProblems = [];
for (const [declared, want] of AFFINITY_CASES) {
  const got = browserProfile.affinityFor(declared);
  if (got !== want) affinityProblems.push(`${JSON.stringify(declared)} -> ${got}, want ${want}`);
}
r.check(
  `affinity follows the profile's order, including NUMERIC vs BLOB (${AFFINITY_CASES.length} declared types)`,
  affinityProblems.length === 0,
  affinityProblems,
);

r.finish();
