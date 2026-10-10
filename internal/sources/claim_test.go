package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSession creates a file that looks like one DSH session log.
func writeSession(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "session.v4.jsonl.zstd")
	if err := os.WriteFile(p, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAssignFilesGivesEachFileToOneAdapter is the regression guard for the DSH
// double count: with %DSH_HOME% pointing at the desktop wrapper's own harness
// directory, both DSH adapters match the same files. Without assignment the
// official adapter read all 286 wrapper files and billed every call twice.
//
// The manifests are loaded the way the collector loads them rather than being
// handed over in a chosen order, so the test also pins the ordering the fix
// depends on: LoadAdapters sorts by file name, and "deepseek-harness-wrapper"
// sorts before "deepseek-harness", so the wrapper keeps its own files.
func TestAssignFilesGivesEachFileToOneAdapter(t *testing.T) {
	home := t.TempDir()
	appdata := t.TempDir()
	adapters := t.TempDir()

	wrapperRoot := filepath.Join(appdata, "dsh-desktop", "harness", "sessions")
	officialRoot := filepath.Join(home, ".dsh", "sessions")

	writeSession(t, filepath.Join(wrapperRoot, "--proj-a--", "s1"))
	writeSession(t, filepath.Join(wrapperRoot, "--proj-b--", "s2"))
	officialFile := writeSession(t, filepath.Join(officialRoot, "--proj-c--", "s3"))

	// The exact condition that caused it: DSH Desktop exports DSH_HOME
	// pointing at its private harness dir.
	t.Setenv("DSH_HOME", filepath.Join(appdata, "dsh-desktop", "harness"))
	t.Setenv("APPDATA", appdata)

	writeAdapter(t, adapters, "deepseek-harness.json", `{
	  "id": "deepseek-harness",
	  "label": "DeepSeek Harness（官方）",
	  "kind": "zstd-jsonl",
	  "paths": [
	    "%DSH_HOME%/sessions/**/*.jsonl.zstd",
	    "{home}/.dsh/sessions/**/*.jsonl.zstd"
	  ]
	}`)
	writeAdapter(t, adapters, "deepseek-harness-wrapper.json", `{
	  "id": "deepseek-harness-wrapper",
	  "label": "DeepSeek Harness（DSH Desktop）",
	  "kind": "zstd-jsonl",
	  "paths": ["%APPDATA%/dsh-desktop/harness/sessions/**/*.jsonl.zstd"]
	}`)

	manifests, err := LoadAdapters(adapters, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Home: home, WorkDir: t.TempDir()}

	// The hazard is real: the official adapter matches all three files even
	// though only one of them lives under the official root.
	raw := map[string]int{}
	for _, m := range manifests {
		raw[m.ID] = len(FilesFor(m, ctx))
	}
	if raw["deepseek-harness"] != 3 {
		t.Fatalf("夹具没复现重叠：官方适配器只匹配到 %d 个文件，应为 3", raw["deepseek-harness"])
	}
	if raw["deepseek-harness-wrapper"] != 2 {
		t.Fatalf("夹具没搭好：wrapper 应匹配 2 个文件，实际 %d", raw["deepseek-harness-wrapper"])
	}

	claims := AssignFiles(manifests, ctx)
	got := map[string][]string{}
	var overlap int
	for _, c := range claims {
		got[c.Manifest.ID] = c.Files
		overlap += len(c.Overlap)
	}

	if len(got["deepseek-harness"]) != 1 {
		t.Fatalf("官方适配器应只拿到自己的 1 个文件，实际 %d 个: %v",
			len(got["deepseek-harness"]), got["deepseek-harness"])
	}
	if got["deepseek-harness"][0] != officialFile {
		t.Errorf("官方适配器拿到了别人的文件: %s", got["deepseek-harness"][0])
	}
	if len(got["deepseek-harness-wrapper"]) != 2 {
		t.Fatalf("wrapper 应拿到 2 个文件，实际 %d", len(got["deepseek-harness-wrapper"]))
	}

	// The overlap must be visible, not silently swallowed: two adapters
	// matching one file is a fact about the machine worth printing.
	if overlap != 2 {
		t.Errorf("应报告 2 处重叠，实际 %d", overlap)
	}
	lines := OverlapLines(claims)
	if len(lines) != 1 {
		t.Fatalf("应有 1 行重叠告警，实际 %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "deepseek-harness") ||
		!strings.Contains(lines[0], "deepseek-harness-wrapper") {
		t.Errorf("告警未点名两个适配器: %s", lines[0])
	}
}

func writeAdapter(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAssignFilesFirstManifestWins pins the tie-break: load order decides, so
// the outcome is a function of the repository rather than of map iteration.
func TestAssignFilesFirstManifestWins(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, "shared")
	p := writeSession(t, shared)

	first := &Manifest{ID: "aaa", Kind: "zstd-jsonl",
		Paths: []string{"{home}/shared/**/*.jsonl.zstd"}}
	second := &Manifest{ID: "bbb", Kind: "zstd-jsonl",
		Paths: []string{"{home}/shared/**/*.jsonl.zstd"}}

	claims := AssignFiles([]*Manifest{first, second}, &Context{Home: home, WorkDir: t.TempDir()})
	if len(claims[0].Files) != 1 || claims[0].Files[0] != p {
		t.Errorf("先声明的适配器应拿到文件: %v", claims[0].Files)
	}
	if len(claims[1].Files) != 0 {
		t.Errorf("后声明的适配器应拿不到: %v", claims[1].Files)
	}
	if len(claims[1].Overlap) != 1 || claims[1].Overlap[0].Owner != "aaa" {
		t.Errorf("重叠应记在后者名下并指明属主: %+v", claims[1].Overlap)
	}
}

// TestAssignFilesDisjointAdaptersUnaffected guards the ordinary case: two
// adapters reading genuinely different directories must both keep everything.
func TestAssignFilesDisjointAdaptersUnaffected(t *testing.T) {
	home := t.TempDir()
	a := writeSession(t, filepath.Join(home, "a"))
	b := writeSession(t, filepath.Join(home, "b"))

	claims := AssignFiles([]*Manifest{
		{ID: "one", Kind: "zstd-jsonl", Paths: []string{"{home}/a/**/*.jsonl.zstd"}},
		{ID: "two", Kind: "zstd-jsonl", Paths: []string{"{home}/b/**/*.jsonl.zstd"}},
	}, &Context{Home: home, WorkDir: t.TempDir()})

	if len(claims[0].Files) != 1 || claims[0].Files[0] != a {
		t.Errorf("适配器 one 应只拿到 a: %v", claims[0].Files)
	}
	if len(claims[1].Files) != 1 || claims[1].Files[0] != b {
		t.Errorf("适配器 two 应只拿到 b: %v", claims[1].Files)
	}
	if lines := OverlapLines(claims); lines != nil {
		t.Errorf("互不相交时不应有告警: %v", lines)
	}
}
