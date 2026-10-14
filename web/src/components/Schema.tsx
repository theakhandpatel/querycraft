import React, { useEffect, useState } from 'react';
import type { ExecutionApi, TableDef } from '../types';

export default function Schema({ api, tables: tablesProp }: { api: ExecutionApi; tables?: TableDef[] }) {
  const [tables, setTables] = useState<TableDef[] | null>(tablesProp || null);
  useEffect(() => {
    if (tablesProp) return;
    let alive = true;
    api.getTables().then((res) => {
      if (alive) setTables(res.tables);
    });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  if (!tables) return <div className="empty">Loading schema…</div>;
  return (
    <div className="schema">
      {tables.map((t) => {
        const head = t.preview[0];
        const body = t.preview.slice(1);
        /* A scrollable box must be reachable without a pointer, and a table
           must say what it is. Same pattern as ResultGrid: the grid is a named
           region with a hidden caption, so the keyboard trap can see it and a
           screen reader announces it — this panel is the only thing between the
           learner and the column list they are writing SQL against. */
        const label = `${t.name} preview — ${head.length} column${head.length === 1 ? '' : 's'}, showing ${body.length} of ${t.rows} rows`;
        return (
          <div className="tabledef" key={t.name}>
            <header>{t.name} <span className="muted">· {t.rows} rows</span></header>
            {t.cols.map((c) => (
              <div key={c[0]}>
                <span>{c[0]}{c[2] ? <span className="tag"> {c[2]}</span> : null}</span>
                <span className="type">{c[1]}</span>
              </div>
            ))}
            <div style={{ display: 'block', padding: 0 }}>
              <div
                className="tablewrap"
                role="region"
                aria-label={`${label} — scrollable`}
                tabIndex={0}
                style={{ margin: 0, border: 0, borderTop: '1px solid var(--line)', borderRadius: 0 }}
              >
                <table>
                  <caption className="visually-hidden">{label}</caption>
                  <thead><tr>{head.map((h, i) => <th key={i} scope="col">{h}</th>)}</tr></thead>
                  <tbody>
                    {body.map((r, ri) => (
                      <tr key={ri}>{r.map((v, ci) => <td key={ci} className={v === 'NULL' ? 'null' : undefined}>{v}</td>)}</tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}
