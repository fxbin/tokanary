package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Manifest is one adapter declaration, deserialised straight from
// data/adapters/*.json. The JSON files stay the single source of truth
// for tool specifics - adding a tool must not require a Go change.
type Manifest struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Kind  string   `json:"kind"`
	Paths []string `json:"paths"`

	// sqlite kind
	DB         string `json:"db"`
	Query      string `json:"query"`
	JSONColumn string `json:"json_column"`

	Prefilter any            `json:"prefilter"`
	Require   []string       `json:"require"`
	Where     map[string]any `json:"where"`
	Driver    string         `json:"driver"`
	Dedup     []string       `json:"dedup"`

	Fields map[string][]string `json:"fields"`

	InputSubtract  []string `json:"inputSubtract"`
	OutputAdd      []string `json:"outputAdd"`
	ReasoningIsSub *bool    `json:"reasoningIsSubsetOfOutput"`

	Enabled *bool  `json:"enabled"`
	Group   string `json:"group"`
	Note    string `json:"note"`
	Caveat  string `json:"caveat"`

	// runtime facts filled in by the collector, never read from JSON
	FileName string `json:"-"`
	Files    int    `json:"-"`
	Home     string `json:"-"`
}

// IsEnabled defaults to true when the manifest does not say otherwise.
func (m *Manifest) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// KindOrDefault fills in the implicit "jsonl".
func (m *Manifest) KindOrDefault() string {
	if m.Kind == "" {
		return "jsonl"
	}
	return m.Kind
}

// ReasoningIsSubsetOfOutput defaults to true.
func (m *Manifest) ReasoningIsSubsetOfOutput() bool {
	if m.ReasoningIsSub == nil {
		return true
	}
	return *m.ReasoningIsSub
}

// PrefilterPatterns flattens prefilter, which may be one string or a list.
// Any hit keeps the line; a multi-line structure such as codex turn_context
// must be listed or the carried state is lost.
func (m *Manifest) PrefilterPatterns() []string {
	switch t := m.Prefilter.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}

// LoadAdapters reads every adapter manifest, sorted by filename. When only is
// non-empty, the rest are filtered out.
func LoadAdapters(dir string, only []string) ([]*Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	want := map[string]bool{}
	for _, o := range only {
		o = trimSpace(o)
		if o != "" {
			want[o] = true
		}
	}
	var out []*Manifest
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		if len(want) > 0 && !want[m.ID] {
			continue
		}
		m.FileName = name
		out = append(out, &m)
	}
	return out, nil
}

// KnownKinds is the closed set validate() accepts.
var KnownKinds = []string{"jsonl", "zstd-jsonl", "sqlite"}

// Validate mirrors collect_sources.validate. It checks the declaration only and
// never parses data, so it is cheap enough to run on every collection.
func Validate(m *Manifest) []string {
	var errs []string
	kind := m.KindOrDefault()
	if !contains(KnownKinds, kind) {
		errs = append(errs, "kind 必须是 "+join(KindsForMsg)+" 之一")
	}
	if (kind == "jsonl" || kind == "zstd-jsonl") && len(m.Paths) == 0 {
		errs = append(errs, kind+" 源必须声明 paths")
	}
	if kind == "sqlite" && (m.DB == "" || m.Query == "") {
		errs = append(errs, "sqlite 源必须声明 db 与 query")
	}
	for _, need := range []string{"model", "input", "output"} {
		if len(m.Fields[need]) == 0 {
			errs = append(errs, "fields."+need+" 未声明（至少要能取到模型名与输入/输出）")
		}
	}
	if m.Driver != "" {
		if _, ok := drivers[m.Driver]; !ok {
			errs = append(errs, "driver 无效："+m.Driver)
		}
	}
	return errs
}

var KindsForMsg = KnownKinds

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func join(list []string) string {
	out := ""
	for i, v := range list {
		if i > 0 {
			out += "/"
		}
		out += v
	}
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
