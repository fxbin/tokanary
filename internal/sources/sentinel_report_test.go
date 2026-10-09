package sources_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fxbin/tokanary/internal/sources"
)

// TestSentinelInflationReport measures what honouring the `$file#$line` dedup
// sentinel would cost, per adapter.
//
// The sentinel appears in 13 shipped manifests as a dedup component, but the
// engine never implemented it: dig() splits a dotted path and looks up a key
// literally named "$file#$line", finds nothing, and skips it. So the key those
// adapters actually use is their other fields with an empty component, and a
// record id repeated across two files collapses into one.
//
// Implementing the sentinel would make the key file-scoped and stop those
// collapses. This test does not change any behaviour - it measures the size of
// the change, because the answer decides whether it is a correction or an
// inflation, and that is not a question to answer by guessing.
//
// It reads local sources only, and skips adapters with none. Nothing here
// writes a cache or a payload.
//
// Gated behind TK_SENTINEL_REPORT=1 on purpose: it parses ~290 files / 12GB of
// decompressed sessions and takes ~22s, so it cannot live in the default suite
// and it must not make `go test ./...` depend on the developer's local data.
func TestSentinelInflationReport(t *testing.T) {
	if os.Getenv("TK_SENTINEL_REPORT") != "1" {
		t.Skip("set TK_SENTINEL_REPORT=1 to measure local sources (reads real session logs, ~22s)")
	}
	dir := filepath.Join("..", "..", "data", "adapters")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("adapter dir absent")
	}
	manifests, err := sources.LoadAdapters(dir, nil)
	if err != nil {
		t.Fatalf("LoadAdapters: %v", err)
	}
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	type row struct {
		id        string
		sentinel  bool
		disabled  bool
		eligible  bool
		files     int
		keys      int
		shared    int   // keys present in more than one file
		tokens    int64 // billable tokens under today's key
		inflation int64 // extra billable tokens if the key became file-scoped
		calls     int
		callDelta int
	}
	var rows []row

	for _, m := range manifests {
		r := row{id: m.ID, eligible: sources.PartialCapable(m), disabled: !m.IsEnabled()}
		for _, p := range m.Dedup {
			if strings.Contains(p, "$file") {
				r.sentinel = true
			}
		}
		// Disabled adapters are measured too: "why is this off?" is exactly the
		// question a sentinel change would have to answer, and skipping them
		// would hide the only adapter here that both declares the sentinel and
		// has data. Nothing is written, so flipping enabled is harmless.
		if !r.eligible {
			if r.sentinel {
				t.Logf("%-26s 声明了哨兵但走整工具路径，本次未测", m.ID)
			}
			continue
		}
		ctx := &sources.Context{Home: home, WorkDir: t.TempDir()}
		files := sources.FilesFor(m, ctx)
		r.files = len(files)
		if len(files) == 0 {
			continue
		}

		// One file at a time, so each contribution knows its file. ReadRecords
		// would interleave them and lose that.
		owner := map[string]string{}
		extra := map[string]int64{}
		for _, f := range files {
			p := sources.RunFile(m, ctx, f)
			if p == nil {
				continue
			}
			for _, c := range p.Contributions {
				if !c.HasUsage {
					continue
				}
				r.keys++
				tok := c.Input + c.CacheRead + c.CacheWrite + c.Output
				r.tokens += tok
				if prev, dup := owner[c.Key]; dup && prev != f {
					// Today this record is dropped: the key was already claimed
					// by another file. With a file-scoped key it would count.
					r.shared++
					extra[c.Key] += tok
				} else {
					owner[c.Key] = f
				}
			}
		}
		for _, v := range extra {
			r.inflation += v
		}
		r.calls = len(owner)
		r.callDelta = r.keys - r.calls
		rows = append(rows, r)
	}

	if len(rows) == 0 {
		t.Skip("no local sources for any eligible adapter")
	}
	t.Logf("%-26s %-8s %-6s %6s %8s %8s %12s %12s %8s",
		"adapter", "sentinel", "files", "keys", "shared", "Δcalls", "tokens", "inflation", "Δ%")
	for _, r := range rows {
		pct := "-"
		if r.tokens > 0 {
			p := float64(r.inflation) / float64(r.tokens) * 100
			pct = strings.TrimSuffix(strings.TrimRight(
				// two decimals without importing strconv twice
				format2(p), "0"), ".")
		}
		name := r.id
		if r.disabled {
			name += " *off*"
		}
		t.Logf("%-26s %-8v %-6d %6d %8d %8d %12d %12d %8s",
			name, r.sentinel, r.files, r.keys, r.shared, r.callDelta, r.tokens, r.inflation, pct)
	}
	t.Logf("(*off* = 适配器声明 enabled:false，本次仍纳入测量)")
}

func format2(f float64) string {
	s := []byte{}
	if f == 0 {
		return "0"
	}
	neg := f < 0
	if neg {
		f = -f
	}
	intPart := int64(f)
	frac := int64((f-float64(intPart))*100 + 0.5)
	s = append(s, []byte(itoa64(intPart))...)
	s = append(s, '.')
	if frac < 10 {
		s = append(s, '0')
	}
	s = append(s, []byte(itoa64(frac))...)
	if neg {
		return "-" + string(s)
	}
	return string(s)
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
