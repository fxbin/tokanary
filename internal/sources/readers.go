package sources

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	_ "modernc.org/sqlite"
)

// zstdMagic is the frame marker a zstd stream starts with.
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// ReadZstdJSONL handles both a multi-frame zstd container holding JSONL and
// plain uncompressed JSONL under the same kind.
//
// DeepSeek Harness writes one frame per flush, so a crash can leave a truncated
// trailing frame; frames are decompressed one at a time and a bad frame is
// skipped rather than abandoning the file. Files without the magic are read as
// ordinary lines (compression:none, or a wrapper shell).
func ReadZstdJSONL(paths []string, ctx *Context) []*rawObj {
	pats := ctx.Prefilter
	var out []*rawObj
	var dec *zstd.Decoder
	for _, f := range paths {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		dir := filepath.Base(filepath.Dir(f))
		if !bytes.Contains(data, zstdMagic) {
			lineno := 0
			for _, raw := range bytes.Split(data, []byte("\n")) {
				if o := decodeLine(string(raw), f, dir, lineno, pats); o != nil {
					out = append(out, o)
				}
				lineno++
			}
			continue
		}
		if dec == nil {
			dec, err = zstd.NewReader(nil)
			if err != nil {
				return out
			}
			defer dec.Close()
		}
		// find every frame start; the slice between two starts is one frame
		var starts []int
		for i := 0; i+4 <= len(data); i++ {
			if bytes.Equal(data[i:i+4], zstdMagic) {
				starts = append(starts, i)
			}
		}
		if len(starts) == 0 {
			continue
		}
		lineno := 0
		for idx, s := range starts {
			end := len(data)
			if idx+1 < len(starts) {
				end = starts[idx+1]
			}
			chunk, err := dec.DecodeAll(data[s:end], nil)
			if err != nil {
				continue // incomplete trailing frame after a crash
			}
			for _, raw := range bytes.Split(chunk, []byte("\n")) {
				if o := decodeLine(string(raw), f, dir, lineno, pats); o != nil {
					out = append(out, o)
				}
				lineno++
			}
		}
	}
	return out
}

// ReadSQLite copies db+wal+shm, opens the copy read-only, and flattens one JSON
// column to the top level so the same dotted paths work for sqlite and jsonl.
func ReadSQLite(m *Manifest, ctx *Context) []*rawObj {
	if m.DB == "" {
		return nil
	}
	dbPath := ExpandHome(subst(m.DB, ctx), ctx.Home)
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	work := filepath.Join(ctx.WorkDir, "sqlite-"+m.ID)
	os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil
	}
	defer os.RemoveAll(work)

	base := filepath.Base(dbPath)
	for _, suf := range []string{"", "-wal", "-shm"} {
		src := dbPath + suf
		if _, err := os.Stat(src); err == nil {
			if b, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(filepath.Join(work, base+suf), b, 0o644)
			}
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(work, base))+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(m.Query)
	if err != nil {
		return nil
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil
	}
	dir := filepath.Base(filepath.Dir(dbPath))
	var out []*rawObj
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		rec := make(map[string]any, len(cols)+1)
		for i, c := range cols {
			rec[c] = normalizeSQLValue(vals[i])
		}
		if m.JSONColumn != "" {
			if s, ok := rec[m.JSONColumn].(string); ok {
				var parsed any
				if err := jsonUnmarshal([]byte(s), &parsed); err == nil {
					if pm, ok := parsed.(map[string]any); ok {
						for k, v := range pm {
							rec[k] = v
						}
					}
				}
			}
		}
		rec["$file"] = dbPath
		rec["$dir"] = dir
		out = append(out, &rawObj{Obj: rec, File: dbPath, Dir: dir})
	}
	return out
}

// normalizeSQLValue turns []byte into string so JSON parsing downstream works
// and path lookups compare as text.
func normalizeSQLValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

var _ = strings.TrimSpace
