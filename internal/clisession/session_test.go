package clisession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const fixtures = "../../testdata/clisession"

// TZ is pinned to UTC so day/hour derivations are reproducible; the golden
// generator pins UTC too. Named zones are not usable on Windows (no tzdata).
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

func TestGoldenParity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(fixtures, "golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var want Golden
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	got := Collect([]string{fixtures}, nil).Golden()

	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	wantJSON, _ := json.MarshalIndent(want, "", "  ")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
}

func TestRevisionsExcluded(t *testing.T) {
	res := Collect([]string{fixtures}, nil)
	for _, tr := range res.Turns {
		if tr.Session == "sess-c" && tr.Total != 10 {
			t.Errorf("revision copy leaked into totals: sess-c total=%d want 10", tr.Total)
		}
		if tr.Total > 1_000_000 {
			t.Errorf("implausible turn total %d (revision double count?)", tr.Total)
		}
	}
}

func TestToolErrorCounted(t *testing.T) {
	res := Collect([]string{fixtures}, nil)
	got, ok := res.Tools["read"]
	if !ok || got[0] != 1 || got[1] != 1 {
		t.Errorf("tools[read] = %v, want [1 1] (isError must be attributed to the tool)", got)
	}
}

func TestExcludeIDsDedupe(t *testing.T) {
	all := Collect([]string{fixtures}, nil)
	excluded := Collect([]string{fixtures}, map[string]bool{"sess-a": true, "sess-b": true})
	if excluded.SkippedDup != 2 {
		t.Errorf("SkippedDup = %d, want 2", excluded.SkippedDup)
	}
	if len(excluded.Sessions) != len(all.Sessions)-2 {
		t.Errorf("sessions = %d, want %d", len(excluded.Sessions), len(all.Sessions)-2)
	}
	for _, s := range excluded.Sessions {
		if s.ID == "sess-a" || s.ID == "sess-b" {
			t.Errorf("excluded session %s still present", s.ID)
		}
	}
}

func TestTurnSemantics(t *testing.T) {
	res := Collect([]string{fixtures}, nil)
	bySession := map[string][]TurnRow{}
	for _, tr := range res.Turns {
		bySession[tr.Session] = append(bySession[tr.Session], tr)
	}
	a := bySession["sess-a"]
	if len(a) != 1 {
		t.Fatalf("sess-a turns = %d, want 1 (one user message = one turn)", len(a))
	}
	if a[0].Input != 6 || a[0].Output != 64 || a[0].CacheRead != 1611 || a[0].CacheWrite != 1674 || a[0].Total != 3355 {
		t.Errorf("sess-a tool-loop totals wrong: %+v", a[0])
	}
	if len(bySession["sess-b"]) != 3 {
		t.Errorf("sess-b turns = %d, want 3 (user, compaction, user)", len(bySession["sess-b"]))
	}
}

func TestCanonicalModel(t *testing.T) {
	cases := map[string]string{
		"azure-gpt-5.6-sol":           "gpt-5.6-sol",
		"kimi/kimi-k3":                "kimi-k3",
		"gpt-5.6-sol--int":            "gpt-5.6-sol",
		"deepseek-v4-flash-ga-260731": "deepseek-v4-flash",
		"gpt-5.6-sol":                 "gpt-5.6-sol",
		"":                            "(unknown)",
	}
	for in, want := range cases {
		if got := CanonicalModel(in); got != want {
			t.Errorf("CanonicalModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncatedLineTolerated(t *testing.T) {
	res := Collect([]string{fixtures}, nil)
	for _, tr := range res.Turns {
		if tr.Input == 999 || tr.Output == 999 {
			t.Errorf("truncated line was parsed: %+v", tr)
		}
	}
}
