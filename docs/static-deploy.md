# The browser sample

Querycraft builds in two modes from one codebase.

| Mode | Command | What it serves |
| --- | --- | --- |
| Full | `make build` | The Go gateway, the SPA on one port, accounts, AI, stored courses, runs, and admin |
| Simple | `make static` | A static folder: the landing, the built-in library, in-browser SQLite and PGlite, local progress |

The simple build is the public sample. It has no server, no account, and no AI.
The library, grading, workbook, themes, export, and both in-browser engines are
the same code as the full build.

## Build

```sh
make static
```

This runs `web/package.json`'s `build:simple` script with
`VITE_APP_MODE=simple`, `VITE_API_MODE=pglite`, and a relative Vite base. It then
assembles:

```
web/dist-static/
  index.html          the landing, with hosted copy hidden
  landing/assets/     the landing images and icon
  app/                the SPA
  .nojekyll           GitHub Pages must not run Jekyll
  build-info.json     mode, version, commit, build time
```

The output is ignored by git. Rebuild it; do not edit it.

Check it where it will live:

```sh
make static-check
```

The check serves the folder under a `/querycraft/` prefix. It drives the landing
and the app with the Chrome binary the accessibility audit uses. Set `QC_CHROME`
on a machine with a different binary. Add `--screenshot` to write the 1440 px
landing and app images to `.agents/archive/static-shots/`.

## GitHub Pages

`.github/workflows/pages.yml` runs on each push to `main` and on manual dispatch.
It installs the web dependencies, type-checks, lints, runs `make static`, and
publishes `web/dist-static`.

Once, in the repository settings:

1. Open **Settings → Pages**.
2. Set **Source** to **GitHub Actions**.
3. Run the workflow, or push to `main`.

A project site lives at `https://<owner>.github.io/<repo>/`. Every internal URL
in the build is relative, so the app works under that prefix. The same artifact
works at a custom domain root.

The build needs no secrets. It does not run Go.

## What the sample leaves out

- **Accounts.** The sample opens as a local workspace. There is no sign-in.
- **AI.** Course generation, the tutor, the challenge lab, and course review
  need the gateway. Their entry points are hidden, not disabled.
- **Stored courses.** The library is the curriculum that ships with the build.
- **Runs and admin.** There is no generation audit trail and no administration.
- **Cross-device progress.** Progress, notes, bookmarks, and themes live in this
  browser. Clearing site data clears them.

The full build keeps all of these.

## Engine loading

SQLite Essentials is the first course. The sample reads chapter markdown from
the bundle and warms only the selected course's engine. It must not request the
PGlite data bundle for a SQLite course. `make static-check` fails if it does.

The SQLite WASM build is about 868 KB. The PGlite data bundle is about 6 MB.
Both load on demand, during the reading for the course that needs them.

## When the landing changes

The hosted and sample landings share `web/landing/index.html`. Hosted-only blocks
carry `data-gateway-only`; browser-only alternatives carry `data-static-only`.
The static build sets `data-build="static"` on the document and the page's CSS
flips the two. If a hosted promise is added without a marker, it will appear in
the sample. Run `make static-check` after landing copy changes.
