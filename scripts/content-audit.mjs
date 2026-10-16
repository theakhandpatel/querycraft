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
import { assertFreshBundle } from './lib/bundle.mjs';
const require = createRequire('/tmp/');
assertFreshBundle('/tmp/content-seed.cjs', 'audit-content', ['web/src/lib/contentSeed.ts']);
assertFreshBundle('/tmp/curriculum-for-content.cjs', 'audit-content', ['web/src/data/curriculum.ts']);
assertFreshBundle('/tmp/dialect-for-content.cjs', 'audit-content', ['web/src/lib/dialect.ts']);
assertFreshBundle('/tmp/dataset-for-content.cjs', 'audit-content', ['web/src/lib/dataset.ts']);
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
/* Checked against the catalog, not a hand-written list. The list is how a new
   course shipped with its prefix mapped to the default course: the four
   hard-coded assertions still passed because none of them mentioned the new
   prefix. This one cannot pass unless every published module maps home. */
const misprefixed = published
  .filter((p) => cs.courseForModule(p.id) !== p.course)
  .map((p) => `${p.course}/${p.id} maps to ${cs.courseForModule(p.id)}`);
check('every published module maps back to its own course', misprefixed.length === 0, misprefixed);
check('an unmapped prefix falls back to the default course', cs.courseForModule('zzz') === 'sql-analytics');

/* ---------- dialect (M6 step 1, design 15 D15) ----------

   Every chapter runs on exactly one dialect, and it is a dialect the app can
   actually run. The value is data — a course can come from a bundle, a URL or
   the database — so it is checked here rather than trusted. A chapter that
   declares nothing inherits its course, and a course that declares nothing is
   PostgreSQL: the order is asserted, not assumed, because "no dialect" silently
   becoming SQLite would re-grade 40 chapters against the wrong engine. */
const dialect = require('/tmp/dialect-for-content.cjs');
console.log('dialect');
check('the dialect set is closed and in contract order', dialect.DIALECTS.join(',') === 'postgresql,sqlite', dialect.DIALECTS);
const badCourseDialect = curriculum.COURSES
  .filter((c) => c.dialect !== undefined && !dialect.isDialect(c.dialect))
  .map((c) => `${c.id}=${String(c.dialect)}`);
check('every course dialect is one the app can run', badCourseDialect.length === 0, badCourseDialect);
const badModuleDialect = published
  .map((p) => ({ p, mod: (curriculum.COURSES.find((c) => c.id === p.course)?.modules || []).find((m) => m.id === p.id) }))
  .filter(({ mod }) => mod?.dialect !== undefined && !dialect.isDialect(mod.dialect))
  .map(({ p, mod }) => `${p.course}/${p.id}=${String(mod.dialect)}`);
check('every chapter dialect is one the app can run', badModuleDialect.length === 0, badModuleDialect);
const unresolved = published
  .map(({ course, id }) => {
    const c = curriculum.COURSES.find((x) => x.id === course);
    const m = (c?.modules || []).find((x) => x.id === id);
    return `${course}/${id}=${dialect.effectiveDialect(m, c)}`;
  })
  .filter((s) => !/=(postgresql|sqlite)$/.test(s));
check('every chapter resolves to exactly one dialect', unresolved.length === 0, unresolved);
check(
  'a chapter with no dialect inherits its course',
  dialect.effectiveDialect({}, { dialect: 'sqlite' }) === 'sqlite',
  dialect.effectiveDialect({}, { dialect: 'sqlite' }),
);
check(
  'a course with no dialect is PostgreSQL, not SQLite',
  dialect.effectiveDialect({}, {}) === 'postgresql' && dialect.effectiveDialect(undefined, undefined) === 'postgresql',
);
check(
  'a chapter overrides its course',
  dialect.effectiveDialect({ dialect: 'sqlite' }, { dialect: 'postgresql' }) === 'sqlite',
);
check('an unknown dialect string is not accepted as one', dialect.isDialect('mysql') === false && dialect.asDialect('mysql') === undefined);
check('the label is the words a learner reads', dialect.dialectLabel('sqlite') === 'SQLite' && dialect.dialectLabel('postgresql') === 'PostgreSQL');
check('support is unknown until the engine answers', dialect.dialectSupported('sqlite', undefined) === null && dialect.dialectSupported('sqlite', ['postgresql']) === false && dialect.dialectSupported('postgresql', ['postgresql', 'sqlite']) === true);

/* ---------- dataset (M5) ----------

   `dataset` names the workspace a course's SQL runs against. It is the same
   kind of fact as `dialect` and it is checked the same way, because getting it
   wrong is silent: the `plans` course states that customer 7 has 184 orders and
   that the fixture holds 10 000 orders and 20 000 order items, and every one of
   those numbers is *false* in the commerce workspace — a learner ran the
   course's own SQL and saw 80 orders next to prose claiming 10 000, and every
   exercise in the course was graded against the wrong rows. So:

     - the set is closed, and both a course and a block must name one the app
       can serve;
     - a course that names nothing runs on commerce (the default is asserted,
       not assumed);
     - and an exercise that carries a perf contract must live in a course that
       runs on the perf fixture. That last one is the defect above, as a gate. */
const dataset = require('/tmp/dataset-for-content.cjs');
console.log('dataset');
check('the dataset set is closed and in a stable order', dataset.DATASETS.join(',') === 'commerce,perf', dataset.DATASETS);
const badCourseDataset = curriculum.COURSES
  .filter((c) => c.dataset !== undefined && !dataset.isDataset(c.dataset))
  .map((c) => `${c.id}=${String(c.dataset)}`);
check('every course dataset is one the app can serve', badCourseDataset.length === 0, badCourseDataset);
const badBlockDataset = curriculum.COURSES
  .flatMap((c) => (c.modules || []).flatMap((m) => (m.blocks || []).map((b) => ({ c, m, b }))))
  .filter(({ b }) => b?.dataset !== undefined && !dataset.isDataset(b.dataset))
  .map(({ c, m, b }) => `${c.id}/${m.id}/${b.id}=${String(b.dataset)}`);
check('every block dataset is one the app can serve', badBlockDataset.length === 0, badBlockDataset);
const unresolvedDataset = curriculum.COURSES
  .flatMap((c) => (c.modules || []).map((m) => `${c.id}/${m.id}=${dataset.datasetFor({ course: c, block: m })}`))
  .filter((s) => !/=(commerce|perf)$/.test(s));
check('every chapter resolves to exactly one dataset', unresolvedDataset.length === 0, unresolvedDataset);
check('a course with no dataset runs on commerce', dataset.datasetFor({ course: {} }) === 'commerce' && dataset.datasetFor({}) === 'commerce');
check('a block overrides its course', dataset.datasetFor({ course: { dataset: 'commerce' }, block: { dataset: 'perf' } }) === 'perf');
check('an explicit dataset outranks both', dataset.datasetFor({ dataset: 'commerce', course: { dataset: 'perf' }, block: { dataset: 'perf' } }) === 'commerce');
check('an unknown dataset string is not accepted as one', dataset.isDataset('mysql') === false && dataset.asDataset('mysql') === undefined);
check('an unknown dataset degrades to the default, with a reason', dataset.datasetFor({ course: { dataset: 'mysql' } }) === 'commerce' && dataset.datasetReason({ course: { dataset: 'mysql' } }) === 'unknown-dataset');

/* The gate for the defect this section documents: a perf contract is a claim
   about the perf fixture, so it may only be authored in a course that runs
   there. `perf` on the block and `dataset` on the course are two halves of one
   fact, and they are checked to agree. */
const orphanPerf = curriculum.COURSES
  .flatMap((c) => (c.modules || []).flatMap((m) => (m.blocks || []).map((b) => ({ c, m, b }))))
  .filter(({ c, b }) => b?.perf !== undefined && dataset.datasetFor({ course: c }) !== 'perf')
  .map(({ c, m, b }) => `${c.id}/${m.id}/${b.id} carries a perf contract but runs on ${dataset.datasetFor({ course: c })}`);
check('every exercise with a perf contract runs on the perf fixture', orphanPerf.length === 0, orphanPerf);
const perfCoursesWithoutContracts = curriculum.COURSES
  .filter((c) => dataset.datasetFor({ course: c }) === 'perf')
  .filter((c) => !(c.modules || []).some((m) => (m.blocks || []).some((b) => b?.perf !== undefined)))
  .map((c) => c.id);
check('a course that runs on the perf fixture grades at least one contract there', perfCoursesWithoutContracts.length === 0, perfCoursesWithoutContracts);

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

/* ---------- 5. authored check codes are inside the contract enum ---------- */

/*
 * Every exercise's `checks[].code` is a feedback code that must be one the
 * contract declares (contracts/evaluation.schema.json). The mock adapter passes
 * an authored code straight through, while the Go evaluator clamps unknown
 * codes to VALUE_MISMATCH — so a code the schema omits shows a *different*
 * feedback row depending on which adapter served the answer. Eight authored
 * codes were outside the enum before this guard existed. Reading the enum from
 * the schema (not a copy of it here) is the point: a copy would drift the same
 * way.
 */
console.log('check codes');
const repoRoot = new URL('..', import.meta.url).pathname;
const evalSchema = JSON.parse(readFileSync(`${repoRoot}/contracts/evaluation.schema.json`, 'utf8'));
const checkCodeEnum = new Set(evalSchema.properties.checks.items.properties.code.enum);
const authoredCodes = new Set();
const curriculumAll = require('/tmp/curriculum-for-content.cjs');
for (const exported of Object.values(curriculumAll)) {
  if (!Array.isArray(exported)) continue;
  for (const mod of exported) {
    for (const b of mod?.blocks ?? []) {
      for (const c of b?.checks ?? []) if (c?.code) authoredCodes.add(c.code);
    }
  }
}
const strays = [...authoredCodes].filter((c) => !checkCodeEnum.has(c));
check(`every authored check code is in the contract enum (${authoredCodes.size} codes)`, strays.length === 0, strays.join(', '));

if (failed) {
  console.log(`\ncontent-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log('\ncontent-audit: OK');
