/*
 * Dataset mapping audit — M6 step 2.
 *
 * The SQLite dataset is a second authoring of the commerce dataset, not a copy:
 * SQLite has no schemas, no roles, no date type and no fixed-scale decimal, so
 * some columns deliberately diverge. `backend/internal/dataset/mapping.json` is
 * the checked manifest of that divergence — one row per column, with the
 * declared type on each side, the SQLite affinity the type selects, and a reason
 * on every row where the two dialects disagree.
 *
 * This audit is the executable half of that manifest. It reads the manifest and
 * both dialect's schema and seed files and fails when:
 *
 *   - a mapped column is missing from either schema;
 *   - a column in either schema is not mapped (an omission is a divergence too);
 *   - the manifest's declared types disagree with the schemas;
 *   - the manifest's affinity disagrees with SQLite's own affinity algorithm;
 *   - a row marked mismatch has no note (a divergence without a reason);
 *   - the two seeds disagree on a table's row count, or the counts drift from
 *     the documented commerce dataset (technical design §7.1);
 *   - a required §7.1 edge case is missing from either seed.
 *
 *   node scripts/dataset-map-audit.mjs
 *   make audit-dataset-map
 *
 * Dependency-free: node:fs, plus the shared Reporter from scripts/lib.
 */
import fs from 'node:fs';
import path from 'node:path';
import { Reporter, repoRoot } from './lib/gateway.mjs';

const datasetDir = path.join(repoRoot, 'backend', 'internal', 'dataset');
const files = {
  mapping: path.join(datasetDir, 'mapping.json'),
  pgSchema: path.join(datasetDir, 'schema.sql'),
  pgSeed: path.join(datasetDir, 'seed.sql'),
  sqliteSchema: path.join(datasetDir, 'schema.sqlite.sql'),
  sqliteSeed: path.join(datasetDir, 'seed.sqlite.sql'),
};

const read = (p) => fs.readFileSync(p, 'utf8');

/* ---------- parsing ---------- */

/**
 * parseSchema reads a DDL file into Map<table, Map<column, declaredType>>.
 * Identifiers are quoted in the PostgreSQL file and bare in the SQLite one, so
 * the CREATE TABLE pattern is a parameter; a column line is "name type ..." in
 * both, and the block ends at the ");" line.
 */
function parseSchema(sql, createTable) {
  const tables = new Map();
  let current = null;
  for (const raw of sql.split('\n')) {
    if (current === null) {
      const m = createTable.exec(raw);
      if (m) {
        current = new Map();
        tables.set(m[1], current);
      }
      continue;
    }
    if (/^\)\s*;/.test(raw)) {
      current = null;
      continue;
    }
    const line = raw.trim();
    if (!line) continue;
    const [name, type] = line.replace(/,$/, '').trim().split(/\s+/);
    if (!name || !type) continue;
    current.set(name.replace(/"/g, ''), type);
  }
  return tables;
}

/** parseSeed returns Map<table, string[]> of the raw tuple lines per table. */
function parseSeed(sql) {
  const rows = new Map();
  let current = null;
  for (const raw of sql.split('\n')) {
    /* The PostgreSQL seed qualifies the table as "dataset"."customers"; the
       SQLite seed has no schema to qualify. */
    const m = /^INSERT INTO (?:"dataset"\.)?"?(\w+)"? /.exec(raw);
    if (m) {
      current = m[1];
      if (!rows.has(current)) rows.set(current, []);
      continue;
    }
    if (current === null || !raw.startsWith('\t(')) continue;
    rows.get(current).push(raw);
    if (raw.trimEnd().endsWith(';')) current = null;
  }
  return rows;
}

/** tupleValues splits one "(a, b, c);" line into trimmed values, quote-aware. */
function tupleValues(line) {
  let s = line.trim();
  if (s.endsWith(';')) s = s.slice(0, -1);
  s = s.replace(/^\(/, '').replace(/\)$/, '');
  const out = [];
  let value = '';
  let quoted = false;
  for (const ch of s) {
    if (ch === "'") {
      quoted = !quoted;
      value += ch;
      continue;
    }
    if (ch === ',' && !quoted) {
      out.push(value.trim());
      value = '';
      continue;
    }
    value += ch;
  }
  out.push(value.trim());
  return out;
}

/**
 * sqliteAffinity is SQLite's own declared-type-to-affinity algorithm
 * (https://sqlite.org/datatype3.html §3.1), in order. It is duplicated here on
 * purpose: the audit must compute the affinity rather than trust the manifest,
 * or a wrong `affinity` field would agree with itself.
 */
function sqliteAffinity(declared) {
  const t = String(declared).toUpperCase();
  if (t.includes('INT')) return 'INTEGER';
  if (t.includes('CHAR') || t.includes('CLOB') || t.includes('TEXT')) return 'TEXT';
  if (t === '' || t.includes('BLOB')) return 'BLOB';
  if (t.includes('REAL') || t.includes('FLOA') || t.includes('DOUB')) return 'REAL';
  return 'NUMERIC';
}

const AFFINITIES = ['INTEGER', 'TEXT', 'BLOB', 'REAL', 'NUMERIC'];
const norm = (t) => String(t).replace(/\s+/g, ' ').trim().toLowerCase();

/* ---------- load ---------- */

const mapping = JSON.parse(read(files.mapping));
const pgSchema = parseSchema(read(files.pgSchema), /^CREATE TABLE "dataset"\."(\w+)" \(/);
const sqliteSchema = parseSchema(read(files.sqliteSchema), /^CREATE TABLE (\w+) \(/);
const pgSeed = parseSeed(read(files.pgSeed));
const sqliteSeed = parseSeed(read(files.sqliteSeed));

// The commerce dataset as the design documents it (§7.1). Both dialects must
// still build it; the mapping is what allows the *representation* to differ.
const EXPECTED_COUNTS = { customers: 30, products: 20, orders: 80, order_items: 160 };

const r = new Reporter('dataset-map-audit');
r.section(`mapping: ${path.relative(repoRoot, files.mapping)}`);

/* ---------- the manifest itself ---------- */

const REQUIRED_KEYS = ['table', 'column', 'pg_type', 'sqlite_type', 'affinity', 'mismatch', 'note'];
let shapeOK = Array.isArray(mapping) && mapping.length > 0;
const badRows = [];
for (const [i, row] of mapping.entries()) {
  const keys = Object.keys(row).sort();
  const rowOK =
    REQUIRED_KEYS.every((k) => keys.includes(k)) &&
    typeof row.mismatch === 'boolean' &&
    AFFINITIES.includes(row.affinity) &&
    typeof row.note === 'string' &&
    row.note.trim() !== '';
  if (!rowOK) badRows.push(`${i}:${row.table || '?'}.${row.column || '?'}`);
}
r.check(`manifest rows are well formed (${mapping.length} rows)`, shapeOK && badRows.length === 0, badRows);

/* ---------- every mapped column exists on both sides ---------- */

const missing = [];
for (const row of mapping) {
  if (!pgSchema.get(row.table)?.has(row.column)) missing.push(`postgresql ${row.table}.${row.column}`);
  if (!sqliteSchema.get(row.table)?.has(row.column)) missing.push(`sqlite ${row.table}.${row.column}`);
}
r.check('every mapped column exists in both schemas', missing.length === 0, missing);

/* ---------- nothing on either side is unmapped ---------- */

const mapped = new Set(mapping.map((row) => `${row.table}.${row.column}`));
const unmapped = [];
for (const [table, columns] of pgSchema) {
  for (const column of columns.keys()) if (!mapped.has(`${table}.${column}`)) unmapped.push(`postgresql ${table}.${column}`);
}
for (const [table, columns] of sqliteSchema) {
  for (const column of columns.keys()) if (!mapped.has(`${table}.${column}`)) unmapped.push(`sqlite ${table}.${column}`);
}
r.check(`every schema column is mapped (${mapped.size} mapped)`, unmapped.length === 0, unmapped);

/* ---------- the manifest's declared types match the schemas ---------- */

const typeDrift = [];
for (const row of mapping) {
  const pgType = pgSchema.get(row.table)?.get(row.column);
  const sqliteType = sqliteSchema.get(row.table)?.get(row.column);
  if (pgType !== undefined && norm(pgType) !== norm(row.pg_type)) {
    typeDrift.push(`${row.table}.${row.column}: manifest ${row.pg_type} vs schema ${pgType}`);
  }
  if (sqliteType !== undefined && norm(sqliteType) !== norm(row.sqlite_type)) {
    typeDrift.push(`${row.table}.${row.column}: manifest ${row.sqlite_type} vs schema ${sqliteType}`);
  }
}
r.check('declared types match both schemas', typeDrift.length === 0, typeDrift);

/* ---------- the manifest's affinity matches SQLite's algorithm ---------- */

const affinityDrift = [];
for (const row of mapping) {
  const computed = sqliteAffinity(row.sqlite_type);
  if (computed !== row.affinity) {
    affinityDrift.push(`${row.table}.${row.column}: manifest ${row.affinity} but ${row.sqlite_type} selects ${computed}`);
  }
}
r.check('affinity matches the SQLite algorithm', affinityDrift.length === 0, affinityDrift);

/* ---------- every divergence carries a reason ---------- */

const mismatches = mapping.filter((row) => row.mismatch);
const unexplained = mismatches.filter((row) => !row.note || !row.note.trim());
r.check(
  `every mismatch has a reason (${mismatches.length} of ${mapping.length} rows)`,
  mismatches.length > 0 && unexplained.length === 0,
  unexplained.map((row) => `${row.table}.${row.column}`),
);

/* ---------- the two seeds build the same shape ---------- */

const countDrift = [];
const tables = new Set([...pgSeed.keys(), ...sqliteSeed.keys()]);
for (const table of [...tables].sort()) {
  const pg = pgSeed.get(table)?.length ?? 0;
  const lite = sqliteSeed.get(table)?.length ?? 0;
  const expected = EXPECTED_COUNTS[table];
  const detail = `${table}: postgresql ${pg}, sqlite ${lite}${expected === undefined ? '' : `, expected ${expected}`}`;
  if (pg !== lite || (expected !== undefined && pg !== expected)) countDrift.push(detail);
}
r.check(
  `both seeds build the same counts (${[...tables].sort().map((t) => `${t} ${pgSeed.get(t)?.length ?? 0}`).join(', ')})`,
  countDrift.length === 0,
  countDrift,
);

/* ---------- the required §7.1 edge cases survive in both dialects ---------- */

function edgeCases(seed, dialect) {
  const problems = [];
  const rowsOf = (table) => (seed.get(table) || []).map(tupleValues);

  const customers = rowsOf('customers');
  if (customers.length && !customers.some((row) => row[2] === 'NULL')) problems.push(`${dialect}: no NULL email`);
  if (customers.length && !customers.some((row) => row[3] === 'NULL')) problems.push(`${dialect}: no NULL city`);

  const products = rowsOf('products');
  if (products.length && !products.some((row) => row[4] === '0' && /^(0|FALSE|false)$/.test(row[5]))) {
    problems.push(`${dialect}: no zero-stock inactive product`);
  }

  const statuses = new Set(rowsOf('orders').map((row) => row[3].replace(/^'|'$/g, '')));
  if (statuses.size < 4) problems.push(`${dialect}: only ${statuses.size} order statuses`);

  const items = rowsOf('order_items');
  if (items.some((row) => row[2] === '20')) problems.push(`${dialect}: product 20 is ordered, but it must never be`);
  const orders = rowsOf('orders').length;
  if (items.length <= orders) problems.push(`${dialect}: ${items.length} items for ${orders} orders — no multi-item orders`);

  return problems;
}

const edgeProblems = [...edgeCases(pgSeed, 'postgresql'), ...edgeCases(sqliteSeed, 'sqlite')];
r.check('both seeds carry the §7.1 edge cases', edgeProblems.length === 0, edgeProblems);

/* ---------- the two dialects agree on dataset_version ---------- */

const version = /dataset_version:\s*(\d+)/;
const versions = {
  'schema.sql': version.exec(read(files.pgSchema))?.[1],
  'schema.sqlite.sql': version.exec(read(files.sqliteSchema))?.[1],
  'seed.sql': version.exec(read(files.pgSeed))?.[1],
  'seed.sqlite.sql': version.exec(read(files.sqliteSeed))?.[1],
};
const versionValues = new Set(Object.values(versions));
const metaRow = /INSERT INTO (?:dataset_meta|"dataset"."dataset_meta") \([^)]*\) VALUES \('commerce', (\d+)\)/
  .exec(read(files.sqliteSchema))?.[1];
r.check(
  `dataset_version agrees across both dialects (${[...versionValues].join('/')}, dataset_meta ${metaRow})`,
  versionValues.size === 1 && !versionValues.has(undefined) && metaRow === versions['schema.sqlite.sql'],
  versions,
);

/* ---------- the mapping, printed ---------- */

r.section(`mapping (${mapping.length} columns, ${mismatches.length} named divergences)`);
const width = (key) => Math.max(...mapping.map((row) => `${row.table}.${row.column}`.length)) + 2;
for (const row of mapping) {
  const left = `${row.table}.${row.column}`;
  const types = `${row.pg_type} -> ${row.sqlite_type} (${row.affinity})`;
  console.log(`  ${left.padEnd(width())}${types.padEnd(46)}${row.mismatch ? 'MISMATCH' : 'same    '}`);
  r.record(left, types, row.mismatch ? 'yes' : 'no', row.note);
}
r.section('reasons');
for (const row of mapping) {
  const flag = row.mismatch ? '!' : ' ';
  console.log(`  ${flag} ${row.table}.${row.column}: ${row.note}`);
}

r.finish();
