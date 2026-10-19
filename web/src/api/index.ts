import { Option, Schema } from 'effect';
import { createExecutionApi } from './execution/service';
import type { ExecutionApi } from '../types';

/* One seam. Everything above it (screens, workbook, lesson) is adapter-agnostic.
   VITE_API_MODE picks the adapter:
     - "pglite" (default): real PostgreSQL (WASM) in the page, no server needed
     - "http": the Go gateway */

type ApiMode = 'pglite' | 'http';

/* The gateway's `/health` answer. Optional fields: a probe that cannot read one
   is a "no" for the engine label, not a crash. */
const HealthSchema = Schema.Struct({
  status: Schema.optional(Schema.String),
  engine: Schema.optional(Schema.String),
  version: Schema.optional(Schema.String),
});

let resolvedMode: ApiMode | null = null;
let resolvedBase: string | null = null;
let resolvedEngine: string | null = null;

/**
 * Ask the gateway what engine is really behind the API.
 *
 * `/health` reports the live server version ("PostgreSQL 18.3"), which is what
 * the UI should print — not a version string baked into the bundle. Failure is
 * non-fatal: the caller keeps its generic label.
 */
async function probeGatewayEngine(base: string): Promise<string | null> {
  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 1500);
    const res = await fetch(`${base.replace(/\/$/, '')}/health`, {
      signal: controller.signal,
      headers: { accept: 'application/json' },
    });
    clearTimeout(timer);
    if (!res.ok) return null;
    const raw: Schema.Json = await res.json();
    const body = Option.getOrNull(Schema.decodeUnknownOption(HealthSchema)(raw));
    if (body?.status !== 'ok' || body?.engine !== 'postgresql') return null;
    const version = body.version?.trim() ?? '';
    return version || 'PostgreSQL';
  } catch {
    return null;
  }
}

function requestedMode(): ApiMode {
  const requested = import.meta.env.VITE_API_MODE;
  return requested === 'http' ? 'http' : 'pglite';
}

/**
 * Pick the adapter once, before the app renders.
 *
 * An explicit VITE_API_MODE always wins. Otherwise a production build probes
 * its own origin for the gateway: when the single binary is serving the page
 * the database lives in the gateway, so the browser sends SQL over HTTP and
 * PGlite is never downloaded. Anything else — the Vite dev server, a static
 * host — falls back to the in-browser engine, which needs no setup at all.
 */
export async function resolveApiMode(): Promise<void> {
  if (resolvedMode) return;
  const requested = import.meta.env.VITE_API_MODE;
  if (requested === 'http' || requested === 'pglite') {
    resolvedMode = requested;
    resolvedBase = import.meta.env.VITE_API_BASE || 'http://localhost:8080';
    if (requested === 'http') resolvedEngine = await probeGatewayEngine(resolvedBase);
    return;
  }
  if (!import.meta.env.PROD || typeof window === 'undefined' || window.location.protocol === 'file:') {
    resolvedMode = 'pglite';
    return;
  }
  const engine = await probeGatewayEngine(window.location.origin);
  if (engine) {
    resolvedMode = 'http';
    resolvedBase = window.location.origin;
    resolvedEngine = engine;
    return;
  }
  resolvedMode = 'pglite';
}

export function createApi({
  onLog,
  onUnauthorized,
}: {
  onLog?: (log: never[], count: number) => void;
  onUnauthorized?: () => void;
} = {}): ExecutionApi {
  const mode: ApiMode = resolvedMode ?? requestedMode();
  const base = resolvedBase ?? import.meta.env.VITE_API_BASE ?? 'http://localhost:8080';
  return createExecutionApi({
    mode,
    baseUrl: base,
    engine: resolvedEngine,
    onLog: onLog as ((log: import('../types').ApiLogEntry[], count: number) => void) | undefined,
    onUnauthorized,
  });
}
