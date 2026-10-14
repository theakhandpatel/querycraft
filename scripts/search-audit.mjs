/*
 * Search audit (design 10 / M2 step 5).
 *
 * Search is a promise that a learner can find their own work, so the rules are
 * decided here: results are ranked, the query must match in full (no fuzzy
 * guessing), and an empty query returns nothing instead of the whole catalog.
 *
 *   make audit-search
 *   node scripts/search-audit.mjs
 */
import { createRequire } from 'node:module';
const require = createRequire('/tmp/');
const S = require('/tmp/search.cjs');

const problems = [];
const check = (name, ok, detail) => {
  if (!ok) problems.push(`${name}${detail !== undefined ? ` (${detail})` : ''}`);
};

const course = {
  id: 'sql-analytics',
  title: 'SQL Analytics',
  modules: [
    {
      id: 'm1',
      n: 1,
      title: 'Window functions and ranking',
      summary: 'Compare rows inside a group without collapsing them.',
      objectives: ['Use OVER to compute across rows'],
      blocks: [{ type: 'exercise', id: 'ex.1.1', prompt: 'Rank products within each category by price.' }],
    },
    {
      id: 'm2',
      n: 2,
      title: 'Cohort retention',
      summary: 'Define a cohort, then measure who comes back with window functions.',
      objectives: [],
      blocks: [],
    },
  ],
};
const content = { m1: [{ section: 'Rank inside a group', markdown: 'The window orders rows; RANK gives ties the same rank.' }] };
const progress = {
  version: 5, seeded: true, events: [], passed: {}, accepted: {}, firstTry: {}, done: {},
  notes: [
    { course: 'sql-analytics', lesson: 1, kind: 'note', ts: 1, text: 'grain trap reminder' },
    { course: 'sql-analytics', lesson: 2, kind: 'bookmark', ts: 2, text: 'cohort grid', section: 0 },
  ],
  saved: [{ course: 'sql-analytics', lesson: 1, name: 'Top categories', sql: 'SELECT 1;' }],
  attempts: 0, attemptsByDay: [], timeMs: {},
};

const index = S.buildIndex({ courses: [course], content, progress });

/* 1. The index covers every source. */
const kinds = new Set(index.map((d) => d.kind));
for (const kind of ['lesson', 'exercise', 'section', 'objective', 'note', 'bookmark', 'saved']) {
  check(`index has ${kind}`, kinds.has(kind), [...kinds].join(','));
}
check('index carries the chapter and course id', index.every((d) => d.course === 'sql-analytics' && d.lesson >= 1));

/* 2. Empty input returns nothing, never everything. */
check('empty query returns nothing', S.search(index, '').length === 0);
check('whitespace query returns nothing', S.search(index, '   ').length === 0);
check('null query returns nothing', S.search(index, null).length === 0);

/* 3. Ranking: a title hit beats a body-only hit. */
const rankHits = S.search(index, 'window');
check('window matches more than one doc', rankHits.length >= 2, rankHits.length);
check('the chapter title outranks a body mention', rankHits[0].doc.title.toLowerCase().includes('window'), rankHits[0].doc.title);
check('scores are descending', rankHits.every((h, i) => i === 0 || rankHits[i - 1].score >= h.score));

/* 4. AND across words, not OR. */
const andHits = S.search(index, 'window rank');
check('every word must match', andHits.length > 0 && andHits.every((h) => {
  const t = (h.doc.title + ' ' + h.doc.text).toLowerCase();
  return t.includes('window') && t.includes('rank');
}), andHits.length);
check('a word that matches nothing drops the doc', S.search(index, 'window zzzz').length === 0);

/* 5. No fuzzy guessing: a transposed typo is a miss, not a guess. */
check('typos do not match', S.search(index, 'windwo').length === 0);
check('prefixes still match', S.search(index, 'windo').length > 0);

/* 6. A query with no match is empty, not an error. */
check('no-match returns empty', S.search(index, 'quantum tunnelling').length === 0);

/* 7. The learner's own work is searchable. */
check('a note is findable by its text', S.search(index, 'grain trap').some((h) => h.doc.kind === 'note'));
check('a bookmark is findable', S.search(index, 'cohort grid').some((h) => h.doc.kind === 'bookmark'));
check('a saved query is findable by its SQL', S.search(index, 'select 1').some((h) => h.doc.kind === 'saved'));

/* 8. Limit and grouping. */
check('limit is respected', S.search(index, 'a', 2).length <= 2);
const groups = S.groupHits(S.search(index, 'rank'));
check('grouping drops empty groups', groups.every((g) => g.hits.length > 0));
check('grouping preserves kind order', groups.map((g) => g.kind).join(',') === S.search(index, 'rank')
  .map((h) => h.doc.kind)
  .filter((k, i, arr) => arr.indexOf(k) === i)
  .sort((a, b) => ['lesson', 'exercise', 'section', 'objective', 'bookmark', 'note', 'saved'].indexOf(a) - ['lesson', 'exercise', 'section', 'objective', 'bookmark', 'note', 'saved'].indexOf(b))
  .join(','));

console.log(`docs: ${index.length}  kinds: ${kinds.size}  problems: ${problems.length}`);
for (const p of problems) console.log(`  FAIL ${p}`);
if (problems.length) process.exitCode = 1;
