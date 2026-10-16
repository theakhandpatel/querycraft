/* The execution seam, split by transport.

   `pglite.ts` holds the browser layer, `http.ts` the gateway layer, and
   `service.ts` picks one and exposes the promise facade. This index re-exports
   every name `api/execution.ts` used to export, so callers keep their import
   path. */
export {
  AUTH_TOKEN_KEY,
  AUTH_USER_KEY,
  ExecutionService,
  GatewayError,
  clearAuthToken,
  readAuthToken,
  readAuthUser,
  writeAuthToken,
  writeAuthUser,
} from './shared';
export type { ExecutionServiceShape } from './shared';
export { PGLITE_CONFIG, pgliteLayer } from './pglite';
export { httpLayer } from './http';
export { createExecutionApi } from './service';
