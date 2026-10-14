/*
 * Offline-store audit (design 8.1 / M2 step 2).
 *
 * Three things the exit gate asks about, decided by a script rather than a
 * reviewer:
 *
 *   1. the store layout matches 8.1, by name, at version 2;
 *   2. one progress snapshot splits across those stores without loss, and reads
 *      back whole (attempts, evaluations, notes, bookmarks, saved queries, meta);
 *   3. a write that cannot land rejects with PERSISTENCE_UNAVAILABLE instead of
 *      resolving { ok: true } -- the "it's a mock" failure this guards against.
 *
 *   make audit-storage
 *   node scripts/storage-audit.mjs
 *
 * No browser and no extra dependency: the failure case is the real one this
 * environment has (no IndexedDB), and it is exactly the branch the toast relies
 * on.
 */
import { createRequire } from 'node:module';
const require = createRequire('/tmp/');
const idb = require('/tmp/idb.cjs');
const bundle = require('/tmp/offline-bundle.cjs');

const WANT_STORES = ['attempts', 'evaluations', 'progress', 'notes', 'bookmarks', 'saved_queries', 'drafts', 'meta'];
const problems = [];
const check = (name, ok, detail) => {
  if (!ok) problems.push(`${name}${detail !== undefined ? ` (${detail})` : ''}`);
};

/* 1. Layout. */
check('idb version is 2', idb.IDB_VERSION === 2, `got ${idb.IDB_VERSION}`);
check(
  'store layout matches 8.1',
  JSON.stringify(idb.OFFLINE_STORES) === JSON.stringify(WANT_STORES),
  JSON.stringify(idb.OFFLINE_STORES),
);

/* 2. Split without loss, then read back. */
const progress = {
  version: 5,
  seeded: true,
  events: [
    { ts: 100, id: 'ex.1.1', attemptId: 'att_1', kind: 'failed', title: 'First try', course: 'sql-analytics', lesson: 1 },
    { ts: 200, id: 'ex.1.1', attemptId: 'att_2', kind: 'passed', title: 'Passed', course: 'sql-analytics', lesson: 1, datasetVersion: 3, exerciseVersion: 2 },
    { ts: 300, id: 'ex.1.1', attemptId: 'att_3', kind: 'hint', title: 'Hint viewed', course: 'sql-analytics', lesson: 1 },
  ],
  passed: { 'ex.1.1': true },
  accepted: {},
  firstTry: {},
  done: { m1: true },
  notes: [
    { course: 'sql-analytics', lesson: 1, kind: 'note', ts: 10, text: 'remember the grain' },
    { course: 'sql-analytics', lesson: 2, kind: 'bookmark', ts: 11, text: 'window functions' },
  ],
  saved: [{ course: 'sql-analytics', lesson: 1, name: 'Top customers' }],
  attempts: 3,
  attemptsByDay: new Array(7).fill(0),
  timeMs: { 'sql-analytics': 1000 },
};

const rows = bundle.bundleFromProgress(progress, {
  courseId: 'sql-analytics',
  datasetVersion: 3,
  exportFormatVersion: 1,
  now: new Date('2026-10-14T00:00:00Z'),
});
const byStore = (store) => rows.filter((r) => r.store === store);

check('the snapshot reads back whole', JSON.stringify(byStore('progress')[0].value) === JSON.stringify(progress));
check('attempts hold every event', byStore('attempts').length === 3, byStore('attempts').length);
check('attempt keys are the client request ids', byStore('attempts').map((r) => r.key).join(',') === 'att_1,att_2,att_3');
check('attempt keys are unique', new Set(byStore('attempts').map((r) => r.key)).size === byStore('attempts').length);
check('attempts keep the exercise id', byStore('attempts').every((r) => r.value.id === 'ex.1.1'));
check('evaluations only for graded attempts', byStore('evaluations').length === 2, byStore('evaluations').length);
check('evaluation links its attempt', byStore('evaluations').every((r) => r.value.attempt_id === r.key));
check('version stamps survive', byStore('evaluations').some((r) => r.value.dataset_version === 3 && r.value.exercise_version === 2));
check('notes and bookmarks are split', byStore('notes').length === 1 && byStore('bookmarks').length === 1);
check('saved queries are stored', byStore('saved_queries').length === 1);
check(
  'meta counts match the snapshot',
  JSON.stringify(byStore('meta')[0].value.counts) ===
    JSON.stringify({ attempts: 3, evaluations: 2, notes: 1, bookmarks: 1, saved_queries: 1 }),
  JSON.stringify(byStore('meta')[0].value.counts),
);
check('meta carries the versions', byStore('meta')[0].value.dataset_version === 3 && byStore('meta')[0].value.export_format_version === 1);
check('no row lands in an unknown store', rows.every((r) => WANT_STORES.includes(r.store)));

/* Every 8.1 store except `drafts` (written per keystroke, not per snapshot) is
   populated by the split, so the layout is not merely declared. */
const touched = new Set(rows.map((r) => r.store));
for (const store of WANT_STORES) {
  if (store === 'drafts') continue;
  check(`split populates ${store}`, touched.has(store));
}

/* 3. A write that cannot land must be typed, never swallowed. */
/* Events from before §8.2 have no request id; they must still get distinct
   keys, or two real attempts collapse into one row. */
const legacy = {
  ...progress,
  events: [
    { ts: 1, id: 'ex.1.1', kind: 'failed', title: 'a', course: 'sql-analytics', lesson: 1 },
    { ts: 2, id: 'ex.1.1', kind: 'passed', title: 'b', course: 'sql-analytics', lesson: 1 },
  ],
};
const legacyRows = bundle.bundleFromProgress(legacy, { courseId: 'sql-analytics', datasetVersion: 3, exportFormatVersion: 1 });
const legacyAttempts = legacyRows.filter((r) => r.store === 'attempts');
check('legacy attempts still get unique keys', new Set(legacyAttempts.map((r) => r.key)).size === 2, legacyAttempts.map((r) => r.key).join(','),);

globalThis.indexedDB = undefined;
let putCode = null;
try {
  await idb.idbPut(idb.STORE_PROGRESS, 'x', 1);
} catch (e) {
  putCode = e && e.code;
}
check('a blocked write rejects PERSISTENCE_UNAVAILABLE', putCode === 'PERSISTENCE_UNAVAILABLE', String(putCode));

let manyCode = null;
try {
  await idb.idbPutMany([[idb.STORE_META, 'workspace', { ok: true }]]);
} catch (e) {
  manyCode = e && e.code;
}
check('a blocked bundle write is typed too', manyCode === 'PERSISTENCE_UNAVAILABLE', String(manyCode));

console.log(`stores: ${WANT_STORES.length}  rows: ${rows.length}  problems: ${problems.length}`);
for (const p of problems) console.log(`  FAIL ${p}`);
if (problems.length) process.exitCode = 1;
