package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Per-file aggregates let an unchanged source file be skipped instead of
// re-parsed. On a real machine that is the difference between touching 246
// files and touching the 3 that actually moved.
//
// Two properties make it safe, and both come from what the cache IS rather than
// how clever it is:
//
//   - It is derived, not authoritative. The session logs are the truth; a lost
//     or stale partial costs one re-parse, never data. So there is no flush
//     protocol, no write-ahead log and no crash recovery - none of the machinery
//     a storage engine needs for data it owns. Only atomicity matters, because a
//     half-written partial would silently poison every later merge.
//   - It cannot change a total. MergePartials refuses to add anything up when
//     two files claim the same dedup key, and the caller falls back to a whole
//     parse. Since the dedup key deliberately excludes the file name, summing is
//     only valid while no key crosses a file boundary, and that is checked
//     rather than assumed.

// partialSchemaVer invalidates every cached partial when the format or the
// aggregation semantics change. A stale partial that is merely re-read would
// merge into wrong totals; a stale partial that is ignored costs a re-parse.
const partialSchemaVer = 1

// Contribution is one claimed record: the identity dedup works on, plus the
// record it produced (nil when the record carried no usage).
//
// Storing both is what makes a cache replayable. On a real machine the
// DeepSeek Desktop wrapper repeats 79% of its message ids across files, so
// cross-file dedup is where almost all of its duplicates are found - an
// aggregate-only partial cannot express that, and summing one would report those
// calls about four times over.
type Contribution struct {
	Key        string `json:"k"`
	Model      string `json:"m,omitempty"`
	Session    string `json:"s,omitempty"`
	Ts         string `json:"t,omitempty"`
	Input      int64  `json:"i,omitempty"`
	CacheRead  int64  `json:"cr,omitempty"`
	CacheWrite int64  `json:"cw,omitempty"`
	Output     int64  `json:"o,omitempty"`
	Reasoning  int64  `json:"r,omitempty"`
	HasUsage   bool   `json:"u,omitempty"`
}

// record rebuilds what Aggregate would have folded, or nil for a claim that
// carried no usage - matching normalize, which drops those records entirely.
func (c Contribution) record(tool string) *Record {
	if !c.HasUsage {
		return nil
	}
	return &Record{
		Tool: tool, Model: c.Model, Session: c.Session, Ts: c.Ts,
		Input: c.Input, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite,
		Output: c.Output, Reasoning: c.Reasoning,
	}
}

// Partial is one source file's contribution to a tool aggregate.
//
// It stores the claimed records rather than pre-summed counters, because the
// sums are only valid within a single file: the same message id can legitimately
// appear in two files, and which copy survives is a decision only the merge can
// make, in file order.
type Partial struct {
	SchemaVer     int            `json:"schemaVer"`
	Tool          string         `json:"tool"`
	Path          string         `json:"path"`
	Size          int64          `json:"size"`
	Mtime         int64          `json:"mtime"`
	Dropped       int            `json:"dropped"`
	Contributions []Contribution `json:"c,omitempty"`
}

// PartialCapable reports whether a manifest may be collected file by file.
//
// Two kinds are excluded on purpose. A driver needs the whole stream at once -
// codex carries model names forward across lines and differences cumulative
// counters, so a per-file slice would silently produce different numbers. A
// sqlite source is usually a single database file, where "per file" degenerates
// to "all of it" and the bookkeeping buys nothing; incremental sqlite needs a
// row watermark, which is a different feature.
func PartialCapable(m *Manifest) bool {
	if m.Driver != "" {
		return false
	}
	switch m.KindOrDefault() {
	case "jsonl", "json", "zstd-jsonl":
		return true
	}
	return false
}

// partialDir is where a tool's partials live. It sits under the cache dir
// because it is rebuildable, exactly like the rest of .cache.
func partialDir(workDir, tool string) string {
	return filepath.Join(workDir, "partials", tool)
}

// partialName encodes both identity and freshness, so a stale file cannot be
// mistaken for a current one and truncation needs no separate check: a smaller
// file simply hashes to a different name.
func partialName(path string, size, mtime int64) string {
	sum := sha256.Sum256([]byte(path))
	h := hex.EncodeToString(sum[:8])
	return fmt.Sprintf("%s-%d-%d-v%d.json", h, size, mtime, partialSchemaVer)
}

// LoadPartial reads the cached aggregate for one file, or reports false when
// there is nothing usable.
func LoadPartial(workDir string, p *Partial) bool {
	if p.SchemaVer != partialSchemaVer {
		return false
	}
	path := filepath.Join(partialDir(workDir, p.Tool), partialName(p.Path, p.Size, p.Mtime))
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var got Partial
	if err := json.Unmarshal(raw, &got); err != nil {
		// A truncated or corrupted partial is a cache miss, not an error: the
		// file it describes is still on disk and can be re-read.
		return false
	}
	if got.SchemaVer != partialSchemaVer || got.Path != p.Path || got.Size != p.Size {
		return false
	}
	*p = got
	return true
}

// SavePartial writes a partial atomically.
//
// The temporary file is in the same directory so the rename cannot cross a
// filesystem boundary, and a crash therefore leaves either the previous partial
// or the new one - never a half-written file that would parse as valid JSON
// with truncated counters.
func SavePartial(workDir string, p *Partial) error {
	dir := partialDir(workDir, p.Tool)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	final := filepath.Join(dir, partialName(p.Path, p.Size, p.Mtime))
	tmp, err := os.CreateTemp(dir, ".partial-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, final)
}

// RunFile parses one file and records what it claims.
func RunFile(m *Manifest, ctx *Context, file string) *Partial {
	st, err := os.Stat(file)
	if err != nil {
		return nil
	}
	var raw []*rawObj
	switch m.KindOrDefault() {
	case "json":
		raw = ReadJSON([]string{file}, ctx)
	case "zstd-jsonl":
		raw = ReadZstdJSONL([]string{file}, ctx)
	default:
		raw = ReadJSONL([]string{file}, ctx)
	}

	p := &Partial{
		SchemaVer: partialSchemaVer,
		Tool:      m.ID,
		Path:      file,
		Size:      st.Size(),
		Mtime:     st.ModTime().Unix(),
	}
	droppedBefore := ctx.Dropped
	normalizeAll(raw, m, ctx, func(key string, rec *Record) {
		c := Contribution{Key: key}
		if rec != nil {
			c.Model, c.Session = rec.Model, rec.Session
			if ts, ok := rec.Ts.(string); ok {
				c.Ts = ts
			}
			c.Input, c.CacheRead, c.CacheWrite = rec.Input, rec.CacheRead, rec.CacheWrite
			c.Output, c.Reasoning, c.HasUsage = rec.Output, rec.Reasoning, true
		}
		p.Contributions = append(p.Contributions, c)
	})
	p.Dropped = ctx.Dropped - droppedBefore
	return p
}

// MergePartials replays per-file contributions into the tool aggregate.
//
// This is a literal re-run of normalizeAll's dedup followed by Aggregate's
// arithmetic, over the same file order a whole-tool parse would have used. That
// is the point: a key seen in an earlier file drops the later copy exactly as it
// would have, so the totals cannot differ from a full parse. Summing counters
// would have been cheaper and wrong - on the DeepSeek Desktop wrapper 79% of
// message ids appear in more than one file.
//
// dropped reports how many records the replay discarded, so the caller's note
// line reads the same as after a full parse.
func MergePartials(m *Manifest, parts []*Partial, files int) (*ToolAgg, int) {
	a := newAccumulator()
	seen := map[string]bool{}
	dropped := 0
	for _, p := range parts {
		dropped += p.Dropped
		for _, c := range p.Contributions {
			if seen[c.Key] {
				dropped++
				continue
			}
			seen[c.Key] = true
			if rec := c.record(m.ID); rec != nil {
				a.add(*rec)
			}
		}
	}
	return a.agg(m, files), dropped
}

// PrunePartials removes cached partials for files that no longer exist or no
// longer match, so the cache cannot grow without bound as sessions rotate.
//
// files must be the tool's current file list, already carrying size and mtime.
func PrunePartials(workDir, tool string, current []Partial) {
	dir := partialDir(workDir, tool)
	want := map[string]bool{}
	for _, c := range current {
		want[partialName(c.Path, c.Size, c.Mtime)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !want[n] {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// FileStamp reads the identity fields a partial is keyed by.
func FileStamp(path string) (size, mtime int64, ok bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	return st.Size(), st.ModTime().Unix(), true
}

// CollectAggregate produces a tool's aggregate, reusing cached per-file results
// for the sources that have not moved.
//
// parsed is how many files were actually read this call, and is what makes the
// saving visible: a machine where one session is active should report 1, not
// the number of files it accumulated over a month. A whole-tool collection
// reports -1, because for those the split is not available and the number would
// only invite a comparison that does not mean anything.
func CollectAggregate(m *Manifest, ctx *Context, files []string) (*ToolAgg, int) {
	if !PartialCapable(m) {
		return Aggregate(Run(m, ctx), m), -1
	}
	// --full re-reads on purpose, so it neither reads nor writes the cache:
	// serving a partial here would make the flag a lie.
	cached := !ctx.ForceFull

	entered := ctx.Dropped
	parts := make([]*Partial, 0, len(files))
	parsed := 0
	for _, f := range files {
		size, mtime, ok := FileStamp(f)
		if !ok {
			continue
		}
		stamp := &Partial{SchemaVer: partialSchemaVer, Tool: m.ID, Path: f, Size: size, Mtime: mtime}
		if cached && LoadPartial(ctx.WorkDir, stamp) {
			// The cached partial carries its own drop count; replay it so the
			// note line reads the same as it would after a full parse.
			ctx.Dropped += stamp.Dropped
			parts = append(parts, stamp)
			continue
		}
		p := RunFile(m, ctx, f)
		if p == nil {
			continue
		}
		if cached {
			_ = SavePartial(ctx.WorkDir, p)
		}
		parts = append(parts, p)
		parsed++
	}

	if cached {
		stamps := make([]Partial, 0, len(parts))
		for _, p := range parts {
			stamps = append(stamps, *p)
		}
		PrunePartials(ctx.WorkDir, m.ID, stamps)
	}

	if len(parts) == 0 {
		ctx.Dropped = entered
		return Aggregate(nil, m), parsed
	}
	agg, dropped := MergePartials(m, parts, len(files))
	// The replay reproduces the whole-tool drop count, including the ones found
	// across files, so the note line is identical either way.
	ctx.Dropped = entered + dropped
	return agg, parsed
}
