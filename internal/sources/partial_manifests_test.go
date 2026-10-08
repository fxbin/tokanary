package sources_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fxbin/tokanary/internal/sources"
)

// TestShippedManifestsDeclareTheirPartialEligibility pins which of the shipped
// adapters actually take the per-file path.
//
// It exists because "eligible" is invisible from the outside: a manifest that
// quietly stops qualifying loses the cache with no error anywhere, and the only
// symptom is a collect that is slow again. The two exclusions are deliberate -
// a driver needs the whole stream, a sqlite source is usually one file - so this
// test fails if one of them ever changes shape without anyone deciding whether
// it should still be excluded.
func TestShippedManifestsDeclareTheirPartialEligibility(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "adapters")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("adapter dir absent")
	}
	manifests, err := sources.LoadAdapters(dir, nil)
	if err != nil {
		t.Fatalf("LoadAdapters: %v", err)
	}
	if len(manifests) == 0 {
		t.Fatalf("no manifests loaded from %s", dir)
	}

	var eligible, whole []string
	for _, m := range manifests {
		if sources.PartialCapable(m) {
			eligible = append(eligible, m.ID)
			continue
		}
		whole = append(whole, m.ID)
	}
	t.Logf("per-file: %v", eligible)
	t.Logf("whole-tool: %v", whole)

	// The adapters that dominate a real machine must be on the cached path, or
	// the feature does nothing where it matters.
	for _, mustBeCached := range []string{
		"deepseek-harness-wrapper", "deepseek-harness",
		"claude-code", "openclaw", "qwen-code",
	} {
		var found bool
		for _, id := range eligible {
			if id == mustBeCached {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not collected per file; the cache would not cover the heaviest source", mustBeCached)
		}
	}
	// And the two that must never be: codex needs cross-line driver state.
	for _, mustBeWhole := range []string{"codex", "opencode"} {
		var m *sources.Manifest
		for _, x := range manifests {
			if x.ID == mustBeWhole {
				m = x
			}
		}
		if m == nil {
			t.Errorf("%s manifest missing", mustBeWhole)
			continue
		}
		if sources.PartialCapable(m) {
			t.Errorf("%s must stay on the whole-tool path", mustBeWhole)
		}
	}
}

// TestCollectAggregateIsReachedForARealManifest is the end-to-end check that the
// command wires the cached path in at all. A per-file cache that the CLI never
// calls is a library nothing uses, and every unit test would still pass.
func TestCollectAggregateIsReachedForARealManifest(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "adapters")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("adapter dir absent")
	}
	manifests, err := sources.LoadAdapters(dir, nil)
	if err != nil {
		t.Fatalf("LoadAdapters: %v", err)
	}
	var m *sources.Manifest
	for _, x := range manifests {
		if x.ID == "deepseek-harness-wrapper" {
			m = x
		}
	}
	if m == nil {
		t.Skip("deepseek-harness-wrapper manifest absent")
	}
	// No source files exist here, so nothing is parsed; what matters is that
	// the call reports a per-file count rather than the whole-tool -1.
	agg, parsed := sources.CollectAggregate(m, &sources.Context{WorkDir: t.TempDir()}, nil)
	if parsed < 0 {
		t.Fatalf("parsed = %d, want >= 0: the heaviest shipped adapter is not on the per-file path", parsed)
	}
	if agg.Detected {
		t.Errorf("Detected = true with no source files")
	}
}
