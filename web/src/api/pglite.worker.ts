/*
 * The in-browser Postgres runs here, off the main thread.
 *
 * technical-design.md D2 decides on "PGlite in a Web Worker", and it is what
 * makes the M1 exit gate — "a runaway query is stopped by the host" — true: a
 * statement that never finishes would freeze the tab if it ran on the main
 * thread, and no `statement_timeout` can pre-empt a synchronous WASM loop. In a
 * worker the host can simply terminate it (see executeSql in ./pglite.ts).
 */
import { PGlite } from '@electric-sql/pglite';
import { worker } from '@electric-sql/pglite/worker';

worker({
  init: async () => new PGlite(),
});
