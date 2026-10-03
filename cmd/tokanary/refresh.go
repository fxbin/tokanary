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

// runRefresh is the Go replacement for refresh.cmd: collect, then extract, then
// run the frontend tests and build. The .cmd version had to be ASCII-only and
// free of < > in REM lines because cmd.exe applies redirection at parse time,
// and it re-used a cached binary so source edits silently had no effect. Both
// hazards are gone here.
//
// Node is still required for the frontend test/build step, but not for serving
// the dashboard - see `tokanary serve`.
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

// runSchedule is the Go replacement for install-schedule.cmd. Windows keeps
// using schtasks; elsewhere it prints the equivalent cron entry rather than
// pretending to have registered anything.
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

	fmt.Printf("Registering scheduled task %q - every %d minute(s) ...\n", scheduleTaskName, minutes)
	fmt.Printf("  runs: %s refresh --quiet\n", exe)
	// The .cmd version shelled out to a second .cmd (refresh-silent.cmd) to get
	// a quiet, non-interactive run. A flag on the same binary does that here.
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
