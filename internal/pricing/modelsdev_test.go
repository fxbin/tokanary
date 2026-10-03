package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixture is a miniature models.dev payload carrying the cases that actually
// bit during development: a bare leaf offered by a first-party provider and by
// two resellers at different prices, a gateway-style vendor prefix, an
// aggregator that namespaces another vendor's model, and a model with no
// price at all.
const fixture = `{
  "alibaba": {"models": {"qwen3.8-max": {"name":"Qwen3.8 Max","cost":{"input":2,"output":6,"cache_read":0.25,"cache_write":2.5}}}},
  "302ai":   {"models": {"qwen3.8-max": {"name":"Qwen","cost":{"input":2.16,"output":6.36}}}},
  "deepseek":{"models": {"deepseek-v4-pro": {"name":"V4 Pro","cost":{"input":0.435,"output":0.87,"cache_read":0.003625}},
                          "deepseek-v4-flash":{"name":"V4 Flash","cost":{"input":0.15,"output":0.6,"cache_read":0.003}}}},
  "azure":   {"models": {"deepseek-v4-pro": {"name":"V4 Pro on Azure","cost":{"input":1.74,"output":3.48}},
                          "gpt-5.6-sol":    {"name":"Sol on Azure","cost":{"input":4,"output":20,"cache_read":0.5,"cache_write":6.25}}}},
  "openai":  {"models": {"gpt-5.6-sol": {"name":"GPT-5.6 Sol","cost":{"input":4,"output":20,"cache_read":0.4,"cache_write":5}}}},
  "openrouter": {"models": {"google/gemini-3.6-flash": {"name":"Gemini Flash","cost":{"input":0.75,"output":3.75,"cache_read":0.075}}}},
  "google":  {"models": {"gemini-3.6-flash": {"name":"Gemini 3.6 Flash","cost":{"input":0.75,"output":3.75,"cache_read":0.075}}}},
  "unpriced": {"models": {"mystery-model": {"name":"Mystery"}}}
}`

func testIndex(t *testing.T) *MDIndex {
	t.Helper()
	ix, err := ParseModelsDev([]byte(fixture))
	if err != nil {
		t.Fatalf("ParseModelsDev: %v", err)
	}
	return ix
}

func TestResolvePrefersFirstPartyOverReseller(t *testing.T) {
	ix := testIndex(t)
	// 302ai and alibaba both sell qwen3.8-max. Alphabetically 302ai wins, which
	// is exactly the wrong answer; first party must win instead.
	e, key, ok := ix.Resolve("qwen3.8-max")
	if !ok || e.Provider != "alibaba" {
		t.Fatalf("qwen3.8-max -> %s/%s ok=%v, want alibaba", e.Provider, key, ok)
	}
}

func TestResolveDoesNotInheritGatewayPrefixOrder(t *testing.T) {
	ix := testIndex(t)
	// KeyVariants probes "azure/" well before "deepseek/" because that order is
	// right for the LiteLLM cost map. A bare "deepseek-v4-pro" must still land
	// on deepseek's own price (0.435), not azure's 4x markup (1.74).
	e, _, ok := ix.Resolve("deepseek-v4-pro")
	if !ok || e.Provider != "deepseek" {
		t.Fatalf("deepseek-v4-pro -> %s ok=%v, want deepseek", e.Provider, ok)
	}
	if e.Cost.Input == nil || *e.Cost.Input != 0.435 {
		t.Fatalf("deepseek-v4-pro input = %v, want 0.435", e.Cost.Input)
	}
}

func TestResolveHonoursVendorPrefixInObservedID(t *testing.T) {
	ix := testIndex(t)
	// The id says azure, so azure's price is the one actually being billed -
	// even though openai outranks azure as a first-party publisher.
	e, _, ok := ix.Resolve("azure-gpt-5.6-sol")
	if !ok || e.Provider != "azure" {
		t.Fatalf("azure-gpt-5.6-sol -> %s ok=%v, want azure", e.Provider, ok)
	}
	e2, _, ok2 := ix.Resolve("gpt-5.6-sol--int")
	if !ok2 || e2.Provider != "openai" {
		t.Fatalf("gpt-5.6-sol--int -> %s ok=%v, want openai (--int peeled)", e2.Provider, ok2)
	}
}

func TestResolveSkipsAggregatorNamespace(t *testing.T) {
	ix := testIndex(t)
	// openrouter nests the name as "google/gemini-3.6-flash". Asking for the
	// bare leaf must reach google's own entry, not openrouter's re-brand.
	e, _, ok := ix.Resolve("gemini-3.6-flash")
	if !ok || e.Provider != "google" {
		t.Fatalf("gemini-3.6-flash -> %s ok=%v, want google", e.Provider, ok)
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	ix := testIndex(t)
	// A leaf with several equally-ranked candidates must return the same entry
	// on every call, or a regenerated table would drift between runs.
	first, _, _ := ix.Resolve("deepseek-v4-pro")
	for i := 0; i < 200; i++ {
		e, _, _ := ix.Resolve("deepseek-v4-pro")
		if e.FullID != first.FullID {
			t.Fatalf("run %d resolved %s, first run gave %s", i, e.FullID, first.FullID)
		}
	}
}

func TestResolveModelWithoutPriceIsAHitNotAMiss(t *testing.T) {
	ix := testIndex(t)
	e, _, ok := ix.Resolve("mystery-model")
	if e == nil {
		t.Fatal("mystery-model: expected an entry, got nil")
	}
	if ok {
		t.Fatal("mystery-model has no input price; ok must be false")
	}
	if e.HasInput {
		t.Fatal("HasInput must be false for a priceless entry")
	}
}

func TestResolveUnknownModel(t *testing.T) {
	ix := testIndex(t)
	if e, _, ok := ix.Resolve("totally-unknown-xyz"); e != nil || ok {
		t.Fatalf("unknown model -> %v ok=%v, want nil/false", e, ok)
	}
}

func TestResolveExactFullKey(t *testing.T) {
	ix := testIndex(t)
	e, key, ok := ix.Resolve("alibaba/qwen3.8-max")
	if !ok || key != "alibaba/qwen3.8-max" {
		t.Fatalf("full key -> %s ok=%v", key, ok)
	}
	if e.Provider != "alibaba" {
		t.Fatalf("provider = %s, want alibaba", e.Provider)
	}
}

// A generated table must be readable by the same loader the pricer uses, and
// each observed alias must reach its row through KeyVariants.
func TestGeneratedTableRoundTripsThroughLoadCurated(t *testing.T) {
	ix := testIndex(t)
	observed := []string{
		"azure-gpt-5.6-sol", "gpt-5.6-sol--int", "deepseek-v4-pro",
		"qwen3.8-max", "gemini-3.6-flash", "deepseek-v4-flash",
	}
	type row struct {
		Target string   `json:"target"`
		PiIDs  []string `json:"piIds"`
		Cost   Cost     `json:"cost"`
	}
	byDev := map[string]*row{}
	for _, id := range observed {
		md, _, ok := ix.Resolve(id)
		if !ok {
			t.Fatalf("%s did not resolve", id)
		}
		r, seen := byDev[md.FullID]
		if !seen {
			r = &row{Target: md.Model, Cost: md.Cost}
			byDev[md.FullID] = r
		}
		r.PiIDs = append(r.PiIDs, id)
	}
	doc := struct {
		Models []row `json:"models"`
	}{}
	for _, r := range byDev {
		doc.Models = append(doc.Models, *r)
	}
	blob, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "prices-raw.json")
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	curated, _, ok := LoadCurated(path)
	if !ok {
		t.Fatal("LoadCurated rejected the generated table")
	}
	for _, id := range observed {
		hit := false
		for _, cand := range KeyVariants(id) {
			if _, found := curated[cand]; found {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("%s cannot reach any generated row via KeyVariants", id)
		}
	}
}
