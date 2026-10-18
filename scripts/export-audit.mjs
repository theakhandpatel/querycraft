/*
 * Export-bundle audit (technical design §8.3).
 *
 * The export is the learner's only copy of their own work, and the documented
 * migration path between browser and server mode. These checks pin the contract
 * the document states: a single JSON document with a `format_version`, the
 * content versions the data was produced against, every workbook entity, and
 * no credentials.
 *
 *   make audit-export          # bundles src/lib/exportBundle.ts and runs this
 *   node scripts/export-audit.mjs
 */
import { createRequire } from 'node:module';
const require = createRequire('/tmp/');
const x = require('/tmp/export-bundle-for-export.cjs');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

const progress = {
  version: 5,
  seeded: false,
  events: [{ ts: 1791960635578, id: 'ex.1.1', kind: 'passed', title: 'Exercise 1.1', course: 'sql-analytics', lesson: 0 }],
  passed: { 'ex.1.1': true },
  accepted: {},
  firstTry: { 'ex.1.1': true },
  done: { m1: true },
  notes: [{ course: 'sql-analytics', lesson: 0, kind: 'note', ts: 1, text: 'grain matters' }],
  saved: [{ course: 'sql-analytics', lesson: 0, name: 'revenue by month' }],
  attempts: 1,
  attemptsByDay: [0, 0, 0, 0, 0, 0, 1],
  timeMs: { 'sql-analytics': 180000 },
};

console.log(`format version: ${x.EXPORT_FORMAT_VERSION}`);
check('EXPORT_FORMAT_VERSION is a positive integer', Number.isInteger(x.EXPORT_FORMAT_VERSION) && x.EXPORT_FORMAT_VERSION >= 1, x.EXPORT_FORMAT_VERSION);

const at = new Date('2026-10-14T09:00:00.000Z');
const bundle = x.buildExportBundle(progress, { 'ex.1.1': 'SELECT 1' }, 1, at);

console.log('document shape');
check('carries a format_version', bundle.format_version === x.EXPORT_FORMAT_VERSION, bundle.format_version);
check('carries an ISO exported_at', bundle.exported_at === '2026-10-14T09:00:00.000Z', bundle.exported_at);
check('carries the dataset_version', bundle.content_versions.dataset_version === 1, bundle.content_versions);
check('carries the progress version the data was written at', bundle.content_versions.progress_version === 5, bundle.content_versions);

console.log('workbook entities');
check('keeps every attempt event', bundle.progress.events.length === 1);
check('keeps passes', bundle.progress.passed['ex.1.1'] === true);
check('keeps chapter completion', bundle.progress.done.m1 === true);
check('keeps notes', bundle.progress.notes.length === 1);
check('keeps saved queries', bundle.progress.saved.length === 1);
check('keeps measured study time', bundle.progress.timeMs['sql-analytics'] === 180000);
check('keeps editor drafts, keyed by cell id', bundle.drafts['ex.1.1'] === 'SELECT 1', bundle.drafts);

console.log('serialization');
const text = JSON.stringify(bundle);
const round = JSON.parse(text);
check('round-trips through JSON without loss', JSON.stringify(round) === text);
check('is a single document, not a stream', text.startsWith('{') && text.endsWith('}'));
for (const forbidden of ['token', 'password', 'hash', 'authorization', 'bearer', 'secret']) {
  check(`never contains the word "${forbidden}"`, !new RegExp(forbidden, 'i').test(text));
}

console.log('filename');
check('names the file by date', x.exportFilename(at) === 'querycraft-export-2026-10-14.json', x.exportFilename(at));

console.log('draft collection');
const fakeStorage = (() => {
  const map = new Map([
    ['qc.draft.ex.1.1', 'SELECT 1'],
    ['qc.draft.ex.1.2', ''],
    ['qc.state', '{"selectedCourseId":"sql-analytics"}'],
    ['qc.progress', '{}'],
  ]);
  return {
    get length() { return map.size; },
    key: (i) => [...map.keys()][i] ?? null,
    getItem: (k) => (map.has(k) ? map.get(k) : null),
  };
})();
const drafts = x.collectDrafts(fakeStorage);
check('collects only draft cells', Object.keys(drafts).length === 1 && drafts['ex.1.1'] === 'SELECT 1', drafts);
check('skips an empty draft', drafts['ex.1.2'] === undefined);
check('skips non-draft state', drafts.state === undefined && drafts.progress === undefined);
check('a missing storage is not an error', JSON.stringify(x.collectDrafts(null)) === '{}');

if (failed) {
  console.log(`\nexport-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log('\nexport-audit: OK');
