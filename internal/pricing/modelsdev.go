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
	// CanonicalID is the catalogue's own answer to "who publishes this model".
	// It is present on an entry generated from another provider's model (the
	// TOML's `base_model`) and absent on an entry that defines its own.
	CanonicalID string
	SourceURL   string
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
	// labs holds the catalogue ids that other providers name as the origin of a
	// model. It is a proof rather than a guess: some other entry's
	// canonical_model_id points at each one. A key with no entry of its own is
	// kept - it cannot match a candidate, and the count of those is a fact about
	// the catalogue worth being able to see.
	labs  map[string]bool
	Count int
}

type mdProvider struct {
	Models map[string]struct {
		Name        string `json:"name"`
		CanonicalID string `json:"canonical_model_id"`
		Cost        *struct {
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
				FullID:      p + "/" + m,
				Provider:    p,
				Model:       m,
				Name:        md.Name,
				CanonicalID: md.CanonicalID,
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
	ix.labs = ix.findLabs()
	return ix, nil
}

// findLabs marks the entries that other providers point at as the origin of a
// model, which is what makes publisher preference a lookup instead of a
// hand-maintained list.
//
// The evidence is canonical_model_id: a relay's entry names the lab's entry.
// Nothing is inferred from a name, so a provider the catalogue never links is
// not promoted and its entries stay in the reseller tier.
//
// The set is not filtered against the entries that exist. A key with no entry
// behind it can never equal a candidate's FullID, so the check would cost a
// map lookup per reference and change nothing: dropping it took the set from
// 230 keys to 496 and moved no resolution over 995 observed and multi-candidate
// ids. Keys are kept anyway because a relay routinely names an origin the
// catalogue does not carry, and the size of that gap is the interesting part.
func (ix *MDIndex) findLabs() map[string]bool {
	labs := map[string]bool{}
	for _, e := range ix.byFull {
		if e.CanonicalID == "" || e.CanonicalID == e.FullID {
			continue
		}
		labs[e.CanonicalID] = true
	}
	return labs
}

// firstParty is gone, and there is no list in its place. An earlier revision
// carried one and it was wrong in both directions at once: five of its ids were
// not in the catalogue at all (gemini, mistralai, meta-llama, baidu, bytedance),
// and a name cannot tell a publisher from a relay that resells it - alibaba
// publishes Qwen and resells DeepSeek, and the string "alibaba" says neither.
//
// Where the catalogue gives no evidence a candidate now falls to the reseller
// tier and the provider order decides. That is a convention rather than a fact,
// and it is the lesser evil: the row names a reseller in modelsDevId, where a
// reader can see it, instead of naming a publisher the data never claimed.
//
// aggregators re-brand other vendors' models under a namespaced name
// (openrouter/anthropic/claude-sonnet-4-5). The leaf index peels the namespace
// off, so the re-brand lands in the same leaf group as the vendor's own entry
// and would tie with it; the penalty is what keeps the ordering explicit.
var aggregators = map[string]bool{
	"openrouter": true, "302ai": true, "siliconflow": true, "novita": true,
	"together": true, "fireworks-ai": true, "nebius": true, "hyperbolic": true,
}

// providerScore ranks a candidate for a bare leaf. Higher wins.
//
//	400  the observed id itself names the provider (azure-gpt-5.6-sol)
//	300  the publisher: the catalogue points at this entry as the origin
//	100  ordinary reseller
//	 50  aggregator re-brand
//
// There is one publisher tier because there is one source of truth for it. An
// earlier revision shared 300 between the catalogue's canonical_model_id links
// and a list of provider names, and the tie fell to provider order often enough
// to record the wrong publisher: for "glm-5.2" the relay alibaba beat zhipuai,
// the origin, because "alibaba" sorts first. Both priced the model at 1.4, so no
// total moved - but the row named the relay in modelsDevId, provider and
// sourceUrl alike, which is what those three fields are for.
func providerScore(observed string, e *MDModel, lab bool) int {
	p := strings.ToLower(e.Provider)
	obs := strings.ToLower(observed)
	if obs != "" && (strings.Contains(obs, p+"/") || strings.HasPrefix(obs, p+"-")) {
		return 400
	}
	if lab {
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
// A publisher the catalogue proves is decided by canonical_model_id (findLabs),
// so mistral and xiaomi rank beside openai without either being named in this
// file, and a relay cannot pass itself off as one by spelling its name right.
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
	//
	// Priced and priceless candidates are tracked separately. A guessed provider
	// prefix that happens to exist without a price (azure/deepseek-v4.1-flash)
	// must not shadow the priced entries: returning it would report "no such
	// model" for something 59 resellers list. But a model that exists nowhere
	// with a price is still a hit, not a miss - the caller distinguishes "the
	// model is real but unpriced" from "no such model", and collapsing the two
	// would hide the difference.
	var priced *MDModel
	pricedScore := -1
	var hit *MDModel
	consider := func(e *MDModel) {
		if hit == nil {
			hit = e
		}
		if !e.HasInput {
			return
		}
		if s := providerScore(modelID, e, ix.labs[e.FullID]); s > pricedScore {
			priced, pricedScore = e, s
		}
	}
	seen := map[string]bool{}
	for _, cand := range KeyVariants(modelID) {
		if e, ok := ix.byFull[cand]; ok && !seen[e.FullID] {
			seen[e.FullID] = true
			consider(e)
		}
	}
	if priced != nil && pricedScore >= 300 {
		return priced, priced.FullID, true
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
	if priced != nil {
		return priced, priced.FullID, true
	}
	if hit != nil {
		return hit, hit.FullID, false
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
