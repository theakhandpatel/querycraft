/*
 * Result-contract fixture audit (technical design §6.3).
 *
 * The evaluator exists twice — JavaScript (web/src/api/compare.ts) and Go
 * (backend/internal/eval) — and both are judged against the same
 * language-neutral files in contracts/fixtures/. This is the JavaScript half:
 * it bundles the comparator and asserts every fixture's expected_outcome
 * against it. The Go half is backend/internal/eval/fixtures_test.go.
 *
 * Neither harness hardcodes an expected value; the only shared literal is the
 * path to the fixture directory.
 *
 *   make audit-fixtures   # bundles src/api/compare.ts and runs this
 *   node scripts/fixtures-audit.mjs
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';

const require = createRequire('/tmp/');
const { compareResult, compareColumns } = require('/tmp/fixtures.cjs');

const here = path.dirname(fileURLToPath(import.meta.url));
const fixturesDir = path.resolve(here, '..', 'contracts', 'fixtures');

// Required keys of a fixture file; `note` is the only optional addition.
const REQUIRED_KEYS = ['contract', 'actual_result', 'expected_outcome'];

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

// expected_outcome.codes is an order- and duplicate-insensitive set.
function codeSet(codes) {
  return [...new Set((codes || []).map(String))].sort();
}

// Mirror of the Go harness: a failed execution is an execution failure, not a
// wrong answer, so it never reaches the comparator (§6.1 rule 1). An absent
// `contract.rows` means a columns-only check, matching the Go evaluator.
function evaluateFixture(fx) {
  const actual = fx.actual_result || {};
  const status = actual.status || 'succeeded';
  const actualResult = { columns: actual.columns || [], rows: actual.rows || [] };
  if (status !== 'succeeded') {
    return { passed: false, codes: [] };
  }
  const contract = fx.contract || {};
  if (contract.rows == null) {
    const cmp = compareColumns(contract, actualResult);
    return { passed: cmp.ok, codes: cmp.codes };
  }
  const expected = { columns: contract.columns || [], rows: contract.rows };
  const cmp = compareResult(expected, contract, actualResult);
  return { passed: cmp.ok, codes: cmp.codes };
}

// perf-*.json is a different contract family (a PerfContract graded against a
// PlanBundle); it is read by web/scripts/plan-conformance-audit.mjs and
// backend/internal/perf, and it has no rows for this comparator to compare.
const files = fs
  .readdirSync(fixturesDir)
  .filter((name) => name.endsWith('.json') && !name.startsWith('perf-'))
  .sort();

if (files.length < 20) {
  console.log(`fixtures-audit: expected at least 20 fixtures in ${fixturesDir}, found ${files.length}`);
  process.exit(1);
}

console.log(`fixtures: ${files.length} in contracts/fixtures`);

for (const file of files) {
  const fx = JSON.parse(fs.readFileSync(path.join(fixturesDir, file), 'utf8'));

  // Every fixture object has exactly the documented keys.
  const keys = Object.keys(fx);
  const shapeOK =
    REQUIRED_KEYS.every((k) => keys.includes(k)) &&
    keys.every((k) => k === 'note' || REQUIRED_KEYS.includes(k));
  check(`${file} shape`, shapeOK, keys);

  const got = evaluateFixture(fx);
  const want = { passed: !!fx.expected_outcome.passed, codes: codeSet(fx.expected_outcome.codes) };
  check(
    `${file} verdict`,
    got.passed === want.passed && JSON.stringify(codeSet(got.codes)) === JSON.stringify(want.codes),
    { passed: got.passed, codes: codeSet(got.codes), want },
  );
}

if (failed) {
  console.log(`\nfixtures-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nfixtures-audit: OK (${files.length} fixtures)`);
