package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/fxbin/tokanary/internal/webui"
)

// runRefresh drives the whole pipeline: collect, then extract, then run the
// frontend tests and build. It is a Go program rather than a shell script on
// purpose. The entry points it replaced had to be ASCII-only and free of < > in
// REM lines because cmd.exe applies redirection at parse time, and they re-used
// a cached binary so source edits silently had no effect. Both hazards are gone
// here.
//
// Node is still required for the frontend test/build step, but not for serving
// the dashboard - that is the Wails window in desktop.go, which reads the
// warehouse through /api/dashboard.
func runRefresh(args []string) int {
	repoRoot := findRepoRoot()
	if repoRoot == "" {
		fmt.Fprintln(os.Stderr, "[error] 找不到仓库根（需要包含 data/adapters 的目录）")
		return 1
	}
	// The .cmd entry points did `cd /d "%~dp0"` first. Several subcommands
	// resolve --out/--db against the cwd, so pin it to the repo root.
	if err := os.Chdir(repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "[error] chdir %s: %v\n", repoRoot, err)
		return 1
	}

	var piArgs []string
	skipWeb := false
	quiet := false
	for _, a := range args {
		switch a {
		case "--no-web":
			skipWeb = true
		case "--quiet":
			quiet = true
		default:
			if !strings.HasPrefix(a, "-") && len(piArgs) == 0 {
				piArgs = append(piArgs, a)
			}
		}
	}
	if env := os.Getenv("PI_HOME"); env != "" && len(piArgs) == 0 && !quiet {
		fmt.Printf("[info] PI_HOME=%s\n", env)
	}

	step := 0
	total := 5
	next := func(label string) {
		step++
		if !quiet {
			fmt.Printf("\n[%d/%d] %s\n", step, total, label)
		}
	}

	// ---- 1. collect
	next("Collecting CLI usage (declarative source adapters) ...")
	if rc := runCollect(nil); rc != 0 {
		fmt.Println("[WARN] collection failed - keeping the last " +
			filepath.Join(".cache", "external-usage.json"))
	}

	// ---- 2. price table
	next("Checking price table ...")
	pricePath := filepath.Join(repoRoot, ".cache", "prices-raw.json")
	if _, err := os.Stat(pricePath); err == nil {
		if !quiet {
			fmt.Println("  [ok] .cache/prices-raw.json")
		}
	} else if !quiet {
		fmt.Println("  [warn] missing - all costs will be $0. Generate it with:")
		fmt.Println("           tokanary prices")
	}

	// ---- 3. extract
	next("Extracting token usage from pi.sqlite ...")
	buildArgs := append([]string{}, piArgs...)
	if rc := runBuild(buildArgs); rc != 0 {
		fmt.Fprintf(os.Stderr, "[error] extraction failed.\n"+
			"         Custom path:  tokanary refresh \"D:\\path\\to\\.pi\"\n"+
			"         Or set env PI_HOME\n")
		return rc
	}

	if skipWeb {
		if !quiet {
			fmt.Println("\n[--] --no-web: skipped the frontend test and build.")
		}
		return 0
	}

	npm, err := exec.LookPath("npm")
	if err != nil && runtime.GOOS == "windows" {
		// npm is a .cmd shim; LookPath finds it but cannot exec it directly.
		npm = npmShimPath()
	}
	if npm == "" {
		if !quiet {
			fmt.Println("\n[SKIP] npm not found - data refreshed, web/dist left stale.")
			fmt.Println("       Install Node, or run: npm --prefix web run build")
		}
		return 0
	}

	// ---- 4. frontend tests
	next("Web tests (vitest) ...")
	if runNpm(npm, "test") != 0 {
		// Report next to the failure output, then still build: a broken test
		// should not leave web/dist stale.
		fmt.Fprintln(os.Stderr, "\n[WARN] web tests reported failures - see output above.")
	}

	// ---- 5. frontend build
	next("Building dashboard (web/dist) ...")
	if rc := runNpm(npm, "build"); rc != 0 {
		fmt.Fprintln(os.Stderr, "\n[WARN] web build failed - data refreshed but web/dist is stale.")
		return 1
	}
	// vite can succeed and still emit a dist whose asset paths only resolve at
	// the server root. Assert that here rather than discovering it in a browser.
	if err := webui.VerifyDist(filepath.Join(repoRoot, "web", "dist")); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}

	if !quiet {
		fmt.Println("\nDone. Warehouse + web/dist are up to date.")
		fmt.Println("Reload the desktop window (or restart tokanary-desktop) to see new numbers.")
	}
	return 0
}

func npmShimPath() string {
	for _, c := range []string{"npm.cmd", "npm.bat"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// runNpm forwards stdout/stderr so vitest and vite output stays visible.
func runNpm(npm, script string) int {
	cmd := exec.Command(npm, "--prefix", "web", "run", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

// ---- schedule --------------------------------------------------------------

const (
	scheduleTaskName = "Tokanary Refresh"
	scheduleLegacy   = "TokenTraker Refresh" // pre-rename task; drop it to avoid double refresh
)

// runSchedule registers the periodic refresh. Windows keeps using schtasks;
// elsewhere it prints the equivalent cron entry rather than pretending to have
// registered anything.
func runSchedule(args []string) int {
	remove := false
	minutes := 60
	show := false
	for _, a := range args {
		switch {
		case a == "--remove" || a == "/remove":
			remove = true
		case a == "--show" || a == "/query":
			show = true
		default:
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				minutes = n
			} else {
				fmt.Fprintf(os.Stderr, "[error] minutes must be a positive integer, e.g. `tokanary schedule 30`\n")
				return 1
			}
		}
	}

	exe, err := os.Executable()
	if err != nil || exe == "" {
		fmt.Fprintln(os.Stderr, "[error] cannot determine my own path, so the scheduled command would be wrong.")
		return 1
	}
	self := strconv.Quote(exe)

	if runtime.GOOS != "windows" {
		return scheduleNonWindows(self, minutes, remove, show)
	}
	schtasks, err := exec.LookPath("schtasks")
	if err != nil {
		fmt.Fprintln(os.Stderr, "[error] schtasks not found - this needs Windows Task Scheduler.")
		return 1
	}

	if remove {
		fmt.Printf("Removing scheduled task %q ...\n", scheduleTaskName)
		if err := exec.Command(schtasks, "/Delete", "/TN", scheduleTaskName, "/F").Run(); err != nil {
			fmt.Println("[WARN] no such task, or it could not be removed.")
		} else {
			fmt.Println("Removed.")
		}
		_ = exec.Command(schtasks, "/Delete", "/TN", scheduleLegacy, "/F").Run()
		return 0
	}
	if show {
		_ = exec.Command(schtasks, "/Query", "/TN", scheduleTaskName, "/FO", "LIST").Run()
		return 0
	}

	// Registering is the one branch that must not run from a throwaway binary.
	// `go run ./cmd/tokanary schedule` executes out of the build cache, so the
	// task would point at a path the toolchain is free to delete at the next
	// `go clean -cache` - and it would fail silently, at a minute of the user's
	// choosing, with nothing in the repo to explain it.
	if why, ephemeral := ephemeralBinary(exe); ephemeral {
		fmt.Fprintf(os.Stderr,
			"[error] 拒绝注册：当前这个可执行文件是临时的（%s）。\n"+
				"        计划任务会记住这个路径，一旦被清理就只剩一个静默失败的条目。\n"+
				"        请先装一个稳定位置的二进制再注册，例如：\n"+
				"          go build -o tokanary.exe ./cmd/tokanary\n"+
				"          .\\tokanary.exe schedule %d\n", why, minutes)
		return 1
	}

	fmt.Printf("Registering scheduled task %q - every %d minute(s) ...\n", scheduleTaskName, minutes)
	fmt.Printf("  runs: %s refresh --quiet\n", exe)
	// A quiet, non-interactive run is just a flag on this binary; the shell
	// entry points it replaced needed a second script to get one.
	cmd := exec.Command(schtasks, "/Create", "/TN", scheduleTaskName,
		"/TR", self+" refresh --no-web --quiet",
		"/SC", "MINUTE", "/MO", strconv.Itoa(minutes), "/F")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[error] failed to register the task: %v\n", err)
		return 1
	}
	_ = exec.Command(schtasks, "/Delete", "/TN", scheduleLegacy, "/F").Run()

	fmt.Println("\nRegistered.")
	fmt.Println("  - warehouse data refreshes in the background every " + strconv.Itoa(minutes) + " minute(s).")
	fmt.Println("  - reload the desktop window to pick up new numbers.")
	fmt.Println("  - remove it with:  tokanary schedule --remove")
	return 0
}

func scheduleNonWindows(self string, minutes int, remove, show bool) int {
	exe := strings.Trim(self, `"`)
	cron := fmt.Sprintf("*/%d * * * * cd %s && %s refresh --no-web --quiet >> .cache/schedule.log 2>&1",
		minutes, shellQuote(filepath.Dir(exe)), shellQuote(exe))
	switch {
	case remove:
		fmt.Println("Removing the cron entry ...")
		fmt.Println("  run:  crontab -e   and delete the Tokanary line")
	case show:
		fmt.Println("Current crontab:")
		_ = exec.Command("crontab", "-l").Run()
	default:
		fmt.Printf("This platform has no built-in scheduler wiring in Tokanary yet.\n")
		fmt.Printf("Add this line to your crontab (crontab -e):\n\n  %s\n\n", cron)
	}
	return 0
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ephemeralBinary reports whether exe lives somewhere the toolchain is free to
// delete, and names the place when it does.
//
// It exists because `tokanary schedule` records os.Executable(). Under `go run`
// that is a build-cache artefact with a name derived from the package contents,
// so the registered task keeps pointing at a file the next `go clean -cache`
// removes. Nothing about that failure is legible later: the task entry looks
// registered, the dashboard quietly stops updating, and the repo has no record
// of why.
func ephemeralBinary(exe string) (string, bool) {
	abs, err := filepath.Abs(exe)
	if err != nil {
		abs = exe
	}
	type root struct{ dir, why string }
	var roots []root
	if cache, err := os.UserCacheDir(); err == nil && cache != "" {
		roots = append(roots, root{filepath.Join(cache, "go-build"), "Go 构建缓存（go run 的产物）"})
	}
	if cache := os.Getenv("GOCACHE"); cache != "" {
		roots = append(roots, root{cache, "GOCACHE"})
	}
	if tmp := os.TempDir(); tmp != "" {
		roots = append(roots, root{tmp, "临时目录"})
	}
	if goroot := runtime.GOROOT(); goroot != "" {
		roots = append(roots, root{goroot, "GOROOT"})
	}
	for _, r := range roots {
		if withinDir(abs, r.dir) {
			return r.why, true
		}
	}
	return "", false
}

// withinDir reports whether path sits inside dir.
//
// Case is folded only where the filesystem ignores it. On Windows the case of
// a drive letter or a directory component is not stable across the APIs that
// produce these strings, so comparing raw would miss real matches. Elsewhere
// folding would invent them: on a case-sensitive filesystem /opt/Foo is not
// inside /opt/foo, and a build cache that happens to differ only by case would
// slip past the check this exists to perform.
func withinDir(path, dir string) bool {
	norm := func(s string) string {
		s = filepath.Clean(s)
		if runtime.GOOS == "windows" {
			s = strings.ToLower(s)
		}
		return s
	}
	p, d := norm(path), norm(dir)
	if p == d {
		return true
	}
	rel, err := filepath.Rel(d, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
