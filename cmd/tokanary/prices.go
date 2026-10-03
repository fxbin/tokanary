package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fxbin/tokanary/internal/clisession"
	"github.com/fxbin/tokanary/internal/pidata"
	"github.com/fxbin/tokanary/internal/pricing"
	"github.com/fxbin/tokanary/internal/warehouse"
)

// curatedDoc is the on-disk shape of .cache/prices-raw.json. It is written so the
// result loads back through pricing.LoadCurated unchanged.
type curatedDoc struct {
	FetchedAt string         `json:"fetchedAt"`
	Source    string         `json:"source"`
	Note      string         `json:"note"`
	Models    []curatedModel `json:"models"`
}

type curatedModel struct {
	Target      string           `json:"target"`
	DisplayName string           `json:"displayName"`
	ModelsDevID string           `json:"modelsDevId"`
	Provider    string           `json:"provider"`
	PiIDs       []string         `json:"piIds"`
	Cost        pricing.Cost     `json:"cost"`
	Confidence  string           `json:"confidence"`
	SourceURL   string           `json:"sourceUrl"`
	Note        string           `json:"note"`
	Variants    []curatedVariant `json:"variants,omitempty"`
}

type curatedVariant struct {
	Label    string       `json:"label"`
	Provider string       `json:"provider"`
	Cost     pricing.Cost `json:"cost"`
}

type priceRow struct {
	target      string
	modelsDevID string
	provider    string
	piIDs       []string
	cost        pricing.Cost
	confidence  string
	sourceURL   string
}

func runPrices(args []string) int {
	var (
		out        = filepath.Join(".cache", "prices-raw.json")
		modelsArg  string
		url        string
		force      bool
		includePi  = true
		reportPath string
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
		case "--force":
			force = true
		case "--no-pi":
			includePi = false
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

	// ---- guard: never silently clobber a curated file
	if _, err := os.Stat(out); err == nil && !force {
		fmt.Fprintf(os.Stderr, "[error] %s 已存在。加 --force 覆盖（会先写一份 .bak），或换个 --out。\n", out)
		return 1
	}

	// ---- gather the model ids actually in use
	observed := map[string]string{} // model id -> where it came from
	if modelsArg != "" {
		for _, m := range splitTrim(modelsArg) {
			observed[m] = "--models"
		}
	}

	extPath := externalUsagePath(repoRoot)
	if ext, err := pricing.LoadExternalUsage(extPath); err == nil && ext != nil {
		for _, t := range ext.Tools {
			for mid := range t.Models {
				if mid == "" {
					continue
				}
				if _, ok := observed[mid]; !ok {
					observed[mid] = t.Tool
				}
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "[warn] 读不到 %s，仅按 --models 生成。先跑一次 tokanary collect。\n", extPath)
	}

	if includePi {
		if ids, err := piModelIDs(piDir, workDir); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] 取 pi 模型清单失败: %v\n", err)
		} else {
			for _, m := range ids {
				if _, ok := observed[m]; !ok {
					observed[m] = "pi"
				}
			}
		}
	}

	if len(observed) == 0 {
		fmt.Fprintln(os.Stderr, "[error] 没有观测到任何模型 id。跑 tokanary collect，或用 --models 显式指定。")
		return 1
	}

	ids := make([]string, 0, len(observed))
	for id := range observed {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic output regardless of map order

	fmt.Printf("[..] 观测到 %d 个模型 id，拉取 models.dev ...\n", len(ids))
	ix, err := pricing.FetchModelsDev(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		return 1
	}
	providers, withInput := ix.Stats()
	fmt.Printf("[ok] models.dev: %d 个 provider / %d 个模型（%d 个带 input 价）\n", providers, ix.Count, withInput)

	// ---- resolve, grouping every observed alias onto one catalogue entry
	rows := map[string]*priceRow{} // modelsDevID -> row
	var unmatched []string
	exact, normalized := 0, 0

	for _, id := range ids {
		md, matched, ok := ix.Resolve(id)
		if !ok {
			unmatched = append(unmatched, id)
			continue
		}
		if strings.EqualFold(id, matched) || id == md.FullID || id == md.Model {
			exact++
		} else {
			normalized++
		}
		row, seen := rows[md.FullID]
		if !seen {
			row = &priceRow{
				target:      md.Model,
				modelsDevID: md.FullID,
				provider:    md.Provider,
				cost:        md.Cost,
				confidence:  pricing.NormalizeConfidence(id, matched, id == md.FullID),
				sourceURL:   md.SourceURL,
			}
			rows[md.FullID] = row
		}
		row.piIDs = append(row.piIDs, id)
	}

	// stable order: by target
	order := make([]string, 0, len(rows))
	for k := range rows {
		order = append(order, k)
	}
	sort.Strings(order)
	used := map[string]bool{}

	doc := curatedDoc{
		FetchedAt: time.Now().Format("2006-01-02"),
		Source:    "models.dev",
		Note: "由 `tokanary prices` 依据 models.dev 公开目录生成。单位：USD / 每 100 万 token。" +
			"target 与 piIds 为本机模型别名映射，cost 为目录原值；variants 仅在目录给出长上下文分档时才生成。",
	}
	for _, k := range order {
		r := rows[k]
		sort.Strings(r.piIDs)
		// pricing.LoadCurated keys the table by target, so two rows sharing a
		// target would silently drop one. Disambiguate with the provider.
		target := r.target
		if used[target] {
			target = r.provider + "/" + r.target
		}
		used[target] = true
		doc.Models = append(doc.Models, curatedModel{
			Target:      target,
			DisplayName: target,
			ModelsDevID: r.modelsDevID,
			Provider:    r.provider,
			PiIDs:       r.piIDs,
			Cost:        r.cost,
			Confidence:  r.confidence,
			SourceURL:   r.sourceURL,
			Note: fmt.Sprintf("models.dev %s；本机别名 %s 经归一化匹配。",
				r.modelsDevID, strings.Join(r.piIDs, ", ")),
		})
	}

	blob, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] 序列化: %v\n", err)
		return 1
	}
	blob = append(blob, '\n')

	if _, err := os.Stat(out); err == nil {
		bak := out + ".bak"
		if err := os.WriteFile(bak, mustRead(out), 0o644); err == nil {
			fmt.Printf("[..] 旧文件已备份到 %s\n", filepath.Base(bak))
		}
	}
	if err := os.WriteFile(out, blob, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "[error] 写入 %s: %v\n", out, err)
		return 1
	}

	fmt.Printf("[ok] %s  (%d 个模型，精确 %d / 归一化 %d)\n",
		filepath.Base(out), len(doc.Models), exact, normalized)

	if len(unmatched) > 0 {
		fmt.Printf("[warn] %d 个模型在 models.dev 里没有对应项，已跳过：\n", len(unmatched))
		for _, u := range unmatched {
			fmt.Printf("       %-42s (来自 %s)\n", u, observed[u])
		}
		fmt.Println("       这些模型会显示为 unpriced。可手写一行补进上表，或改用 --models 精确指定。")
	}

	if reportPath != "" {
		if !filepath.IsAbs(reportPath) {
			reportPath = filepath.Join(repoRoot, reportPath)
		}
		rep := map[string]any{
			"generatedAt": time.Now().Format(time.RFC3339),
			"catalogue":   map[string]any{"providers": providers, "models": ix.Count, "withInputPrice": withInput},
			"matched":     len(doc.Models),
			"exact":       exact,
			"normalized":  normalized,
			"unmatched":   unmatched,
			"observed":    observed,
		}
		if b, err := json.MarshalIndent(rep, "", "  "); err == nil {
			_ = os.WriteFile(reportPath, append(b, '\n'), 0o644)
			fmt.Printf("[ok] 报告 %s\n", reportPath)
		}
	}
	return 0
}

func mustRead(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// piModelIDs lists the distinct model ids present in the pi warehouse. It goes
// through the same snapshot + warehouse path as `tokanary build` so the live
// pi.sqlite is never opened for writing - build's warehouse.DB deletes the db
// file it is given, and handing it the live source would fail while pi is
// running.
func piModelIDs(piDir, workDir string) ([]string, error) {
	src, err := resolveSource(piDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	snap, err := pidata.Snapshot(src.Dir, workDir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(filepath.Join(workDir, "pi-snapshot"))

	data, err := pidata.Read(snap, src.DBPath)
	if err != nil {
		return nil, err
	}
	cliDirs := defaultCLIDirs()
	var cli *clisession.Result
	if len(cliDirs) > 0 {
		exclude := map[string]bool{}
		for _, s := range data.Sessions {
			exclude[s.ID] = true
		}
		res := clisession.Collect(cliDirs, exclude)
		if len(res.Sessions) > 0 || len(res.Turns) > 0 {
			cli = &res
		}
	}

	dbPath := filepath.Join(workDir, "tokanary-prices.sqlite")
	db, err := warehouse.DB(dbPath, data, cli)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT DISTINCT model_canon FROM turns WHERE model_canon <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}
