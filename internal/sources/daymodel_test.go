package sources

import "testing"

func TestAccumulatorHasDayModel(t *testing.T) {
	a := newAccumulator()
	a.add(Record{Tool: "t", Model: "m1", Session: "s1", Ts: "2026-09-08T05:00:00Z", Output: 10})
	a.add(Record{Tool: "t", Model: "m1", Session: "s2", Ts: "2026-09-09T06:00:00Z", Output: 20})
	a.add(Record{Tool: "t", Model: "m2", Session: "s3", Ts: "2026-09-08T07:00:00Z", Output: 30})
	t.Logf("byModel=%d byDay=%d byDayModel=%d", len(a.byModel), len(a.byDay), len(a.byDayModel))
	for k, v := range a.byDayModel {
		t.Logf("  key=%q total=%d", k, v.Total())
	}
	if len(a.byDayModel) != 3 {
		t.Fatalf("byDayModel has %d entries, want 3", len(a.byDayModel))
	}
	agg := a.agg(&Manifest{ID: "t"}, 1)
	t.Logf("ToolAgg.DayModel entries=%d", len(agg.DayModel))
	if len(agg.DayModel) != 3 {
		t.Fatalf("ToolAgg.DayModel has %d entries, want 3", len(agg.DayModel))
	}
}

// The separator must round-trip: external.go splits on it to recover day and
// model, so a padded key would ship whitespace into the payload.
func TestDayModelKeySplitsCleanly(t *testing.T) {
	a := newAccumulator()
	a.add(Record{Tool: "t", Model: "claude-opus-5", Session: "s", Ts: "2026-09-08T05:00:00Z", Output: 1})
	for k := range a.byDayModel {
		day, model, ok := cutDayModel(k)
		if !ok {
			t.Fatalf("key %q does not split", k)
		}
		t.Logf("day=%q model=%q", day, model)
		if day != "2026-09-08" || model != "claude-opus-5" {
			t.Fatalf("round-trip gave day=%q model=%q", day, model)
		}
	}
}

func cutDayModel(k string) (string, string, bool) {
	const sep = "\x1f"
	for i := 0; i+len(sep) <= len(k); i++ {
		if k[i:i+len(sep)] == sep {
			return k[:i], k[i+len(sep):], true
		}
	}
	return "", "", false
}
