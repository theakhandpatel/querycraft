import { useEffect, useRef, useState } from 'react';
import { DEFAULT_DATASET, DEFAULT_DIALECT } from '../types';
import type { DatasetId, Dialect, ExecutionApi, TableDef } from '../types';

export default function Schema({
  api,
  dataset = DEFAULT_DATASET,
  dialect = DEFAULT_DIALECT,
  focus = null,
}: {
  api: ExecutionApi;
  /** M5. Which fixture to describe. The two datasets are the same schema at two
      scales, so the wrong one is not an obvious error in the drawer — it is a
      column list that happens to be right next to numbers that are not. The
      drawer passes the dataset its course runs on. */
  dataset?: DatasetId;
  /** M6. Which engine's schema to describe. The SQLite fixture is a mirror of
      commerce but its declared types are different claims
      (`INTEGER`/`TEXT`/`NUMERIC`/`DATE`/`TIMESTAMP`/`BOOLEAN`), so describing
      the PostgreSQL relations to a SQLite chapter would name a schema the
      chapter's engine does not have. The drawer passes the dialect the open
      chapter resolves to. */
  dialect?: Dialect;
  /** A table to bring into view once the schema has loaded. The tutor drawer's
      table chips set this, so a chip lands on the table it names. */
  focus?: string | null;
}) {
  const [tables, setTables] = useState<TableDef[] | null>(null);
  const [failed, setFailed] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    let alive = true;
    /* Cleared first: when the dataset changes, the previous fixture's tables are
       not a preview of this one, and leaving them up would label perf columns
       with commerce row counts. */
    setTables(null);
    setFailed(false);
    api.getTables(dataset, dialect).then((res) => {
      if (!alive) return;
      setTables(res.tables);
    }).catch(() => {
      /* A failed read is not an empty schema. Without this the panel rendered a
         blank region with no loading and no error, so "the read failed" and
         "this workspace has no tables" looked identical. */
      if (alive) setFailed(true);
    });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataset, dialect]);
  /* Land on the table a tutor chip named, once the schema is on screen. A plain
     open leaves `focus` null and changes nothing. */
  useEffect(() => {
    if (!focus || !tables || !rootRef.current) return;
    const node = Array.from(rootRef.current.querySelectorAll<HTMLElement>('.tabledef')).find(
      (el) => el.dataset.table === focus
    );
    node?.scrollIntoView({ block: 'start' });
  }, [focus, tables]);
  if (failed) return <div className="empty">Could not read the schema. It is still loading… try reopening the drawer.</div>;
  if (!tables) return <div className="empty">Loading schema…</div>;
  if (tables.length === 0) return <div className="empty">This workspace has no tables yet. Create one and reopen the drawer.</div>;
  return (
    <div className="schema" ref={rootRef}>
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
          <div className="tabledef" key={t.name} data-table={t.name}>
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
                  <thead><tr>{/* A fixed column list; the position names the column. */}{head.map((h, i) => <th key={i} scope="col">{h}</th>)}</tr></thead>
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
