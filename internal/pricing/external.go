package pricing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ModelUsage is one model's token counters inside an external tool row.
type ModelUsage struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
}

// Total bills input + cacheRead + cacheWrite + output, excluding reasoning
// because it is already inside output.
func (m ModelUsage) Total() int64 {
	return m.Input + m.CacheRead + m.CacheWrite + m.Output
}

// ToolUsage is one tool row inside external-usage.json.
type ToolUsage struct {
	Tool       string                 `json:"tool"`
	Label      string                 `json:"label"`
	Home       string                 `json:"home"`
	Detected   bool                   `json:"detected"`
	Sessions   int                    `json:"sessions"`
	Calls      int                    `json:"calls"`
	Input      int64                  `json:"input"`
	CacheRead  int64                  `json:"cacheRead"`
	CacheWrite int64                  `json:"cacheWrite"`
	Output     int64                  `json:"output"`
	Reasoning  int64                  `json:"reasoning"`
	Models     map[string]*ModelUsage `json:"models"`
	ModelKeys  orderedModelKeys       `json:"-"`
	Days       map[string]*ModelUsage `json:"days"`
	DayKeys    orderedModelKeys       `json:"-"`
	Hours      map[string]*ModelUsage `json:"hours,omitempty"`
	HourKeys   orderedModelKeys       `json:"-"`
	FirstTs    string                 `json:"firstTs"`
	LastTs     string                 `json:"lastTs"`
	DedupNote  string                 `json:"dedupNote"`
	Files      int                    `json:"_files"`

	modelKeys []string
	dayKeys   []string
}

// modelOrder returns the model ids in source order, falling back to sorted keys
// when the source order was not captured.
func (t *ToolUsage) modelOrder() []string {
	if len(t.modelKeys) > 0 {
		return t.modelKeys
	}
	out := make([]string, 0, len(t.Models))
	for k := range t.Models {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (t *ToolUsage) hourOrder() []string {
	if len(t.HourKeys.Keys) > 0 {
		return t.HourKeys.Keys
	}
	out := make([]string, 0, len(t.Hours))
	for k := range t.Hours {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (t *ToolUsage) dayOrder() []string {
	if len(t.dayKeys) > 0 {
		return t.dayKeys
	}
	out := make([]string, 0, len(t.Days))
	for k := range t.Days {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// UnmarshalJSON decodes the row and captures the source key order of models and
// days so the stable sort by -total reproduces python's tie handling.
func (t *ToolUsage) UnmarshalJSON(b []byte) error {
	type alias ToolUsage
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*t = ToolUsage(a)
	var probe struct {
		Models json.RawMessage `json:"models"`
		Days   json.RawMessage `json:"days"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	if len(probe.Models) > 0 {
		if err := json.Unmarshal(probe.Models, &t.ModelKeys); err == nil {
			t.modelKeys = t.ModelKeys.Keys
		}
	}
	if len(probe.Days) > 0 {
		if err := json.Unmarshal(probe.Days, &t.DayKeys); err == nil {
			t.dayKeys = t.DayKeys.Keys
		}
	}
	return nil
}

// ExternalUsage is the whole external-usage.json payload.
type ExternalUsage struct {
	GeneratedAt string                `json:"generatedAt"`
	Unit        string                `json:"unit"`
	Convention  string                `json:"convention"`
	Framework   string                `json:"framework"`
	Tools       []ToolUsage           `json:"tools"`
	Prices      map[string]PriceEntry `json:"prices"`
}

// orderedModelKeys records the key order of a models/days object as it appears
// in the JSON.
//
// Python iterates dicts in insertion order and then applies a stable sort by
// -total, so rows with equal totals keep their source order. Go map iteration
// is randomised, which makes that ordering unreproducible run to run - and it
// also disagreed with python, because the two sides then sorted the same tied
// rows differently. The order has to be captured from the source document.
type orderedModelKeys struct {
	Keys []string
}

func (o *orderedModelKeys) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return err
		}
		ks, ok := k.(string)
		if !ok {
			return fmt.Errorf("non-string key")
		}
		o.Keys = append(o.Keys, ks)
		// skip the value
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
	}
	_, err = dec.Token() // closing }
	return err
}

// LoadExternalUsage parses external-usage.json.
func LoadExternalUsage(path string) (*ExternalUsage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e ExternalUsage
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// ExtModelRow is one model line inside the dashboard's external block.
type ExtModelRow struct {
	ID         string     `json:"id"`
	Input      int64      `json:"input"`
	CacheRead  int64      `json:"cacheRead"`
	CacheWrite int64      `json:"cacheWrite"`
	Output     int64      `json:"output"`
	Reasoning  int64      `json:"reasoning"`
	Total      int64      `json:"total"`
	Price      PriceEntry `json:"price"`
}

// ExtDayRow is one daily bucket inside the external block. The engine's ZERO
// tuple does not include total, so it is recomputed from the four billable
// counters on export - otherwise the dashboard's rangeExt/stackBar would draw
// an out-of-range bar when total is 0.
type ExtDayRow struct {
	D          string `json:"d"`
	Total      int64  `json:"total"`
	Input      int64  `json:"input"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Output     int64  `json:"output"`
	Reasoning  int64  `json:"reasoning"`
}

// ExtHourRow is one `YYYY-MM-DDTHH` bucket inside the external block. Adapters
// stamp records with hour-resolution ISO timestamps, so the heatmap is not
// limited to whichever tools happen to feed a warehouse.
//
//nolint:revive // mirrors ExtDayRow field-for-field
type ExtHourRow struct {
	H          string `json:"h"`
	Total      int64  `json:"total"`
	Input      int64  `json:"input"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Output     int64  `json:"output"`
	Reasoning  int64  `json:"reasoning"`
}

// ExtTool is one tool block inside the dashboard's external section.
type ExtTool struct {
	Tool       string        `json:"tool"`
	Label      string        `json:"label"`
	Home       string        `json:"home"`
	Sessions   int           `json:"sessions"`
	Calls      int           `json:"calls"`
	Input      int64         `json:"input"`
	CacheRead  int64         `json:"cacheRead"`
	CacheWrite int64         `json:"cacheWrite"`
	Output     int64         `json:"output"`
	Reasoning  int64         `json:"reasoning"`
	Total      int64         `json:"total"`
	FirstTs    string        `json:"firstTs"`
	LastTs     string        `json:"lastTs"`
	Note       string        `json:"note"`
	Models     []ExtModelRow `json:"models"`
	Days       []ExtDayRow   `json:"days"`
	Hours      []ExtHourRow  `json:"hours,omitempty"`
}

// ExtTotal is the external section's grand total.
type ExtTotal struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	Total      int64 `json:"total"`
	Calls      int   `json:"calls"`
	Sessions   int   `json:"sessions"`
	Tools      int   `json:"tools"`
}

// External is the dashboard's external section.
type External struct {
	GeneratedAt string                `json:"generatedAt"`
	Convention  string                `json:"convention"`
	Totals      ExtTotal              `json:"totals"`
	Tools       []ExtTool             `json:"tools"`
	Prices      map[string]PriceEntry `json:"prices"`
}

// BuildExternal reshapes external-usage.json into the dashboard's external
// block. Zero-usage models are dropped (for example claude's <synthetic>), and
// tools are sorted by total descending.
func BuildExternal(e *ExternalUsage) *External {
	if e == nil {
		return nil
	}
	out := &External{
		GeneratedAt: e.GeneratedAt,
		Convention:  e.Convention,
		Totals:      ExtTotal{},
		Prices:      e.Prices,
	}
	if out.Prices == nil {
		out.Prices = map[string]PriceEntry{}
	}
	for _, t := range e.Tools {
		if !t.Detected {
			continue
		}
		var rows []ExtModelRow
		for _, mid := range t.modelOrder() {
			mm := t.Models[mid]
			if mm == nil {
				continue
			}
			tot := mm.Total()
			if tot <= 0 {
				continue
			}
			rows = append(rows, ExtModelRow{
				ID: mid, Input: mm.Input, CacheRead: mm.CacheRead,
				CacheWrite: mm.CacheWrite, Output: mm.Output,
				Reasoning: mm.Reasoning, Total: tot,
				Price: e.Prices[mid],
			})
		}
		// stable sort by -total; ties keep source order, matching python
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Total > rows[j].Total })
		toolTotal := int64(0)
		for _, r := range rows {
			toolTotal += r.Total
		}
		if toolTotal <= 0 && len(rows) == 0 {
			continue
		}
		days := make([]ExtDayRow, 0, len(t.Days))
		for _, k := range t.dayOrder() {
			v := t.Days[k]
			if v == nil {
				continue
			}
			days = append(days, ExtDayRow{
				D: k, Total: v.Total(), Input: v.Input, CacheRead: v.CacheRead,
				CacheWrite: v.CacheWrite, Output: v.Output, Reasoning: v.Reasoning,
			})
		}
		sort.SliceStable(days, func(i, j int) bool { return days[i].D < days[j].D })

		hours := make([]ExtHourRow, 0, len(t.Hours))
		for _, k := range t.hourOrder() {
			v := t.Hours[k]
			if v == nil {
				continue
			}
			hours = append(hours, ExtHourRow{
				H: k, Total: v.Total(), Input: v.Input, CacheRead: v.CacheRead,
				CacheWrite: v.CacheWrite, Output: v.Output, Reasoning: v.Reasoning,
			})
		}
		sort.SliceStable(hours, func(i, j int) bool { return hours[i].H < hours[j].H })

		out.Tools = append(out.Tools, ExtTool{
			Tool: t.Tool, Label: t.Label, Home: t.Home, Sessions: t.Sessions,
			Calls: t.Calls, Input: t.Input, CacheRead: t.CacheRead,
			CacheWrite: t.CacheWrite, Output: t.Output, Reasoning: t.Reasoning,
			Total: toolTotal, FirstTs: t.FirstTs, LastTs: t.LastTs,
			Note: t.DedupNote, Models: rows, Days: days, Hours: hours,
		})
	}
	sort.SliceStable(out.Tools, func(i, j int) bool { return out.Tools[i].Total > out.Tools[j].Total })
	for _, t := range out.Tools {
		out.Totals.Input += t.Input
		out.Totals.CacheRead += t.CacheRead
		out.Totals.CacheWrite += t.CacheWrite
		out.Totals.Output += t.Output
		out.Totals.Reasoning += t.Reasoning
		out.Totals.Calls += t.Calls
		out.Totals.Sessions += t.Sessions
	}
	out.Totals.Total = out.Totals.Input + out.Totals.CacheRead + out.Totals.CacheWrite + out.Totals.Output
	out.Totals.Tools = len(out.Tools)
	return out
}
