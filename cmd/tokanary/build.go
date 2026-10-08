package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fxbin/tokanary/internal/clisession"
	"github.com/fxbin/tokanary/internal/dashboard"
	"github.com/fxbin/tokanary/internal/pidata"
	"github.com/fxbin/tokanary/internal/pricing"
	"github.com/fxbin/tokanary/internal/sources"
	"github.com/fxbin/tokanary/internal/warehouse"
)

type buildOpts struct {
	piDir      string
	out        string
	dbPath     string
	adapters   string
	workDir    string
	noExternal bool
	check      bool
	against    string
}

// runBuild rebuilds the warehouse from pi sources and writes the dashboard
// payload as JSON (default .cache/dashboard.json). The desktop window reads
// the same payload shape from the warehouse via /api/dashboard; this file is
// for tests and offline inspection.
func runBuild(args []string) int {
	o := buildOpts{
		out:      filepath.Join(".cache", "dashboard.json"),
		dbPath:   ".cache/tokanary.sqlite",
		adapters: filepath.Join("data", "adapters"),
		workDir:  ".cache",
		against:  filepath.Join(".cache", "dashboard.json"),
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pi-dir":
			if i+1 < len(args) {
				i++
				o.piDir = args[i]
			}
		case "--out":
			if i+1 < len(args) {
				i++
				o.out = args[i]
			}
		case "--db":
			if i+1 < len(args) {
				i++
				o.dbPath = args[i]
			}
		case "--adapters":
			if i+1 < len(args) {
				i++
				o.adapters = args[i]
			}
		case "--work-dir":
			if i+1 < len(args) {
				i++
				o.workDir = args[i]
			}
		case "--no-external":
			o.noExternal = true
		case "--check":
			o.check = true
		case "--against":
			if i+1 < len(args) {
				i++
				o.against = args[i]
			}
		}
	}

	src, err := resolveSource(o.piDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	if err := os.MkdirAll(o.workDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}

	snap, err := pidata.Snapshot(src.Dir, o.workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	defer func() {
		if err := pidata.RemoveSnapshot(snap); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] 清理 pi 快照目录失败: %v\n", err)
		}
	}()

	data, err := pidata.Read(snap, src.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] 读源库: %v\n", err)
		return 1
	}

	cliDirs := defaultCLIDirs()
	var cli *clisession.Result
	if len(cliDirs) > 0 {
		exclude := map[string]bool{}
		for _, s := range data.Sessions {
			exclude[s.ID] = true
		}
		res := clisession.Collect(cliDirs, exclude)
		if len(res.Sessions) > 0 || len(cli.Turns) > 0 || len(res.Turns) > 0 {
			cli = &res
		}
	}

	db, err := warehouse.DB(o.dbPath, data, cli)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] 建仓库: %v\n", err)
		return 1
	}
	defer db.Close()

	usage, err := warehouse.ExportUsage(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] 导出: %v\n", err)
		return 1
	}

	// Input paths are resolved against the repo root, not the process cwd.
	// Resolving them against cwd meant that running the binary from go/ silently
	// produced a payload with pricing=null and external=null -
	// a wrong dashboard with no error anywhere.
	repoRoot := findRepoRoot()
	if repoRoot == "" {
		fmt.Fprintln(os.Stderr, "[error] 找不到仓库根（需要包含 data/adapters 的目录）")
		return 1
	}

	var cliStats map[string]string
	if cli != nil {
		cliStats = warehouse.CLIStats(db)
	}
	var walSize int64
	if st, err := os.Stat(src.DBPath + "-wal"); err == nil {
		walSize = st.Size()
	}
	var firstTs, lastTs any
	if usage.FirstTs.Valid {
		firstTs = usage.FirstTs.Int64
	}
	if usage.LastTs.Valid {
		lastTs = usage.LastTs.Int64
	}
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	meta := pricing.BuildMeta(
		time.Now().Format("2006-01-02T15:04:05-07:00"),
		src.DBPath, src.Dir, nil, data.DBVersion, src.DBSize, walSize,
		firstTs, lastTs, cliStats, home)

	payload, err := dashboard.AssembleFromUsage(dashboard.Options{
		RepoRoot:   repoRoot,
		DBPath:     o.dbPath,
		NoExternal: o.noExternal,
		Meta:       &meta,
	}, usage, cliStats)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] 装配看板数据: %v\n", err)
		return 1
	}
	if err := dashboard.WriteJSON(o.out, payload); err != nil {
		fmt.Fprintf(os.Stderr, "[error] 写 %s: %v\n", o.out, err)
		return 1
	}
	billed := usage.Totals.Input + usage.Totals.CacheRead + usage.Totals.CacheWrite + usage.Totals.Output
	fmt.Printf("[ok] %s  (turns %d, sessions %d, %d models, %.2fM billable token)\n",
		o.out, usage.Totals.Turns, usage.Totals.Sessions, len(payload.Models), float64(billed)/1e6)

	if o.check {
		return checkJSON(o.out, o.against)
	}
	return 0
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 6; i++ {
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

func checkJSON(built, against string) int {
	a, err := os.ReadFile(built)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	b, err := os.ReadFile(against)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	var ga, gb map[string]any
	if err := json.Unmarshal(a, &ga); err != nil {
		fmt.Fprintf(os.Stderr, "[error] built: %v\n", err)
		return 1
	}
	if err := json.Unmarshal(b, &gb); err != nil {
		fmt.Fprintf(os.Stderr, "[error] against: %v\n", err)
		return 1
	}
	diffs := diffTopLevel(ga, gb)
	if len(diffs) == 0 {
		fmt.Printf("[ok] 与 %s 全等\n", against)
		return 0
	}
	fmt.Printf("[FAIL] 与 %s 有 %d 处顶层差异\n", against, len(diffs))
	for i, d := range diffs {
		if i >= 20 {
			fmt.Printf("  ... 另有 %d 处\n", len(diffs)-20)
			break
		}
		fmt.Printf("  %s\n", d)
	}
	return 1
}

func diffTopLevel(ga, gb map[string]any) []string {
	var out []string
	keys := map[string]bool{}
	for k := range ga {
		keys[k] = true
	}
	for k := range gb {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sortStrings(names)
	for _, k := range names {
		if !jsonEqual(ga[k], gb[k]) {
			out = append(out, fmt.Sprintf("%s differs", k))
		}
	}
	return out
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

var (
	_ = sql.ErrNoRows
	_ = sources.Aggregate
)
