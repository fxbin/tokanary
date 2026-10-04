package sources

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Context carries collection-time facts the engine needs. It is the Go analogue
// of collect_sources' ctx dict.
type Context struct {
	Home      string
	WorkDir   string
	Prefilter []string
	Manifest  *Manifest

	// mutable counters the collector reads back for the note lines
	Dropped  int
	Resets   int
	Calls    int
	RawInput int64
	// PathOverrides lets --paths replace a manifest's paths
	PathOverrides map[string][]string
}

func (c *Context) patternsFor(m *Manifest) []string {
	if c.PathOverrides != nil {
		if v, ok := c.PathOverrides[m.ID]; ok {
			return v
		}
	}
	return m.Paths
}

// ExpandHome resolves a leading ~ the way engine.expand_home does.
func ExpandHome(p string, home string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return filepath.Join(home, strings.TrimLeft(p[1:], `\/`))
}

// subst replaces {home} and any other context key, then expands %VAR%.
func subst(s string, ctx *Context) string {
	if ctx.Home != "" {
		s = strings.ReplaceAll(s, "{home}", ctx.Home)
	}
	// $VAR / ${VAR} first (os.ExpandEnv leaves %VAR% alone), then %VAR%.
	return expandWinEnv(os.ExpandEnv(s), ctx.Home)
}

// expandWinEnv expands %VAR% references. os.ExpandEnv only handles $VAR and
// ${VAR}, but the adapter manifests are written with %VAR% - which is what
// python's os.path.expandvars accepts. Expanding only one of the two syntaxes
// silently yielded zero files for the DSH Desktop adapter, whose 13 of 14 paths
// are %APPDATA%-relative.
//
// A variable that is not set is resolved through the platform fallback chain in
// pathvars.go, so the same manifest works off Windows. Anything that still
// cannot be resolved is left as a literal, which UnresolvedVars reports.
func expandWinEnv(s, home string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		end := strings.IndexByte(s[i+1:], '%')
		if end < 0 {
			b.WriteByte(s[i])
			continue
		}
		name := s[i+1 : i+1+end]
		if name == "" {
			b.WriteByte('%') // "%%" is an escaped percent
			i += end + 1
			continue
		}
		if v, ok := os.LookupEnv(name); ok {
			b.WriteString(v)
		} else if v, ok := lookupPathVar(name, home); ok {
			b.WriteString(v)
		} else {
			// unknown vars pass through unchanged, matching expandvars
			b.WriteByte('%')
			b.WriteString(name)
			b.WriteByte('%')
		}
		i += end + 1
	}
	return b.String()
}

// ExpandPaths resolves ~, %ENV%, {home} and globs, returning a sorted unique
// file list.
func ExpandPaths(paths []string, ctx *Context) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, raw := range paths {
		p := ExpandHome(subst(raw, ctx), ctx.Home)
		if strings.ContainsAny(p, "*?") {
			// split the glob into a literal base dir and a relative pattern.
			// Everything before the first wildcard is the base; the rest is
			// matched with filepath.Match after **/ is collapsed to *.
			norm := strings.ReplaceAll(p, `\`, "/")
			star := strings.IndexAny(norm, "*?")
			if star < 0 {
				star = len(norm)
			}
			head := strings.TrimRight(norm[:star], "/")
			base := head
			if base == "" {
				base = "."
			}
			rel := strings.TrimLeft(norm[star:], "/")
			rel = strings.ReplaceAll(rel, "**/", "")
			rel = strings.ReplaceAll(rel, "**", "*")
			walkFiles(base, rel, add)
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			add(p)
			continue
		}
		walkFiles(p, "*", add)
	}
	sort.Strings(out)
	return out
}

func walkFiles(base, rel string, add func(string)) {
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// match the basename against the collapsed pattern
		if ok, err := filepath.Match(rel, filepath.Base(path)); err == nil && ok {
			add(path)
			return nil
		}
		// fall back to matching the path relative to base, for patterns that
		// still carry directory segments
		if r, err := filepath.Rel(base, path); err == nil {
			if ok, err := filepath.Match(rel, filepath.ToSlash(r)); err == nil && ok {
				add(path)
			}
		}
		return nil
	})
}

// FilesFor returns the files a manifest reads, which for a sqlite source is
// the db plus its wal/shm siblings.
func FilesFor(m *Manifest, ctx *Context) []string {
	if m.KindOrDefault() == "sqlite" && m.DB != "" {
		db := ExpandHome(subst(m.DB, ctx), ctx.Home)
		var out []string
		for _, suf := range []string{"", "-wal", "-shm"} {
			p := db + suf
			if _, err := os.Stat(p); err == nil {
				out = append(out, p)
			}
		}
		return out
	}
	return ExpandPaths(ctx.patternsFor(m), ctx)
}

// rawObj is a decoded record plus the provenance the engine and the codex
// driver both need.
type rawObj struct {
	Obj  map[string]any
	File string
	Dir  string
	Line int
}

// decodeLine parses one JSONL line into a rawObj, or reports nil to skip.
func decodeLine(line string, file, dir string, lineno int, pats []string) *rawObj {
	line = strings.TrimSpace(line)
	if line == "" || (line[0] != '{' && line[0] != '[') {
		return nil
	}
	if len(pats) > 0 {
		hit := false
		for _, p := range pats {
			if strings.Contains(line, p) {
				hit = true
				break
			}
		}
		if !hit {
			return nil
		}
	}
	var v any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return &rawObj{Obj: m, File: file, Dir: dir, Line: lineno}
}

// ReadJSONL streams every JSON object out of the given files.
func ReadJSONL(paths []string, ctx *Context) []*rawObj {
	pats := ctx.Prefilter
	var out []*rawObj
	for _, f := range paths {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		dir := filepath.Base(filepath.Dir(f))
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		lineno := 0
		for sc.Scan() {
			lineno++
			if o := decodeLine(sc.Text(), f, dir, lineno, pats); o != nil {
				out = append(out, o)
			}
		}
		fh.Close()
	}
	return out
}

// ReadJSON reads one whole JSON document per file: either a single object or
// an array of objects (Gemini CLI chat dumps, Cline task logs, …).
// When the manifest sets json_rows, that nested array is flattened and each
// element is emitted with parent ids copied in (sessionId, model, …).
func ReadJSON(paths []string, ctx *Context) []*rawObj {
	pats := ctx.Prefilter
	rowsPath := ""
	if ctx.Manifest != nil {
		rowsPath = ctx.Manifest.JSONRows
	}
	var out []*rawObj
	for _, f := range paths {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if len(pats) > 0 {
			hit := false
			for _, p := range pats {
				if p != "" && strings.Contains(string(data), p) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		dir := filepath.Base(filepath.Dir(f))
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			var arr []map[string]any
			if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
				continue
			}
			for i, obj := range arr {
				if obj == nil {
					continue
				}
				if rowsPath != "" {
					out = append(out, flattenRows(obj, rowsPath, f, dir, i+1)...)
					continue
				}
				out = append(out, &rawObj{Obj: obj, File: f, Dir: dir, Line: i + 1})
			}
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil || obj == nil {
			continue
		}
		if rowsPath != "" {
			out = append(out, flattenRows(obj, rowsPath, f, dir, 1)...)
			continue
		}
		out = append(out, &rawObj{Obj: obj, File: f, Dir: dir, Line: 1})
	}
	return out
}

// flattenRows expands parent[rowsPath] (an array of objects). Each child gets
// missing identity fields copied from the parent (sessionId, model, timestamp…).
func flattenRows(parent map[string]any, rowsPath, file, dir string, line int) []*rawObj {
	raw, ok := parent[rowsPath]
	if !ok {
		return []*rawObj{{Obj: parent, File: file, Dir: dir, Line: line}}
	}
	arr, ok := raw.([]any)
	if !ok {
		return []*rawObj{{Obj: parent, File: file, Dir: dir, Line: line}}
	}
	inherit := []string{"sessionId", "session_id", "id", "model", "modelVersion", "timestamp", "startTime", "createTime"}
	var out []*rawObj
	for i, el := range arr {
		m, ok := el.(map[string]any)
		if !ok || m == nil {
			continue
		}
		for _, k := range inherit {
			if _, has := m[k]; !has {
				if v, ok := parent[k]; ok {
					m[k] = v
				}
			}
		}
		out = append(out, &rawObj{Obj: m, File: file, Dir: dir, Line: line*1000 + i})
	}
	return out
}
