/*
 * The SQLite result caps.
 *
 * These live apart from `sqliteEngine.ts` on purpose. The adapter needs the
 * numbers before it has an engine, and importing them from the engine pulled
 * the 868 KB WASM glue into the initial bundle. The engine still owns the
 * behaviour; this module owns the two numbers both sides agree on.
 *
 * The row cap and byte cap mirror the gateway's `engine.DefaultLimits`, so a
 * capped result is described the same way in both adapters: hitting the row cap
 * is a successful, truncated result, and hitting the byte cap is
 * `RESULT_LIMIT_EXCEEDED` with the rows already collected.
 */
export const SQLITE_MAX_ROWS = 500;
export const SQLITE_MAX_BYTES = 1 << 20;
