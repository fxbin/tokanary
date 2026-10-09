package sources

import (
	"strconv"
	"testing"
	"time"
)

func TestLocalHourKey(t *testing.T) {
	// 上一条线的时区假设：适配器写的是 UTC（尾缀 Z）或带偏移，本地时读回来必须
	// 和 time.Parse(...).Local() 一致，否则小时轴就是错的。
	offsets := map[string]bool{
		"2026-09-08T05:45:23.848Z":  true,
		"2026-09-08T05:45:23+08:00": true,
		"2026-09-08 05:45:23":       false,
		"2026-09-08T05:45:23.848":   false,
	}
	for ts, parseable := range offsets {
		got := localHourKey(ts)
		if got == "" {
			t.Fatalf("%q produced no bucket", ts)
		}
		if len(got) != len("2006-01-02T15") {
			t.Fatalf("%q: bucket %q is not YYYY-MM-DDTHH", ts, got)
		}
		if parseable {
			tm, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil {
				t.Fatalf("parse %q: %v", ts, err)
			}
			if want := tm.Local().Format("2006-01-02T15"); got != want {
				t.Fatalf("%q: got %q, want local %q", ts, got, want)
			}
		} else if want := ts[:13]; got != want {
			t.Fatalf("%q: got %q, want wall-clock %q", ts, got, want)
		}
	}
	// 短于 13 字符的戳没有小时可言。
	for _, ts := range []string{"", "2026-09-08"} {
		if got := localHourKey(ts); got != "" {
			t.Fatalf("%q should not bucket, got %q", ts, got)
		}
	}
}

// 小时桶必须是本地时，否则 UTC 的记录会整体平移若干小时。
func TestLocalHourKeyConvertsOffset(t *testing.T) {
	if time.Local == time.UTC {
		t.Skip("本机时区就是 UTC，转换不可观测")
	}
	utc := "2026-09-08T23:30:00Z"
	got := localHourKey(utc)
	ts, err := time.Parse(time.RFC3339Nano, utc)
	if err != nil {
		t.Fatal(err)
	}
	want := ts.Local().Format("2006-01-02T15")
	if got != want {
		t.Fatalf("got %q, want local %q", got, want)
	}
	// 关键性质：转换后不能跨日 —— 否则同一条记录会在小时表和日表里落在不同天。
	if got[:10] != ts.Local().Format("2006-01-02") {
		t.Fatalf("hour bucket date drifted from local date: %q", got)
	}
}

// 小时桶不得影响日桶：日桶切的是源自己的字符串，改动它等于改掉所有既有数字。
func TestHoursDoNotDisturbDays(t *testing.T) {
	m := &Manifest{ID: "t"}
	recs := []Record{
		{Tool: "t", Model: "m", Session: "s", Ts: "2026-09-08T05:45:23.848Z", Output: 10},
		{Tool: "t", Model: "m", Session: "s", Ts: "2026-09-08T06:45:23.848Z", Output: 20},
		{Tool: "t", Model: "m", Session: "s", Ts: "2026-09-09T05:45:23.848Z", Output: 30},
	}
	a := Aggregate(recs, m)
	if len(a.Days) != 2 {
		t.Fatalf("expected 2 day buckets, got %d (%v)", len(a.Days), a.Days)
	}
	// 日键取自源字符串的前 10 位，与改动前一致。
	if a.Days["2026-09-08"].Output != 30 {
		t.Fatalf("2026-09-08 output = %d, want 30", a.Days["2026-09-08"].Output)
	}
	if a.Days["2026-09-09"].Output != 30 {
		t.Fatalf("2026-09-09 output = %d, want 30", a.Days["2026-09-09"].Output)
	}
	if a.Output != 60 {
		t.Fatalf("total output = %d, want 60", a.Output)
	}
	var hourSum int64
	for _, h := range a.Hours {
		hourSum += h.Total()
	}
	if hourSum != a.Output {
		t.Fatalf("hour buckets sum to %d, days/total say %d — 小时桶漏了记录", hourSum, a.Output)
	}
}

// partial 重放也必须产出小时 —— 否则增量路径与整工具路径不等价，
// 而这正是上一批修复「整工具解析 vs 缓存重放逐字段一致」建立起来的保证。
func TestPartialMergeCarriesHours(t *testing.T) {
	m := &Manifest{ID: "t"}
	recs := []Record{
		{Tool: "t", Model: "m", Session: "s1", Ts: "2026-09-08T05:45:23.848Z", Output: 10},
		{Tool: "t", Model: "m", Session: "s2", Ts: "2026-09-08T05:55:23.848Z", Output: 20},
		{Tool: "t", Model: "m", Session: "s3", Ts: "2026-09-09T21:10:00.000Z", Output: 40},
	}
	full := Aggregate(recs, m)

	// 每个 partial 装一条记录的贡献：Contribution.Ts 在真实路径上由 RunFile
	// 从 Record.Ts 抄来，Key 则是 dedupKeyFor 给出的身份（缺字段时回退到
	// `ID:file#line`，永不返回空串）。这里照抄那两层，验证 merge 侧会重算出
	// 同一批小时桶 —— 键若为空会被当成重复丢掉，正是上一批修复要防的那类偏差。
	parts := make([]*Partial, 0, len(recs))
	for i, r := range recs {
		ts, _ := r.Ts.(string)
		parts = append(parts, &Partial{
			Tool: m.ID,
			Contributions: []Contribution{{
				Key: m.ID + ":s" + strconv.Itoa(i) + "#" + strconv.Itoa(i),
				Ts:  ts, Model: r.Model, Session: r.Session,
				Output: r.Output, HasUsage: true,
			}},
		})
	}
	merged, _ := MergePartials(m, parts, len(parts))

	if len(full.Hours) != len(merged.Hours) {
		t.Fatalf("hour bucket count differs: whole=%d replay=%d", len(full.Hours), len(merged.Hours))
	}
	for k, a := range full.Hours {
		b, ok := merged.Hours[k]
		if !ok {
			t.Fatalf("replay is missing hour bucket %q", k)
		}
		if a.Total() != b.Total() {
			t.Fatalf("hour %q: whole=%d replay=%d", k, a.Total(), b.Total())
		}
	}
	if len(full.Hours) != 2 {
		t.Fatalf("expected 2 distinct hours, got %d (%v)", len(full.Hours), full.Hours)
	}
}
