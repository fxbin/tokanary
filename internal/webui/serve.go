// Package webui serves the built dashboard from a directory.
//
// It backs the Wails window's asset handler (and any future host that serves
// web/dist). Keeping one implementation means the traversal guard and the
// cache rules stay consistent.
package webui

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

var mimeTypes = map[string]string{
	".html":    "text/html; charset=utf-8",
	".js":      "text/javascript; charset=utf-8",
	".mjs":     "text/javascript; charset=utf-8",
	".css":     "text/css; charset=utf-8",
	".json":    "application/json; charset=utf-8",
	".wasm":    "application/wasm",
	".duckdb":  "application/octet-stream",
	".parquet": "application/octet-stream",
	".svg":     "image/svg+xml",
	".png":     "image/png",
	".ico":     "image/x-icon",
	".woff2":   "font/woff2",
	".csv":     "text/csv; charset=utf-8",
}

// CacheControl keeps index.html uncached so reloading after a refresh shows
// the new shell. Hashed assets under /assets/ are immutable. Dashboard JSON is
// served by the /api handler with its own no-store header.
//
// The argument is a request path in slash form, never a filesystem path: the
// rules describe the shape of the URL, and on Windows a resolved path uses
// backslashes, which would silently fail the "/assets/" test.
func CacheControl(urlPath string) string {
	if strings.HasSuffix(urlPath, "index.html") ||
		strings.HasSuffix(urlPath, ".duckdb") {
		return "no-store"
	}
	if strings.Contains(urlPath, "/assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "no-cache"
}

// ResolveTarget maps a request path onto a file inside root, or reports that
// the request escaped it. It also returns the cleaned request path in slash
// form, which is what the cache rules key off.
//
// Dot segments are collapsed BEFORE percent-decoding, because that is the order
// a URL normaliser uses. It is also what makes the two traversal cases differ:
// "/../../data.js" has its dot segments removed by the normaliser and resolves
// to a real file, while "/..%2f..%2fdata.js" survives normalisation (the escape
// is not a separator yet) and is only revealed as an escape once decoded.
func ResolveTarget(root, escapedPath string) (abs string, urlPath string, inside bool) {
	cleaned := path.Clean("/" + strings.TrimPrefix(escapedPath, "/"))
	decoded, err := url.PathUnescape(cleaned)
	if err != nil {
		decoded = cleaned
	}
	urlPath = decoded
	if strings.HasSuffix(urlPath, "/") {
		urlPath += "index.html"
	}
	abs = filepath.Join(root, filepath.FromSlash(urlPath))
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", urlPath, false
	}
	return abs, urlPath, true
}

// NewHandler serves the dashboard out of root.
//
// A miss is answered with a hint rather than a bare 404, because the usual
// cause is simply that the pipeline has not run yet.
func NewHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, urlPath, inside := ResolveTarget(root, r.URL.EscapedPath())
		if !inside {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		st, err := os.Stat(target)
		if err != nil || st.IsDir() {
			NotFound(w, r)
			return
		}
		f, err := os.Open(target)
		if err != nil {
			NotFound(w, r)
			return
		}
		defer f.Close()

		ctype := mimeTypes[strings.ToLower(filepath.Ext(target))]
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		w.Header().Set("Cache-Control", CacheControl(urlPath))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.Copy(w, f)
	})
}

// NotFound explains what to do when the dashboard has not been built.
func NotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "404 not found: %s\n提示：先运行 tokanary refresh 生成数据产物。\n",
		r.URL.EscapedPath())
}

// HasIndex reports whether root looks like a built dashboard.
func HasIndex(root string) bool {
	_, err := os.Stat(filepath.Join(root, "index.html"))
	return err == nil
}

// VerifyDist checks that a freshly built dashboard uses relative asset paths.
//
// vite is configured with base: './' so the bundle can be served from any
// origin root. If that base is ever changed to '/', the build still succeeds and
// the dist still looks fine on disk, but every asset 404s once it is mounted
// somewhere other than the server root. That failure is invisible until a
// browser is opened, which is why it is asserted here instead.
//
// Nothing invokes a separate dist path checker any more; this assertion is
// where that check lives now.
func VerifyDist(root string) error {
	index := filepath.Join(root, "index.html")
	raw, err := os.ReadFile(index)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", index, err)
	}
	html := string(raw)
	var problems []string
	if strings.Contains(html, `src="/assets/`) || strings.Contains(html, `href="/assets/`) {
		problems = append(problems, "资源用了绝对路径 /assets/（vite base 应为 './'）")
	}
	if !strings.Contains(html, "./assets/") {
		problems = append(problems, "index.html 没有相对引用 ./assets/，请检查 vite base")
	}
	if len(problems) > 0 {
		return fmt.Errorf("dist 路径校验失败:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}
