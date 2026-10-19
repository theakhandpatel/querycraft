// Package webui embeds the built Querycraft SPA and serves it from the Go
// gateway, so `make build` produces a single binary that answers both the JSON
// API and the browser app on the same origin.
//
// The `dist` directory is populated by the root Makefile's `embed` target
// (copying web/dist). A committed .gitkeep keeps the directory present so
// `go:embed` compiles before the first frontend build.
package webui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// sub is the embedded tree rooted at dist/. The committed .gitkeep keeps the
// directory present so fs.Sub cannot fail; a binary built without a frontend
// has no index.html, which Available reports as "no SPA".
var sub, _ = fs.Sub(dist, "dist")

// indexFile is the SPA entry point inside the embedded tree. The landing page
// lives beside it at landing/index.html; the app is served at /app/ so the
// landing can own the root without changing the SPA's hash routes.
const (
	indexFile        = "index.html"
	landingIndexFile = "landing/index.html"
	appPath          = "/app"
)

// Available reports whether a real frontend build was embedded. It is true iff
// dist/index.html exists in the embedded filesystem; with only the .gitkeep
// placeholder it is false.
func Available() bool {
	f, err := distFS().Open(indexFile)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Handler serves the embedded SPA. Existing files are served by
// http.FileServer; any other path falls back to index.html with a 200 so the
// client's history-API router can take over. If no build is embedded there is
// no index to fall back to, so unmatched paths 404; callers that want a human
// usable page in that case should use Placeholder instead.
func Handler() http.Handler {
	files := http.FileServer(http.FS(distFS()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean, ok := cleanPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if clean == "/" {
			// The landing page owns the root when the build carries one; a
			// binary built before the landing existed still serves the app.
			if fileExists(landingIndexFile) {
				serveLanding(w, r)
				return
			}
			serveIndex(w, r)
			return
		}
		if clean == appPath || strings.HasPrefix(clean, appPath+"/") {
			serveIndex(w, r)
			return
		}
		name := strings.TrimPrefix(clean, "/")
		if f, err := distFS().Open(name); err == nil {
			_ = f.Close()
			// Hashed, immutable assets go out gzip-compressed from a cache.
			// index.html is unhashed and keeps no-store semantics, so it is
			// served raw rather than through this path.
			if serveCompressed(w, r, name) {
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = clean
			files.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r)
	})
}

// Placeholder is served when the SPA was not embedded: it explains, in the
// browser, how to produce a binary that includes the frontend.
func Placeholder() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, placeholderHTML)
	})
}

// distFS returns the embedded subtree rooted at dist/.
func distFS() fs.FS {
	return sub
}

// fileExists reports whether a path is part of the embedded frontend.
func fileExists(name string) bool {
	f, err := distFS().Open(name)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// serveLanding serves the marketing page at "/". It has the same no-cache rule
// as the app shell: the file is unhashed and changes with every deploy.
func serveLanding(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(distFS(), landingIndexFile)
	if err != nil {
		serveIndex(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(distFS(), indexFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// cleanPath normalizes a request path and guards against traversal. It returns
// the cleaned, always-leading-slash path, or false if the input escapes the
// tree (a ".." segment) or contains a NUL byte.
func cleanPath(p string) (string, bool) {
	if strings.ContainsRune(p, 0) {
		return "", false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return path.Clean("/" + p), true
}

const placeholderHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Querycraft — frontend not embedded</title>
  <style>
    :root { color-scheme: light dark; }
    body { margin: 0; font-family: system-ui, sans-serif; display: grid; place-items: center; min-height: 100vh; background: #0f1115; color: #e7e9ee; }
    main { max-width: 40rem; padding: 2rem; }
    h1 { margin-top: 0; }
    code { background: #1d2027; padding: 0.15em 0.4em; border-radius: 4px; }
    li { margin: 0.4rem 0; }
  </style>
</head>
<body>
  <main>
    <h1>Querycraft gateway</h1>
    <p>The API is running, but the frontend was not embedded in this binary.</p>
    <p>From the repository root, build the single deployable binary:</p>
    <ul>
      <li><code>make build</code> — build the SPA, embed it, and compile the gateway</li>
      <li><code>make web</code> — only build the frontend into web/dist</li>
      <li><code>make serve</code> — run the binary built by <code>make build</code></li>
    </ul>
    <p>See <a href="/docs">/docs</a> for the gateway API.</p>
  </main>
</body>
</html>
`
