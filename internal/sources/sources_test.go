package sources_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fxbin/tokanary/internal/sources"
)

// TestAdapterParity compares the per-tool aggregate against a reference
// external-usage.json, field for field.
//
// It only means something against a frozen source home. The live opencode.db is
// written continuously (including by the act of running this test), so comparing
// a Go run against a reference taken minutes earlier measures wall-clock drift,
// not adapter correctness. Hence the guard: the test runs only when USERPROFILE
// points inside a frozen home.
//
// NOTE: this gate is currently unreachable. It was driven by
// .cache/run_g3_parity.ps1, which built a synthetic home of frozen sources and
// pointed USERPROFILE/APPDATA at it for both collectors. That script called
// `python core/collect_sources.py` and was removed with the Python pipeline, and
// the frozen home it produced went with it. Nothing rewrites the driver in Go
// yet, so this test always skips. It is kept because the comparison logic is
// correct and worth re-enabling; wiring a hermetic frozen home up in Go is the
// missing piece.
//
// The long-lived replacement for "did the Go port keep python's numbers" is
// TestFrozenParity in internal/warehouse, which compares against a frozen sqlite
// fixture that does not age.
func TestAdapterParity(t *testing.T) {
	if !frozenHomeActive() {
		t.Skip("no frozen source home active; the hermetic driver for this gate has not been ported to Go")
	}
	root := repoRoot(t)
	adapters := filepath.Join(root, "data", "adapters")
	if _, err := os.Stat(adapters); err != nil {
		t.Skip("adapter dir absent")
	}

	refPath := filepath.Join(root, ".cache", "frozen-external-usage.json")
	if _, err := os.Stat(refPath); err != nil {
		refPath = filepath.Join(root, ".cache", "external-usage.json")
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Skipf("reference absent: %v", err)
	}
	var ref struct {
		Tools []struct {
			Tool       string                      `json:"tool"`
			Calls      int                         `json:"calls"`
			Sessions   int                         `json:"sessions"`
			Input      int64                       `json:"input"`
			CacheRead  int64                       `json:"cacheRead"`
			CacheWrite int64                       `json:"cacheWrite"`
			Output     int64                       `json:"output"`
			Reasoning  int64                       `json:"reasoning"`
			Models     map[string]map[string]int64 `json:"models"`
			Days       map[string]map[string]int64 `json:"days"`
			FirstTs    string                      `json:"firstTs"`
			LastTs     string                      `json:"lastTs"`
			Files      int                         `json:"_files"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(refRaw, &ref); err != nil {
		t.Fatalf("reference parse: %v", err)
	}
	refByTool := map[string]int{}
	for i, tr := range ref.Tools {
		refByTool[tr.Tool] = i
	}

	home := os.Getenv("USERPROFILE")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	work := t.TempDir()

	// NOTE: the live opencode.db is written by the very act of running these
	// tools, so a gate that compares against a reference captured earlier
	// measures wall-clock drift, not port correctness. The hermetic gate
	// a hermetic driver would build a synthetic home with frozen sources
	// and points USERPROFILE/APPDATA at it for BOTH the python reference and
	// this test, so both read identical bytes. When run standalone, set
	// USERPROFILE/APPDATA to a frozen home yourself, or accept that opencode
	// (the only continuously-written source) will drift.

	manifests, err := sources.LoadAdapters(adapters, nil)
	if err != nil {
		t.Fatalf("load adapters: %v", err)
	}
	// deterministic order
	seenDetected := 0
	for _, m := range manifests {
		if !m.IsEnabled() {
			continue
		}
		idx, hasRef := refByTool[m.ID]
		if !hasRef {
			continue
		}
		r := ref.Tools[idx]
		// only assert on tools the reference actually detected
		if r.Calls == 0 {
			continue
		}
		seenDetected++
		ctx := &sources.Context{Home: home, WorkDir: work, Prefilter: m.PrefilterPatterns()}
		records := sources.Run(m, ctx)
		agg := sources.Aggregate(records, m)
		p := m.ID
		if agg.Calls != r.Calls {
			t.Errorf("%s.calls: go=%d ref=%d", p, agg.Calls, r.Calls)
		}
		if agg.Sessions != r.Sessions {
			t.Errorf("%s.sessions: go=%d ref=%d", p, agg.Sessions, r.Sessions)
		}
		eq := []struct {
			name     string
			got, ref int64
		}{
			{"input", agg.Input, r.Input},
			{"cacheRead", agg.CacheRead, r.CacheRead},
			{"cacheWrite", agg.CacheWrite, r.CacheWrite},
			{"output", agg.Output, r.Output},
			{"reasoning", agg.Reasoning, r.Reasoning},
		}
		for _, e := range eq {
			if e.got != e.ref {
				t.Errorf("%s.%s: go=%d ref=%d", p, e.name, e.got, e.ref)
			}
		}
		if agg.FirstTs != r.FirstTs {
			t.Errorf("%s.firstTs: go=%q ref=%q", p, agg.FirstTs, r.FirstTs)
		}
		if agg.LastTs != r.LastTs {
			t.Errorf("%s.lastTs: go=%q ref=%q", p, agg.LastTs, r.LastTs)
		}
		// per-model and per-day token sums
		if len(agg.Models) != len(r.Models) {
			t.Errorf("%s.models.count: go=%d ref=%d", p, len(agg.Models), len(r.Models))
		}
		for name, gm := range agg.Models {
			rm, ok := r.Models[name]
			if !ok {
				t.Errorf("%s.models.%s: go present, ref absent", p, name)
				continue
			}
			if gm.Input != rm["input"] || gm.Output != rm["output"] {
				t.Errorf("%s.models.%s: go(in=%d,out=%d) ref(in=%d,out=%d)",
					p, name, gm.Input, gm.Output, rm["input"], rm["output"])
			}
		}
		if len(agg.Days) != len(r.Days) {
			t.Errorf("%s.days.count: go=%d ref=%d", p, len(agg.Days), len(r.Days))
		}
		for day, gd := range agg.Days {
			rd, ok := r.Days[day]
			if !ok {
				t.Errorf("%s.days.%s: go present, ref absent", p, day)
				continue
			}
			// python's ZERO tuple sums all five counters, reasoning included
			goTotal := gd.Input + gd.CacheRead + gd.CacheWrite + gd.Output + gd.Reasoning
			if gd.Input != rd["input"] || goTotal != rdTotal(rd) {
				t.Errorf("%s.days.%s: go(in=%d,total=%d) ref(in=%d,total=%d)",
					p, day, gd.Input, goTotal, rd["input"], rdTotal(rd))
			}
		}
	}
	if seenDetected == 0 {
		t.Log("no detected tools in reference; nothing compared")
	}
}

// frozenHomeActive reports whether USERPROFILE points at the synthetic lab home
// built by a hermetic frozen-source driver.
func frozenHomeActive() bool {
	up := os.Getenv("USERPROFILE")
	if up == "" {
		return false
	}
	return strings.Contains(filepath.ToSlash(up), "/.cache/frozen-lab/")
}

func rdTotal(m map[string]int64) int64 {
	return m["input"] + m["cacheRead"] + m["cacheWrite"] + m["output"] + m["reasoning"]
}

// TestValidate mirrors collect_sources --validate: every adapter declaration
// must be well-formed regardless of data availability.
func TestValidate(t *testing.T) {
	root := repoRoot(t)
	adapters := filepath.Join(root, "data", "adapters")
	manifests, err := sources.LoadAdapters(adapters, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(manifests) == 0 {
		t.Fatal("no adapters loaded")
	}
	for _, m := range manifests {
		if errs := sources.Validate(m); len(errs) > 0 {
			t.Errorf("%s: %v", m.ID, errs)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// go.mod lives at the repo root, so the directory holding it IS the root.
	// Stop before walking past it.
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("cannot locate repo root")
	return ""
}
