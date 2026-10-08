// Package dashboard assembles the dashboard payload from the local warehouse
// and price tables. The desktop window and the CLI both use this path, so the
// numbers on screen always come from the SQLite store rather than a frozen
// script snapshot.
package dashboard

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fxbin/tokanary/internal/ai"
	"github.com/fxbin/tokanary/internal/pricing"
	"github.com/fxbin/tokanary/internal/warehouse"
	"github.com/fxbin/tokanary/internal/yield"
)

// DefaultDBPath is the analysis store under .cache/.
const DefaultDBPath = ".cache/tokanary.sqlite"

// DefaultExternalPath is the collect -> assemble handoff file.
const DefaultExternalPath = ".cache/external-usage.json"

// Options tunes where Assemble looks for inputs. Zero values use the defaults
// relative to RepoRoot.
type Options struct {
	RepoRoot     string
	DBPath       string
	ExternalPath string
	PricesPath   string
	NoExternal   bool
	AIEnable     bool
	// Meta, when non-nil, replaces the meta block built from defaults. refresh
	// passes a block that records the real pi source paths, schema version and
	// sizes; the desktop live path leaves it nil and gets a redacted stub.
	Meta *pricing.Meta
}

// Assemble reads the warehouse plus price tables and returns the dashboard
// payload. It does not ingest pi sources — run `tokanary refresh` (or build)
// first so the warehouse is current.
func Assemble(opt Options) (*pricing.DashboardPayload, error) {
	root := opt.RepoRoot
	if root == "" {
		return nil, fmt.Errorf("dashboard: RepoRoot is required")
	}
	dbPath := opt.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(root, DefaultDBPath)
	} else if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(root, dbPath)
	}
	extPath := opt.ExternalPath
	if extPath == "" {
		extPath = filepath.Join(root, DefaultExternalPath)
	} else if !filepath.IsAbs(extPath) {
		extPath = filepath.Join(root, extPath)
	}
	pricesPath := opt.PricesPath
	if pricesPath == "" {
		pricesPath = filepath.Join(root, ".cache", "prices-raw.json")
	} else if !filepath.IsAbs(pricesPath) {
		pricesPath = filepath.Join(root, pricesPath)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开仓库 %s: %w", dbPath, err)
	}
	defer db.Close()

	usage, err := warehouse.ExportUsage(db)
	if err != nil {
		return nil, fmt.Errorf("导出用量: %w", err)
	}

	return AssembleFromUsage(opt, usage, warehouse.CLIStats(db))
}

// AssembleFromUsage builds the payload from an already-exported usage block.
// build/refresh use this after ingesting sources into the warehouse.

// spanRange returns the earliest/latest day across pi days and external tool days.
func spanRange(payload *pricing.DashboardPayload) (start, end string) {
	day := func(v any) string {
		switch t := v.(type) {
		case string:
			if len(t) >= 10 {
				return t[:10]
			}
			return t
		}
		return ""
	}
	take := func(d string) {
		if d == "" {
			return
		}
		if start == "" || d < start {
			start = d
		}
		if end == "" || d > end {
			end = d
		}
	}
	if list, ok := payload.Days.([]any); ok {
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				take(day(m["d"]))
			}
		}
	}
	if payload.External != nil {
		for _, t := range payload.External.Tools {
			for _, d := range t.Days {
				take(day(d.D))
			}
		}
	}
	return start, end
}

func AssembleFromUsage(opt Options, usage *warehouse.Usage, cliStats map[string]string) (*pricing.DashboardPayload, error) {
	root := opt.RepoRoot
	pricesPath := opt.PricesPath
	if pricesPath == "" {
		pricesPath = filepath.Join(root, ".cache", "prices-raw.json")
	}

	curated, fetchedAt, hasCurated := pricing.LoadCurated(pricesPath)
	curatedFile := ""
	if hasCurated {
		curatedFile = ".cache/prices-raw.json"
	}

	piModels := map[string]*pricing.PiModel{}
	for k, m := range usage.Models {
		ru := map[string]pricing.RawUsage{}
		for rid, r := range m.RawUsage {
			ru[rid] = pricing.RawUsage{
				Turns: r.Turns, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite,
				Input: r.Input, Output: r.Output, Reasoning: r.Reasoning, Total: r.Total,
			}
		}
		piModels[k] = &pricing.PiModel{
			Key: k, RawIDs: m.RawIDs, Turns: m.Turns, MissingUsage: m.MissingUage,
			Statuses: m.Statuses,
			FirstTs:  pricing.NullableInt{V: m.FirstTs.Int64, OK: m.FirstTs.Valid},
			LastTs:   pricing.NullableInt{V: m.LastTs.Int64, OK: m.LastTs.Valid},
			Input:    m.Input, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite,
			Output: m.Output, Reasoning: m.Reasoning, Total: m.Total, RawUsage: ru,
		}
	}
	pricingMap, pricingMeta, modelRows := pricing.BuildPricing(
		piModels, curated, fetchedAt, curatedFile)

	var external *pricing.External
	if !opt.NoExternal {
		extPath := opt.ExternalPath
		if extPath == "" {
			extPath = filepath.Join(root, DefaultExternalPath)
		}
		if _, err := os.Stat(extPath); err == nil {
			eu, err := pricing.LoadExternalUsage(extPath)
			if err != nil {
				return nil, fmt.Errorf("读 external-usage.json: %w", err)
			}
			external = pricing.BuildExternal(eu)
		}
	}

	var cli any
	if len(cliStats) > 0 {
		cli = cliStats
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

	// Live desktop path: describe the warehouse + adapters, not a single pi
	// sqlite. refresh/build pass a richer Meta that records the real source.
	warehouseLabel := filepath.Join(".cache", "tokanary.sqlite")
	if st, err := os.Stat(filepath.Join(root, DefaultDBPath)); err == nil {
		warehouseLabel = fmt.Sprintf("本地仓库（%.1f MB）", float64(st.Size())/1048576)
	}
	nTools := 0
	if external != nil && len(external.Tools) > 0 {
		nTools = len(external.Tools)
	}
	sourceLabel := "pi 本地库"
	if nTools > 0 {
		sourceLabel = fmt.Sprintf("pi + %d 个 CLI 适配器", nTools)
	}

	meta := pricing.BuildMeta(
		time.Now().Format("2006-01-02T15:04:05-07:00"),
		warehouseLabel, sourceLabel, sourceLabel, 0, 0, 0,
		firstTs, lastTs, cli, home)
	if opt.Meta != nil {
		meta = *opt.Meta
		if home != "" {
			// Caller-supplied meta already went through BuildMeta in refresh.
			// Keep it as-is so source paths stay accurate.
		}
	}

	usageJSON, err := json.Marshal(usage)
	if err != nil {
		return nil, err
	}
	var um map[string]any
	_ = json.Unmarshal(usageJSON, &um)

	payload := &pricing.DashboardPayload{
		Meta:         meta,
		Totals:       um["totals"],
		Models:       modelRows,
		Days:         um["days"],
		DayModel:     um["dayModel"],
		Hours:        um["hours"],
		Projects:     um["projects"],
		Sessions:     um["sessions"],
		SessionsAll:  um["sessionsAll"],
		SessionCount: um["sessionCount"],
		Roles:        um["roles"],
		Tools:        um["tools"],
		Pricing:      pricingMap,
		PricingMeta:  pricingMeta,
		External:     external,
		Yield:        yield.ProjectGit(projectRoots(home, usage)),
	}
	if opt.AIEnable {
		var items []ai.TitleTokens
		if list, ok := um["sessions"].([]any); ok {
			for _, it := range list {
				if m, ok := it.(map[string]any); ok {
					title, _ := m["title"].(string)
					tok, _ := m["total"].(float64)
					items = append(items, ai.TitleTokens{Title: title, Tool: "pi", Tokens: int64(tok)})
				}
			}
		}
		if cats, err := ai.ClassifyTitles(ai.LoadConfig(), true, items); err == nil && len(cats) > 0 {
			payload.TaskCategories = cats
		}
	}
	if rs, re := spanRange(payload); rs != "" {
		payload.Meta.RangeStart = rs
		payload.Meta.RangeEnd = re
	}
	return payload, nil
}

// projectRoots resolves a project name to the directory its git activity lives
// in.
//
// The warehouse is the authority: it carries the working directory pi recorded
// for every project, on every platform, and it is already open on this path.
// yield.DiscoverRoots only contributes the two optional third-party agent
// databases, so it is consulted first and then overridden - it guesses from
// `session.directory`, which is a different agent's idea of where work happened
// and can disagree with pi's own record.
func projectRoots(home string, usage *warehouse.Usage) map[string]string {
	roots := yield.DiscoverRoots(home)
	if usage == nil {
		return roots
	}
	for name, path := range usage.ProjectPaths {
		if path != "" {
			roots[name] = path
		}
	}
	return roots
}

// Meta is an optional pre-built meta block (refresh knows the real source paths).
// Stored on Options via the Meta field below.
type Meta = pricing.Meta

// WriteJSON renders the payload as a plain JSON file.
func WriteJSON(path string, d *pricing.DashboardPayload) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, b, 0o644)
}
