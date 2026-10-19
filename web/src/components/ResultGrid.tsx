import { engineLabelFor } from '../lib/dialectChoice';
import type { QueryResult } from '../types';

/**
 * A query result, as a labelled table.
 *
 * Three things here are for the keyboard, not the eye:
 *
 *   - The grid is a scrollable region, so it takes `tabIndex={0}`: a box that
 *     scrolls must be reachable without a pointer, or a long row is unreadable.
 *   - It carries a name (`caption` plus `aria-label`) so a screen reader
 *     announces what the table is instead of reading a wall of cells.
 *   - Column headers use `scope="col"`, which is what ties a cell back to its
 *     column when a table has no visual header row.
 *
 * The error card is a `role="alert"`; a failed statement is the one thing in
 * this component that must be announced without being asked for.
 */
export default function ResultGrid({ qr }: { qr: QueryResult | null }) {
  if (!qr) return null;
  if (qr.status !== 'succeeded') {
    return (
      <div className="qerr" role="alert">
        <span className="code">{qr.error!.code}</span> &nbsp;{qr.error!.message}
        {qr.error!.detail ? <pre>{qr.error!.detail}</pre> : null}
      </div>
    );
  }
  const columns = qr.columns.map((c) => c.name);
  const rows = qr.rows.length
    ? qr.rows
    : [qr.columns.map(() => null)];
  /* One sentence naming the shape, reused for the caption and the region. */
  const label = columns.length
    ? `Query result with ${columns.length} column${columns.length === 1 ? '' : 's'} (${columns.join(', ')}) and ${qr.row_count} row${qr.row_count === 1 ? '' : 's'}`
    : `Query result with ${qr.row_count} row${qr.row_count === 1 ? '' : 's'}`;
  return (
    <>
      <div className="tablewrap" key={qr.request_id ?? label} role="region" aria-label={`${label} — scrollable`} tabIndex={0}>
        <table>
          {/* Visually hidden: the visible description is `.resultfoot`. */}
          <caption className="visually-hidden">{label}</caption>
          <thead>
            <tr>{qr.columns.map((c) => <th key={c.name} scope="col">{c.name}</th>)}</tr>
          </thead>
          <tbody>
            {!qr.rows.length ? (
              <tr>{qr.columns.map((_c, i) => <td key={i} className="null">(no rows)</td>)}</tr>
            ) : (
              rows.map((r, ri) => (
                <tr key={ri}>
                  {r.map((v, ci) => (
                    <td key={ci} className={v === null ? 'null' : undefined}>{v === null ? 'NULL' : String(v)}</td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      <div className="resultfoot">
        <span className="muted">
          {qr.row_count} row{qr.row_count === 1 ? '' : 's'}
          {qr.truncated ? ' · truncated' : ''}
          {qr.request_id ? ` · ${qr.request_id}` : ''}
        </span>
        {/* The label beside a result names the engine that produced *this*
            result. `qr.dialect` is the contract's honest, machine-readable
            answer, so it is what the words are built from — never the
            chapter's dialect and never the adapter's — and the engine's own
            free text is shown only when it already names that dialect. */}
        <span className="muted" data-dialect={qr.dialect}>
          {qr.duration_ms} ms · {engineLabelFor(qr.dialect, qr.engine)}
        </span>
      </div>
    </>
  );
}
