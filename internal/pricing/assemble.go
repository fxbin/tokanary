package pricing

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"database/sql"
)

// PricingMeta describes where the curated price table came from.
type PricingMeta struct {
	Source    string `json:"source"`
	FetchedAt any    `json:"fetchedAt"`
	File      any    `json:"file"`
}

// NullableInt marshals as a JSON number or null, matching python's
// `int | None`. sql.NullInt64 cannot be used here: it serialises as a
// {"Int64":..,"Valid":..} struct, which no python-produced data.js ever
// contains.
type NullableInt struct {
	V  int64
	OK bool
}

// MarshalJSON renders the value, or null when absent.
func (n NullableInt) MarshalJSON() ([]byte, error) {
	if !n.OK {
		return []byte("null"), nil
	}
	return json.Marshal(n.V)
}

func nullable(v sql.NullInt64) NullableInt { return NullableInt{V: v.Int64, OK: v.Valid} }

// ModelListRow is one entry of data.js's models array.
type ModelListRow struct {
	Key          string           `json:"key"`
	RawIDs       []string         `json:"rawIds"`
	Turns        int64            `json:"turns"`
	MissingUsage int64            `json:"missingUsage"`
	Statuses     map[string]int64 `json:"statuses"`
	FirstTs      NullableInt      `json:"firstTs"`
	LastTs       NullableInt      `json:"lastTs"`
	CacheRead    int64            `json:"cacheRead"`
	CacheWrite   int64            `json:"cacheWrite"`
	Input        int64            `json:"input"`
	Output       int64            `json:"output"`
	Reasoning    int64            `json:"reasoning"`
	Total        int64            `json:"total"`
	Priced       bool             `json:"priced"`
	RawUsage     []RawListRow     `json:"rawUsage"`
}

// RawListRow is one raw-id line inside a model's rawUsage array.
type RawListRow struct {
	ID         string `json:"id"`
	Turns      int64  `json:"turns"`
	Total      int64  `json:"total"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
}

// PiModel is the warehouse-side model shape the pricing pass consumes.
type PiModel struct {
	Key          string
	RawIDs       []string
	Turns        int64
	MissingUsage int64
	Statuses     map[string]int64
	FirstTs      NullableInt
	LastTs       NullableInt
	Input        int64
	CacheRead    int64
	CacheWrite   int64
	Output       int64
	Reasoning    int64
	Total        int64
	RawUsage     map[string]RawUsage
}

// LoadCurated reads .cache/prices-raw.json into target -> raw entry.
func LoadCurated(path string) (map[string]json.RawMessage, any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	var doc struct {
		FetchedAt any               `json:"fetchedAt"`
		Models    []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, false
	}
	out := map[string]json.RawMessage{}
	for _, m := range doc.Models {
		var probe struct {
			Target string `json:"target"`
		}
		if err := json.Unmarshal(m, &probe); err != nil || probe.Target == "" {
			continue
		}
		out[probe.Target] = m
	}
	return out, doc.FetchedAt, true
}

func costHasInput(c *Cost) bool { return c != nil && c.Input != nil }

// BuildPricing assembles the pricing block from the curated models.dev table
// plus the per-model list the dashboard renders.
func BuildPricing(
	models map[string]*PiModel,
	curated map[string]json.RawMessage,
	curatedFetchedAt any,
	curatedFile string,
) (map[string]json.RawMessage, PricingMeta, []ModelListRow) {
	pricing := map[string]json.RawMessage{}
	meta := PricingMeta{Source: "models.dev", FetchedAt: curatedFetchedAt, File: nil}
	if curatedFile != "" {
		meta.File = curatedFile
	}
	for target, raw := range curated {
		pricing[target] = raw
	}

	// per-model list, sorted by total descending
	rows := make([]ModelListRow, 0, len(models))
	for key, m := range models {
		var pr *Cost
		if raw, ok := pricing[key]; ok {
			var e struct {
				Cost *Cost `json:"cost"`
			}
			if err := json.Unmarshal(raw, &e); err == nil {
				pr = e.Cost
			}
		}
		rawRows := make([]RawListRow, 0, len(m.RawUsage))
		for rid, ru := range m.RawUsage {
			rawRows = append(rawRows, RawListRow{
				ID: rid, Turns: ru.Turns, Total: ru.Total, Input: ru.Input,
				Output: ru.Output, CacheRead: ru.CacheRead, CacheWrite: ru.CacheWrite,
			})
		}
		sort.SliceStable(rawRows, func(i, j int) bool {
			return rawRows[i].Total > rawRows[j].Total
		})
		rawIDs := append([]string{}, m.RawIDs...)
		sort.Strings(rawIDs)
		statuses := m.Statuses
		if statuses == nil {
			statuses = map[string]int64{}
		}
		rows = append(rows, ModelListRow{
			Key: key, RawIDs: rawIDs, Turns: m.Turns, MissingUsage: m.MissingUsage,
			Statuses: statuses, FirstTs: m.FirstTs, LastTs: m.LastTs,
			CacheRead: m.CacheRead, CacheWrite: m.CacheWrite, Input: m.Input,
			Output: m.Output, Reasoning: m.Reasoning, Total: m.Total,
			Priced: costHasInput(pr), RawUsage: rawRows,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Total > rows[j].Total })
	return pricing, meta, rows
}

// Meta is the data.js meta block.
type Meta struct {
	GeneratedAt string  `json:"generatedAt"`
	DBPath      string  `json:"dbPath"`
	PiDir       string  `json:"piDir"`
	SourceNote  any     `json:"sourceNote"`
	DBVersion   int64   `json:"dbVersion"`
	DBSizeMB    float64 `json:"dbSizeMB"`
	WALSizeMB   float64 `json:"walSizeMB"`
	RangeStart  any     `json:"rangeStart"`
	RangeEnd    any     `json:"rangeEnd"`
	CLI         any     `json:"cli"`
}

// RedactHome rewrites a path that sits under the user's home directory to use
// a leading ~.
//
// The dashboard renders meta.dbPath in its footer. An absolute path carries the
// account name, so a shared screenshot or an accidentally committed payload
// leaks it. The directory is still worth showing - ".pi-desktop vs .pi tells you
// which of two overlapping sources was read - so only the home prefix is folded
// away.
//
// Comparison is separator-agnostic so a Windows-style path still folds on a
// Unix host (tests assert the Windows shapes explicitly).
func RedactHome(p, home string) string {
	if p == "" || home == "" {
		return p
	}
	pNorm := normalizeSlashes(pathClean(p))
	homeNorm := normalizeSlashes(pathClean(home))
	if strings.EqualFold(pNorm, homeNorm) {
		return "~"
	}
	if !hasPathPrefix(pNorm, homeNorm) {
		return p
	}
	rest := pNorm[len(homeNorm)+1:]
	return "~/" + rest
}

// pathClean collapses duplicate separators and trailing slashes without
// treating the opposite platform's separator as special. filepath.Clean only
// knows the host separator, so Windows paths stay opaque on a Unix host.
func pathClean(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	if len(s) > 1 && strings.HasSuffix(s, "/") {
		s = strings.TrimRight(s, "/")
		if s == "" {
			s = "/"
		}
	}
	return s
}

func hasPathPrefix(path, prefix string) bool {
	if len(path) <= len(prefix) {
		return false
	}
	if !strings.EqualFold(path[:len(prefix)], prefix) {
		return false
	}
	c := path[len(prefix)]
	return c == '/'
}

func normalizeSlashes(s string) string {
	return strings.ReplaceAll(s, "\\", "/")
}

// redactHomeFields folds the home prefix out of every path in the meta block.
func redactHomeFields(m *Meta, home string) {
	if m == nil || home == "" {
		return
	}
	m.DBPath = RedactHome(m.DBPath, home)
	m.PiDir = RedactHome(m.PiDir, home)
	redactCLIDirs(&m.CLI, home)
}

// redactCLIDirs handles both shapes the CLI block arrives in. warehouse.CLIStats
// returns map[string]string, so asserting on map[string]any alone silently did
// nothing and the account name survived into the payload.
func redactCLIDirs(cli *any, home string) {
	switch v := (*cli).(type) {
	case map[string]string:
		if d, ok := v["dirs"]; ok {
			v["dirs"] = redactHomeList(d, home)
		}
	case map[string]any:
		if d, ok := v["dirs"].(string); ok {
			v["dirs"] = redactHomeList(d, home)
		}
	}
}

// redactHomeList folds each entry of a comma-joined path list.
func redactHomeList(list, home string) string {
	parts := strings.Split(list, ",")
	for i, part := range parts {
		parts[i] = RedactHome(strings.TrimSpace(part), home)
	}
	return strings.Join(parts, ", ")
}

// BuildMeta assembles the meta block. home is used to fold the user's home
// prefix out of every path it records; pass "" to keep them verbatim.
func BuildMeta(generatedAt, dbPath, piDir string, sourceNote any, dbVersion int64,
	dbSize, walSize int64, firstTs, lastTs any, cli any, home string) Meta {
	var dbMB, walMB float64
	if dbSize > 0 {
		dbMB = round1(float64(dbSize) / 1048576)
	}
	if walSize > 0 {
		walMB = round1(float64(walSize) / 1048576)
	}
	m := Meta{
		GeneratedAt: generatedAt, DBPath: dbPath, PiDir: piDir,
		SourceNote: sourceNote, DBVersion: dbVersion, DBSizeMB: dbMB, WALSizeMB: walMB,
		RangeStart: formatRange(firstTs), RangeEnd: formatRange(lastTs), CLI: cli,
	}
	redactHomeFields(&m, home)
	return m
}

// round1 mirrors python's round(x, 1).
func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// formatRange renders a millisecond timestamp as local "YYYY-MM-DD HH:MM".
func formatRange(v any) any {
	ms, ok := v.(int64)
	if !ok || ms == 0 {
		return nil
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04")
}

// DashboardPayload is the full dashboard JSON payload.
type DashboardPayload struct {
	Meta         Meta                       `json:"meta"`
	Totals       any                        `json:"totals"`
	Models       []ModelListRow             `json:"models"`
	Days         any                        `json:"days"`
	DayModel     any                        `json:"dayModel"`
	Hours        any                        `json:"hours"`
	Projects     any                        `json:"projects"`
	Sessions     any                        `json:"sessions"`
	SessionsAll  any                        `json:"sessionsAll"`
	SessionCount any                        `json:"sessionCount"`
	Roles        any                        `json:"roles"`
	Tools        any                        `json:"tools"`
	Pricing      map[string]json.RawMessage `json:"pricing"`
	PricingMeta  PricingMeta                `json:"pricingMeta"`
	External     *External                  `json:"external"`
}
