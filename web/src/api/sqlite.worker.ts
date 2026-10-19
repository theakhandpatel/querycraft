/*
 * The in-browser SQLite engine runs here, off the main thread.
 *
 * Same reason as the PGlite worker (design D2): a statement that never finishes
 * would freeze the tab if it ran on the page's thread, and no timeout can
 * pre-empt a synchronous WASM loop from outside. In a Worker the host can stop
 * it — and unlike PGlite, this engine can also stop itself: the deadline is a
 * `sqlite3_progress_handler` that returns SQLITE_INTERRUPT from inside SQLite's
 * step loop (see ./sqliteEngine.ts), so an ordinary runaway statement is stopped
 * by the engine rather than by terminating the thread.
 *
 * The Worker is deliberately a *transport* and nothing else. All of the engine,
 * and all of the dialect's rules, live outside it, so the main-thread fallback
 * (`./sqlite.ts`, for pages whose policy refuses `new Worker`) runs exactly the
 * same code with exactly the same results.
 */
import { Effect, Option, Schema } from 'effect';
import {
  openWorkspace,
  type EngineFailure,
  type EngineRows,
  type EngineTable,
  type SqliteWorkspace,
} from './sqliteEngine';

export interface WorkerRequest {
  id: number;
  op: 'open' | 'exec' | 'tables' | 'reset' | 'close';
  args?: unknown[];
}

/** Every payload a worker reply can carry. `null` is `close`, which reports no
    value. Named so the binding holds a contract instead of an open `unknown`. */
type WorkerReply =
  | { version: string }
  | { ok: true; value: EngineRows }
  | { ok: false; error: EngineFailure }
  | EngineTable[]
  | { duration_ms: number }
  | null;

/* A failure that is not the engine's own still has to reach the caller as data.
   Each leaf tolerates a mistyped field, reading it as absent rather than failing
   the whole error. */
const WorkerFailureSchema = Schema.Struct({
  message: Schema.optional(Schema.String).pipe(
    Schema.catchDecoding(() => Effect.succeed(Option.some(undefined))),
  ),
});

let workspace: SqliteWorkspace | null = null;

async function getWorkspace(): Promise<SqliteWorkspace> {
  if (workspace === null) workspace = await openWorkspace();
  return workspace;
}

self.onmessage = async (event: MessageEvent<WorkerRequest>) => {
  const { id, op, args = [] } = event.data;
  try {
    const ws = await getWorkspace();
    let value: WorkerReply;
    switch (op) {
      case 'open':
        value = { version: ws.version };
        break;
      case 'exec':
        value = ws.exec(String(args[0] ?? ''), Number(args[1] ?? 0), Number(args[2] ?? 0), Number(args[3] ?? 0));
        break;
      case 'tables':
        value = ws.tables();
        break;
      case 'reset':
        value = { duration_ms: ws.reset() };
        break;
      case 'close':
        ws.close();
        workspace = null;
        value = null;
        break;
      default:
        throw new Error(`unknown sqlite worker op: ${String(op)}`);
    }
    postMessage({ id, ok: true, value });
  } catch (e) {
    /* A failure that is not the engine's own (a bug here, an out-of-memory
       WASM allocation) still has to reach the caller as data, because the
       caller's job is to report it, not to guess at it. */
    const err = Option.getOrNull(Schema.decodeUnknownOption(WorkerFailureSchema)(e));
    postMessage({
      id,
      ok: false,
      error: { message: err?.message ?? String(e), code: null, name: 'WorkerError' },
    });
  }
};
