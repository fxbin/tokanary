package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fxbin/tokanary/internal/pricing"
)

// runPrices is the manual entry point. It resolves flags and guards, then hands
// the work to buildPriceTable - the same builder the automatic staleness check
// uses, so there is one implementation of "make a price table" and not two that
// have to be kept in agreement.
func runPrices(args []string) int {
	var (
		out        = filepath.Join(".cache", "prices-raw.json")
		modelsArg  string
		url        string
		force      bool
		ifStale    bool
		includePi  = true
		reportPath string
		maxAge     = pricing.DefaultPriceMaxAge
	)
	piDir := ""
	workDir := ".cache"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			if i+1 < len(args) {
				i++
				out = args[i]
			}
		case "--models":
			if i+1 < len(args) {
				i++
				modelsArg = args[i]
			}
		case "--url":
			if i+1 < len(args) {
				i++
				url = args[i]
			}
		case "--pi-dir":
			if i+1 < len(args) {
				i++
				piDir = args[i]
			}
		case "--db":
			if i+1 < len(args) {
				i++
				workDir = args[i]
			}
		case "--work-dir":
			if i+1 < len(args) {
				i++
				workDir = args[i]
			}
		case "--report":
			if i+1 < len(args) {
				i++
				reportPath = args[i]
			}
		case "--max-age-days":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n >= 0 {
					maxAge = time.Duration(n) * 24 * time.Hour
				} else {
					fmt.Fprintf(os.Stderr, "[error] --max-age-days 需要一个非负整数\n")
					return 1
				}
			}
		case "--force":
			force = true
		case "--if-stale":
			ifStale = true
		case "--no-pi":
			includePi = false
		case "--status":
			return printPriceStatus(out, maxAge)
		}
	}

	repoRoot := findRepoRoot()
	if repoRoot == "" {
		fmt.Fprintln(os.Stderr, "[error] 找不到仓库根（需要包含 data/adapters 的目录）")
		return 1
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(repoRoot, out)
	}

	opts := priceOpts{
		RepoRoot: repoRoot, Out: out, WorkDir: workDir, URL: url,
		ModelsArg: modelsArg, PiDir: piDir, IncludePi: includePi, ReportPath: reportPath,
	}

	// --if-stale is the everyday form: regenerate only when the table has aged
	// out, so it is safe to wire into an unattended path. A fresh table is left
	// exactly as it is, and the run exits without touching the network.
	if ifStale {
		updated, err := maybeRefreshPrices(opts, maxAge, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			return 1
		}
		if !updated {
			fmt.Println("[ok] 价表仍在有效期内，未更新。")
		}
		return 0
	}

	// ---- guard: never silently clobber a curated file
	if _, err := os.Stat(out); err == nil && !force {
		st := pricing.InspectPrices(out, maxAge, time.Now())
		fmt.Fprintf(os.Stderr, "[error] %s 已存在（%s）。加 --force 覆盖（会先写一份 .bak），或换 --out。\n",
			out, st.FetchedAt)
		if st.Stale {
			fmt.Fprintf(os.Stderr, "       它已经过期：%s。也可以用 --if-stale 只在过期时更新。\n", st.Reason)
		} else {
			fmt.Fprintf(os.Stderr, "       它还有 %d 天有效期（阈值 --max-age-days，默认 7）。\n",
				int((maxAge-st.Age).Hours()/24))
		}
		return 1
	}

	res, err := buildPriceTable(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	fmt.Printf("[ok] %s  (%d 个模型，精确 %d / 归一化 %d)\n",
		filepath.Base(out), res.Models, res.Exact, res.Normalized)
	if len(res.Unmatched) > 0 {
		fmt.Printf("[warn] %d 个模型在 models.dev 里没有对应项，已跳过：\n", len(res.Unmatched))
		for _, u := range res.Unmatched {
			fmt.Printf("       %-42s\n", u)
		}
		fmt.Println("       这些模型会显示为 unpriced。可在模型页手动填价。")
	}
	return 0
}

// printPriceStatus answers "how old is my price table" without touching the
// network, so it is safe to run at any time.
func printPriceStatus(out string, maxAge time.Duration) int {
	repoRoot := findRepoRoot()
	if repoRoot == "" {
		fmt.Fprintln(os.Stderr, "[error] 找不到仓库根（需要包含 data/adapters 的目录）")
		return 1
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(repoRoot, out)
	}
	st := pricing.InspectPrices(out, maxAge, time.Now())
	if !st.Exists {
		fmt.Printf("[..] 没有价表（%s）。生成：tokanary prices\n", out)
		return 0
	}
	state := "有效"
	if st.Stale {
		state = "已过期"
	}
	fmt.Printf("[%s] %s\n  抓取于 %s（%d 天前）· 有效期阈值 %d 天\n",
		state, filepath.Base(out), st.FetchedAt, int(st.Age.Hours()/24), int(maxAge.Hours()/24))
	// The table is now rewritten unattended every 7 days, which makes the
	// previous copy the only way back if models.dev publishes something broken.
	// So say it exists and say what it is - otherwise it is just a stray file.
	if prev := tableStamp(out + ".bak"); prev != "" {
		fmt.Printf("  回滚副本 %s（抓取于 %s）\n", filepath.Base(out)+".bak", prev)
	}
	if st.Stale {
		fmt.Println("  更新：tokanary prices --force")
	}
	return 0
}

// tableStamp reads just the fetchedAt stamp out of a table file.
func tableStamp(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		FetchedAt any `json:"fetchedAt"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	s, _ := doc.FetchedAt.(string)
	return s
}

var _ = strings.TrimSpace
