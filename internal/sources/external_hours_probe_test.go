package sources_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fxbin/tokanary/internal/sources"
)

// 外部工具真的只有「日」粒度吗？
//
// 聚合器把 Ts 截到 10 字符存进 byDay，小时被丢在聚合边界上；但 Record.Ts 与
// Contribution.Ts 一路带着完整时间戳。所以这个问题不是「数据有没有」，而是
// 「有没有人去读」。这个测试直接问源文件。
func TestExternalRecordsCarryHour(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "adapters")
	manifests, err := sources.LoadAdapters(dir, nil)
	if err != nil {
		t.Fatalf("LoadAdapters: %v", err)
	}
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	type probe struct {
		ID          string   `json:"id"`
		Files       int      `json:"files"`
		Probed      int      `json:"probed"`
		Records     int      `json:"records"`
		WithTs      int      `json:"withTs"`
		WithHour    int      `json:"withHour"`
		HourPct     float64  `json:"hourPct"`
		Days        []string `json:"days"`
		SampleHours []string `json:"sampleHours"`
		SampleTs    []string `json:"sampleTs"`
	}
	var out []probe
	for _, m := range manifests {
		if !m.IsEnabled() {
			continue
		}
		ctx := &sources.Context{Home: home, WorkDir: t.TempDir()}
		files := sources.FilesFor(m, ctx)
		if len(files) == 0 {
			continue
		}
		// 每个适配器最多探 3 个文件：DSH wrapper 有 250 个，全读要 12 秒。
		n := len(files)
		if n > 3 {
			n = 3
		}
		p := probe{ID: m.ID, Files: len(files), Probed: n, Days: []string{}, SampleHours: []string{}, SampleTs: []string{}}
		for _, f := range files[:n] {
			part := sources.RunFile(m, ctx, f)
			if part == nil {
				continue
			}
			for _, c := range part.Contributions {
				if !c.HasUsage {
					continue
				}
				p.Records++
				ts := c.Ts
				if ts == "" {
					continue
				}
				p.WithTs++
				if len(ts) > 10 {
					if len(p.SampleHours) < 4 {
						p.SampleHours = append(p.SampleHours, ts)
					}
					p.WithHour++
				}
				if len(p.SampleTs) < 3 {
					p.SampleTs = append(p.SampleTs, ts)
				}
				d := ts
				if len(d) > 10 {
					d = d[:10]
				}
				if len(p.Days) < 5 {
					p.Days = append(p.Days, d)
				}
			}
		}
		if p.WithTs > 0 {
			p.HourPct = float64(p.WithHour) / float64(p.WithTs) * 100
		}
		out = append(out, p)
	}
	b, _ := json.MarshalIndent(out, "", " ")
	t.Log("外部工具小时粒度探测：\n" + string(b))
}
