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
)

// priceOpts is the resolved shape of everything the table builder needs. It
// exists so the manual command and the automatic staleness check build the table
// through exactly one code path - two builders would drift, and the one that
// drifts is the unattended one.
type priceOpts struct {
	RepoRoot   string
	Out        string // absolute path of the table to write
	WorkDir    string // scratch dir for the pi snapshot
	URL        string // models.dev catalogue URL; empty means the default
	ModelsArg  string // explicit comma-separated ids, bypassing observation
	PiDir      string // pi source dir override
	IncludePi  bool
	ReportPath string
	Quiet      bool
}

type priceBuildResult struct {
	Path        string
	Models      int
	Exact       int
	Normalized  int
	Unmatched   []string
	Providers   int
	Catalogue   int
	WithInput   int
	ReplacedOld bool
}

func say(q bool, format string, a ...any) {
	if !q {
		fmt.Printf(format+"\n", a...)
	}
}

// maybeRefreshPrices re-fetches the table when it is older than maxAge.
//
// It never returns an error to its caller and never leaves the table missing or
// half-written: a network failure keeps the old table and prints one line. The
// whole point is that a collect run must not start failing because a website is
// slow - the previous table is a perfectly good fallback, just an old one.
func maybeRefreshPrices(o priceOpts, maxAge time.Duration, now time.Time) (bool, error) {
	st := pricing.InspectPrices(o.Out, maxAge, now)
	if !st.Exists {
		// No table at all: generate one, because without it every cost is $0 and
		// a silently-zero dashboard is worse than a slow one.
		if _, err := buildPriceTable(o); err != nil {
			return false, err
		}
		return true, nil
	}
	if !st.Stale {
		return false, nil
	}
	say(o.Quiet, "  [prices] %s，重新抓取 models.dev ...", st.Reason)
	res, err := buildPriceTable(o)
	if err != nil {
		// Keep going with the old table. Say so, and say how to retry by hand.
		fmt.Fprintf(os.Stderr, "  [warn] 价表更新失败（%v）；继续使用 %d 天前的旧表。\n", err, int(st.Age.Hours()/24))
		fmt.Fprintf(os.Stderr, "         需要时手动重试：tokanary prices --force\n")
		return false, nil
	}
	say(o.Quiet, "  [ok] 价表已更新：%d 个模型（原 %s）", res.Models, st.FetchedAt)
	return true, nil
}

func buildPriceTable(o priceOpts) (priceBuildResult, error) {
	var res priceBuildResult
	res.Path = o.Out

	observed := map[string]string{} // model id -> where it came from
	if o.ModelsArg != "" {
		for _, m := range splitTrim(o.ModelsArg) {
			observed[m] = "--models"
		}
	}
	// External ids come from the previous collect's output. A brand-new external
	// model therefore lands in the table one collect late - at a 7-day cadence
	// that is noise, and re-deriving them here would mean parsing the sources
	// twice per collect.
	extPath := externalUsagePath(o.RepoRoot)
	if raw, err := os.ReadFile(extPath); err == nil {
		var doc struct {
			Tools []struct {
				Tool   string `json:"tool"`
				Models map[string]struct {
					RawIDs []string `json:"rawIds"`
				} `json:"models"`
			} `json:"tools"`
		}
		if json.Unmarshal(raw, &doc) == nil {
			for _, t := range doc.Tools {
				for _, m := range t.Models {
					for _, mid := range m.RawIDs {
						if _, ok := observed[mid]; !ok {
							observed[mid] = t.Tool
						}
					}
				}
			}
		}
	}
	if o.IncludePi {
		if ids, err := piModelIDs(o.PiDir, o.WorkDir); err == nil {
			for _, m := range ids {
				if _, ok := observed[m]; !ok {
					observed[m] = "pi"
				}
			}
		} else if !o.Quiet {
			fmt.Fprintf(os.Stderr, "  [warn] 读 pi 模型 id 失败：%v\n", err)
		}
	}
	if len(observed) == 0 {
		return res, fmt.Errorf("没有观测到任何模型 id。跑 tokanary collect，或用 --models 显式指定")
	}

	ids := make([]string, 0, len(observed))
	for id := range observed {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	say(o.Quiet, "[..] 观测到 %d 个模型 id，拉取 models.dev ...", len(ids))
	ix, err := pricing.FetchModelsDev(o.URL)
	if err != nil {
		return res, err
	}
	providers, withInput := ix.Stats()
	res.Providers, res.Catalogue, res.WithInput = providers, ix.Count, withInput
	say(o.Quiet, "[ok] models.dev: %d 个 provider / %d 个模型（%d 个带 input 价）", providers, ix.Count, withInput)

	rows := map[string]*priceRow{}
	var unmatched []string
	exact, normalized := 0, 0

	for _, id := range ids {
		md, matched, ok := ix.Resolve(id)
		if !ok {
			unmatched = append(unmatched, id)
			continue
		}
		if equalFoldOrEqual(id, matched) || id == md.FullID || id == md.Model {
			exact++
		} else {
			normalized++
		}
		row, seen := rows[md.FullID]
		if !seen {
			row = &priceRow{
				target: md.FullID, modelsDevID: md.FullID, provider: md.Provider,
				cost: md.Cost, confidence: pricing.NormalizeConfidence(id, matched, id == md.FullID),
				sourceURL: md.SourceURL,
			}
			rows[md.FullID] = row
		}
		row.piIDs = append(row.piIDs, id)
	}
	res.Exact, res.Normalized, res.Unmatched = exact, normalized, unmatched

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
		return res, fmt.Errorf("序列化: %w", err)
	}
	blob = append(blob, '\n')

	if _, err := os.Stat(o.Out); err == nil {
		if bak := mustRead(o.Out); bak != nil {
			_ = os.WriteFile(o.Out+".bak", bak, 0o644)
		}
		res.ReplacedOld = true
	}
	if err := pricing.AtomicWriteFile(o.Out, blob); err != nil {
		return res, fmt.Errorf("写入 %s: %w", o.Out, err)
	}
	res.Models = len(doc.Models)

	if o.ReportPath != "" {
		if !filepath.IsAbs(o.ReportPath) {
			o.ReportPath = filepath.Join(o.RepoRoot, o.ReportPath)
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
			_ = os.WriteFile(o.ReportPath, append(b, '\n'), 0o644)
			say(o.Quiet, "[ok] 报告 %s", o.ReportPath)
		}
	}
	return res, nil
}

func equalFoldOrEqual(a, b string) bool { return a == b || strings.EqualFold(a, b) }
