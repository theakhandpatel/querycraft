/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "pglite" (default, real Postgres in the browser) or "http" to talk to
      the Go gateway. */
  readonly VITE_API_MODE?: 'pglite' | 'http';
  /** Base URL of the Go gateway when VITE_API_MODE=http. */
  readonly VITE_API_BASE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
