package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fxbin/tokanary/internal/pricing"
	"github.com/fxbin/tokanary/internal/sources"
)

type collectOpts struct {
	adapters     string
	out          string
	tools        string
	home         string
	workDir      string
	listOnly     bool
	validateOnly bool
	full         bool
	curatedPath  string
}

func runCollect(args []string) int {
	repoRoot := findRepoRoot()
	if repoRoot == "" {
		fmt.Fprintln(os.Stderr, "[error] 找不到仓库根（需要包含 data/adapters 的目录）")
		return 1
	}
	quiet := hasFlag(args, "--quiet")
	o := collectOpts{
		adapters:    filepath.Join(repoRoot, "data", "adapters"),
		out:         externalUsagePath(repoRoot),
		workDir:     filepath.Join(repoRoot, ".cache"),
		curatedPath: filepath.Join(repoRoot, ".cache", "prices-raw.json"),
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		o.home = h
	} else if h, err := os.UserHomeDir(); err == nil {
		o.home = h
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--adapters":
			if i+1 < len(args) {
				i++
				o.adapters = args[i]
			}
		case "--out":
			if i+1 < len(args) {
				i++
				o.out = args[i]
			}
		case "--tools":
			if i+1 < len(args) {
				i++
				o.tools = args[i]
			}
		case "--home":
			if i+1 < len(args) {
				i++
				o.home = args[i]
			}
		case "--work-dir":
			if i+1 < len(args) {
				i++
				o.workDir = args[i]
			}
		case "--list":
			o.listOnly = true
		case "--validate":
			o.validateOnly = true
		case "--full":
			o.full = true
		}
	}

	var only []string
	if o.tools != "" {
		for _, t := range splitTrim(o.tools) {
			only = append(only, t)
		}
	}
	manifests, err := sources.LoadAdapters(o.adapters, only)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] 读适配器: %v\n", err)
		return 1
	}
	if len(manifests) == 0 {
		fmt.Fprintln(os.Stderr, "[error] 没有找到任何适配器")
		return 1
	}

	if o.validateOnly {
		bad := 0
		for _, m := range manifests {
			errs := sources.Validate(m)
			mark := "OK  "
			if len(errs) > 0 {
				mark = "FAIL"
				bad++
			}
			fmt.Printf("  %s %-28s %s\n", mark, m.ID, m.Label)
			for _, e := range errs {
				fmt.Printf("        - %s\n", e)
			}
		}
		fmt.Printf("\n%d 个适配器，%d 个有问题\n", len(manifests), bad)
		if bad > 0 {
			return 1
		}
		return 0
	}

	enabled := make([]*sources.Manifest, 0, len(manifests))
	for _, m := range manifests {
		if m.IsEnabled() {
			enabled = append(enabled, m)
		}
	}

	if o.listOnly {
		fmt.Printf("%-28s%-12s%-10s%s\n", "id", "kind", "driver", "state")
		for _, m := range manifests {
			state := "off"
			if m.IsEnabled() {
				state = "on"
			}
			fmt.Printf("%-28s%-12s%-10s%s\n", m.ID, m.KindOrDefault(),
				orDash(m.Driver), state)
		}
		return 0
	}

	if err := os.MkdirAll(o.workDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}

	statePath := filepath.Join(o.workDir, "collect-state.json")
	prevState := loadCollectState(statePath)
	var prevTools map[string]pricing.ToolUsage
	if prev, err := pricing.LoadExternalUsage(o.out); err == nil && prev != nil {
		prevTools = map[string]pricing.ToolUsage{}
		for _, t := range prev.Tools {
			prevTools[t.Tool] = t
		}
	}
	nextState := collectState{Tools: map[string]toolFingerprint{}}

	// Each source file belongs to exactly one adapter. Two manifests may
	// declare overlapping paths and on this machine they do: deepseek-harness
	// declares %DSH_HOME%, and DSH Desktop points that variable at its own
	// harness dir, so the "official" adapter matched all 286 wrapper files and
	// billed every one of them twice. Assignment happens before the loop so the
	// decision sees every adapter at once.
	claimCtx := &sources.Context{Home: o.home, WorkDir: o.workDir}
	valid := make([]*sources.Manifest, 0, len(enabled))
	for _, m := range enabled {
		if errs := sources.Validate(m); len(errs) > 0 {
			fmt.Printf("[!!] %s: 适配器定义有问题，跳过 —— %v\n", m.ID, errs)
			continue
		}
		valid = append(valid, m)
	}
	claims := sources.AssignFiles(valid, claimCtx)
	claimByID := map[string]sources.FileClaim{}
	for _, c := range claims {
		claimByID[c.Manifest.ID] = c
	}
	for _, line := range sources.OverlapLines(claims) {
		fmt.Println(line)
	}

	rows := make([]pricing.ToolUsage, 0, len(enabled))
	for _, m := range valid {
		ctx := &sources.Context{
			Home:      o.home,
			WorkDir:   o.workDir,
			Prefilter: m.PrefilterPatterns(),
			ForceFull: o.full,
		}
		files := claimByID[m.ID].Files
		fp := fingerprintFiles(files)
		nextState.Tools[m.ID] = fp

		// Incremental: reuse the previous tool row when no source file changed.
		if !o.full {
			if old, ok := prevState.Tools[m.ID]; ok && old == fp {
				if prev, ok := prevTools[m.ID]; ok && prev.Detected {
					fmt.Printf("[ok] %s: 源文件未变化，复用上次结果（%d 会话 · %d 次调用）\n",
						m.ID, prev.Sessions, prev.Calls)
					rows = append(rows, prev)
					continue
				}
			}
		}

		fmt.Printf("[..] %s: 解析 %d 个文件 …\n", m.ID, len(files))
		// A path whose %VAR% cannot be resolved stays a literal string and
		// silently matches nothing. Say so, otherwise the symptom is just
		// "this tool's numbers are missing" with nothing pointing at the cause.
		if missing := sources.MissingVarsFor(m, o.home); len(missing) > 0 {
			for id, vars := range missing {
				fmt.Printf("[warn] %s: 路径变量在本机无法解析，该适配器贡献为 0 —— 需要 %s\n",
					id, strings.Join(vars, ", "))
			}
		}

		agg, parsedFiles := sources.CollectAggregate(m, ctx, files)
		if parsedFiles >= 0 {
			// Say how much was actually read. With per-file caching "解析 N 个
			// 文件" is no longer the whole story: on an unchanged month of
			// sessions it is a handful, and that is the number worth watching.
			fmt.Printf("[ok] %s: 重读 %d/%d 个文件（其余命中部分聚合缓存）\n",
				m.ID, parsedFiles, len(files))
		}
		if !agg.Detected {
			continue
		}
		// python prepends a dedup summary to the adapter's own note so the
		// dashboard can show how many duplicate rows were dropped.
		// agg.Calls is the surviving record count - the same number len(records)
		// used to carry - so the line reads identically either way.
		note := agg.DedupNote
		if ctx.Dropped > 0 {
			note = fmt.Sprintf("按 %v 去重，丢弃重复行 %d 条（原始 %d 行 → %d 次调用）。%s",
				m.Dedup, ctx.Dropped, agg.Calls+ctx.Dropped, agg.Calls, note)
		}
		// home is the directory of the first file read, matching python's
		// m["_home"] = str(files[0].parent if files else "")
		home := ""
		if len(files) > 0 {
			// Fold the account name away: this lands in the dashboard payload
			// and is shown in the cross-tool section.
			home = pricing.RedactHome(filepath.Dir(files[0]), o.home)
		}
		rows = append(rows, pricing.ToolUsage{
			Tool: agg.Tool, Label: agg.Label, Home: home, Detected: true,
			Sessions: agg.Sessions, Calls: agg.Calls,
			Input: agg.Input, CacheRead: agg.CacheRead, CacheWrite: agg.CacheWrite,
			Output: agg.Output, Reasoning: agg.Reasoning,
			Models: convertModels(agg.Models), Days: convertDays(agg.Days),
			Hours:    convertDays(agg.Hours),
			DayModel: convertDays(agg.DayModel),
			FirstTs:  agg.FirstTs, LastTs: agg.LastTs,
			DedupNote: note, Files: len(files),
		})
		fmt.Printf("     %d 会话 · %d 次调用 · %.1fM billable token%s\n",
			agg.Sessions, agg.Calls,
			float64(agg.Input+agg.CacheRead+agg.CacheWrite+agg.Output)/1e6,
			map[bool]string{true: fmt.Sprintf("（去重丢弃 %d 条）", ctx.Dropped)}[ctx.Dropped > 0])
	}
	saveCollectState(statePath, nextState)

	sort.SliceStable(rows, func(i, j int) bool {
		return billable(rows[i]) > billable(rows[j])
	})

	// Price resolution: curated models.dev table only (.cache/prices-raw.json).
	// No gateway overlay.
	//
	// The table ages: models get added and repriced upstream on a weekly-ish
	// cadence, and a stale table does not announce itself - it just prices the
	// models you are actually running at $0. So refresh it here when it has aged
	// out. This is the single hook: `refresh` calls runCollect, and the desktop's
	// incremental Touch does too, so one placement covers every entry point.
	// maybeRefreshPrices never fails the run and never leaves the table missing -
	// a slow website must not stop a collect.
	if !hasFlag(args, "--no-prices") {
		_, _ = maybeRefreshPrices(priceOpts{
			RepoRoot:  findRepoRoot(),
			Out:       o.curatedPath,
			WorkDir:   o.workDir,
			IncludePi: true,
			Quiet:     quiet,
		}, pricing.DefaultPriceMaxAge, time.Now())
	}
	curated, _, hasCurated := pricing.LoadCurated(o.curatedPath)
	if !hasCurated {
		fmt.Fprintln(os.Stderr, "[warn] .cache/prices-raw.json 缺失，模型将多数标记为 unpriced")
	}
	prices := pricing.PriceModels(rows, curated)

	payload := pricing.ExternalUsage{
		GeneratedAt: time.Now().Format("2006-01-02 15:04"),
		Unit:        "token",
		Convention:  "input=非缓存输入；output 已含 reasoning；cost = input+output+cache_read+cache_write 四项计费",
		Framework:   "data/adapters/（声明式适配器；加工具 = 加一个 JSON）",
		Tools:       rows,
		Prices:      prices,
	}
	if err := writeJSON(o.out, payload); err != nil {
		fmt.Fprintf(os.Stderr, "[error] 写 %s: %v\n", o.out, err)
		return 1
	}

	fmt.Printf("\n[ok] %s\n", o.out)
	for _, t := range rows {
		fmt.Printf("  %-28s%5d 会话 %7d 调用 %9.1fM token\n",
			t.Label, t.Sessions, t.Calls, float64(billable(t))/1e6)
	}
	return 0
}

func billable(t pricing.ToolUsage) int64 {
	return t.Input + t.CacheRead + t.CacheWrite + t.Output
}

func convertModels(in map[string]*sources.ModelAgg) map[string]*pricing.ModelUsage {
	out := make(map[string]*pricing.ModelUsage, len(in))
	for k, v := range in {
		out[k] = &pricing.ModelUsage{
			Input: v.Input, CacheRead: v.CacheRead, CacheWrite: v.CacheWrite,
			Output: v.Output, Reasoning: v.Reasoning,
		}
	}
	return out
}

func convertDays(in map[string]*sources.ModelAgg) map[string]*pricing.ModelUsage {
	out := make(map[string]*pricing.ModelUsage, len(in))
	for k, v := range in {
		out[k] = &pricing.ModelUsage{
			Input: v.Input, CacheRead: v.CacheRead, CacheWrite: v.CacheWrite,
			Output: v.Output, Reasoning: v.Reasoning,
		}
	}
	return out
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func splitTrim(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, trimStr(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	if trimStr(cur) != "" {
		out = append(out, trimStr(cur))
	}
	return out
}

func trimStr(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ---- incremental collect ---------------------------------------------------

type toolFingerprint struct {
	FileCount int   `json:"fileCount"`
	MaxMTime  int64 `json:"maxMtime"`
	TotalSize int64 `json:"totalSize"`
}

type collectState struct {
	Tools map[string]toolFingerprint `json:"tools"`
}

func fingerprintFiles(files []string) toolFingerprint {
	var fp toolFingerprint
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		fp.FileCount++
		fp.TotalSize += st.Size()
		if mt := st.ModTime().Unix(); mt > fp.MaxMTime {
			fp.MaxMTime = mt
		}
	}
	return fp
}

func loadCollectState(path string) collectState {
	var st collectState
	raw, err := os.ReadFile(path)
	if err != nil {
		return collectState{Tools: map[string]toolFingerprint{}}
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.Tools == nil {
		return collectState{Tools: map[string]toolFingerprint{}}
	}
	return st
}

func saveCollectState(path string, st collectState) {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}
