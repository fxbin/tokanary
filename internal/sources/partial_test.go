package sources

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// jsonl writes one JSONL session file and returns its path.
func jsonl(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func msg(id, session, model, ts string, in, out, cr, cw int) string {
	return `{"message":{"id":"` + id + `"},"sessionId":"` + session +
		`","model":"` + model + `","timestamp":"` + ts + `",` +
		`"usage":{"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) +
		`,"cache_read_input_tokens":` + itoa(cr) + `,"cache_creation_input_tokens":` + itoa(cw) + `}}`
}

func itoa(n int) string {
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

func demoManifest(paths ...string) *Manifest {
	return &Manifest{
		ID: "demo", Label: "Demo", Kind: "jsonl", Paths: paths,
		Dedup: []string{"message.id"},
		Fields: map[string][]string{
			"model":   {"model"},
			"session": {"sessionId"},
			"time":    {"timestamp"},
			"input":   {"usage.input_tokens"},
			"output":  {"usage.output_tokens"},
		},
	}
}

// wholeTool is the operation the cache has to be indistinguishable from.
func wholeTool(t *testing.T, m *Manifest, work string) *ToolAgg {
	t.Helper()
	ctx := &Context{WorkDir: work}
	return Aggregate(Run(m, ctx), m)
}

// TestPartialMergeEqualsWholeToolParse is the safety net for the whole feature.
//
// A summed cache is only legitimate if it produces exactly what parsing
// everything at once produces. Anything less - a rounding difference, a session
// double-counted, a day bucket off - would quietly move a number on a billing
// dashboard, so the comparison is field by field rather than on totals alone.
func TestPartialMergeEqualsWholeToolParse(t *testing.T) {
	dir := t.TempDir()
	jsonl(t, dir, "a.jsonl",
		msg("m1", "s1", "alpha", "2026-03-01T10:00:00Z", 100, 20, 900, 50),
		msg("m2", "s1", "alpha", "2026-03-01T11:00:00Z", 100, 20, 900, 50),
		msg("m3", "s2", "beta", "2026-03-02T09:00:00Z", 7, 3, 0, 0),
	)
	// A duplicate inside one file: the dedup rule must drop it in both paths.
	jsonl(t, dir, "b.jsonl",
		msg("m4", "s3", "beta", "2026-03-02T12:00:00Z", 5, 1, 0, 0),
		msg("m4", "s3", "beta", "2026-03-02T12:00:00Z", 5, 1, 0, 0),
	)
	// A record with no usage still claims its identity, exactly as before.
	jsonl(t, dir, "c.jsonl",
		msg("m5", "s4", "gamma", "2026-03-03T08:00:00Z", 0, 0, 0, 0),
		msg("m6", "s4", "gamma", "2026-03-03T08:30:00Z", 11, 2, 3, 4),
	)
	m := demoManifest(filepath.Join(dir, "*.jsonl"))

	want := wholeTool(t, m, t.TempDir())

	work := t.TempDir()
	ctx := &Context{WorkDir: work}
	files := ExpandPaths(m.Paths, ctx)
	got, parsed := CollectAggregate(m, ctx, files)
	if parsed != len(files) {
		t.Fatalf("first collect parsed %d files, want %d", parsed, len(files))
	}

	if !reflect.DeepEqual(normalizeForCompare(got), normalizeForCompare(want)) {
		t.Fatalf("merged partials differ from a whole-tool parse\n got: %+v\nwant: %+v", got, want)
	}
}

// TestSecondCollectReadsNothing is the payoff: the second run must not open a
// single source file, or the cache is decoration.
func TestSecondCollectReadsNothing(t *testing.T) {
	dir := t.TempDir()
	jsonl(t, dir, "a.jsonl", msg("m1", "s1", "alpha", "2026-03-01T10:00:00Z", 100, 20, 900, 50))
	jsonl(t, dir, "b.jsonl", msg("m2", "s2", "beta", "2026-03-02T10:00:00Z", 3, 1, 0, 0))
	m := demoManifest(filepath.Join(dir, "*.jsonl"))
	work := t.TempDir()

	ctx := &Context{WorkDir: work}
	files := ExpandPaths(m.Paths, ctx)
	first, parsedFirst := CollectAggregate(m, ctx, files)
	if parsedFirst != 2 {
		t.Fatalf("first collect parsed %d, want 2", parsedFirst)
	}

	ctx2 := &Context{WorkDir: work}
	files2 := ExpandPaths(m.Paths, ctx2)
	second, parsedSecond := CollectAggregate(m, ctx2, files2)
	if parsedSecond != 0 {
		t.Fatalf("second collect parsed %d files, want 0", parsedSecond)
	}
	if !reflect.DeepEqual(normalizeForCompare(second), normalizeForCompare(first)) {
		t.Fatalf("cached collect differs from the collect that populated it\n got: %+v\nwant: %+v", second, first)
	}
}

// TestOnlyTheChangedFileIsReread is the case that made this worth building: a
// month of sessions where one is still being written.
func TestOnlyTheChangedFileIsReread(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		jsonl(t, dir, n+".jsonl", msg("m"+n, "s"+n, "alpha", "2026-03-01T10:00:00Z", 10, 1, 0, 0))
	}
	m := demoManifest(filepath.Join(dir, "*.jsonl"))
	work := t.TempDir()

	ctx := &Context{WorkDir: work}
	CollectAggregate(m, ctx, ExpandPaths(m.Paths, ctx))

	// The active session gets one more message.
	live := filepath.Join(dir, "c.jsonl")
	extra := msg("mc2", "sc", "alpha", "2026-03-01T11:00:00Z", 4, 2, 0, 0)
	fh, err := os.OpenFile(live, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(extra + "\n"); err != nil {
		t.Fatal(err)
	}
	fh.Close()

	ctx2 := &Context{WorkDir: work}
	_, parsed := CollectAggregate(m, ctx2, ExpandPaths(m.Paths, ctx2))
	if parsed != 1 {
		t.Fatalf("re-collected %d files after one changed, want 1", parsed)
	}

	want := wholeTool(t, m, t.TempDir())
	got, _ := CollectAggregate(m, &Context{WorkDir: work}, ExpandPaths(m.Paths, &Context{WorkDir: work}))
	if !reflect.DeepEqual(normalizeForCompare(got), normalizeForCompare(want)) {
		t.Fatalf("incremental result differs from a whole-tool parse\n got: %+v\nwant: %+v", got, want)
	}
}

// TestTruncatedFileInvalidatesItsPartial covers the case a size check exists
// for. A session file that shrinks was rotated or truncated, so the cached
// aggregate for it describes data that no longer exists and must not be summed.
func TestTruncatedFileInvalidatesItsPartial(t *testing.T) {
	dir := t.TempDir()
	p := jsonl(t, dir, "a.jsonl",
		msg("m1", "s1", "alpha", "2026-03-01T10:00:00Z", 100, 20, 900, 50),
		msg("m2", "s1", "alpha", "2026-03-01T11:00:00Z", 100, 20, 900, 50),
	)
	m := demoManifest(filepath.Join(dir, "*.jsonl"))
	work := t.TempDir()

	ctx := &Context{WorkDir: work}
	CollectAggregate(m, ctx, []string{p})

	// Rewrite with a single record: smaller than before.
	jsonl(t, dir, "a.jsonl", msg("m1", "s1", "alpha", "2026-03-01T10:00:00Z", 100, 20, 900, 50))

	ctx2 := &Context{WorkDir: work}
	got, parsed := CollectAggregate(m, ctx2, []string{p})
	if parsed != 1 {
		t.Fatalf("truncated file was served from cache (parsed %d, want 1)", parsed)
	}
	if got.Calls != 1 {
		t.Fatalf("calls = %d, want 1: a truncated file must not keep the old records", got.Calls)
	}
}

// TestCrossFileDuplicateIsCountedOnceByTheReplay is the case that decided the
// cache's shape.
//
// The dedup key names no file, so a message id present in two files is one call.
// A summed partial would report it twice. This is not hypothetical: on a real
// machine the DeepSeek Desktop wrapper repeats 79% of its message ids across
// its 250 session files, and its entire duplicate count comes from cross-file
// collisions. The replay therefore has to drop the later copy, and drop it the
// same way a whole-tool parse would - which is what the field-by-field
// comparison pins.
func TestCrossFileDuplicateIsCountedOnceByTheReplay(t *testing.T) {
	dir := t.TempDir()
	shared := msg("shared-id", "s1", "alpha", "2026-03-01T10:00:00Z", 500, 100, 0, 0)
	jsonl(t, dir, "a.jsonl", shared, msg("m2", "s1", "alpha", "2026-03-01T11:00:00Z", 1, 1, 0, 0))
	jsonl(t, dir, "b.jsonl", shared, msg("m3", "s2", "alpha", "2026-03-01T12:00:00Z", 1, 1, 0, 0))
	m := demoManifest(filepath.Join(dir, "*.jsonl"))
	work := t.TempDir()

	ctx := &Context{WorkDir: work}
	got, _ := CollectAggregate(m, ctx, ExpandPaths(m.Paths, ctx))

	if got.Input != 502 {
		t.Fatalf("input = %d, want 502 (500 + 1 + 1): the shared id was counted twice", got.Input)
	}
	if got.Calls != 3 {
		t.Fatalf("calls = %d, want 3: the shared id survived as an extra call", got.Calls)
	}
	want := wholeTool(t, m, t.TempDir())
	if !reflect.DeepEqual(normalizeForCompare(got), normalizeForCompare(want)) {
		t.Fatalf("replay differs from a whole-tool parse\n got: %+v\nwant: %+v", got, want)
	}
}

// TestReplayReproducesTheWholeToolDropCount keeps the note line honest. The
// wrapper's dashboard card reports how many duplicate rows were dropped; if the
// replay counted only the within-file ones it would silently start claiming a
// much smaller number than before.
func TestReplayReproducesTheWholeToolDropCount(t *testing.T) {
	dir := t.TempDir()
	shared := msg("shared-id", "s1", "alpha", "2026-03-01T10:00:00Z", 500, 100, 0, 0)
	jsonl(t, dir, "a.jsonl", shared, shared, msg("m2", "s1", "alpha", "2026-03-01T11:00:00Z", 1, 1, 0, 0))
	jsonl(t, dir, "b.jsonl", shared, msg("m3", "s2", "alpha", "2026-03-01T12:00:00Z", 1, 1, 0, 0))
	m := demoManifest(filepath.Join(dir, "*.jsonl"))

	wholeCtx := &Context{WorkDir: t.TempDir()}
	Aggregate(Run(m, wholeCtx), m)
	if wholeCtx.Dropped != 2 {
		t.Fatalf("whole-tool dropped %d, want 2 (one repeat in a.jsonl, one repeat across files)", wholeCtx.Dropped)
	}

	replayCtx := &Context{WorkDir: t.TempDir()}
	CollectAggregate(m, replayCtx, ExpandPaths(m.Paths, replayCtx))
	if replayCtx.Dropped != wholeCtx.Dropped {
		t.Fatalf("replay dropped %d, whole-tool dropped %d", replayCtx.Dropped, wholeCtx.Dropped)
	}
}

// TestDriverAndSQLiteKeepTheWholeToolPath documents what is deliberately NOT
// incremental. A driver needs the whole stream (codex carries model names
// forward and differences cumulative counters); a sqlite source is usually one
// file, where per-file means everything.
func TestDriverAndSQLiteKeepTheWholeToolPath(t *testing.T) {
	drv := &Manifest{ID: "d", Kind: "jsonl", Driver: "codex"}
	if PartialCapable(drv) {
		t.Error("a manifest with a driver must not be collected per file")
	}
	sq := &Manifest{ID: "s", Kind: "sqlite", DB: "x.db"}
	if PartialCapable(sq) {
		t.Error("a sqlite manifest must not be collected per file")
	}
	for _, k := range []string{"jsonl", "json", "zstd-jsonl"} {
		if !PartialCapable(&Manifest{ID: "k", Kind: k}) {
			t.Errorf("kind %q should support per-file collection", k)
		}
	}
	// An unset kind means jsonl, so it inherits the file-based path rather than
	// opting out of it.
	if !PartialCapable(&Manifest{ID: "k"}) {
		t.Error("the default kind is jsonl and should support per-file collection")
	}
	// An unrecognised kind must not be swept in: nothing knows how to read it,
	// and a cache would freeze whatever it produced.
	if PartialCapable(&Manifest{ID: "k", Kind: "mystery"}) {
		t.Error("an unknown kind must not opt in")
	}
}

// TestCollectIsIdempotent is the discipline TokenTracker documents as a hard
// lesson: run the incremental path twice and the second run must not shift
// anything. State pollution in an incremental collector is invisible in normal
// use, because nobody collects twice and diffs.
func TestCollectIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	jsonl(t, dir, "a.jsonl",
		msg("m1", "s1", "alpha", "2026-03-01T10:00:00Z", 100, 20, 900, 50),
		msg("m1", "s1", "alpha", "2026-03-01T10:00:00Z", 100, 20, 900, 50),
		msg("m2", "s2", "beta", "2026-03-02T10:00:00Z", 4, 2, 0, 0))
	jsonl(t, dir, "b.jsonl", msg("m3", "s3", "beta", "2026-03-02T11:00:00Z", 9, 3, 1, 1))
	m := demoManifest(filepath.Join(dir, "*.jsonl"))
	work := t.TempDir()

	var runs [3]*ToolAgg
	for i := range runs {
		ctx := &Context{WorkDir: work}
		agg, _ := CollectAggregate(m, ctx, ExpandPaths(m.Paths, ctx))
		runs[i] = agg
	}
	for i := 1; i < len(runs); i++ {
		if !reflect.DeepEqual(normalizeForCompare(runs[i]), normalizeForCompare(runs[0])) {
			t.Fatalf("run %d differs from run 0\n got: %+v\nwant: %+v", i, runs[i], runs[0])
		}
	}
}

// TestPrunePartialsDropsOrphans keeps the cache from growing as sessions
// rotate away.
func TestPrunePartialsDropsOrphans(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	keep := jsonl(t, dir, "keep.jsonl", msg("m1", "s1", "alpha", "2026-03-01T10:00:00Z", 1, 1, 0, 0))
	gone := jsonl(t, dir, "gone.jsonl", msg("m2", "s2", "alpha", "2026-03-01T10:00:00Z", 1, 1, 0, 0))
	m := demoManifest(filepath.Join(dir, "*.jsonl"))

	ctx := &Context{WorkDir: work}
	CollectAggregate(m, ctx, []string{keep, gone})

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	ctx2 := &Context{WorkDir: work}
	CollectAggregate(m, ctx2, []string{keep})

	entries, err := os.ReadDir(filepath.Join(work, "partials", m.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("partial dir holds %d files, want 1: %v", len(entries), names)
	}
}

// normalizeForCompare drops the fields that legitimately differ between a
// cached and a fresh run, so the comparison is about the numbers.
func normalizeForCompare(a *ToolAgg) any {
	type view struct {
		Tool            string
		Label           string
		Detected        bool
		Sessions, Calls int
		Input           int64
		CacheRead       int64
		CacheWrite      int64
		Output          int64
		Reasoning       int64
		Models, Days    map[string]*ModelAgg
		FirstTs, LastTs string
	}
	cp := func(m map[string]*ModelAgg) map[string]*ModelAgg {
		out := map[string]*ModelAgg{}
		for k, v := range m {
			c := *v
			out[k] = &c
		}
		return out
	}
	return view{
		Tool: a.Tool, Label: a.Label, Detected: a.Detected,
		Sessions: a.Sessions, Calls: a.Calls,
		Input: a.Input, CacheRead: a.CacheRead, CacheWrite: a.CacheWrite,
		Output: a.Output, Reasoning: a.Reasoning,
		Models: cp(a.Models), Days: cp(a.Days),
		FirstTs: a.FirstTs, LastTs: a.LastTs,
	}
}
