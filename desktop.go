// Command tokanary-desktop is the Tokanary dashboard as a native window.
//
// The Vue frontend in web/dist is rendered in the WebView. Numbers come from
// the local SQLite warehouse through /api/dashboard — there is no data.js
// snapshot and no browser entry. The collection pipeline stays in cmd/tokanary:
// run `tokanary refresh` to rebuild the warehouse, then reload the window.
//
// Build:  go build -o tokanary-desktop .
// Run:    ./tokanary-desktop
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/fxbin/tokanary/internal/dashboard"
	"github.com/fxbin/tokanary/internal/refresh"
	"github.com/fxbin/tokanary/internal/webui"
)

// fallbackHTML is shown when web/dist has not been built yet.
const fallbackHTML = `<!doctype html>
<html lang="zh"><head><meta charset="utf-8">
<title>Tokanary</title>
<style>
 body{font:15px/1.7 -apple-system,"Segoe UI",system-ui,sans-serif;
      margin:0;display:grid;place-items:center;min-height:100vh;
      background:#0f1115;color:#e6e6e6}
 .card{max-width:34rem;padding:2rem 2.25rem;background:#171a21;
       border:1px solid #262b36;border-radius:14px}
 h1{font-size:1.35rem;margin:0 0 .75rem}
 p{margin:.5rem 0;color:#a8b0c0}
 code{background:#0b0d11;padding:.15rem .45rem;border-radius:5px;color:#7fd1a0}
</style></head>
<body><div class="card">
 <h1>看板还没构建</h1>
 <p>在仓库根目录运行：</p>
 <p><code>tokanary refresh</code></p>
 <p>它会采集数据、重建仓库并构建前端，然后重新加载本窗口即可。</p>
</div></body></html>
`

// APIService serves dashboard JSON from the local warehouse. Mounted on the
// asset server at /api so the WebView can fetch() it same-origin.
type APIService struct {
	repoRoot string
}

func (s *APIService) ServiceName() string { return "dashboard" }

func (s *APIService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api")
	path = strings.TrimPrefix(path, "/")
	if path == "" || path == "dashboard" || path == "dashboard.json" {
		s.serveDashboard(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *APIService) serveDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Read-time incremental refresh: pick up new agent log lines without a
	// full `tokanary refresh`. Cheap when nothing changed (mtime fingerprints).
	if _, err := refresh.Touch(s.repoRoot); err != nil {
		// Still serve whatever the warehouse has — a transient collect error
		// must not blank the dashboard.
		fmt.Fprintf(os.Stderr, "[warn] live refresh: %v\n", err)
	}
	payload, err := dashboard.Assemble(dashboard.Options{RepoRoot: s.repoRoot})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// DesktopService exposes a couple of read-only helpers to the window.
type DesktopService struct {
	repoRoot string
}

func (d *DesktopService) ServiceName() string { return "DesktopService" }

// RepoRoot tells the frontend where the repository is, for display only.
func (d *DesktopService) RepoRoot() string { return d.repoRoot }

// FindRepoRoot walks up from the executable looking for data/adapters. The
// desktop binary may be launched from anywhere, including a Start-menu shortcut
// with no working directory, so a hard-coded relative path is not enough.
func FindRepoRoot() string {
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, dir, filepath.Dir(dir))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	for _, start := range candidates {
		if root := searchUp(start); root != "" {
			return root
		}
	}
	return ""
}

func searchUp(dir string) string {
	for i := 0; i < 8; i++ {
		if st, err := os.Stat(filepath.Join(dir, "data", "adapters")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func main() {
	repoRoot := FindRepoRoot()
	if repoRoot == "" {
		fmt.Fprintln(os.Stderr,
			"[error] 找不到仓库根（需要包含 data/adapters 的目录）。\n"+
				"        请把 tokanary-desktop 放在仓库根目录，或从仓库根目录启动。")
		os.Exit(1)
	}

	dist := filepath.Join(repoRoot, "web", "dist")
	if !webui.HasIndex(dist) {
		fmt.Fprintf(os.Stderr, "[warn] %s 还没有 index.html - 窗口会显示提示页。\n        先运行 tokanary refresh\n", dist)
	}

	assets := webui.NewHandler(dist)
	api := &APIService{repoRoot: repoRoot}
	svc := &DesktopService{repoRoot: repoRoot}

	var appIcon []byte
	if b, err := os.ReadFile(filepath.Join(repoRoot, "assets", "app-icon-1024.png")); err == nil {
		appIcon = b
	}

	app := application.New(application.Options{
		Name:        "Tokanary",
		Description: "本地 AI 用量与成本看板",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewServiceWithOptions(api, application.ServiceOptions{
				Name:  "dashboard",
				Route: "/api",
			}),
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler:        assets,
			Middleware:     indexFallback(dist),
			DisableLogging: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "9f0b1c2e-4a7d-4c1b-9b3e-tokanary000001",
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Tokanary",
		Width:            1440,
		Height:           940,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: application.NewRGBA(15, 17, 21, 255),
	})

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[error] 应用退出: %v\n", err)
		os.Exit(1)
	}
}

// indexFallback answers "/" and any other index.html request with the hint page
// while the frontend build is missing.
func indexFallback(dist string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if p == "/" || strings.HasSuffix(p, "/index.html") {
				if webui.HasIndex(dist) {
					next.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(fallbackHTML))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
