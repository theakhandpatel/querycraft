import type { CheckResult, ResultContract, RowValue } from '../types';

/* Result-contract comparison, ported from the Go evaluator
   (backend/internal/eval/compare.go) so the in-browser engine grades the same
   way the gateway does: correctness comes from comparing the real result to the
   contract, never from matching SQL text. See docs/technical-design.md §6.1. */

const CODE_VALUE_MISMATCH = 'VALUE_MISMATCH';
const CODE_COLUMN = 'COLUMN_MISMATCH';
const CODE_COLUMN_ORDER = 'COLUMN_ORDER_MISMATCH';
const CODE_ROW_COUNT = 'ROW_COUNT_MISMATCH';
const CODE_ROW_ORDER = 'ROW_ORDER_MISMATCH';

export interface ExpectedResult {
  columns: string[];
  rows: RowValue[][];
}

interface ActualResult {
  columns: string[];
  rows: RowValue[][];
}

interface NormalizedContract {
  columns: string[];
  columnOrder: 'exact' | 'ignored';
  rowOrder: 'exact' | 'ignored';
  duplicates: 'preserved' | 'ignored';
  numericTolerance: number;
}

/** Unknown or empty enum values default per §6.1: order ignored, duplicates preserved. */
function normalizeContract(contract?: Partial<ResultContract> | null): NormalizedContract {
  const c = contract || {};
  const declared = c.numeric_tolerance;
  const tolerance = declared != null && declared > 0 ? declared : 0;
  return {
    columns: Array.isArray(c.columns) ? c.columns.slice() : [],
    columnOrder: c.column_order === 'exact' ? 'exact' : 'ignored',
    rowOrder: c.row_order === 'exact' ? 'exact' : 'ignored',
    duplicates: c.duplicates === 'ignored' ? 'ignored' : 'preserved',
    numericTolerance: tolerance,
  };
}

const DECIMAL = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;

/* RowValue is the closed scalar union every adapter decodes before a row
   reaches the comparator, so each branch is a type guard on that domain, not a
   `typeof` narrowing a representation. `pglite.normalizeValue` has already
   turned a bigint into a number or a string, so no bigint branch is needed. */
function isNumber(v: RowValue): v is number {
  return typeof v === 'number';
}

function isString(v: RowValue): v is string {
  return typeof v === 'string';
}

function isBoolean(v: RowValue): v is boolean {
  return typeof v === 'boolean';
}

function toNumber(v: RowValue): number | null {
  if (isNumber(v)) return Number.isFinite(v) ? v : null;
  if (isString(v)) {
    const s = v.trim();
    if (s === '' || !DECIMAL.test(s)) return null;
    const n = Number(s);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

function toBool(v: RowValue): boolean | null {
  if (isBoolean(v)) return v;
  if (isString(v)) {
    const s = v.trim().toLowerCase();
    if (s === 'true' || s === 't' || s === '1') return true;
    if (s === 'false' || s === 'f' || s === '0') return false;
  }
  return null;
}

/* §6.1 rules 5-7: NULL only equals NULL; numbers compare with tolerance; a
   string is coerced to a number only when the other side is already numeric,
   so genuine text is never normalized ("042" != "42"). */
function valueEqual(a: RowValue, b: RowValue, tolerance: number): boolean {
  if (a === null || b === null) return a === null && b === null;
  if (isNumber(a) || isNumber(b)) {
    const an = toNumber(a);
    const bn = toNumber(b);
    if (an !== null && bn !== null) {
      if (tolerance > 0) return Math.abs(an - bn) <= tolerance;
      return an === bn;
    }
    return false;
  }
  const ab = toBool(a);
  const bb = toBool(b);
  if (ab !== null && bb !== null) return ab === bb;
  if (isString(a) && isString(b)) return a === b;
  return a === b;
}

function rowsEqual(a: RowValue[], b: RowValue[], tolerance: number): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (!valueEqual(a[i], b[i], tolerance)) return false;
  }
  return true;
}

function containsRow(rows: RowValue[][], want: RowValue[], tolerance: number): boolean {
  return rows.some((r) => rowsEqual(r, want, tolerance));
}

function uniqueRows(rows: RowValue[][], tolerance: number): RowValue[][] {
  const out: RowValue[][] = [];
  for (const r of rows) if (!containsRow(out, r, tolerance)) out.push(r);
  return out;
}

function multisetEqual(expected: RowValue[][], actual: RowValue[][], tolerance: number, duplicates: 'preserved' | 'ignored'): boolean {
  if (duplicates === 'ignored') {
    const expSet = uniqueRows(expected, tolerance);
    const actSet = uniqueRows(actual, tolerance);
    if (expSet.length !== actSet.length) return false;
    return expSet.every((e) => containsRow(actSet, e, tolerance));
  }
  if (expected.length !== actual.length) return false;
  const used = Array.from({ length: actual.length }, () => false);
  for (const e of expected) {
    let found = false;
    for (let j = 0; j < actual.length; j++) {
      if (!used[j] && rowsEqual(e, actual[j], tolerance)) {
        used[j] = true;
        found = true;
        break;
      }
    }
    if (!found) return false;
  }
  return true;
}

function sameSet(expected: RowValue[][], actual: RowValue[][], tolerance: number): boolean {
  const expSet = uniqueRows(expected, tolerance);
  const actSet = uniqueRows(actual, tolerance);
  if (expSet.length !== actSet.length) return false;
  return expSet.every((e) => containsRow(actSet, e, tolerance));
}

function dedupe(codes: string[]): string[] {
  return [...new Set(codes)];
}

/** Whether the column names (and, when exact, their order) match, and the codes
    that say how they did not. Named, so the return contract is a type the
    comparator owns rather than an anonymous object literal. */
interface ColumnMatch {
  ok: boolean;
  codes: string[];
}

function matchColumns(want: string[], got: string[], order: 'exact' | 'ignored'): ColumnMatch {
  if (want.length !== got.length) return { ok: false, codes: [CODE_COLUMN] };
  if (order === 'exact') {
    const codes: string[] = [];
    const gotSet = new Set(got);
    for (let i = 0; i < want.length; i++) {
      if (want[i] !== got[i]) {
        codes.push(CODE_COLUMN);
        if (gotSet.has(want[i])) codes.push(CODE_COLUMN_ORDER);
      }
    }
    if (codes.length > 0) return { ok: false, codes: dedupe(codes) };
    return { ok: true, codes: [] };
  }
  const used = Array.from({ length: got.length }, () => false);
  for (const w of want) {
    let found = false;
    for (let j = 0; j < got.length; j++) {
      if (!used[j] && got[j] === w) {
        used[j] = true;
        found = true;
        break;
      }
    }
    if (!found) return { ok: false, codes: [CODE_COLUMN] };
  }
  return { ok: true, codes: [] };
}

/** For each expected column, the index of the matching actual column. */
function alignColumns(want: string[], got: string[], order: 'exact' | 'ignored'): number[] {
  if (order === 'exact') return want.map((_, i) => i);
  const used = Array.from({ length: got.length }, () => false);
  return want.map((w) => {
    for (let j = 0; j < got.length; j++) {
      if (!used[j] && got[j] === w) {
        used[j] = true;
        return j;
      }
    }
    return -1;
  });
}

function projectRows(rows: RowValue[][], order: number[]): RowValue[][] {
  return rows.map((row) => order.map((src) => (src >= 0 && src < row.length ? row[src] : null)));
}

export interface Comparison {
  ok: boolean;
  codes: string[];
  columnsOk: boolean;
  /** null when there was no expected row set to compare against. */
  rowsCountOk: boolean | null;
  valuesOk: boolean | null;
  /** null when the contract ignores row order. */
  rowOrderOk: boolean | null;
  rowsCompared: boolean;
}

/** Compare only column names — the fallback when an exercise has no reference. */
export function compareColumns(contract: Partial<ResultContract> | null | undefined, actual: ActualResult): Comparison {
  const c = normalizeContract(contract);
  const want = c.columns.length > 0 ? c.columns : actual.columns;
  const res = matchColumns(want, actual.columns, c.columnOrder);
  return { ok: res.ok, codes: res.codes, columnsOk: res.ok, rowsCountOk: null, valuesOk: null, rowOrderOk: null, rowsCompared: false };
}

/**
 * Apply every §6.1 rule to a successful result. Execution failures are handled
 * by the caller and never reach here.
 */
export function compareResult(expected: ExpectedResult, contract: Partial<ResultContract> | null | undefined, actual: ActualResult): Comparison {
  const c = normalizeContract(contract);
  const want = c.columns.length > 0 ? c.columns : expected.columns;

  const colCheck = matchColumns(want, actual.columns, c.columnOrder);
  if (!colCheck.ok) {
    return { ok: false, codes: colCheck.codes, columnsOk: false, rowsCountOk: null, valuesOk: null, rowOrderOk: null, rowsCompared: false };
  }

  const order = alignColumns(want, actual.columns, c.columnOrder);
  const actRows = projectRows(actual.rows, order);
  const tolerance = c.numericTolerance;

  if (expected.rows.length === 0 && actual.rows.length === 0) {
    return { ok: true, codes: [], columnsOk: true, rowsCountOk: true, valuesOk: true, rowOrderOk: c.rowOrder === 'exact' ? true : null, rowsCompared: true };
  }

  const base: Comparison = { ok: true, codes: [], columnsOk: true, rowsCountOk: true, valuesOk: true, rowOrderOk: c.rowOrder === 'exact' ? true : null, rowsCompared: true };

  if (c.rowOrder === 'exact') {
    if (expected.rows.length !== actRows.length) {
      return { ...base, ok: false, codes: [CODE_ROW_COUNT], rowsCountOk: false, valuesOk: null };
    }
    let mismatched = false;
    for (let i = 0; i < expected.rows.length; i++) {
      if (!rowsEqual(expected.rows[i], actRows[i], tolerance)) mismatched = true;
    }
    if (!mismatched) return base;
    const codes = [CODE_VALUE_MISMATCH];
    // Values are all present, just reordered?
    const reordered = multisetEqual(expected.rows, actRows, tolerance, c.duplicates);
    if (reordered) codes.push(CODE_ROW_ORDER);
    return { ...base, ok: false, codes: dedupe(codes), valuesOk: false, rowOrderOk: false };
  }

  if (c.duplicates === 'ignored') {
    if (!sameSet(expected.rows, actRows, tolerance)) {
      const countDiffers = uniqueRows(expected.rows, tolerance).length !== uniqueRows(actRows, tolerance).length;
      return { ...base, ok: false, codes: [countDiffers ? CODE_ROW_COUNT : CODE_VALUE_MISMATCH], rowsCountOk: countDiffers ? false : true, valuesOk: false };
    }
    return base;
  }

  if (expected.rows.length !== actRows.length) {
    return { ...base, ok: false, codes: [CODE_ROW_COUNT], rowsCountOk: false, valuesOk: null };
  }
  const used = Array.from({ length: actRows.length }, () => false);
  for (const e of expected.rows) {
    let found = false;
    for (let j = 0; j < actRows.length; j++) {
      if (!used[j] && rowsEqual(e, actRows[j], tolerance)) {
        used[j] = true;
        found = true;
        break;
      }
    }
    if (!found) {
      return { ...base, ok: false, codes: [CODE_VALUE_MISMATCH], valuesOk: false };
    }
  }
  return base;
}

/**
 * Turn a comparison into the checklist the lesson UI renders. The labels are
 * derived from the real comparison, so a passing check means the database
 * actually returned the right thing.
 */
export function checksFromComparison(cmp: Comparison, contract: Partial<ResultContract> | null | undefined): CheckResult[] {
  const c = normalizeContract(contract);
  const checks: CheckResult[] = [];

  checks.push({
    label: c.columnOrder === 'exact' ? 'Columns and column order' : 'Column names',
    code: cmp.codes.includes(CODE_COLUMN_ORDER) ? CODE_COLUMN_ORDER : CODE_COLUMN,
    ok: cmp.columnsOk,
  });

  if (cmp.rowsCountOk !== null) {
    checks.push({ label: 'Row count', code: CODE_ROW_COUNT, ok: cmp.rowsCountOk });
  }
  if (cmp.valuesOk !== null) {
    checks.push({ label: 'Row values against the reference result', code: CODE_VALUE_MISMATCH, ok: cmp.valuesOk });
  }
  if (cmp.rowOrderOk !== null) {
    checks.push({ label: 'Row order', code: CODE_ROW_ORDER, ok: cmp.rowOrderOk });
  }
  return checks;
}
