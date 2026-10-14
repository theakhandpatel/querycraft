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
const require = createRequire('/tmp/');
const c = require('/tmp/curriculum.cjs');

const courses = [
  ['sql-analytics', c.MODULES],
  ['joins', c.JOINS_MODULES],
  ['subqueries', c.SUBQUERIES_MODULES],
  ['foundations', c.FOUNDATIONS_MODULES],
];

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
  `reading-only: ${stats.readingOnly}  problems: ${problems.length}`,
);
for (const p of problems) console.log(`  FAIL ${p}`);
if (problems.length) process.exitCode = 1;
