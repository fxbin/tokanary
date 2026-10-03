package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DefaultModelsDevURL is the public models.dev catalogue. The payload is a few
// megabytes, so it is fetched once per `tokanary prices` run and never cached
// to disk - .cache/prices-raw.json is the local, reviewable artefact.
const DefaultModelsDevURL = "https://models.dev/api.json"

// MDModel is one entry of the models.dev catalogue, reduced to what the
// dashboard needs. Cost reuses the curated-table shape on purpose: the field
// names are models.dev's own, so a generated table round-trips through
// LoadCurated with no translation layer.
type MDModel struct {
	FullID    string // "provider/model", the catalogue's own key
	Provider  string
	Model     string
	Name      string // human label, may be empty
	Cost      Cost
	HasInput  bool
	InputTier *Cost // models.dev "tiers"/"context_over_200k" long-context band
	SourceURL string
}

// MDIndex is a lookup structure over the catalogue. Entries are indexed both
// by their full "provider/model" key and by leaf name. The leaf index keeps
// *every* candidate, because a bare leaf like "qwen3.8-max" is offered by
// several providers at different prices (302ai 2.16 vs alibaba 2.00) and
// picking the alphabetically-first one would silently price a first-party model
// at a reseller's rate.
type MDIndex struct {
	byFull map[string]*MDModel
	byLeaf map[string][]*MDModel
	Count  int
}

type mdProvider struct {
	Models map[string]struct {
		Name string `json:"name"`
		Cost *struct {
			Input      *float64 `json:"input"`
			Output     *float64 `json:"output"`
			CacheRead  *float64 `json:"cache_read"`
			CacheWrite *float64 `json:"cache_write"`
		} `json:"cost"`
		Limit *struct {
			Context *int64 `json:"context"`
		} `json:"limit"`
	} `json:"models"`
}

// FetchModelsDev downloads and indexes the catalogue.
func FetchModelsDev(url string) (*MDIndex, error) {
	if url == "" {
		url = DefaultModelsDevURL
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("拉取 %s 失败: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("拉取 %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 响应: %w", url, err)
	}
	return ParseModelsDev(body)
}

// ParseModelsDev indexes an already-downloaded catalogue payload. It is
// separated from the HTTP call so the matching rules can be tested offline
// against a fixture instead of the live 5 MB document.
func ParseModelsDev(body []byte) (*MDIndex, error) {
	var doc map[string]mdProvider
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("解析 models.dev 目录: %w", err)
	}

	ix := &MDIndex{byFull: map[string]*MDModel{}, byLeaf: map[string][]*MDModel{}}
	providers := make([]string, 0, len(doc))
	for p := range doc {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	for _, p := range providers {
		models := doc[p].Models
		names := make([]string, 0, len(models))
		for m := range models {
			names = append(names, m)
		}
		sort.Strings(names)
		for _, m := range names {
			md := models[m]
			e := &MDModel{
				FullID:   p + "/" + m,
				Provider: p,
				Model:    m,
				Name:     md.Name,
				SourceURL: fmt.Sprintf(
					"https://raw.githubusercontent.com/sst/models.dev/dev/providers/%s/models/%s.toml", p, m),
			}
			if md.Cost != nil {
				e.Cost = Cost{
					Input:      md.Cost.Input,
					Output:     md.Cost.Output,
					CacheRead:  md.Cost.CacheRead,
					CacheWrite: md.Cost.CacheWrite,
				}
				e.HasInput = md.Cost.Input != nil
			}
			ix.byFull[e.FullID] = e
			// Leaf candidates are appended in sorted provider order, so the
			// preference scoring below - not iteration order - picks the winner.
			leaf := m
			if i := strings.LastIndex(leaf, "/"); i >= 0 {
				leaf = leaf[i+1:]
			}
			ix.byLeaf[leaf] = append(ix.byLeaf[leaf], e)
			ix.Count++
		}
	}
	if ix.Count == 0 {
		return nil, fmt.Errorf("models.dev 目录里没有解析到任何模型")
	}
	return ix, nil
}

// firstParty lists the providers that publish their own pricing. When a bare
// model name is offered by both a first-party provider and a reseller, the
// curated table's own rule is "优先原厂" - prefer first party.
var firstParty = map[string]bool{
	"openai": true, "anthropic": true, "google": true, "gemini": true,
	"deepseek": true, "alibaba": true, "moonshotai": true, "xai": true,
	"mistralai": true, "cohere": true, "meta-llama": true, "zhipuai": true,
	"minimax": true, "inception": true, "baidu": true, "bytedance": true,
}

// aggregators re-brand other vendors' models under a namespaced name
// (openrouter/google/gemini-3.6-flash). Their leaf is a slash path, so a
// nested entry never competes with the vendor's own entry on leaf alone; the
// penalty just keeps the ordering explicit.
var aggregators = map[string]bool{
	"openrouter": true, "302ai": true, "siliconflow": true, "novita": true,
	"together": true, "fireworks-ai": true, "nebius": true, "hyperbolic": true,
}

// providerScore ranks a candidate for a bare leaf. Higher wins.
//
//	400  the observed id itself names the provider (deepseek/deepseek-v4-pro)
//	300  first-party publisher
//	100  ordinary reseller
//	 50  aggregator re-brand
func providerScore(observed, provider string) int {
	p := strings.ToLower(provider)
	obs := strings.ToLower(observed)
	if obs != "" && (strings.Contains(obs, p+"/") || strings.HasPrefix(obs, p+"-")) {
		return 400
	}
	if firstParty[p] {
		return 300
	}
	if aggregators[p] {
		return 50
	}
	return 100
}

// Resolve maps an observed model id onto a catalogue entry.
//
// Order: exact "provider/model" key from the id's own prefix, then the same
// KeyVariants ladder the pricer uses (it already peels vendor prefixes, the
// `--int` routing suffix, `-ga-` and date suffixes), then a scored leaf lookup.
//
// Every candidate the ladder produces is scored, not just the first hit.
// KeyVariants is ordered for the LiteLLM gateway cost map, where "azure/" is
// deliberately probed before "deepseek/"; reusing that order here made a bare
// "deepseek-v4-pro" resolve to azure's 1.74 instead of deepseek's own 0.435.
// The ladder supplies candidates, providerScore decides.
//
// The returned bool reports whether a usable input price was found. A hit with
// HasInput false is still a hit: the model exists upstream but carries no
// price, which is a different fact from "no such model".
func (ix *MDIndex) Resolve(modelID string) (*MDModel, string, bool) {
	// 1) the id may already be a full catalogue key
	if e, ok := ix.byFull[modelID]; ok {
		return e, modelID, e.HasInput
	}

	// 2) walk the shared ladder, scoring every exact provider/model hit and
	//    every leaf hit, then take the best.
	var best *MDModel
	bestScore := -1
	consider := func(e *MDModel) {
		if s := providerScore(modelID, e.Provider); s > bestScore {
			best, bestScore = e, s
		}
	}
	seen := map[string]bool{}
	for _, cand := range KeyVariants(modelID) {
		if e, ok := ix.byFull[cand]; ok && !seen[e.FullID] {
			seen[e.FullID] = true
			consider(e)
		}
	}
	if best != nil && bestScore >= 300 {
		return best, best.FullID, best.HasInput
	}
	// 3) leaf candidates for anything the exact keys missed
	leafSeen := map[string]bool{}
	for _, cand := range KeyVariants(modelID) {
		for _, e := range ix.byLeaf[cand] {
			if leafSeen[e.FullID] || seen[e.FullID] {
				continue
			}
			leafSeen[e.FullID] = true
			consider(e)
		}
	}
	if best != nil {
		return best, best.FullID, best.HasInput
	}
	return nil, "", false
}

// Stats describes the catalogue for the report line.
func (ix *MDIndex) Stats() (providers, withInput int) {
	seen := map[string]bool{}
	for _, e := range ix.byFull {
		seen[e.Provider] = true
		if e.HasInput {
			withInput++
		}
	}
	return len(seen), withInput
}

// VariantLabel keeps generated variant labels consistent with the hand-written
// ones already in the table ("<provider> 原厂 · 基础档").
func VariantLabel(provider, band string) string {
	name := provider
	switch {
	case provider == "openai":
		name = "openai 原厂"
	case provider == "anthropic":
		name = "anthropic 原厂"
	case provider == "google":
		name = "google 原厂"
	case provider == "azure":
		name = "Azure"
	}
	if band == "" {
		return name + " · 基础档"
	}
	return name + " · " + band
}

// NormalizeConfidence grades how much to trust a generated row. An exact
// provider/model hit is as good as it gets; anything that only matched after
// KeyVariants rewrote the id is a guess and is labelled as one.
func NormalizeConfidence(modelID, matchedKey string, exact bool) string {
	if exact {
		return "high"
	}
	if strings.EqualFold(modelID, matchedKey) {
		return "high"
	}
	return "medium"
}
