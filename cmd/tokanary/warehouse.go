package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fxbin/tokanary/internal/clisession"
	"github.com/fxbin/tokanary/internal/pidata"
	"github.com/fxbin/tokanary/internal/warehouse"
)

type whOpts struct {
	piDir   string
	dbPath  string
	cliDirs []string
	noCLI   bool
	check   bool
	against string
	asJSON  bool
}

func runWarehouse(args []string) int {
	// The parity gate compares against a payload produced OUTSIDE this
	// repository, by the collector that predates the Go pipeline. That
	// reference is the whole point of the gate, so it cannot be substituted
	// with something this repo builds itself: LoadDashboardPayload parses the
	// `window.X = {...};` shape, while .cache/dashboard.json is bare JSON whose
	// `convention` string contains "=" - and even past that, its meta block
	// carries a date-only range and dbVersion 0, which the comparison would
	// report as drift on every run. The default is therefore a name that only
	// resolves where the old reference still exists; pass --against to point
	// somewhere else.
	o := whOpts{dbPath: ".cache/tokanary.sqlite", against: "data.js"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pi-dir":
			if i+1 < len(args) {
				i++
				o.piDir = args[i]
			}
		case "--db":
			if i+1 < len(args) {
				i++
				o.dbPath = args[i]
			}
		case "--cli-dir":
			if i+1 < len(args) {
				i++
				o.cliDirs = append(o.cliDirs, args[i])
			}
		case "--no-cli":
			o.noCLI = true
		case "--check":
			o.check = true
		case "--against":
			if i+1 < len(args) {
				i++
				o.against = args[i]
			}
		case "--json":
			o.asJSON = true
		}
	}

	src, err := resolveSource(o.piDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	work := filepath.Join(filepath.Dir(o.dbPath), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}

	snap, err := pidata.Snapshot(src.Dir, work)
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

	var cli *clisession.Result
	if !o.noCLI {
		dirs := o.cliDirs
		if len(dirs) == 0 {
			dirs = defaultCLIDirs()
		}
		if len(dirs) > 0 {
			// sqlite wins on duplicate session ids, so the snapshot's ids
			// are the exclusion set
			exclude := map[string]bool{}
			for _, s := range data.Sessions {
				exclude[s.ID] = true
			}
			res := clisession.Collect(dirs, exclude)
			if len(res.Sessions) > 0 || len(res.Turns) > 0 {
				cli = &res
			}
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

	fmt.Printf("[ok] warehouse %s  (turns %d, sessions %d, msgs %d)\n",
		o.dbPath, usage.Totals.Turns, usage.Totals.Sessions, usage.Totals.Messages)
	if cli != nil {
		fmt.Printf("[ok] 官方 CLI JSONL 已并入  (sessions %d, turns %d, 重复跳过 %d, 无用量跳过 %d)  <- %s\n",
			len(cli.Sessions), len(cli.Turns), cli.SkippedDup, cli.SkippedEmpty,
			strings.Join(cli.Dirs, ", "))
	}

	if o.asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(usage)
		return 0
	}

	if o.check {
		return runCheck(db, usage, o.against)
	}
	return 0
}

func resolveSource(explicit string) (pidata.Source, error) {
	if explicit != "" {
		p, err := filepath.Abs(explicit)
		if err != nil {
			return pidata.Source{}, err
		}
		if _, err := os.Stat(filepath.Join(p, pidata.DBName)); err != nil {
			return pidata.Source{}, fmt.Errorf("在 %s 里找不到 %s", p, pidata.DBName)
		}
		return pidata.Source{Dir: p, Label: pidata.PiDirLabel(p),
			DBPath: filepath.Join(p, pidata.DBName)}, nil
	}
	found := pidata.DetectPiDirs()
	if len(found) == 0 {
		return pidata.Source{}, fmt.Errorf("没有找到 pi 数据目录，请用 --pi-dir 指定")
	}
	return found[0], nil
}

func defaultCLIDirs() []string {
	home := os.Getenv("USERPROFILE")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home == "" {
		return nil
	}
	var out []string
	for _, c := range []string{
		filepath.Join(home, ".pi", "agent", "sessions"),
		filepath.Join(home, ".config", "pi", "agent", "sessions"),
	} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

// runCheck is the G2 parity gate: every integer in the exported payload must
// equal the one python produced in data.js.
func runCheck(db *sql.DB, u *warehouse.Usage, against string) int {
	payload, err := warehouse.LoadDashboardPayload(against)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	diffs := warehouse.Compare(payload, u)
	if len(diffs) == 0 {
		fmt.Printf("[ok] 与 %s 逐字段对账通过\n", against)
		return 0
	}
	fmt.Printf("[FAIL] 与 %s 有 %d 处差异\n", against, len(diffs))
	limit := len(diffs)
	if limit > 40 {
		limit = 40
	}
	for _, d := range diffs[:limit] {
		fmt.Printf("  %-46s go=%-16v js=%v\n", d.Path, d.Go, d.JS)
	}
	if len(diffs) > limit {
		fmt.Printf("  ... 另有 %d 处\n", len(diffs)-limit)
	}
	return 1
}

var _ = sort.Strings
var _ = clisession.SchemaVersion
