/*
 * Plan-contract conformance audit (M5 exit gate, design §14).
 *
 * `contracts/plan.schema.json` and `contracts/perf-contract.schema.json` are
 * frozen, language-neutral contracts: the Go engine writes a `PlanBundle` and
 * the browser writes the same shape, and the perf grader reads a structural
 * contract. This audit is what keeps the pinned fixtures honest:
 *
 *   - every `contracts/fixtures/perf-*.json` bundle validates against the plan
 *     schema,
 *   - every fixture's perf contract validates against the perf contract schema,
 *   - and no fixture *asserts* on a timing. A plan may report `actual_time_ms`
 *     (the exit gate requires that it does), but a fixture that grades one is
 *     exactly the lottery M5 forbids, so a timing key outside the bundle fails
 *     the audit.
 *
 * The fixtures are authored by the backend/perf track. If none exist yet this
 * suite fails loudly rather than passing vacuously — an empty directory is the
 * one result that would prove nothing.
 *
 *   cd web && npm run audit-perf
 *   node scripts/plan-conformance-audit.mjs
 */
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
/* ajv lives in web/node_modules; a draft-2020-12 schema needs the 2020 build. */
const Ajv2020 = require('ajv/dist/2020').default;

const here = path.dirname(fileURLToPath(import.meta.url));
const contractsDir = path.resolve(here, '..', '..', 'contracts');
const fixturesDir = path.join(contractsDir, 'fixtures');

/* The app's own two functions, bundled the same way the other web audits bundle
   their subject, so this file grades with the code the browser runs rather than
   a second implementation that could drift from it. */
const planContractBundle = path.join(os.tmpdir(), 'qc-plan-contract.cjs');
execFileSync(
  path.join(here, '..', 'node_modules', '.bin', 'esbuild'),
  ['--bundle', '--format=cjs', '--platform=node', `--outfile=${planContractBundle}`,
   path.join(here, '..', 'src', 'api', 'planContract.ts')],
  { stdio: ['ignore', 'ignore', 'inherit'] },
);
const { summarizePlan, gradePlan } = require(planContractBundle);

/** The first path where two derived summaries disagree, for a readable failure. */
function firstDifference(got, want, base = '') {
  if (got === want) return null;
  if (Array.isArray(got) && Array.isArray(want)) {
    if (got.length !== want.length) return `${base || '(root)'} has ${got.length} entries, want ${want.length}`;
    for (let i = 0; i < got.length; i++) {
      const at = firstDifference(got[i], want[i], `${base}[${i}]`);
      if (at !== null) return at;
    }
    return null;
  }
  if (isRecord(got) && isRecord(want)) {
    for (const key of new Set([...Object.keys(got), ...Object.keys(want)])) {
      const at = firstDifference(got[key], want[key], base ? `${base}.${key}` : key);
      if (at !== null) return at;
    }
    return null;
  }
  return `${base || '(root)'}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`;
}

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

function readSchema(file) {
  return JSON.parse(fs.readFileSync(path.join(contractsDir, file), 'utf8'));
}

function compile(schema) {
  /* One Ajv instance per schema: both files carry an $id and compiling them
     into one instance makes ajv refuse the second as a duplicate. */
  return new Ajv2020({ allErrors: true, strict: false }).compile(schema);
}

function formatErrors(errors) {
  return (errors ?? []).map((e) => `${e.instancePath || '(root)'} ${e.message}`).join('; ');
}

const validatePlan = compile(readSchema('plan.schema.json'));
const validatePerf = compile(readSchema('perf-contract.schema.json'));

/* ---------- fixture shape ---------- */

const BUNDLE_KEYS = ['bundle', 'actual_plan', 'plan_bundle', 'plan'];
const CONTRACT_KEYS = ['contract', 'perf', 'perf_contract'];

function isRecord(v) {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/** The value that is a PlanBundle: a named key, or the fixture body itself. */
function findBundle(fx) {
  for (const key of BUNDLE_KEYS) {
    if (isRecord(fx[key]) && isRecord(fx[key].summary) && 'timing_precision' in fx[key]) return fx[key];
  }
  if (isRecord(fx.summary) && 'timing_precision' in fx) return fx;
  return null;
}

function findContract(fx) {
  for (const key of CONTRACT_KEYS) if (isRecord(fx[key])) return fx[key];
  return null;
}

/* ---------- the no-timing rule ---------- */

/** Keys that name a duration. A fixture may not declare one outside the
    reported bundle: a graded assertion over a timing is not reproducible. */
const TIMING_KEY = /(^|_)(time|times|timing|duration|ms|millis|milliseconds)(_|$)/i;
/* Prose fields, not assertions. A `note` may say "this used to be slow". */
const PROSE_KEYS = new Set(['note', '$comment', 'description']);

function timingPaths(value, base = '', acc = []) {
  if (Array.isArray(value)) {
    value.forEach((v, i) => timingPaths(v, `${base}[${i}]`, acc));
    return acc;
  }
  if (!isRecord(value)) return acc;
  for (const [key, v] of Object.entries(value)) {
    if (PROSE_KEYS.has(key)) continue;
    const at = base ? `${base}.${key}` : key;
    if (TIMING_KEY.test(key)) acc.push(at);
    timingPaths(v, at, acc);
  }
  return acc;
}

/* ---------- run ---------- */

let files;
try {
  files = fs
    .readdirSync(fixturesDir)
    .filter((name) => name.startsWith('perf-') && name.endsWith('.json'))
    .sort();
} catch {
  files = [];
}

if (files.length === 0) {
  console.log(`plan-conformance-audit: fixtures not present yet — no contracts/fixtures/perf-*.json in ${fixturesDir}.`);
  console.log('  The M5 plan fixtures are authored by the backend/perf track; this audit is green only once they land.');
  process.exit(1);
}

console.log(`perf fixtures: ${files.length} in contracts/fixtures`);

for (const file of files) {
  const fx = JSON.parse(fs.readFileSync(path.join(fixturesDir, file), 'utf8'));

  const bundle = findBundle(fx);
  check(`${file} carries a PlanBundle`, bundle !== null, Object.keys(fx));
  if (bundle) {
    const ok = validatePlan(bundle);
    check(`${file} bundle validates against plan.schema.json`, ok, formatErrors(validatePlan.errors));
  }

  const contract = findContract(fx);
  check(`${file} carries a PerfContract`, contract !== null, Object.keys(fx));
  if (contract) {
    const ok = validatePerf(contract);
    check(`${file} contract validates against perf-contract.schema.json`, ok, formatErrors(validatePerf.errors));
  }

  /* The summary is the only thing the graders read, so a fixture whose stored
     summary is not what the walk of its own plan derives is a fixture that
     proves nothing: it would pin a number the app never produces. Re-deriving
     it here is what keeps a capture honest across both languages — the walk
     skips a relationless Bitmap Index Scan unless the parent's relation is read
     back into it, and that is the difference this check is looking for. */
  if (bundle) {
    const derived = summarizePlan(bundle.plan);
    const difference = firstDifference(derived, bundle.summary);
    check(`${file} summary is the walk of its own plan`, difference === null, difference);
  }

  /* And the pins themselves: both languages must reach the verdict the fixture
     declares, from the same document. */
  if (bundle && contract) {
    const verdict = gradePlan(bundle, contract);
    const codes = verdict.checks.filter((c) => !c.ok).map((c) => c.code).sort();
    const want = (fx.expected_outcome?.failures ?? []).slice().sort();
    const expected = fx.expected_outcome?.passed;
    check(
      `${file} grades to the pinned verdict (${expected ? 'passed' : 'failed'})`,
      verdict.passed === expected && JSON.stringify(codes) === JSON.stringify(want),
      { passed: verdict.passed, codes, want },
    );
  }

  /* The entire fixture, minus the reported bundle, is the assertion surface.
     No timing may appear there. */
  const outside = { ...fx };
  for (const key of BUNDLE_KEYS) {
    if (outside[key] === bundle) delete outside[key];
  }
  const offends = timingPaths(outside);
  check(`${file} asserts on no timing field`, offends.length === 0, offends);
}

if (failed) {
  console.log(`\nplan-conformance-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nplan-conformance-audit: OK (${files.length} fixture${files.length === 1 ? "" : "s"})`);
