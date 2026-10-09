package pricing_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxbin/tokanary/internal/pricing"
)

func day(n int) time.Time {
	return time.Date(2026, 10, 8+n, 12, 0, 0, 0, time.UTC)
}

func writeTable(t *testing.T, dir, stamp string) string {
	t.Helper()
	p := filepath.Join(dir, "prices-raw.json")
	body := `{"fetchedAt":"` + stamp + `","source":"models.dev","models":[{"target":"x","cost":{"input":1}}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPriceTableAge(t *testing.T) {
	// Stamp is a date, not an instant: a table generated this morning must not
	// read as several hours stale.
	age, ok := pricing.PriceTableAge("2026-10-08", day(0))
	if !ok {
		t.Fatal("2026-10-08 should parse")
	}
	if age != 0 {
		t.Fatalf("same-day age = %v, want 0", age)
	}
	age, ok = pricing.PriceTableAge("2026-10-01", day(0))
	if !ok || age != 7*24*time.Hour {
		t.Fatalf("7-day age = %v ok=%v", age, ok)
	}
	// Unusable stamps report unusable rather than "fresh forever".
	for _, bad := range []any{nil, "", "not-a-date", 12345} {
		if _, ok := pricing.PriceTableAge(bad, day(0)); ok {
			t.Fatalf("%v should not parse", bad)
		}
	}
}

func TestInspectPrices(t *testing.T) {
	dir := t.TempDir()
	maxAge := 7 * 24 * time.Hour

	// 没有价表也算 stale。只看 Stale 的调用方必须能发现「缺表」——否则全部金额停在
	// $0 而没有任何东西触发更新，而这正是这个检查存在的理由。
	missing := pricing.InspectPrices(filepath.Join(dir, "nope.json"), maxAge, day(0))
	if missing.Exists || !missing.Stale {
		t.Fatalf("missing table must read as stale: %+v", missing)
	}

	p := writeTable(t, dir, "2026-10-08")
	st := pricing.InspectPrices(p, maxAge, day(0))
	if !st.Exists || st.Stale || st.FetchedAt != "2026-10-08" {
		t.Fatalf("fresh table reported stale/odd: %+v", st)
	}

	old := writeTable(t, dir, "2026-09-01")
	st = pricing.InspectPrices(old, maxAge, day(0))
	if !st.Stale {
		t.Fatalf("37-day-old table should be stale: %+v", st)
	}
	if st.Reason == "" {
		t.Fatal("a stale table must explain itself in words the UI can show")
	}

	// An unreadable stamp is stale, not infinitely fresh: otherwise a corrupt
	// date would silently pin the table in place forever.
	bad := writeTable(t, dir, "garbage")
	if st := pricing.InspectPrices(bad, maxAge, day(0)); !st.Stale {
		t.Fatalf("garbage stamp should be stale: %+v", st)
	}
}

// The table is about to be rewritten without a human watching, so a crash
// mid-write must not be able to leave a truncated catalogue behind - every later
// run would then price against a partial table and under-report silently.
func TestAtomicWriteFileReplacesCleanly(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "prices-raw.json")
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pricing.AtomicWriteFile(dst, []byte("new-and-longer")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new-and-longer" {
		t.Fatalf("content = %q", b)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "prices-raw.json" {
			t.Fatalf("leftover file: %s", e.Name())
		}
	}
}

// Windows rename cannot replace an existing file, so the temp path has to be
// reached through a rename that succeeds. This asserts the end state rather than
// the mechanism, which is what actually broke before.
func TestAtomicWriteFileOverwritesRepeatedly(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "t.json")
	for i, want := range []string{"a", "bb", "ccc"} {
		if err := pricing.AtomicWriteFile(dst, []byte(want)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		b, _ := os.ReadFile(dst)
		if string(b) != want {
			t.Fatalf("write %d: content = %q, want %q", i, b, want)
		}
	}
}
