/*
 * The schema CodeMirror uses for identifier completion.
 *
 * `getTables` is the same seam the Tables drawer uses, so the editor completes
 * the workspace the learner is actually in: the SQLite relations on a SQLite
 * chapter, the PostgreSQL relations on a PostgreSQL one. It is fetched on first
 * interaction, not on mount, and cached by dataset and dialect. A cell never
 * opens an engine just to offer a completion list.
 */
import type { DatasetId, Dialect, ExecutionApi } from '../types';

/** The shape @codemirror/lang-sql accepts at its top level: table → columns. */
export type SqlSchema = Record<string, string[]>;

const cache = new Map<string, SqlSchema>();
const pending = new Map<string, Promise<SqlSchema>>();

function keyFor(dataset: DatasetId, dialect: Dialect): string {
  return `${dataset}::${dialect}`;
}

function fromTables(tables: { name: string; cols: [string, string, string][] }[]): SqlSchema {
  return Object.fromEntries(tables.map((table) => [table.name, table.cols.map(([name]) => name)]));
}

/** The cached schema, or null when this workspace has not been read yet. */
export function cachedSqlSchema(dataset: DatasetId, dialect: Dialect): SqlSchema | null {
  return cache.get(keyFor(dataset, dialect)) ?? null;
}

/** Read the schema once per dataset and dialect. Concurrent calls share one read. */
export function loadSqlSchema(api: ExecutionApi, dataset: DatasetId, dialect: Dialect): Promise<SqlSchema> {
  const key = keyFor(dataset, dialect);
  const ready = cache.get(key);
  if (ready) return Promise.resolve(ready);
  const inFlight = pending.get(key);
  if (inFlight) return inFlight;

  const request = api
    .getTables(dataset, dialect)
    .then((result) => {
      const schema = fromTables(result.tables);
      cache.set(key, schema);
      return schema;
    })
    .finally(() => {
      pending.delete(key);
    });
  pending.set(key, request);
  return request;
}
