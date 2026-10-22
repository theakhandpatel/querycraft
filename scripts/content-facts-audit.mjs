/*
 * Commerce content facts: every authored result row, checked against the seed.
 *
 * A chapter's prose can be rewritten without touching the curriculum, and the
 * curriculum can keep a `result:` row from an older seed. That is not a
 * cosmetic problem: an example renders those rows, and a mismatch message on a
 * graded exercise quotes them. The joins course shipped both kinds at once
 * (order 1007 with four lines, a customer count of 18 for a product that has
 * ten orders), and no audit noticed because `course-audit` grades the
 * reference solution, not the authored sample.
 *
 * This audit asks the only question the learner can see: is the row set the
 * lesson claims the row set the seeded database returns?
 *
 * It runs every example's `sql` and every exercise's `solution` that carries a
 * `result:`. The perf course is skipped: it has `perf-content-audit`, which
 * also checks plan structure. `EXPLAIN` examples are skipped because a plan is
 * an engine and statistics artifact, not a portable row set.
 *
 *   make audit-content-facts
 *   node scripts/content-facts-audit.mjs
 */
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { assertFreshBundle } from './lib/bundle.mjs';

const ROOT = path.resolve(import.meta.dirname, '..');
const require = createRequire(import.meta.url);
const webRequire = createRequire(path.join(ROOT, 'web', 'package.json'));
const { PGlite } = webRequire('@electric-sql/pglite');
const sqliteModule = webRequire('@sqlite.org/sqlite-wasm');
const initSqlite = sqliteModule.default || sqliteModule;

assertFreshBundle('/tmp/curriculum-for-content-facts.cjs', 'audit-content-facts', ['web/src/data/curriculum.ts']);
const curriculum = require('/tmp/curriculum-for-content-facts.cjs');

const strip = (text) => text.replace(/^BEGIN;$/m, '').replace(/^COMMIT;$/m, '');
const pad = (n) => String(n).padStart(2, '0');

/** A driver value as the characters a learner would read. A Date is the
    timestamp the driver parsed, not a locale string. */
function text(value) {
  if (value === null || value === undefined) return null;
  if (value instanceof Date) {
    return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())} ` +
      `${pad(value.getHours())}:${pad(value.getMinutes())}:${pad(value.getSeconds())}`;
  }
  return String(value);
}

/** Authored rows state a date at the precision the lesson needs; the driver
    returns a midnight Date for a `date` cast. Compare the stated precision. */
function sameValue(authored, actual) {
  const want = text(authored);
  const got = text(actual);
  if (want === null || got === null) return want === got;
  if (want === got) return true;
  if (/^\d{4}-\d{2}-\d{2}$/.test(want)) return got.slice(0, 10) === want;
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(want)) return got.slice(0, 16) === want;
  const na = Number(want);
  const nb = Number(got);
  if (want.trim() !== '' && got.trim() !== '' && Number.isFinite(na) && Number.isFinite(nb)) return na === nb;
  if (typeof authored === 'boolean' || typeof actual === 'boolean') return want.toLowerCase() === got.toLowerCase();
  return false;
}

function sameRow(authored, actual) {
  if (!Array.isArray(authored) || !Array.isArray(actual) || authored.length !== actual.length) return false;
  return authored.every((value, i) => sameValue(value, actual[i]));
}

function compare(authored, actual, rowOrder) {
  const columnsOk = authored.columns.length === actual.columns.length &&
    authored.columns.every((name, i) => name === actual.columns[i]);
  const rows = actual.rows;
  let rowsOk = false;
  if (rowOrder === 'ignored') {
    if (authored.rows.length !== rows.length) {
      rowsOk = false;
    } else {
      const used = Array.from({ length: rows.length }, () => false);
      rowsOk = authored.rows.every((want) => {
        const index = rows.findIndex((row, i) => !used[i] && sameRow(want, row));
        if (index < 0) return false;
        used[index] = true;
        return true;
      });
    }
  } else {
    rowsOk = authored.rows.length === rows.length && authored.rows.every((want, i) => sameRow(want, rows[i]));
  }
  return { ok: columnsOk && rowsOk, columnsOk, rowsOk };
}

function literal(columns, rows) {
  const value = (v) => {
    if (v === null || v === undefined) return 'null';
    if (v instanceof Date) return `'${text(v).slice(0, 16)}'`;
    return `'${String(v).replace(/\\/g, '\\\\').replace(/'/g, "\\'")}'`;
  };
  return `result:{columns:[${columns.map(value).join(',')}],rows:[${rows.map((r) => `[${r.map(value).join(',')}]`).join(',')}]}`;
}

const courseIsSqlite = (course) => course.dialect === 'sqlite' || (course.modules || []).some((m) => m.dialect === 'sqlite');
const isPlan = (sql) => /^\s*EXPLAIN\b/i.test(sql);

const pg = await PGlite.create();
await pg.exec(strip(fs.readFileSync(path.join(ROOT, 'backend/internal/dataset/schema.sql'), 'utf8')));
await pg.exec(strip(fs.readFileSync(path.join(ROOT, 'backend/internal/dataset/seed.sql'), 'utf8')));
await pg.exec('SET search_path TO dataset, public;');

const sqlite = await initSqlite();
const lite = new sqlite.oo1.DB(':memory:');
lite.exec(strip(fs.readFileSync(path.join(ROOT, 'backend/internal/dataset/schema.sqlite.sql'), 'utf8')));
lite.exec(strip(fs.readFileSync(path.join(ROOT, 'backend/internal/dataset/seed.sqlite.sql'), 'utf8')));

let checked = 0;
let failed = 0;
let skipped = 0;

try {
  for (const course of curriculum.COURSES) {
    if (course.id === 'plans') continue;
    const sqliteDialect = courseIsSqlite(course);
    for (const module of course.modules || []) {
      for (const block of module.blocks || []) {
        if (!block.result) continue;
        const sql = block.type === 'exercise' ? block.solution : block.sql;
        if (!sql) continue;
        if (isPlan(sql)) {
          skipped++;
          continue;
        }
        const label = `${course.id}/${module.id} ${block.id ?? block.type}`;
        let actual;
        try {
          if (sqliteDialect) {
            const rows = lite.exec(sql, { rowMode: 'object', returnValue: 'resultRows' });
            const columns = rows.length ? Object.keys(rows[0]) : [];
            actual = { columns, rows: rows.map((row) => columns.map((name) => row[name])) };
          } else {
            const result = await pg.query(sql);
            const columns = (result.fields || []).map((field) => field.name);
            actual = { columns, rows: (result.rows || []).map((row) => columns.map((name) => row[name])) };
          }
        } catch (error) {
          failed++;
          console.log(`FAIL ${label}: ${error instanceof Error ? error.message : String(error)}`);
          continue;
        }
        checked++;
        const rowOrder = block.contract?.row_order ?? block.result_contract?.row_order ?? 'exact';
        const result = compare(block.result, actual, rowOrder);
        if (result.ok) continue;
        failed++;
        console.log(`FAIL ${label}`);
        if (!result.columnsOk) {
          console.log(`  columns: authored ${JSON.stringify(block.result.columns)} actual ${JSON.stringify(actual.columns)}`);
        }
        if (!result.rowsOk) {
          console.log(`  rows: authored ${block.result.rows.length} actual ${actual.rows.length}`);
          const n = Math.min(block.result.rows.length, actual.rows.length);
          for (let i = 0; i < n; i++) {
            if (!sameRow(block.result.rows[i], actual.rows[i])) {
              console.log(`  first row difference at ${i}: authored ${JSON.stringify(block.result.rows[i])} actual ${JSON.stringify(actual.rows[i])}`);
              break;
            }
          }
        }
        console.log(`  paste: ${literal(actual.columns, actual.rows)}`);
      }
    }
  }
} finally {
  await pg.close().catch(() => undefined);
  lite.close();
}

console.log(`\ncontent-facts-audit: ${checked} authored result(s) checked, ${skipped} plan example(s) skipped, ${failed} failure(s)`);
if (failed > 0) process.exit(1);
