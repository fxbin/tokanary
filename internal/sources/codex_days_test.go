package sources_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/fxbin/tokanary/internal/sources"
)

// Codex parks usage whose model is not known yet (it arrives on a later
// turn_context line). The parked batch used to be emitted with a nil
// timestamp, and Aggregate only files a record into byDay when Ts is a
// non-empty string - so those tokens counted towards byModel and vanished
// from byDay. Measured on a real machine: codex's two views disagreed by
// 1.47B tokens, 4.14% of its total.
//
// The invariant is that a tool's per-model and per-day views are two cuts of
// the same records and must sum to the same thing.
func codexAggregate(t *testing.T, lines []string) *sources.ToolAgg {
	t.Helper()
	root := repoRoot(t)
	ms, err := sources.LoadAdapters(filepath.Join(root, "data", "adapters"), []string{"codex"})
	if err != nil {
		t.Fatalf("LoadAdapters: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("codex adapter not found")
	}
	m := ms[0]

	home := t.TempDir()
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	// The filename carries the session date: rollout-2026-05-01T10-00-00-<uuid>.jsonl
	name := "rollout-2026-05-01T10-00-00-11111111-2222-3333-4444-555555555555.jsonl"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &sources.Context{Home: home, Manifest: m}
	agg := sources.Aggregate(sources.Run(m, ctx), m)
	return agg
}

// sumAgg totals the four billable counters across every bucket of one view.
func sumAgg(agg *sources.ToolAgg, pick func(*sources.ToolAgg) map[string]*sources.ModelAgg) [4]int64 {
	var out [4]int64
	for _, v := range pick(agg) {
		out[0] += v.Input
		out[1] += v.CacheRead
		out[2] += v.CacheWrite
		out[3] += v.Output
	}
	return out
}

func eq4(t *testing.T, what string, got, want [4]int64) {
	t.Helper()
	labels := [4]string{"input", "cacheRead", "cacheWrite", "output"}
	for i := range labels {
		if got[i] != want[i] {
			t.Errorf("%s %s: per-view mismatch %d vs %d", what, labels[i], got[i], want[i])
		}
	}
}

func TestCodexParkedUsageKeepsItsDay(t *testing.T) {
	agg := codexAggregate(t, []string{
		// model is not known yet - this batch gets parked
		`{"timestamp":"2026-05-01T10:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"output_tokens":100,"cached_input_tokens":900,"cache_write_input_tokens":0,"total_tokens":1100}}}}`,
		// a second day, still before the model shows up
		`{"timestamp":"2026-05-02T10:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2000,"output_tokens":200,"cached_input_tokens":1800,"cache_write_input_tokens":0,"total_tokens":2200}}}}`,
		// now the model arrives and the parked batch must flush onto its own days
		`{"timestamp":"2026-05-02T10:00:10.000Z","type":"turn_context","payload":{"model":"gpt-5.4"}}`,
		// after the model is known the normal path runs
		`{"timestamp":"2026-05-03T10:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":3000,"output_tokens":300,"cached_input_tokens":2700,"cache_write_input_tokens":0,"total_tokens":3300}}}}`,
	})

	if !agg.Detected {
		t.Fatal("codex not detected")
	}
	days := make([]string, 0, len(agg.Days))
	for k := range agg.Days {
		days = append(days, k)
	}
	sort.Strings(days)

	// the two parked days must land on their own buckets, not vanish
	for _, want := range []string{"2026-05-01", "2026-05-02", "2026-05-03"} {
		if _, ok := agg.Days[want]; !ok {
			t.Errorf("missing day bucket %s; got %v", want, days)
		}
	}

	eq4(t, "per-model vs per-day",
		sumAgg(agg, func(a *sources.ToolAgg) map[string]*sources.ModelAgg { return a.Models }),
		sumAgg(agg, func(a *sources.ToolAgg) map[string]*sources.ModelAgg { return a.Days }))
}

// input_tokens includes cache, so the parked day's own numbers must show the
// subtraction too - not just survive the trip through the pending bucket.
func TestCodexParkedUsageStillSubtractsCache(t *testing.T) {
	agg := codexAggregate(t, []string{
		`{"timestamp":"2026-05-01T10:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"output_tokens":100,"cached_input_tokens":900,"cache_write_input_tokens":0,"total_tokens":1100}}}}`,
		`{"timestamp":"2026-05-01T10:00:10.000Z","type":"turn_context","payload":{"model":"gpt-5.4"}}`,
	})
	d := agg.Days["2026-05-01"]
	if d == nil {
		t.Fatal("no bucket for 2026-05-01")
	}
	if d.CacheRead != 900 {
		t.Errorf("cacheRead = %d, want 900", d.CacheRead)
	}
	// 1000 raw input - 900 cache read = 100 new input
	if d.Input != 100 {
		t.Errorf("input = %d, want 100 (raw input includes cache)", d.Input)
	}
}

// When the timestamp is missing the rollout filename still knows the session's
// date; without it the parked batch would fall out of byDay again.
func TestCodexParkedUsageFallsBackToFilenameDate(t *testing.T) {
	agg := codexAggregate(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"output_tokens":100,"cached_input_tokens":900,"cache_write_input_tokens":0,"total_tokens":1100}}}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5.4"}}`,
	})
	if _, ok := agg.Days["2026-05-01"]; !ok {
		got := make([]string, 0, len(agg.Days))
		for k := range agg.Days {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Fatalf("filename date not used as fallback; days = %v", got)
	}
}

// The parked batch must still reach the tool-level totals, not just per-model.
// (It does not pin down session attribution: within one day, two sessions'
// parked usage still collapses onto whichever session parked first. Only the
// totals and the day are claimed to be exact.)
func TestCodexParkedUsageReachesAggregate(t *testing.T) {
	agg := codexAggregate(t, []string{
		`{"timestamp":"2026-05-01T10:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"output_tokens":100,"cached_input_tokens":900,"cache_write_input_tokens":0,"total_tokens":1100}}}}`,
		`{"timestamp":"2026-05-01T10:00:10.000Z","type":"turn_context","payload":{"model":"gpt-5.4"}}`,
	})
	if agg.Input == 0 || agg.CacheRead == 0 {
		t.Fatalf("parked usage never reached the aggregate: input=%d cacheRead=%d", agg.Input, agg.CacheRead)
	}
}
