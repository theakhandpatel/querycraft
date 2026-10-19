/*
 * Evaluate-wire audit — the key the HTTP adapter and the Go evaluator share.
 *
 * Item 17a. The gateway decodes the exercise's result contract from
 * `result_contract` (backend/internal/eval/eval.go). The web adapter sent
 * `contract`, which the Go decoder reads as absent, so the released binary
 * graded every submission against the zero contract: column order, row order,
 * duplicates and numeric tolerance were all ignored. The page engine never
 * sends anything, so only the two shipping sources below decide the wire.
 *
 * This audit holds the wire, not the grading. It reads the same two files the
 * compiler and the server read, and fails if either side renames its half:
 *
 *   - `checkAnswer` sends `result_contract:` and `reference_solution:`;
 *   - the http adapter holds no bare `contract:` key anywhere;
 *   - `eval.Exercise.Contract` is tagged `json:"result_contract"`.
 *
 *   make audit-evaluate-wire   # node scripts/evaluate-wire-audit.mjs
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const httpPath = path.join(root, 'web', 'src', 'api', 'execution', 'http.ts');
const evalPath = path.join(root, 'backend', 'internal', 'eval', 'eval.go');

const http = fs.readFileSync(httpPath, 'utf8');
const go = fs.readFileSync(evalPath, 'utf8');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — ' + detail}`);
  }
}

/* The checkAnswer body, from its key to the next service key. Reading only the
   body keeps a `contract:` in some other operation from being mistaken for the
   grading field, and keeps a stray one inside it from hiding. */
const start = http.indexOf('checkAnswer:');
const end = http.indexOf('getTables:', start);
const body = start === -1 ? '' : http.slice(start, end === -1 ? undefined : end);

check('checkAnswer exists to audit', start !== -1, 'no `checkAnswer:` in web/src/api/execution/http.ts');

check(
  'checkAnswer sends `result_contract:`',
  /\bresult_contract\s*:/.test(body),
  'the body no longer names result_contract — the evaluator decodes its contract as absent'
);

check(
  'checkAnswer sends `reference_solution:`',
  /\breference_solution\s*:/.test(body),
  'the body no longer names reference_solution — the gateway cannot derive the expected rows'
);

/* `result_contract:` ends in `contract:`, so the boundary must reject `_`.
   Comments are stripped first: prose may explain the bug by naming the key,
   and only a real object key can mislead the decoder. */
const httpCode = http.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '');
check(
  'the http adapter has no bare `contract:` key',
  !/(^|[^A-Za-z0-9_])contract\s*:/.test(httpCode),
  'a bare `contract:` key is back; the Go evaluator ignores it and grades the zero contract'
);

const tag = go.match(/\bContract\s+[\w.]+\s+`json:"([^"]*)"`/);
check(
  'eval.Exercise.Contract is tagged `result_contract`',
  !!tag && tag[1] === 'result_contract',
  tag ? `the tag is json:"${tag[1]}"` : 'no Contract field with a json tag in backend/internal/eval/eval.go'
);

console.log(failed === 0 ? '\nevaluate-wire-audit: OK' : `\nevaluate-wire-audit: ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
