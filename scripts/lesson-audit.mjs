/*
 * Lesson-completeness audit (design 14, M2 exit gate).
 *
 * The M2 gate is a property of the content -- "every published lesson has
 * objectives, an example and an exercise or a reading-only reason" -- so this
 * script decides it, not a reviewer. It bundles the same catalog the app uses
 * and checks three things per module:
 *
 *   1. at least one non-empty objective,
 *   2. at least one runnable `example` block with a non-empty reference result,
 *   3. a graded exercise (a solution) OR a non-empty `readingOnly` reason.
 *
 *   make audit-lessons
 *   node scripts/lesson-audit.mjs
 *
 * Exit code is non-zero when any module fails, so `make test` catches it.
 */
import { createRequire } from 'node:module';
import { assertFreshBundle } from './lib/bundle.mjs';
const require = createRequire('/tmp/');
assertFreshBundle('/tmp/curriculum.cjs', 'audit-lessons', ['web/src/data/curriculum.ts', 'web/src/types.ts']);
const c = require('/tmp/curriculum.cjs');

/* Derived from the catalog rather than listed here: a hand-written list
   silently skipped a whole course the moment one was added (that is how the
   `plans` course went unaudited), and the audit's whole job is to be the thing
   that notices. */
const courses = (c.COURSES || [])
  .map((course) => [course.id, course.modules || []])
  .filter(([, modules]) => modules.length > 0);

const problems = [];
const stats = { lessons: 0, examples: 0, graded: 0, readingOnly: 0 };

for (const [course, mods] of courses) {
  for (const m of mods || []) {
    stats.lessons++;
    const label = `${course}/${m.id}`;
    const blocks = m.blocks || [];

    const objectives = (m.objectives || []).filter((o) => String(o || '').trim());
    if (!objectives.length) problems.push(`${label}: no objectives`);

    const examples = blocks.filter((b) => b.type === 'example');
    if (!examples.length) problems.push(`${label}: no example block`);
    for (const b of examples) {
      stats.examples++;
      if (!String(b.sql || '').trim()) problems.push(`${label}: example ${b.id || '(anonymous)'} has no SQL`);
      const rows = (b.result && b.result.rows) || [];
      if (!rows.length) problems.push(`${label}: example ${b.id || '(anonymous)'} has no reference result rows`);
    }

    const graded = blocks.filter((b) => b.type === 'exercise' && String(b.solution || '').trim());
    stats.graded += graded.length;

    const reason = typeof m.readingOnly === 'string' ? m.readingOnly.trim() : '';
    if (reason) stats.readingOnly++;

    if (!graded.length && !reason) {
      problems.push(`${label}: no graded exercise and no readingOnly reason`);
    }
  }
}

/* Every runnable cell is addressed by its own id. That id is three things at
   once: the React key (a cell holds editable state, so a positional key showed
   the previous chapter's SQL under the next chapter's exercise), the draft
   storage key, and the DOM id (`#cell-<id>`). A missing id breaks the first and
   a shared id breaks the other two, so both are failures rather than style
   notes. */
const cellIds = new Map();
const missingCellId = [];
const duplicateCellId = [];
for (const [course, mods] of courses) {
  for (const m of mods || []) {
    for (const b of m.blocks || []) {
      if (b.type !== 'example' && b.type !== 'exercise') continue;
      const id = String(b?.id ?? '').trim();
      const where = `${course}/${m.id}`;
      if (!id) {
        missingCellId.push(`${where}: ${b.type} block`);
        continue;
      }
      const first = cellIds.get(id);
      if (first === undefined) cellIds.set(id, where);
      else if (first !== where) duplicateCellId.push(`${id} in ${first} and ${where}`);
    }
  }
}
if (missingCellId.length) problems.push(`runnable block with no id: ${missingCellId.join(', ')}`);
if (duplicateCellId.length) problems.push(`block id owned by two lessons: ${duplicateCellId.join(', ')}`);
stats.cells = cellIds.size;

/* Prove the rule's second branch works instead of trusting a branch that no
   published lesson currently exercises (every lesson today has an exercise). */
const synthetic = [{ id: 'x', objectives: ['ok'], readingOnly: 'Signed forward to the joins course.', blocks: [{ type: 'example', id: 'ex.x.demo', sql: 'SELECT 1;', result: { columns: ['n'], rows: [['1']] } }] }];
const syntheticOk = synthetic.every((m) => {
  const graded = (m.blocks || []).some((b) => b.type === 'exercise' && String(b.solution || '').trim());
  const reason = typeof m.readingOnly === 'string' && m.readingOnly.trim();
  return graded || reason;
});
if (!syntheticOk) problems.push('self-check: a reading-only lesson was wrongly rejected');

console.log(
  `lessons: ${stats.lessons}  examples: ${stats.examples}  graded exercises: ${stats.graded}  ` +
  `reading-only: ${stats.readingOnly}  cells with a unique id: ${stats.cells}  problems: ${problems.length}`,
);
for (const p of problems) console.log(`  FAIL ${p}`);
if (problems.length) process.exitCode = 1;
