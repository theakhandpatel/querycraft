/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "pglite" (default, real Postgres in the browser) or "http" to talk to
      the Go gateway. */
  readonly VITE_API_MODE?: 'pglite' | 'http';
  /** Base URL of the Go gateway when VITE_API_MODE=http. */
  readonly VITE_API_BASE?: string;
  /** "simple" is the browser-only build; unset (or "full") is the gateway. */
  readonly VITE_APP_MODE?: 'simple' | 'full';
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
