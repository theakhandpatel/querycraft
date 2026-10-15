/* The SQLite commerce dataset ships once, in the Go module, next to the
   PostgreSQL pair, and is compiled into the browser bundle as raw text so the
   in-browser SQLite seeds the very same schema and rows the gateway's SQLite
   executor uses. One source of truth, no copies — the same arrangement
   `web/src/api/dataset.ts` has for the PostgreSQL fixtures.

   The SQLite dataset is the commerce fixture's mirror, not the perf one: the
   performance lab is PostgreSQL-only (`contracts/dialects/sqlite.json` has
   `analyze: false` and `plan: false`), so there is nothing for a 10 000-row
   SQLite fixture to teach. `backend/internal/dataset/sqlite_dataset.go` states
   the same thing on the Go side, and `scripts/dataset-map-audit.mjs` pins the
   counts (`customers 30 / products 20 / orders 80 / order_items 160`, the
   commerce grain) against `backend/internal/dataset/mapping.json`. */
import schemaSql from '../../../backend/internal/dataset/schema.sqlite.sql?raw';
import seedSql from '../../../backend/internal/dataset/seed.sqlite.sql?raw';

export { schemaSql, seedSql };

/* The version lives in the schema's own header, exactly as the PostgreSQL
   schema carries it, so the client and the Go seed agree without a second
   constant to keep in sync. */
export const datasetVersion = Number(/dataset_version:\s*(\d+)/.exec(schemaSql)?.[1] ?? 1);
