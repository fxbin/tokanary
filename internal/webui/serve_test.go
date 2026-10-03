package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newDistFixture builds a minimal web/dist the way the frontend build would.
func newDistFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("index.html", "<!doctype html><title>tokanary</title>")
	mustWrite("assets/index-abc123.js", "console.log(1)\n")
	return root
}

func head(t *testing.T, h http.Handler, target string) (int, string, int64) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, target, nil))
	n, _ := strconv.ParseInt(rec.Header().Get("Content-Length"), 10, 64)
	return rec.Code, rec.Header().Get("Cache-Control"), n
}

func TestHandlerRootIsNoStore(t *testing.T) {
	h := NewHandler(newDistFixture(t))
	code, cc, _ := head(t, h, "/")
	if code != 200 || cc != "no-store" {
		t.Fatalf("GET / -> %d cc=%q, want 200 no-store", code, cc)
	}
}

func TestHandlerIndexIsNoStore(t *testing.T) {
	root := newDistFixture(t)
	h := NewHandler(root)
	code, cc, size := head(t, h, "/index.html")
	if code != 200 || cc != "no-store" || size == 0 {
		t.Fatalf("index.html -> %d cc=%q size=%d", code, cc, size)
	}
}

// The cache rule keys off the request path. Passing a resolved filesystem path
// instead would make "/assets/" fail to match on Windows, where the separator is
// a backslash - hashed assets would silently lose their immutable header.
func TestHandlerHashedAssetsAreImmutable(t *testing.T) {
	h := NewHandler(newDistFixture(t))
	code, cc, _ := head(t, h, "/assets/index-abc123.js")
	if code != 200 || !strings.Contains(cc, "immutable") {
		t.Fatalf("asset -> %d cc=%q, want 200 immutable", code, cc)
	}
}

func TestHandlerMissingAssetIs404(t *testing.T) {
	h := NewHandler(newDistFixture(t))
	if code, _, _ := head(t, h, "/assets/"); code != 404 {
		t.Fatalf("/assets/ -> %d, want 404", code)
	}
}

// A literal "../" is collapsed by URL normalisation before we ever look at it,
// so it lands back inside the root and simply misses.
func TestHandlerLiteralDotDotIsNormalisedNotEscaped(t *testing.T) {
	h := NewHandler(newDistFixture(t))
	if code, _, _ := head(t, h, "/../../index.html"); code != 200 {
		t.Fatalf("/../../index.html -> %d, want 200 (normalised to /index.html)", code)
	}
}

// A percent-encoded separator is not a separator during normalisation, so the
// escape only appears after decoding - and must then be refused.
func TestHandlerEncodedTraversalIsRefused(t *testing.T) {
	h := NewHandler(newDistFixture(t))
	if code, _, _ := head(t, h, "/..%2f..%2findex.html"); code != 403 {
		t.Fatalf("/..%%2f..%%2findex.html -> %d, want 403", code)
	}
}

func TestHandlerDeepEncodedTraversalIsRefused(t *testing.T) {
	h := NewHandler(newDistFixture(t))
	for _, p := range []string{
		"/%2e%2e%2f%2e%2e%2findex.html",
		"/..%2f..%2f..%2f..%2fgo.mod",
		"/assets/..%2f..%2f..%2fgo.mod",
	} {
		if code, _, _ := head(t, h, p); code != 403 {
			t.Errorf("%s -> %d, want 403", p, code)
		}
	}
}

func TestHandlerSiblingsAreNotReachable(t *testing.T) {
	// web/dist and web/public are siblings. A handler rooted at dist must never
	// serve public/, even though it is inside the repository.
	repo := t.TempDir()
	root := filepath.Join(repo, "dist")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "public", "secret.js"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(root)
	// Encoded separators survive normalisation and are only decoded
	// afterwards, so these are genuine escapes and must be refused outright.
	for _, p := range []string{"/..%2fpublic%2fsecret.js", "/assets/..%2f..%2fpublic%2fsecret.js"} {
		if code, _, _ := head(t, h, p); code != 403 {
			t.Errorf("%s -> %d, want 403", p, code)
		}
	}
	// The literal form is normalised back into the root, so it can only miss.
	// What matters is that the sibling file is never served.
	for _, p := range []string{"/../public/secret.js", "/public/secret.js"} {
		if code, _, _ := head(t, h, p); code == 200 {
			t.Errorf("%s served a file outside the root", p)
		}
	}
}

func TestHandlerNotFoundBodyPointsAtRefresh(t *testing.T) {
	h := NewHandler(newDistFixture(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope.js", nil))
	if rec.Code != 404 {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "tokanary refresh") {
		t.Fatalf("404 body must point at the refresh command, got %q", rec.Body.String())
	}
}

func TestCacheControlRules(t *testing.T) {
	cases := map[string]string{
		"/index.html":              "no-store",
		"/x.duckdb":                "no-store",
		"/assets/index-abc123.css": "public, max-age=31536000, immutable",
		"/favicon.ico":             "no-cache",
	}
	for p, want := range cases {
		if got := CacheControl(p); got != want {
			t.Errorf("CacheControl(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestResolveTargetStaysInsideRoot(t *testing.T) {
	root := filepath.Join("D:", "repo", "web", "dist")
	ok := map[string]string{
		"/":                  filepath.Join(root, "index.html"),
		"/index.html":        filepath.Join(root, "index.html"),
		"/assets/a/b.js":     filepath.Join(root, "assets", "a", "b.js"),
		"/../../index.html":  filepath.Join(root, "index.html"),
		"/a/./b/../c/app.js": filepath.Join(root, "a", "c", "app.js"),
	}
	for req, want := range ok {
		got, _, inside := ResolveTarget(root, req)
		if !inside {
			t.Errorf("%s: reported escape, want %s", req, want)
			continue
		}
		if got != want {
			t.Errorf("%s -> %s, want %s", req, got, want)
		}
	}
	bad := []string{"/..%2f..%2fgo.mod", "/a/..%2f..%2fgo.mod", "/%2e%2e%2fgo.mod"}
	for _, req := range bad {
		if _, _, inside := ResolveTarget(root, req); inside {
			t.Errorf("%s: escaped the root but was allowed", req)
		}
	}
}

// The Wails window and the loopback server share this handler, so both must
// agree on what an unbuilt dashboard looks like.
func TestHasIndex(t *testing.T) {
	root := t.TempDir()
	if HasIndex(root) {
		t.Error("empty dir must not look built")
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasIndex(root) {
		t.Error("dir with index.html must look built")
	}
}

const goodIndex = `<!doctype html><html><head>
<link rel="stylesheet" crossorigin href="./assets/index-abc.css">
</head><body>
<script type="module" crossorigin src="./assets/index-abc.js"></script>
</body></html>`

func TestVerifyDistAcceptsRelativePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(goodIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDist(root); err != nil {
		t.Fatalf("VerifyDist rejected a good dist: %v", err)
	}
}

// Each of these is a way the build can succeed and still produce a dist that
// 404s on every asset once mounted off the server root.
func TestVerifyDistRejectsAbsolutePaths(t *testing.T) {
	noAssets := strings.ReplaceAll(goodIndex, "./assets/", "assets/")
	bad := map[string]string{
		"absolute asset script": strings.Replace(goodIndex, `"./assets/index-abc.js"`, `"/assets/index-abc.js"`, 1),
		"absolute asset css":    strings.Replace(goodIndex, `"./assets/index-abc.css"`, `"/assets/index-abc.css"`, 1),
		"no relative assets":    noAssets,
	}
	for name, html := range bad {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := VerifyDist(root); err == nil {
			t.Errorf("%s: VerifyDist accepted a dist that cannot work off-root", name)
		}
	}
}

func TestVerifyDistReportsMissingIndex(t *testing.T) {
	if err := VerifyDist(t.TempDir()); err == nil {
		t.Fatal("expected an error when index.html is absent")
	}
}
