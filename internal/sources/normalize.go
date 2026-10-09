package sources

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
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
func normalize(r *rawObj, m *Manifest, ctx *Context) *Record {
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

// dedupKeyFor builds the identity used to recognise a record the collector has
// already accounted for.
//
// The key deliberately does NOT include the file name: the contract is that one
// logical call appears once per tool, no matter how many files carry it. That is
// also why a cached per-file aggregate cannot simply be summed - see
// MergePartials.
func dedupKeyFor(r *rawObj, m *Manifest) string {
	if len(m.Dedup) > 0 {
		parts := make([]string, 0, len(m.Dedup))
		any := false
		for _, p := range m.Dedup {
			s := str(firstOf(r.Obj, []string{p}))
			if s != "" {
				any = true
			}
			parts = append(parts, s)
		}
		if any {
			return m.ID + ":" + joinStr(parts, ":")
		}
	}
	// No usable identity field: fall back to position, which is unique by
	// construction because the file name is part of it.
	return m.ID + ":" + r.File + "#" + strconv.Itoa(r.Line)
}

// normalizeAll applies dedup and normalisation to a record stream, in order.
//
// emit receives every dedup key the run claimed together with the record it
// produced, or nil for a record that turned out to carry no usage. That pairing
// is what a cached per-file aggregate has to keep: the key is the identity that
// cross-file dedup works on, and the record is the contribution that a later
// duplicate has to be prevented from double-counting. A cache that stored only
// one of the two could not be replayed exactly.
//
// The whole-tool path and the per-file cache path share this function on
// purpose: one definition of the dedup rule is what lets a summed cache be
// provably identical to a full parse.
func normalizeAll(raw []*rawObj, m *Manifest, ctx *Context, emit func(key string, rec *Record)) []Record {
	seen := map[string]bool{}
	var out []Record
	for _, r := range raw {
		key := dedupKeyFor(r, m)
		if seen[key] {
			ctx.Dropped++
			continue
		}
		seen[key] = true
		rec := normalize(r, m, ctx)
		if emit != nil {
			emit(key, rec)
		}
		if rec != nil {
			out = append(out, *rec)
		}
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
	return normalizeAll(raw, m, ctx, nil)
}

// ReadRecords dispatches on the manifest kind.
func ReadRecords(m *Manifest, ctx *Context) []*rawObj {
	ctx.Manifest = m
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
	DayModel   map[string]*ModelAgg `json:"dayModel,omitempty"`
	Hours      map[string]*ModelAgg `json:"hours,omitempty"`
	FirstTs    string               `json:"firstTs"`
	LastTs     string               `json:"lastTs"`
	DedupNote  string               `json:"dedupNote"`
	Files      int                  `json:"_files"`
}

// accumulator folds records into the counters a ToolAgg is made of.
//
// Aggregate and MergePartials both drive one of these, which is what makes a
// cache assembled from per-file pieces identical to a full parse: there is one
// piece of arithmetic, reached two ways, not two implementations that have to be
// kept in agreement by hand.
type accumulator struct {
	totals     map[string]int64
	byModel    map[string]*ModelAgg
	byDay      map[string]*ModelAgg
	byHour     map[string]*ModelAgg
	byDayModel map[string]*ModelAgg
	sessions   map[string]bool
	times      []string
	calls      int
}

func newAccumulator() *accumulator {
	return &accumulator{
		totals: blankTokens(), byModel: map[string]*ModelAgg{},
		byDay: map[string]*ModelAgg{}, byHour: map[string]*ModelAgg{},
		byDayModel: map[string]*ModelAgg{}, sessions: map[string]bool{},
	}
}

func (a *accumulator) add(r Record) {
	a.sessions[r.Session] = true
	for _, k := range TokenKeys {
		a.totals[k] += tokenOf(r, k)
	}
	addTo(a.byModel, r.Model, r)
	a.calls++
	if ts, ok := r.Ts.(string); ok && ts != "" {
		day := ts
		if len(day) > 10 {
			day = day[:10]
		}
		addTo(a.byDay, day, r)
		// day×model cross product. pi's warehouse already ships this shape, so
		// without it the weekly Top list can only cover pi - which is what made a
		// six-tool ledger present a one-tool ranking.
		if r.Model != "" {
			addTo(a.byDayModel, day+dayModelSep+r.Model, r)
		}
		a.times = append(a.times, ts)
		if h := localHourKey(ts); h != "" {
			addTo(a.byHour, h, r)
		}
	}
}

// dayModelSep joins day and model into one accumulator key. No padding: the
// key is split back apart by pricing when it is written to the payload, so a
// stray space would ship as part of the day or the model name.
const dayModelSep = "\x1f"

// DayModelKeys returns the day×model keys in sorted order.
func (a *accumulator) DayModelKeys() []string {
	out := make([]string, 0, len(a.byDayModel))
	for k := range a.byDayModel {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// localHourKey buckets a timestamp into `YYYY-MM-DDTHH` **local** time.
//
// The day bucket above is deliberately left alone: it slices the source's own
// string, so daily totals keep the exact keys they have always had. Hour
// buckets cannot do that, because the hour-of-day axis only means something once
// it is converted — an adapter's ISO stamps end in `Z` (UTC), while pi's hours
// come out of SQLite already in localtime (`warehouse.hourExpr`). Merging the
// two without converting would produce a matrix whose columns are half UTC and
// half local, which is not a heatmap of anything.
//
// Timestamps that carry no explicit offset are read as wall-clock local, which
// is what a source writing bare `YYYY-MM-DDTHH:MM:SS` means by them.
func localHourKey(ts string) string {
	if len(ts) < 13 {
		return ""
	}
	if strings.HasSuffix(ts, "Z") || hasOffset(ts) {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return t.Local().Format("2006-01-02T15")
		}
		// A stamp we cannot parse as RFC3339 is still a stamp; fall through to
		// the wall-clock reading rather than dropping the record from hours.
	}
	return ts[:13]
}

// hasOffset reports whether an ISO timestamp ends in a numeric UTC offset
// (`+08:00`) rather than `Z` or nothing at all.
func hasOffset(ts string) bool {
	if len(ts) < 6 {
		return false
	}
	tail := ts[len(ts)-6:]
	return (tail[0] == '+' || tail[0] == '-') && tail[3] == ':'
}

func tokenOf(r Record, key string) int64 {
	switch key {
	case "input":
		return r.Input
	case "cacheRead":
		return r.CacheRead
	case "cacheWrite":
		return r.CacheWrite
	case "output":
		return r.Output
	case "reasoning":
		return r.Reasoning
	}
	return 0
}

func addTo(into map[string]*ModelAgg, key string, r Record) {
	m, ok := into[key]
	if !ok {
		m = &ModelAgg{}
		into[key] = m
	}
	m.Input += r.Input
	m.CacheRead += r.CacheRead
	m.CacheWrite += r.CacheWrite
	m.Output += r.Output
	m.Reasoning += r.Reasoning
}

// agg renders the accumulated counters into the per-tool summary shape.
func (a *accumulator) agg(m *Manifest, files int) *ToolAgg {
	sort.Strings(a.times)
	first, last := "", ""
	if len(a.times) > 0 {
		first, last = a.times[0], a.times[len(a.times)-1]
	}
	label := m.Label
	if label == "" {
		label = m.ID
	}
	return &ToolAgg{
		Tool: m.ID, Label: label, Detected: a.calls > 0, Home: m.Home,
		Sessions: len(a.sessions), Calls: a.calls,
		Input: a.totals["input"], CacheRead: a.totals["cacheRead"],
		CacheWrite: a.totals["cacheWrite"], Output: a.totals["output"],
		Reasoning: a.totals["reasoning"],
		Models:    a.byModel, Days: a.byDay, Hours: a.byHour, DayModel: a.byDayModel,
		FirstTs: first, LastTs: last, DedupNote: m.Note, Files: files,
	}
}

// Aggregate folds canonical records into the per-tool summary shape.
func Aggregate(records []Record, m *Manifest) *ToolAgg {
	a := newAccumulator()
	for _, r := range records {
		a.add(r)
	}
	return a.agg(m, m.Files)
}
