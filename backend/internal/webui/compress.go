package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

// compressible reports whether an asset's media type benefits from gzip.
// WASM is already a dense binary container and compresses poorly relative to
// the CPU it costs on every cold start, so it is served raw; so are the
// bitmap formats and WOFF2, which carry their own compression.
func compressible(name string) bool {
	switch {
	case strings.HasSuffix(name, ".wasm"),
		strings.HasSuffix(name, ".png"),
		strings.HasSuffix(name, ".jpg"),
		strings.HasSuffix(name, ".webp"),
		strings.HasSuffix(name, ".woff2"):
		return false
	}
	return true
}

// gzipPool recycles the writers; gzip.NewWriter allocates tables worth
// reusing under a burst of first-hit asset requests.
var gzipPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// gzMu guards gzCache, which memoizes compressed copies of the immutable
// embedded assets. The asset names are content-hashed by the build, so a
// cached entry is valid for the life of the process.
var (
	gzMu    sync.RWMutex
	gzCache = map[string][]byte{}
)

// compressed reads the embedded asset once and caches its gzip form. The
// extra memory is bounded by the compressed size of the text assets — a few
// hundred kilobytes against megabytes of transfer saved per learner.
func compressed(name string) ([]byte, bool) {
	gzMu.RLock()
	b, ok := gzCache[name]
	gzMu.RUnlock()
	if ok {
		return b, true
	}
	raw, err := fs.ReadFile(distFS(), name)
	if err != nil {
		return nil, false
	}
	gz := gzipPool.Get().(*gzip.Writer)
	defer gzipPool.Put(gz)
	var buf bytes.Buffer
	gz.Reset(&buf)
	if _, err := io.Copy(gz, bytes.NewReader(raw)); err != nil {
		return nil, false
	}
	if err := gz.Close(); err != nil {
		return nil, false
	}
	out := buf.Bytes()
	gzMu.Lock()
	gzCache[name] = out
	gzMu.Unlock()
	return out, true
}

// serveCompressed answers a gzip-negotiating client from the cache. Assets
// are immutable and content-hashed, so the response also carries a long-lived
// immutable cache header — the browser downloads each file once.
func serveCompressed(w http.ResponseWriter, r *http.Request, name string) bool {
	if !compressible(name) || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return false
	}
	body, ok := compressed(name)
	if !ok {
		return false
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Content-Length", fmtInt(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return true
}

// contentType maps the asset suffixes the build emits. http.DetectContentType
// is avoided for JS because it sniffs "text/plain; charset=utf-8" — a body
// browsers refuse to execute under strict MIME checking.
func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".wasm"):
		return "application/wasm"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

// fmtInt renders n in decimal without strconv, which this package would
// otherwise import for one call.
func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
