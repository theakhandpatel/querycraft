/*
 * Content-seeding audit (design 9, M2 step 8).
 *
 * Chapter content is authored as markdown, written into `app.content_docs` and
 * read back from there, so the database — not the bundle — is what a lesson
 * renders. That makes the seeder load-bearing: if it decides not to run, a
 * published lesson renders blank.
 *
 * These checks pin the decision, using the real markdown on disk:
 *
 *   1. The bundle parses. Every authored document yields at least one section
 *      with non-empty markdown.
 *   2. The fingerprint is a content fingerprint: stable under key order, and
 *      sensitive to text, sections and module ids.
 *   3. The seeder is "ensure", not "once": an empty database is seeded, a
 *      database that already matches is left alone, and a database seeded by an
 *      earlier bundle — the case that used to leave a new module blank — is
 *      rewritten.
 *   4. Rows are well formed: a course per module, ordinals from zero, sections
 *      in authored order.
 *
 *   make audit-content          # bundles src/lib/contentSeed.ts and runs this
 *   node scripts/content-audit.mjs
 */
import { createRequire } from 'node:module';
import { readFileSync, readdirSync } from 'node:fs';
const require = createRequire('/tmp/');
const cs = require('/tmp/content-seed.cjs');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

/* ---------- 1. the authored bundle ---------- */

const DIR = 'web/src/data/content';
const modules = readdirSync(DIR).filter((f) => f.endsWith('.md')).map((f) => f.slice(0, -3)).sort();
const bundled = {};
for (const id of modules) bundled[id] = cs.splitSections(readFileSync(`${DIR}/${id}.md`, 'utf8'));

console.log(`authored bundle: ${modules.length} modules, ${Object.values(bundled).reduce((n, s) => n + s.length, 0)} sections`);

check('every authored document parses to at least one section', modules.length > 0 && modules.every((id) => bundled[id].length > 0), modules.filter((id) => !bundled[id].length));
check('no section is empty', Object.values(bundled).every((secs) => secs.every((s) => s.markdown.trim().length > 0)));
check('every section has a name', Object.values(bundled).every((secs) => secs.every((s) => s.section.trim().length > 0)));
check('the title line is not a section of its own', Object.values(bundled).every((secs) => secs.every((s) => !s.markdown.startsWith('# '))));
check('splitSections drops a document with only a title', cs.splitSections('# Just a title\n').length === 0);

/* The modules the curriculum needs content for: the M2 additions are the ones
   that were blank before this fix. */
for (const id of ['m1', 'm8', 'm9', 'f1', 'f2', 'f3', 'f4', 'f5', 'f6', 'f7', 'j1', 'q1']) {
  check(`${id} has content`, Array.isArray(bundled[id]) && bundled[id].length > 0);
}

/* Every module the curriculum publishes needs a document, or its lesson renders
   with no reading content. This is the check that would have caught a new course
   being added to the catalog without its markdown. */
const curriculum = require('/tmp/curriculum-for-content.cjs');
const published = [];
for (const course of curriculum.COURSES) for (const m of course.modules || []) published.push({ course: course.id, id: m.id });
check('the curriculum publishes modules', published.length > 0, published.length);
const noContent = published.filter((p) => !bundled[p.id] || bundled[p.id].length === 0).map((p) => `${p.course}/${p.id}`);
check('every published module has a content document', noContent.length === 0, noContent);
const orphaned = modules.filter((id) => !published.some((p) => p.id === id));
check('no content document belongs to no module', orphaned.length === 0, orphaned);

console.log('course mapping');
check('m-prefixed modules belong to sql-analytics', cs.courseForModule('m6') === 'sql-analytics');
check('j-prefixed modules belong to joins', cs.courseForModule('j2') === 'joins');
check('q-prefixed modules belong to subqueries', cs.courseForModule('q3') === 'subqueries');
check('f-prefixed modules belong to foundations', cs.courseForModule('f4') === 'foundations');

/* ---------- 2. the fingerprint ---------- */

console.log('fingerprint');
const fp = cs.contentFingerprint(bundled);
check('is a hex string', /^[0-9a-f]{8}$/.test(fp), fp);

const reordered = {};
for (const id of [...modules].reverse()) reordered[id] = bundled[id];
check('is stable under key order', cs.contentFingerprint(reordered) === fp);

const edited = { ...bundled, m1: [{ section: 'Overview', markdown: 'different text' }] };
check('changes when a document changes', cs.contentFingerprint(edited) !== fp);

const extraSection = { ...bundled, m1: [...bundled.m1, { section: 'A new section', markdown: 'new' }] };
check('changes when a section is added', cs.contentFingerprint(extraSection) !== fp);

const newModule = { ...bundled, z9: [{ section: 'Overview', markdown: 'brand new lesson' }] };
check('changes when a module is added', cs.contentFingerprint(newModule) !== fp);

/* ---------- 3. the seeding decision ---------- */

console.log('seeding decision');
const empty = { fingerprint: null, modules: [] };
const first = cs.planContentSeed(empty, bundled);
check('an empty database is seeded', first !== null && first.rows.length > 0, first && first.rows.length);
check('the plan records its own fingerprint', first.fingerprint === fp);
check('the first seed writes every authored row', first.rows.length === Object.values(bundled).reduce((n, s) => n + s.length, 0), first.rows.length);

const settled = { fingerprint: fp, modules };
check('a database that already matches is left alone', cs.planContentSeed(settled, bundled) === null);

/* The regression: a database seeded before m9 and the foundations course were
   authored. Its fingerprint matched *that* bundle, so "seed once" skipped it and
   the new lessons rendered blank. */
const older = {};
for (const id of modules) if (!['m9', 'f1', 'f2', 'f3', 'f4', 'f5', 'f6', 'f7'].includes(id)) older[id] = bundled[id];
const olderFingerprint = cs.contentFingerprint(older);
const olderStore = { fingerprint: olderFingerprint, modules: Object.keys(older).sort() };
const upgrade = cs.planContentSeed(olderStore, bundled);
check('a database seeded by an earlier bundle is rewritten', upgrade !== null);
check('and the rewrite carries the newly authored modules', upgrade !== null && ['m9', 'f1', 'f7'].every((id) => upgrade.rows.some((r) => r.module_id === id)));
check('the rewrite uses the new fingerprint', upgrade !== null && upgrade.fingerprint === fp && upgrade.fingerprint !== olderFingerprint);

check(
  'a database missing a module is rewritten even if the fingerprint matches',
  cs.planContentSeed({ fingerprint: fp, modules: modules.filter((id) => id !== 'm9') }, bundled) !== null,
);
check(
  'a database from before fingerprints existed is rewritten once',
  cs.planContentSeed({ fingerprint: null, modules }, bundled) !== null,
);

check('seeding is idempotent: after the rewrite the plan is null', cs.planContentSeed({ fingerprint: fp, modules }, bundled) === null);

/* ---------- 4. the rows ---------- */

console.log('rows');
const rows = first.rows;
check('every row names a course', rows.every((r) => typeof r.course_id === 'string' && r.course_id.length > 0));
check('every row names a module and a section', rows.every((r) => r.module_id && r.section));
check('every row carries markdown', rows.every((r) => typeof r.markdown === 'string' && r.markdown.trim().length > 0));
check('ordinals start at zero and are contiguous per module', (() => {
  const byModule = {};
  for (const r of rows) (byModule[r.module_id] = byModule[r.module_id] || []).push(r);
  return Object.values(byModule).every((rs) => rs.every((r, i) => r.ordinal === i));
})());
check('the course on a row matches its module prefix', rows.every((r) => r.course_id === cs.courseForModule(r.module_id)));
check('sections keep their authored order', (() => {
  const got = rows.filter((r) => r.module_id === 'f4').map((r) => r.section);
  const want = bundled.f4.map((s) => s.section);
  return JSON.stringify(got) === JSON.stringify(want);
})());
const moduleOrder = [...new Set(rows.map((r) => r.module_id))];
check('modules are written in sorted order', JSON.stringify(moduleOrder) === JSON.stringify([...moduleOrder].sort()));

if (failed) {
  console.log(`\ncontent-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log('\ncontent-audit: OK');
