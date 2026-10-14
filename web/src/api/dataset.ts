/* The commerce dataset ships once, in the Go module, and is compiled into the
   browser bundle as raw text so the in-browser Postgres (PGlite) seeds the very
   same schema and rows the gateway uses. One source of truth, no copies. */
import schemaSql from '../../../backend/internal/dataset/schema.sql?raw';
import seedSql from '../../../backend/internal/dataset/seed.sql?raw';

export { schemaSql, seedSql };

/* The dataset version lives in the schema's generated header, so the client and
   the Go seed agree without a second constant to keep in sync. */
export const datasetVersion = Number(/dataset_version:\s*(\d+)/.exec(schemaSql)?.[1] ?? 1);
