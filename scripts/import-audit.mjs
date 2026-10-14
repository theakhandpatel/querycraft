/*
 * Import round-trip audit (M2 step 7, technical-design.md 8.2/8.3).
 *
 * Import is the second half of the export, and the second M2 exit gate: an
 * export and an import have to round-trip without loss. These checks pin four
 * properties, all of them on the pure modules so they hold in every adapter:
 *
 *   1. Validate before writing. A document is refused loudly — bad JSON, an
 *      unknown format version, a shape the schema rejects, a stray credential
 *      field — and nothing is applied when it is refused.
 *   2. Merge, never replace. An existing note is never deleted, an attempt with
 *      a request id already present is never duplicated, and re-importing the
 *      same file adds nothing.
 *   3. Round trip. export -> import into an empty workspace -> export produces
 *      the same document, apart from `exported_at`.
 *   4. Sign-in and adapter independence. Import takes a Progress and a file's
 *      text; it never touches the network, storage or the DOM.
 *
 *   make audit-import          # bundles src/lib/{importBundle,exportBundle,progress}.ts
 *   node scripts/import-audit.mjs
 */
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const require = createRequire('/tmp/');
const ex = require('/tmp/export-bundle.cjs');
const im = require('/tmp/import-bundle.cjs');
const pr = require('/tmp/progress.cjs');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

/* ---------- a realistic snapshot: every entity the exit gate names ---------- */

const AT = new Date('2026-10-14T09:00:00.000Z');

function snapshot() {
  const p = pr.emptyProgress();
  p.events = [
    { ts: 1791960635578, id: 'ex.1.1', attemptId: 'att_a1', kind: 'passed', title: 'Products in Electronics', course: 'sql-analytics', lesson: 0, datasetVersion: 1, exerciseVersion: 1 },
    { ts: 1791961000000, id: 'ex.2.1', attemptId: 'att_b1', kind: 'failed', title: 'Line items per order', course: 'sql-analytics', lesson: 1, datasetVersion: 1, exerciseVersion: 1 },
    { ts: 1791961100000, id: 'ex.2.1', attemptId: 'att_b2', kind: 'passed', title: 'Line items per order', course: 'sql-analytics', lesson: 1, datasetVersion: 1, exerciseVersion: 1 },
    { ts: 1791961200000, id: 'ex.2.1', attemptId: 'att_b3', kind: 'hint', title: 'Line items per order', course: 'sql-analytics', lesson: 1 },
    { ts: 1791961300000, id: 'bookmark.sql-analytics.1.0', kind: 'bookmark', title: 'Overview', course: 'sql-analytics', lesson: 1 },
    { ts: 1791961400000, id: 'ex.j1.1', kind: 'passed', title: 'Orders with customer names', course: 'joins', lesson: 0 },
  ];
  p.passed = { 'ex.1.1': true, 'ex.2.1': true };
  p.accepted = { 'ex.5.2': true };
  p.firstTry = { 'ex.1.1': true };
  p.done = { m1: true };
  p.notes = [
    { course: 'sql-analytics', lesson: 0, kind: 'note', ts: 1791961500000, text: 'Grain first, then join.' },
    { course: 'sql-analytics', lesson: 1, kind: 'bookmark', ts: 1791961300000, text: 'Joins without row multiplication', section: 2 },
  ];
  p.saved = [
    { course: 'sql-analytics', lesson: 0, name: 'products_by_price', sql: 'SELECT name FROM products', exercise: 'ex.1.1', datasetVersion: 1, exerciseVersion: 1, ts: 1791961600000, contract: { columns: ['name'], row_order: 'ignored' } },
    { course: 'joins', lesson: 0, name: 'monthly_revenue.sql' },
  ];
  p.attempts = 4;
  p.attemptsByDay = [0, 0, 0, 0, 0, 0, 4];
  p.timeMs = { 'sql-analytics': 180000, joins: 60000 };
  return p;
}

const DRAFTS = { 'ex.1.1': 'SELECT name, category, price FROM products', 'ex.2.1': '' };

/* ---------- canonical equality: key order is not information ---------- */

function canon(v) {
  if (Array.isArray(v)) return v.map(canon);
  if (v && typeof v === 'object') {
    const out = {};
    for (const k of Object.keys(v).sort()) out[k] = canon(v[k]);
    return out;
  }
  return v;
}
const same = (a, b) => JSON.stringify(canon(a)) === JSON.stringify(canon(b));

/* ---------- 1. validation refuses before it writes ---------- */

console.log('validation');
check('an export document validates', im.validateBundle(ex.buildExportBundle(snapshot(), DRAFTS, 1, AT)).ok === true);

const refuses = (label, raw, code) => {
  const r = im.validateBundle(raw);
  check(label, r.ok === false && r.code === code, r.ok === true ? 'accepted' : r.code);
};

refuses('refuses a non-object', [1, 2, 3], 'NOT_A_DOCUMENT');
refuses('refuses null', null, 'NOT_A_DOCUMENT');
refuses('refuses a document with no format_version', { progress: {} }, 'NOT_A_DOCUMENT');
refuses('refuses a non-integer format_version', { format_version: '1' }, 'NOT_A_DOCUMENT');
refuses('refuses an unknown format version', { ...ex.buildExportBundle(snapshot(), DRAFTS, 1, AT), format_version: 2 }, 'UNKNOWN_FORMAT_VERSION');
refuses('refuses a missing progress block', { format_version: 1, exported_at: AT.toISOString(), content_versions: { dataset_version: 1, progress_version: pr.PROGRESS_VERSION }, drafts: {} }, 'MALFORMED_DOCUMENT');
refuses('refuses a wrong-typed events array', { ...ex.buildExportBundle(snapshot(), DRAFTS, 1, AT), progress: { ...snapshot(), events: 'nope' } }, 'MALFORMED_DOCUMENT');
refuses('refuses a bad exported_at', { ...ex.buildExportBundle(snapshot(), DRAFTS, 1, AT), exported_at: 'yesterday' }, 'MALFORMED_DOCUMENT');
refuses('refuses a snapshot from the future', { ...ex.buildExportBundle(snapshot(), DRAFTS, 1, AT), content_versions: { dataset_version: 1, progress_version: pr.PROGRESS_VERSION + 1 } }, 'UNSUPPORTED_PROGRESS_VERSION');
refuses(
  'refuses a stray credential field',
  { ...ex.buildExportBundle(snapshot(), DRAFTS, 1, AT), token: 'abc' },
  'MALFORMED_DOCUMENT',
);
refuses(
  'refuses a credential smuggled into progress',
  { ...ex.buildExportBundle(snapshot(), DRAFTS, 1, AT), progress: { ...snapshot(), password: 'hunter2' } },
  'MALFORMED_DOCUMENT',
);

check('parse of non-JSON is INVALID_JSON', (() => { const r = im.readImport('{oops'); return r.ok === false && r.code === 'INVALID_JSON'; })());
check('a refusal explains itself in words', (() => { const r = im.validateBundle({ format_version: 9 }); return r.ok === false && typeof r.message === 'string' && r.message.length > 20 && /version/i.test(r.message); })());

const good = im.readImport(JSON.stringify(ex.buildExportBundle(snapshot(), DRAFTS, 1, AT)));
check('parses its own export back', good.ok === true);

/* ---------- 2. merge never deletes, never duplicates ---------- */

console.log('merge policy');
const base = snapshot();
const existing = pr.emptyProgress();
existing.events = [{ ts: 1, id: 'ex.9.9', attemptId: 'att_keep', kind: 'passed', title: 'Mine', course: 'sql-analytics', lesson: 0 }];
existing.notes = [{ course: 'sql-analytics', lesson: 0, kind: 'note', ts: 42, text: 'A note I already had.' }];
existing.passed = { 'ex.9.9': true };
existing.timeMs = { 'sql-analytics': 999000 };

const intoOccupied = im.applyImport(existing, good.bundle, { 'ex.9.9': 'SELECT 9' });
check('keeps the attempt that was already there', intoOccupied.progress.events.some((e) => e.attemptId === 'att_keep'));
check('keeps the note that was already there', intoOccupied.progress.notes.some((n) => n.text === 'A note I already had.'));
check('keeps a pass the import did not mention', intoOccupied.progress.passed['ex.9.9'] === true);
check('does not lower measured study time', intoOccupied.progress.timeMs['sql-analytics'] === 999000, intoOccupied.progress.timeMs);
check('keeps the in-editor draft', intoOccupied.drafts['ex.9.9'] === 'SELECT 9');

const once = im.applyImport(pr.emptyProgress(), good.bundle, {});
const twice = im.applyImport(once.progress, good.bundle, once.drafts);
check('re-importing the same file adds no attempts', twice.counts.attempts === 0, twice.counts);
check('re-importing the same file adds no notes', twice.counts.notes === 0 && twice.counts.bookmarks === 0, twice.counts);
check('re-importing the same file adds no saved queries', twice.counts.saved === 0, twice.counts);
check('re-importing the same file adds no drafts', twice.counts.drafts === 0, twice.counts);
check('re-importing the same file reports what it skipped', twice.counts.alreadyPresent > 0, twice.counts);
check('re-importing the same file changes nothing', same(twice.progress, once.progress));

/* The bug step 2 fixed, restated from the other side: two graded attempts at
   one exercise must both survive an import. */
const pair = once.progress.events.filter(
  (e) => e.id === 'ex.2.1' && ['passed', 'failed', 'accepted', 'error'].includes(e.kind),
);
check('two graded attempts at one exercise both survive', pair.length === 2, pair.length);
check('the help views around them are kept too', once.progress.events.some((e) => e.kind === 'hint'), once.progress.events.length);

/* ---------- 3. the round trip ---------- */

console.log('round trip');
const original = ex.buildExportBundle(snapshot(), DRAFTS, 1, AT);
const read = im.readImport(JSON.stringify(original));
check('an export is readable by the importer', read.ok === true);

const applied = im.applyImport(pr.emptyProgress(), read.bundle, {});
/* Written a minute later, so `exported_at` really does differ and the check
   below is comparing two documents, not one document with itself. */
const LATER = new Date('2026-10-14T09:01:00.000Z');
const again = ex.buildExportBundle(applied.progress, applied.drafts, 1, LATER);
const strip = (b) => { const c = { ...b }; delete c.exported_at; return c; };

check('the round trip is lossless (attempts)', same(strip(again).progress.events, strip(original).progress.events));
check('the round trip is lossless (notes and bookmarks)', same(strip(again).progress.notes, strip(original).progress.notes));
check('the round trip is lossless (saved queries)', same(strip(again).progress.saved, strip(original).progress.saved));
check('the round trip is lossless (drafts)', same(again.drafts, original.drafts));
check('the round trip is lossless (the whole document)', same(strip(again), strip(original)), {
  diff: Object.keys(strip(original)).filter((k) => !same(strip(again)[k], strip(original)[k])),
});
check('the two documents are not byte-identical', JSON.stringify(again) !== JSON.stringify(original));
check('only exported_at differs', again.exported_at === '2026-10-14T09:01:00.000Z' && original.exported_at === '2026-10-14T09:00:00.000Z' && same(strip(again), strip(original)));
check('the report counts every entity', (() => {
  const c = applied.counts;
  return c.attempts === 4 && c.notes === 1 && c.bookmarks === 1 && c.saved === 2 && c.drafts === 1 && c.alreadyPresent === 0;
})(), applied.counts);
check('the report reads as a sentence', /^Imported .*Nothing was overwritten\.$/.test(im.describeImport(applied)), im.describeImport(applied));

/* Sample-data snapshots must survive too: `seeded` is part of the document. */
const sample = snapshot();
sample.seeded = true;
const sampleRound = im.applyImport(pr.emptyProgress(), ex.buildExportBundle(sample, DRAFTS, 1, AT), {});
check('a sample-data snapshot round-trips its flag', sampleRound.progress.seeded === true, sampleRound.progress.seeded);
const sampleIntoReal = im.applyImport(snapshot(), ex.buildExportBundle(sample, DRAFTS, 1, AT), {});
check('sample data imported into real work is not left marked as sample', sampleIntoReal.progress.seeded === false);

/* An empty import is honest about being empty. */
const bare = im.applyImport(pr.emptyProgress(), ex.buildExportBundle(pr.emptyProgress(), {}, 1, AT), {});
check('an export with nothing in it imports nothing', bare.counts.attempts === 0 && bare.counts.notes === 0);
check('and says so instead of claiming success', /Nothing to import/.test(im.describeImport(bare)), im.describeImport(bare));

/* ---------- 4. no network, no storage, no DOM ---------- */

console.log('adapter independence');
/* The whole bundled module, not just its top-level functions: if any reachable
   code path touched the network, a browser store or the DOM, it would be here. */
/* The module's own source, read from disk. Scanning the bundle would be weaker:
   it transitively contains the curriculum prose, where the words "window." and
   "storage" appear as lesson text. */
const src = readFileSync('web/src/lib/importBundle.ts', 'utf8');
for (const forbidden of ['fetch(', 'localStorage', 'indexedDB', 'document.', 'window.', 'XMLHttpRequest']) {
  check(`the import path never uses ${forbidden}`, !src.includes(forbidden));
}
check('the importer never writes: no storage call of its own', !/idbPut|saveProgress|saveDraft/.test(src));
/* And the module it can reach through its imports: everything it pulls in is a
   model module, so there is no adapter for a caller to accidentally bypass. */
const deps = [...src.matchAll(/^import .* from '([^']+)'/gm)].map((m) => m[1]);
check('its imports are model modules only', deps.every((d) => !d.startsWith('../api/')), deps);
check('and the schema it validates against lives in contracts/', src.includes("contracts/export.schema.json"));
check('import exports no default that could bypass validation', typeof im.default === 'undefined');
check('PROGRESS_VERSION is what the merge writes', im.applyImport(pr.emptyProgress(), good.bundle, {}).progress.version === pr.PROGRESS_VERSION);

if (failed) {
  console.log(`\nimport-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log('\nimport-audit: OK');
