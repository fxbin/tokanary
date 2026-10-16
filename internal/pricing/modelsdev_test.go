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
//
// The canonical_model_id links below are copied from the live catalogue, not
// invented for the fixture: 302ai/qwen3.8-max names alibaba's entry, azure's
// entries name openai's and deepseek's, openrouter names google's. That is how
// the tier is earned now that there is no provider list, and it is why the
// publisher cases below still resolve the way the old list made them.
//
// kiln/cup-1 is the same evidence for a lab the catalogue never needed to name:
// the relay points at it and the lab's entry is present, so the lab wins a leaf
// it would otherwise lose alphabetically. barda/pilot-1 and cygnus/wraith-1 are
// the two controls - nobody links "barda", and the link "aaahost" writes for
// wraith-1 names an entry the catalogue does not have, so neither lab earns the
// tier and the reseller order decides instead. The labs are deliberately not
// named after their models: "volta-1" reads as the vendor prefix "volta-" and
// would score 400 on a technicality instead of on evidence.
//
// alibaba/zhipuai on glm-5.2 is the shape that used to go wrong: both names were
// in the removed list, so both reached one tier and the alphabetical tie-break
// handed the model to the relay that resells it.
const fixture = `{
  "alibaba": {"models": {"qwen3.8-max": {"name":"Qwen3.8 Max","cost":{"input":2,"output":6,"cache_read":0.25,"cache_write":2.5}},
                          "glm-5.2": {"name":"GLM 5.2 on Alibaba","cost":{"input":1.4,"output":4.2},
                                      "canonical_model_id":"zhipuai/glm-5.2"}}},
  "302ai":   {"models": {"qwen3.8-max": {"name":"Qwen","cost":{"input":2.16,"output":6.36},
                                          "canonical_model_id":"alibaba/qwen3.8-max"}}},
  "deepseek":{"models": {"deepseek-v4-pro": {"name":"V4 Pro","cost":{"input":0.435,"output":0.87,"cache_read":0.003625}},
                          "deepseek-v4-flash":{"name":"V4 Flash","cost":{"input":0.15,"output":0.6,"cache_read":0.003}}}},
  "azure":   {"models": {"deepseek-v4-pro": {"name":"V4 Pro on Azure","cost":{"input":1.74,"output":3.48},
                                              "canonical_model_id":"deepseek/deepseek-v4-pro"},
                          "gpt-5.6-sol":    {"name":"Sol on Azure","cost":{"input":4,"output":20,"cache_read":0.5,"cache_write":6.25},
                                              "canonical_model_id":"openai/gpt-5.6-sol"}}},
  "openai":  {"models": {"gpt-5.6-sol": {"name":"GPT-5.6 Sol","cost":{"input":4,"output":20,"cache_read":0.4,"cache_write":5}}}},
  "openrouter": {"models": {"google/gemini-3.6-flash": {"name":"Gemini Flash","cost":{"input":0.75,"output":3.75,"cache_read":0.075},
                                                        "canonical_model_id":"google/gemini-3.6-flash"}}},
  "google":  {"models": {"gemini-3.6-flash": {"name":"Gemini 3.6 Flash","cost":{"input":0.75,"output":3.75,"cache_read":0.075}}}},
  "zhipuai": {"models": {"glm-5.2": {"name":"GLM 5.2","cost":{"input":1.4,"output":4.2}}}},
  "kiln":    {"models": {"cup-1": {"name":"Cup 1","cost":{"input":1,"output":2}}}},
  "aaahost": {"models": {"cup-1": {"name":"Cup 1 on AAA","cost":{"input":9,"output":18},
                                  "canonical_model_id":"kiln/cup-1"},
                         "pilot-1": {"name":"Pilot 1 on AAA","cost":{"input":9,"output":18}},
                         "wraith-1": {"name":"Wraith 1 on AAA","cost":{"input":9,"output":18},
                                     "canonical_model_id":"ghostlabs/wraith-1"}}},
  "barda":   {"models": {"pilot-1": {"name":"Pilot 1","cost":{"input":1,"output":2}}}},
  "cygnus":  {"models": {"wraith-1": {"name":"Wraith 1","cost":{"input":1,"output":2}}}},
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

func TestResolvePrefersProvenPublisherOverReseller(t *testing.T) {
	ix := testIndex(t)
	// 302ai and alibaba both sell qwen3.8-max. Alphabetically 302ai wins, which
	// is exactly the wrong answer, and nothing in the fixture names alibaba as a
	// publisher: 302ai's own entry points at alibaba's, which is the catalogue
	// saying who publishes it.
	e, key, ok := ix.Resolve("qwen3.8-max")
	if !ok || e.Provider != "alibaba" {
		t.Fatalf("qwen3.8-max -> %s/%s ok=%v, want alibaba", e.Provider, key, ok)
	}
}

func TestResolveDoesNotInheritGatewayPrefixOrder(t *testing.T) {
	ix := testIndex(t)
	// KeyVariants probes "azure/" well before "deepseek/" because that order is
	// right for the LiteLLM cost map. A bare "deepseek-v4-pro" must still land
	// on deepseek's own price (0.435), not azure's 4x markup (1.74) - azure's
	// entry points back at deepseek's, so the origin wins on evidence and not on
	// the order of a list.
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
	// even though azure's entry names openai as the origin of the same model.
	e, _, ok := ix.Resolve("azure-gpt-5.6-sol")
	if !ok || e.Provider != "azure" {
		t.Fatalf("azure-gpt-5.6-sol -> %s ok=%v, want azure", e.Provider, ok)
	}
	// The same two entries, asked without a provider: now the origin wins.
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

func TestResolvePrefersPublisherProvenByCanonicalID(t *testing.T) {
	ix := testIndex(t)
	// kiln is nobody's famous lab and sorts after its relay. aaahost names it in
	// canonical_model_id, so the catalogue says who publishes cup-1 and the price
	// must follow that, not the alphabet.
	e, _, ok := ix.Resolve("cup-1")
	if !ok || e.Provider != "kiln" {
		t.Fatalf("cup-1 -> %s ok=%v, want kiln", e.Provider, ok)
	}
	if e.Cost.Input == nil || *e.Cost.Input != 1 {
		t.Fatalf("cup-1 input = %v, want the publisher's 1", e.Cost.Input)
	}
	if e.CanonicalID != "" {
		t.Fatalf("the lab's own entry must carry no canonical id, got %q", e.CanonicalID)
	}
}

func TestResolveProvenOriginBeatsAlphabeticallyFirstRelay(t *testing.T) {
	ix := testIndex(t)
	// alibaba and zhipuai both reached the publisher tier while a provider list
	// existed, and the tie fell to provider order - handing zhipuai's model to
	// the relay that resells it, because "alibaba" sorts first. The price
	// happened to match; modelsDevId, provider and sourceUrl did not.
	e, _, ok := ix.Resolve("glm-5.2")
	if !ok || e.Provider != "zhipuai" {
		t.Fatalf("glm-5.2 -> %s ok=%v, want zhipuai (the proven origin)", e.Provider, ok)
	}
	if e.CanonicalID != "" {
		t.Fatalf("zhipuai/glm-5.2 defines the model, so it carries no canonical id, got %q", e.CanonicalID)
	}
}

func TestResolveDoesNotPromoteOnUnprovenLinks(t *testing.T) {
	ix := testIndex(t)
	// Two ways a link proves nothing, and neither may promote anyone: nobody
	// points at barda, and the id aaahost writes for wraith-1 names an entry the
	// catalogue does not have. Both leaves then fall to the reseller tier and
	// the provider order decides - a convention, and the accepted cost of having
	// no provider list: the row names aaahost in modelsDevId instead of claiming
	// a publisher the data never named.
	if e, _, _ := ix.Resolve("pilot-1"); e.Provider != "aaahost" {
		t.Fatalf("pilot-1 -> %s, want the unpromoted aaahost", e.Provider)
	}
	if e, _, _ := ix.Resolve("wraith-1"); e.Provider != "aaahost" {
		t.Fatalf("wraith-1 -> %s, want aaahost (dangling canonical id)", e.Provider)
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

func TestKeyVariantsStripsEfficiencySuffix(t *testing.T) {
	vs := KeyVariants("gpt-5.4(xhigh)")
	found := false
	for _, v := range vs {
		if v == "gpt-5.4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gpt-5.4(xhigh) should derive gpt-5.4, got %v", vs)
	}
	// must NOT peel mini/pro
	vs2 := KeyVariants("gpt-5.4-mini")
	for _, v := range vs2 {
		if v == "gpt-5.4" {
			t.Fatalf("gpt-5.4-mini must not strip -mini")
		}
	}
}
