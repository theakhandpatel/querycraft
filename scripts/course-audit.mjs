/*
 * Exercise-solution audit.
 *
 * Runs every authored exercise `solution` through a live gateway and grades it
 * against itself, so a reference query that no longer matches the dataset is
 * caught before a learner sees it. See docs/course-audit.md.
 *
 *   make audit-courses           # bundles the catalog and runs this script
 *   node scripts/course-audit.mjs
 *
 * Override the target with QC_GATEWAY=http://host:port.
 */
import { createRequire } from 'node:module';
const require = createRequire('/tmp/');
const c = require('/tmp/curriculum.cjs');
const BASE = process.env.QC_GATEWAY || 'http://127.0.0.1:18077';
const courses = [
  ['sql-analytics', c.MODULES],
  ['joins', c.JOINS_MODULES],
  ['subqueries', c.SUBQUERIES_MODULES],
  ['foundations', c.FOUNDATIONS_MODULES],
];
const ex = [];
for (const [course, mods] of courses) for (const m of mods || []) for (const b of m.blocks || []) if (b.type === 'exercise') ex.push({ course, lesson: m.id, b });

const post = async (p, body) => { const r = await fetch(BASE + p, { method:'POST', headers:{'content-type':'application/json'}, body: JSON.stringify(body) }); return { code: r.status, json: await r.json().catch(()=>null) }; };
const rows = [];
for (const { course, lesson, b } of ex) {
  const sol = String(b.solution || '');
  const want = (b.contract && b.contract.columns) || [];
  let run = { ok:false, status:'NO_SQL', rows:0, cols:[] };
  if (sol.trim()) {
    const r = await post('/query', { sql: sol });
    const q = r.json || {};
    run = { ok: q.status === 'succeeded' && !q.truncated, status: q.status, rows: q.row_count, cols: (q.columns||[]).map(x=>x.name), err: q.error && q.error.code };
  }
  const colsOk = want.length === 0 ? null : JSON.stringify(want) === JSON.stringify(run.cols);
  let self = null;
  if (sol.trim()) {
    const r = await post('/answers/evaluate', { sql: sol, exercise: { id: b.id, version: 1, lesson_id: lesson, contract: b.contract, reference_solution: sol } });
    self = r.json && r.json.passed;
  }
  rows.push({ id: b.id, course, lesson, ok: run.ok, status: run.status, rows: run.rows, colsOk, self, want, got: run.cols, err: run.err });
}
const bad = rows.filter(r => !r.ok || r.colsOk === false || r.self === false);
console.log(`exercises: ${rows.length}  broken: ${bad.length}`);
for (const r of bad) console.log(`  BROKEN ${r.course}/${r.id} status=${r.status} rows=${r.rows} colsOk=${r.colsOk} self=${r.self} want=${JSON.stringify(r.want)} got=${JSON.stringify(r.got)} err=${r.err||''}`);
console.log('--- all ---');
for (const r of rows) console.log(`  ${r.ok&&r.colsOk!==false&&r.self!==false?'ok    ':'BAD   '} ${r.course.padEnd(13)} ${String(r.id).padEnd(10)} rows=${String(r.rows).padEnd(3)} colsOk=${r.colsOk} self=${r.self}`);
