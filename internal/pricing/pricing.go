// Package pricing resolves unit prices from the curated models.dev table and
// assembles the external (other-CLI) block the dashboard consumes.
package pricing

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Cost is the four billable unit prices, per 1M tokens.
type Cost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// tokFields maps a cost field to the token counter that weights it. The order
// is fixed because it determines map insertion in the python original.
var tokFields = []struct {
	Field  string
	TokKey string
}{
	{"input", "input"},
	{"output", "output"},
	{"cache_read", "cacheRead"},
	{"cache_write", "cacheWrite"},
}

// prefixes are the provider prefixes tried last during key matching. They are
// guesses, so they rank below the literal derivations.
var prefixes = []string{
	"", "openai/", "azure/", "anthropic/", "gemini/", "vertex_ai/",
	"moonshot/", "moonshotai/", "deepseek/", "openrouter/openai/",
	"openrouter/anthropic/", "openrouter/google/",
}

var (
	reInt      = regexp.MustCompile(`--?int$`)
	reGA       = regexp.MustCompile(`--?ga[-_]?\d{4,6}$`)
	reDateNum  = regexp.MustCompile(`-\d{6}$`)
	reFreeTail = regexp.MustCompile(`(?i)[-:]free$`)
)

// keyVariants maps a gateway model id onto the candidate keys that might exist
// in the LiteLLM cost map, ordered most-specific first.
//
//	azure-gpt-5.6-sol          -> azure/gpt-5.6-sol, then gpt-5.6-sol
//	gpt-5.6-sol--int           -> gpt-5.6-sol
//	kimi/kimi-k3               -> kimi-k3, then moonshot/kimi-k3
//	deepseek-v4-pro-ga-260813  -> deepseek-v4-pro
//	kimi-k3-260716             -> kimi-k3
//
// This must stay an ordered slice, never a set: Go randomises map iteration
// order, so a set would make the same id match a different entry between runs
// and the price would drift. That is the exact reason the python original
// documents avoiding a set.
func KeyVariants(modelID string) []string {
	var out []string
	add := func(x string) {
		if x == "" {
			return
		}
		for _, e := range out {
			if e == x {
				return
			}
		}
		out = append(out, x)
	}

	staged := []string{modelID}

	// 1) azure-xxx -> azure/xxx style prefix rewrite
	for _, pre := range []string{"azure-", "vertex-", "bedrock-", "openai-", "anthropic-", "google-"} {
		if strings.HasPrefix(modelID, pre) {
			staged = append(staged, strings.TrimSuffix(pre, "-")+"/"+modelID[len(pre):])
			break
		}
	}

	// 2) peel suffixes one layer at a time; each layer is appended so the
	//    original spelling always stays ahead of the derived ones
	expand := func(fn func(string) string) {
		for _, s := range append([]string{}, staged...) {
			if v := fn(s); v != "" && !contains(staged, v) {
				staged = append(staged, v)
			}
		}
	}
	expand(func(s string) string { return reInt.ReplaceAllString(s, "") })
	expand(func(s string) string { return reGA.ReplaceAllString(s, "") })
	expand(func(s string) string { return reDateNum.ReplaceAllString(s, "") })

	// 3) drop a provider path prefix (kimi/kimi-k3 -> kimi-k3)
	for _, s := range append([]string{}, staged...) {
		if !strings.Contains(s, "/") {
			continue
		}
		i := strings.Index(s, "/")
		if p := s[i+1:]; p != "" && !contains(staged, p) {
			staged = append(staged, p)
		}
		if p := s[strings.LastIndex(s, "/")+1:]; p != "" && !contains(staged, p) {
			staged = append(staged, p)
		}
	}

	for _, s := range staged {
		add(s)
	}
	// 4) only now try provider prefixes - they are guesses, lowest priority
	for _, s := range staged {
		leaf := s
		if i := strings.LastIndex(s, "/"); i >= 0 {
			leaf = s[i+1:]
		}
		for _, p := range prefixes {
			add(p + leaf)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func round9(x float64) float64 {
	// python round(x, 9): half away from zero on the decimal representation
	const mult = 1e9
	scaled := x * mult
	if scaled >= 0 {
		return float64(int64(scaled+0.5)) / mult
	}
	return float64(int64(scaled-0.5)) / mult
}

// NullableStr is a string that round-trips python's `str | None`.
//
// sql.NullString cannot be used: its UnmarshalJSON is only defined for
// booleans, so it rejects a plain JSON string, and its zero value marshals as
// {}. Both directions are needed here because the same type is used to read
// external-usage.json and to write data.js.
type NullableStr struct {
	S     string
	Valid bool
}

func nstr(s string) NullableStr { return NullableStr{S: s, Valid: s != ""} }

func (n *NullableStr) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		n.S, n.Valid = "", false
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	n.S, n.Valid = s, true
	return nil
}

func (n NullableStr) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.S)
}

// PriceEntry is the per-model price record attached to external-usage.json's
// prices map.
type PriceEntry struct {
	Source     string      `json:"source"`
	MatchedKey NullableStr `json:"matchedKey"`
	Provider   NullableStr `json:"provider"`
	Cost       Cost        `json:"cost"`
	Note       string      `json:"note,omitempty"`
}

// PriceModels resolves a unit price for every model id seen in the external
// tool rows: curated models.dev table, then a free-tier rule, then unpriced.
func PriceModels(rows []ToolUsage, curated map[string]json.RawMessage) map[string]PriceEntry {
	out := map[string]PriceEntry{}
	for _, t := range rows {
		for mid, mm := range t.Models {
			if _, done := out[mid]; done {
				continue
			}
			if mm.Input+mm.CacheRead+mm.CacheWrite+mm.Output == 0 {
				continue
			}
			// python: re.search(r"[-:]free$", mid, re.I) - the model id must
			// END with -free or :free
			if reFreeTail.MatchString(mid) {
				z := 0.0
				out[mid] = PriceEntry{
					Source: "free", MatchedKey: NullableStr{},
					Cost: Cost{&z, &z, &z, &z},
					Note: "模型名以 free 结尾，按免费额度计 $0",
				}
				continue
			}
			base := ""
			for _, c := range KeyVariants(mid) {
				if _, ok := curated[c]; ok {
					base = c
					break
				}
			}
			if base != "" {
				var ce struct {
					Cost struct {
						Input *float64 `json:"input"`
					} `json:"cost"`
				}
				if err := json.Unmarshal(curated[base], &ce); err == nil && ce.Cost.Input != nil {
					var raw struct {
						Target     string `json:"target"`
						Provider   string `json:"provider"`
						Confidence string `json:"confidence"`
						Cost       Cost   `json:"cost"`
					}
					_ = json.Unmarshal(curated[base], &raw)
					out[mid] = PriceEntry{
						Source: "models.dev", MatchedKey: nstr(base),
						Provider: nstr(raw.Provider),
						Cost:     raw.Cost,
					}
					continue
				}
			}
			out[mid] = PriceEntry{
				Source: "unpriced", MatchedKey: NullableStr{},
				Cost: Cost{nil, nil, nil, nil},
			}
		}
	}
	return out
}
