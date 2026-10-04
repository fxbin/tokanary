package sources

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

func jsonUnmarshal(b []byte, v *any) error { return json.Unmarshal(b, v) }

// toISOTs converts a Unix millisecond envelope (the DSH "time" field) into an
// ISO8601 UTC string. Anything else (seconds, an ISO string already) is left
// alone so existing adapters are unaffected. Without this, aggregate's
// str(ts)[:10] day split would produce fake keys like "1789726046".
func toISOTs(v any) any {
	switch t := v.(type) {
	case float64:
		if t > 1e12 {
			return msToISO(int64(t))
		}
		return t
	case int64:
		if t > 1e12 {
			return msToISO(t)
		}
		return t
	}
	return v
}

func msToISO(ms int64) string {
	dt := time.UnixMilli(ms).UTC()
	// python's f"{dt.microsecond // 1000:03d}Z" zero-pads the millisecond
	// field to three digits, which plain Format("000") does not guarantee for
	// a single-digit value like 29 -> "29" instead of "029"
	return dt.Format("2006-01-02T15:04:05.000") + "Z"
}

// normalize turns one raw record into a canonical record per the manifest, or
// nil when the record does not qualify.
func normalize(r *rawObj, m *Manifest, ctx *Context, seen map[string]bool) *Record {
	obj := r.Obj
	// hard requirements
	for _, req := range m.Require {
		v := dig(obj, req)
		if v == nil {
			return nil
		}
		switch t := v.(type) {
		case string:
			if t == "" {
				return nil
			}
		case map[string]any:
			if len(t) == 0 {
				return nil
			}
		case []any:
			if len(t) == 0 {
				return nil
			}
		}
	}
	// where clause
	for path, want := range m.Where {
		if !equalValue(dig(obj, path), want) {
			return nil
		}
	}

	f := m.Fields
	tokIn := toInt(firstOf(obj, f["input"]))
	cr := toInt(firstOf(obj, f["cacheRead"]))
	cw := toInt(firstOf(obj, f["cacheWrite"]))
	out := toInt(firstOf(obj, f["output"]))
	rs := toInt(firstOf(obj, f["reasoning"]))

	// quirk 1: input includes cache (codex) - subtract or cost inflates
	for _, p := range m.InputSubtract {
		tokIn -= toInt(dig(obj, p))
	}
	// quirk 2: reasoning is additive (opencode) - fold into output
	if len(m.OutputAdd) > 0 {
		sum := int64(0)
		for _, a := range m.OutputAdd {
			sum += toInt(firstOf(obj, []string{a}))
		}
		out += sum
	}
	if tokIn < 0 {
		tokIn = 0
	}
	// reasoning is always a subset of output unless made additive above
	if m.ReasoningIsSubsetOfOutput() && rs > out {
		rs = out
	}

	// dedup key
	var dedupKey string
	if len(m.Dedup) > 0 {
		parts := make([]string, 0, len(m.Dedup))
		any := false
		for _, p := range m.Dedup {
			s := str(firstOf(obj, []string{p}))
			if s != "" {
				any = true
			}
			parts = append(parts, s)
		}
		if any {
			dedupKey = m.ID + ":" + joinStr(parts, ":")
		}
	}
	if dedupKey == "" {
		dedupKey = m.ID + ":" + r.File + "#" + strconv.Itoa(r.Line)
	}
	if seen[dedupKey] {
		ctx.Dropped++
		return nil
	}
	seen[dedupKey] = true

	if tokIn+cr+cw+out == 0 {
		return nil
	}

	model := str(firstOf(obj, f["model"]))
	if model == "" {
		model = "(unknown)"
	}
	session := str(firstOf(obj, f["session"]))
	if session == "" {
		session = r.Dir
	}
	if session == "" {
		session = "?"
	}

	return &Record{
		Tool: m.ID, Model: model, Session: session,
		Ts:    toISOTs(firstOf(obj, f["time"])),
		Input: tokIn, CacheRead: cr, CacheWrite: cw,
		Output: out, Reasoning: rs,
	}
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	return ""
}

func joinStr(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// Run executes one manifest and returns its canonical records, delegating to a
// driver when the manifest declares one.
func Run(m *Manifest, ctx *Context) []Record {
	raw := ReadRecords(m, ctx)
	if m.Driver != "" {
		if d, ok := drivers[m.Driver]; ok {
			return d(raw, m, ctx)
		}
		return nil
	}
	seen := map[string]bool{}
	var out []Record
	for _, r := range raw {
		if rec := normalize(r, m, ctx, seen); rec != nil {
			out = append(out, *rec)
		}
	}
	return out
}

// ReadRecords dispatches on the manifest kind.
func ReadRecords(m *Manifest, ctx *Context) []*rawObj {
	switch m.KindOrDefault() {
	case "jsonl":
		return ReadJSONL(ExpandPaths(ctx.patternsFor(m), ctx), ctx)
	case "json":
		return ReadJSON(ExpandPaths(ctx.patternsFor(m), ctx), ctx)
	case "zstd-jsonl":
		return ReadZstdJSONL(ExpandPaths(ctx.patternsFor(m), ctx), ctx)
	case "sqlite":
		return ReadSQLite(m, ctx)
	}
	return nil
}

// ModelAgg is one model's totals inside a tool.
type ModelAgg struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
}

// Total sums the four billable counters. reasoning is excluded because it is a
// subset of output and must never be billed twice.
func (m *ModelAgg) Total() int64 {
	return m.Input + m.CacheRead + m.CacheWrite + m.Output
}

// ToolAgg is the per-tool summary that becomes external-usage.json's entry.
type ToolAgg struct {
	Tool       string               `json:"tool"`
	Label      string               `json:"label"`
	Detected   bool                 `json:"detected"`
	Home       string               `json:"home"`
	Sessions   int                  `json:"sessions"`
	Calls      int                  `json:"calls"`
	Input      int64                `json:"input"`
	CacheRead  int64                `json:"cacheRead"`
	CacheWrite int64                `json:"cacheWrite"`
	Output     int64                `json:"output"`
	Reasoning  int64                `json:"reasoning"`
	Models     map[string]*ModelAgg `json:"models"`
	Days       map[string]*ModelAgg `json:"days"`
	FirstTs    string               `json:"firstTs"`
	LastTs     string               `json:"lastTs"`
	DedupNote  string               `json:"dedupNote"`
	Files      int                  `json:"_files"`
}

// Aggregate folds canonical records into the per-tool summary shape.
func Aggregate(records []Record, m *Manifest) *ToolAgg {
	totals := blankTokens()
	byModel := map[string]*ModelAgg{}
	byDay := map[string]*ModelAgg{}
	sessions := map[string]bool{}
	times := []string{}
	for _, r := range records {
		sessions[r.Session] = true
		ma, ok := byModel[r.Model]
		if !ok {
			ma = &ModelAgg{}
			byModel[r.Model] = ma
		}
		vals := map[string]int64{
			"input": r.Input, "cacheRead": r.CacheRead, "cacheWrite": r.CacheWrite,
			"output": r.Output, "reasoning": r.Reasoning,
		}
		for _, k := range TokenKeys {
			totals[k] += vals[k]
			switch k {
			case "input":
				ma.Input += vals[k]
			case "cacheRead":
				ma.CacheRead += vals[k]
			case "cacheWrite":
				ma.CacheWrite += vals[k]
			case "output":
				ma.Output += vals[k]
			case "reasoning":
				ma.Reasoning += vals[k]
			}
		}
		if ts, ok := r.Ts.(string); ok && ts != "" {
			day := ts
			if len(day) > 10 {
				day = day[:10]
			}
			da, ok := byDay[day]
			if !ok {
				da = &ModelAgg{}
				byDay[day] = da
			}
			for _, k := range TokenKeys {
				switch k {
				case "input":
					da.Input += vals[k]
				case "cacheRead":
					da.CacheRead += vals[k]
				case "cacheWrite":
					da.CacheWrite += vals[k]
				case "output":
					da.Output += vals[k]
				case "reasoning":
					da.Reasoning += vals[k]
				}
			}
			times = append(times, ts)
		}
	}
	sort.Strings(times)
	first, last := "", ""
	if len(times) > 0 {
		first, last = times[0], times[len(times)-1]
	}
	label := m.Label
	if label == "" {
		label = m.ID
	}
	return &ToolAgg{
		Tool: m.ID, Label: label, Detected: len(records) > 0, Home: m.Home,
		Sessions: len(sessions), Calls: len(records),
		Input: totals["input"], CacheRead: totals["cacheRead"],
		CacheWrite: totals["cacheWrite"], Output: totals["output"],
		Reasoning: totals["reasoning"],
		Models:    byModel, Days: byDay,
		FirstTs: first, LastTs: last, DedupNote: m.Note, Files: m.Files,
	}
}
