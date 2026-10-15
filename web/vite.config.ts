import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The client is adapter-agnostic. VITE_API_MODE selects where ExecutionService
// lives: "pglite" (default) runs real PostgreSQL in the page, "http" talks to
// the Go gateway in backend/, and "mock" is the original canned-data adapter.
// The dataset SQL is imported as raw text from backend/internal/dataset, so the
// dev server must be allowed to read the repository root.
export default defineConfig({
  plugins: [react()],
  /* PGlite locates its .wasm/.data files with `new URL(..., import.meta.url)`.
     Vite's dependency pre-bundling rewrites those paths and the engine then
     fetches the SPA's index.html instead of its 6 MB data bundle, failing with
     "Invalid FS bundle size". Excluding it keeps the real asset URLs. */
  /* Same trap for the SQLite engine (M6): `@sqlite.org/sqlite-wasm` locates its
     868 KB `sqlite3.wasm` from `import.meta.url`, and pre-bundling rewrites that
     path so the engine fetches the SPA's index.html instead of the WASM. The
     adapter passes an explicit `locateFile` as well — see
     src/api/sqliteEngine.ts — so the asset is emitted by the bundler and the URL
     is right in dev and in a production build. */
  optimizeDeps: { exclude: ['@electric-sql/pglite', '@sqlite.org/sqlite-wasm'] },
  /* The in-browser Postgres runs in a module Worker (src/api/pglite.worker.ts).
     Module workers code-split, and Vite's default worker output format (iife)
     cannot code-split, so the build fails with:
       [vite:worker-import-meta-url] Invalid value "iife" for option
       "output.format" — UMD and IIFE output formats are not supported for
       code-splitting builds. */
  worker: { format: 'es' },
  server: { port: 5173, host: true, fs: { allow: ['..'] } },
  preview: { port: 4173, host: true },
});
