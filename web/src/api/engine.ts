import type { Block, QueryErrorShape, RowValue } from '../types';

/* The simulated PostgreSQL engine behind the mock adapter. Kept pure and
   synchronous so both the mock layer and tests can call it directly. */

export interface EngineOutcome {
  status: 'succeeded' | 'failed';
  columns: { name: string; type: string }[];
  rows: RowValue[][];
  row_count: number;
  truncated: boolean;
  error: QueryErrorShape | null;
}

export function engineExecute(sql: string, expect?: Block): EngineOutcome {
  const fail = (code: string, message: string, detail?: string): EngineOutcome => ({
    status: 'failed',
    columns: [],
    rows: [],
    row_count: 0,
    truncated: false,
    error: { code, message, detail: detail ?? null },
  });

  const s = String(sql).replace(/--[^\n]*/g, '').trim();
  if (!s) return fail('EMPTY_QUERY', 'Blank SQL. Write a statement, then Run.');
  if (/\.\.\./.test(sql)) {
    return fail('QUERY_SYNTAX_ERROR', 'syntax error at end of input', 'The statement looks like a scaffold. Replace the placeholder with real SQL.');
  }
  if (!/^\s*(select|with|explain|insert|update|delete)/i.test(s)) {
    const tok = (s.split(/\s+/)[0] || '').slice(0, 24);
    return fail('QUERY_SYNTAX_ERROR', `syntax error at or near "${tok}"`);
  }
  const badTable = s.match(/\bfrom\s+(orderss|productss|customer|order_items?ss)\b/i);
  if (badTable) {
    return fail('QUERY_RUNTIME_ERROR', `relation "${badTable[1]}" does not exist`, `LINE 1\n  FROM ${badTable[1]}\n         ^`);
  }
  const res = (expect && expect.result) || { columns: ['result'], rows: [] as RowValue[][] };
  const columns = res.columns.map((name) => ({ name, type: 'text' }));
  return {
    status: 'succeeded',
    columns,
    rows: res.rows,
    row_count: res.rows.length,
    truncated: false,
    error: null,
  };
}
