// Package refresh implements incremental, read-time refresh of the local
// warehouse and external-tool aggregates. The desktop /api/dashboard path
// calls Touch before assembling so the UI tracks live agent logs without a
// full `tokanary refresh`.
package refresh

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fxbin/tokanary/internal/clisession"
	"github.com/fxbin/tokanary/internal/pidata"
	"github.com/fxbin/tokanary/internal/pricing"
	"github.com/fxbin/tokanary/internal/sources"
	"github.com/fxbin/tokanary/internal/warehouse"
)

// Result reports what Touch did.
type Result struct {
	WarehouseRebuilt bool   `json:"warehouseRebuilt"`
	ToolsParsed      int    `json:"toolsParsed"`
	ToolsReused      int    `json:"toolsReused"`
	Note             string `json:"note,omitempty"`
}

// Fingerprint is a cheap change detector over a set of files.
type Fingerprint struct {
	FileCount int   `json:"fileCount"`
	MaxMTime  int64 `json:"maxMtime"`
	TotalSize int64 `json:"totalSize"`
}

type state struct {
	Pi    Fingerprint            `json:"pi"`
	Tools map[string]Fingerprint `json:"tools"`
}

func loadState(path string) state {
	st := state{Tools: map[string]Fingerprint{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.Tools == nil {
		return state{Tools: map[string]Fingerprint{}}
	}
	return st
}

func saveState(path string, st state) {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}

func fingerprintFiles(files []string) Fingerprint {
	var fp Fingerprint
	for _, f := range files {
		s, err := os.Stat(f)
		if err != nil {
			continue
		}
		fp.FileCount++
		fp.TotalSize += s.Size()
		if mt := s.ModTime().Unix(); mt > fp.MaxMTime {
			fp.MaxMTime = mt
		}
	}
	return fp
}

// Touch incrementally refreshes external-usage.json and rebuilds the
// warehouse when the pi source changed. Safe to call on every dashboard poll.
func Touch(repoRoot string) (Result, error) {
	var res Result
	if repoRoot == "" {
		return res, fmt.Errorf("refresh: repoRoot required")
	}
	cache := filepath.Join(repoRoot, ".cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return res, err
	}
	statePath := filepath.Join(cache, "collect-state.json")
	st := loadState(statePath)

	extPath := filepath.Join(cache, "external-usage.json")
	if err := collectExternal(repoRoot, cache, extPath, &st, &res); err != nil {
		return res, err
	}

	src, err := detectPi()
	if err != nil {
		res.Note = "no pi source"
		saveState(statePath, st)
		return res, nil
	}
	piFP := fingerprintFiles([]string{src.DBPath, src.DBPath + "-wal", src.DBPath + "-shm"})
	if piFP != st.Pi {
		if err := rebuildWarehouse(repoRoot, cache, src); err != nil {
			return res, err
		}
		st.Pi = piFP
		res.WarehouseRebuilt = true
	}

	saveState(statePath, st)
	return res, nil
}

func detectPi() (pidata.Source, error) {
	found := pidata.DetectPiDirs()
	if len(found) == 0 {
		return pidata.Source{}, fmt.Errorf("no pi data directory")
	}
	return found[0], nil
}

func rebuildWarehouse(repoRoot, cache string, src pidata.Source) error {
	work := filepath.Join(cache, "refresh-work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	snap, err := pidata.Snapshot(src.Dir, work)
	if err != nil {
		return fmt.Errorf("pi snapshot: %w", err)
	}
	defer func() {
		if err := pidata.RemoveSnapshot(snap); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] 清理 pi 快照目录失败: %v\n", err)
		}
	}()

	data, err := pidata.Read(snap, src.DBPath)
	if err != nil {
		return fmt.Errorf("pi read: %w", err)
	}

	var cli *clisession.Result
	if dirs := cliSessionDirs(); len(dirs) > 0 {
		exclude := map[string]bool{}
		for _, s := range data.Sessions {
			exclude[s.ID] = true
		}
		res := clisession.Collect(dirs, exclude)
		if len(res.Sessions) > 0 || len(res.Turns) > 0 {
			cli = &res
		}
	}

	dbPath := filepath.Join(cache, "tokanary.sqlite")
	db, err := warehouse.DB(dbPath, data, cli)
	if err != nil {
		return fmt.Errorf("warehouse: %w", err)
	}
	return db.Close()
}

func cliSessionDirs() []string {
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

func collectExternal(repoRoot, cache, extPath string, st *state, res *Result) error {
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	manifests, err := sources.LoadAdapters(filepath.Join(repoRoot, "data", "adapters"), nil)
	if err != nil {
		return fmt.Errorf("load adapters: %w", err)
	}

	prevTools := map[string]pricing.ToolUsage{}
	if prev, err := pricing.LoadExternalUsage(extPath); err == nil && prev != nil {
		for _, t := range prev.Tools {
			prevTools[t.Tool] = t
		}
	}
	prevFP := map[string]Fingerprint{}
	for k, v := range st.Tools {
		prevFP[k] = v
	}
	nextTools := map[string]Fingerprint{}

	rows := make([]pricing.ToolUsage, 0, len(manifests))
	for _, m := range manifests {
		if !m.IsEnabled() {
			continue
		}
		if errs := sources.Validate(m); len(errs) > 0 {
			continue
		}
		ctx := &sources.Context{
			Home:      home,
			WorkDir:   cache,
			Prefilter: m.PrefilterPatterns(),
		}
		files := sources.FilesFor(m, ctx)
		fp := fingerprintFiles(files)
		nextTools[m.ID] = fp

		if old, ok := prevFP[m.ID]; ok && old == fp {
			if prev, ok := prevTools[m.ID]; ok && prev.Detected {
				rows = append(rows, prev)
				res.ToolsReused++
				continue
			}
		}

		records := sources.Run(m, ctx)
		agg := sources.Aggregate(records, m)
		res.ToolsParsed++
		if !agg.Detected {
			continue
		}
		note := agg.DedupNote
		if ctx.Dropped > 0 {
			note = fmt.Sprintf("按 %v 去重，丢弃重复行 %d 条。%s", m.Dedup, ctx.Dropped, note)
		}
		dirHome := ""
		if len(files) > 0 {
			dirHome = pricing.RedactHome(filepath.Dir(files[0]), home)
		}
		rows = append(rows, pricing.ToolUsage{
			Tool: agg.Tool, Label: agg.Label, Home: dirHome, Detected: true,
			Sessions: agg.Sessions, Calls: agg.Calls,
			Input: agg.Input, CacheRead: agg.CacheRead, CacheWrite: agg.CacheWrite,
			Output: agg.Output, Reasoning: agg.Reasoning,
			Models: convertModels(agg.Models), Days: convertDays(agg.Days),
			FirstTs: agg.FirstTs, LastTs: agg.LastTs,
			DedupNote: note, Files: len(files),
		})
	}
	st.Tools = nextTools

	curated, _, _ := pricing.LoadCurated(filepath.Join(cache, "prices-raw.json"))
	payload := pricing.ExternalUsage{
		GeneratedAt: time.Now().Format("2006-01-02 15:04"),
		Unit:        "token",
		Convention:  "input=非缓存输入；output 已含 reasoning；cost = input+output+cache_read+cache_write 四项计费",
		Framework:   "data/adapters/（声明式适配器）",
		Tools:       rows,
		Prices:      pricing.PriceModels(rows, curated),
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(extPath, b, 0o644)
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
	return convertModels(in)
}
