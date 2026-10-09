package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxbin/tokanary/internal/pricing"
)

func writeStaleTable(t *testing.T, stamp string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "prices-raw.json")
	body := `{"fetchedAt":"` + stamp + `","source":"models.dev","models":[{"target":"keep-me","cost":{"input":1}}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 抓取失败必须：保留旧表、不让调用方失败、说清还能怎么手动重试。
// 这是自动刷新能放进无人值守路径的唯一前提 —— 一次 collect 不该因为某个网站慢而失败。
func TestMaybeRefreshPricesKeepsOldTableOnFailure(t *testing.T) {
	p := writeStaleTable(t, "2026-01-01")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	// Port 9 (discard) refuses immediately: no wait, no flakiness.
	opts := priceOpts{
		RepoRoot: t.TempDir(), Out: p, WorkDir: t.TempDir(),
		URL: "http://127.0.0.1:9/dead", ModelsArg: "some-model", Quiet: true,
	}
	updated, err := maybeRefreshPrices(opts, 7*24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("a failed fetch must not surface as an error: %v", err)
	}
	if updated {
		t.Fatal("updated should be false when the fetch failed")
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("the old table must survive a failed fetch: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("table was modified by a failed fetch:\n before %s\n after  %s", before, after)
	}
	// And no half-written temp file left in place of it.
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if e.Name() != "prices-raw.json" {
			t.Fatalf("leftover file after failed fetch: %s", e.Name())
		}
	}
}

// 有效期内不碰网络：--if-stale 是要挂进日常流程的，每跑一次都抓 5MB 不可接受。
func TestMaybeRefreshPricesSkipsFreshTable(t *testing.T) {
	stamp := time.Now().Format("2006-01-02")
	p := writeStaleTable(t, stamp) // written fresh today
	before, _ := os.ReadFile(p)
	opts := priceOpts{
		RepoRoot: t.TempDir(), Out: p, WorkDir: t.TempDir(),
		URL: "http://127.0.0.1:9/dead", ModelsArg: "some-model", Quiet: true,
	}
	updated, err := maybeRefreshPrices(opts, 7*24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("fresh table must not even try: %v", err)
	}
	if updated {
		t.Fatal("a fresh table must not be refreshed")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("fresh table was rewritten")
	}
}

// 完全没有表时必须生成一张：没有它，每个金额都是 $0，而一个静默归零的看板比一个
// 慢的看板更糟。
func TestMaybeRefreshPricesGeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "missing.json")
	// No table, and the catalogue URL is dead: the attempt must be made and the
	// failure reported, because "no table" is not a state to sit in quietly.
	opts := priceOpts{
		RepoRoot: dir, Out: p, WorkDir: t.TempDir(),
		URL: "http://127.0.0.1:9/dead", ModelsArg: "some-model", Quiet: true,
	}
	updated, err := maybeRefreshPrices(opts, 7*24*time.Hour, time.Now())
	if err == nil {
		t.Fatal("generating from a dead URL must report the failure")
	}
	if updated {
		t.Fatal("nothing was written, so updated must be false")
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("a failed first generation must not leave a file behind")
	}
}

// 过期阈值可配置，且默认是 7 天 —— 上游目录的迭代节奏，不是本项目的偏好。
func TestDefaultPriceMaxAgeIsAWeek(t *testing.T) {
	if pricing.DefaultPriceMaxAge != 7*24*time.Hour {
		t.Fatalf("default max age = %v, want 7 days", pricing.DefaultPriceMaxAge)
	}
}
