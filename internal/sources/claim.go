package sources

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// FileClaim is one adapter's share of the source files, plus what it had to
// hand back because an earlier adapter already owned it.
type FileClaim struct {
	Manifest *Manifest
	Files    []string
	// Overlap lists files this adapter matched but did not get, mapped to the
	// adapter that claimed them first. It is never empty in silence: a file
	// that two adapters both match is always reported.
	Overlap []Overlap
}

// Overlap is one source file two adapters both matched.
type Overlap struct {
	Path  string
	Owner string // the adapter that claimed it, i.e. the one that reads it
	Loser string // the adapter that also matched it and therefore skips it
}

// AssignFiles expands every adapter's paths and hands each source file to
// exactly one adapter.
//
// Two adapters may legitimately declare overlapping paths, and on a real
// machine they do: deepseek-harness declares %DSH_HOME%, while DSH Desktop
// points that same variable at its own private harness directory. The
// "official" adapter therefore matched all 286 wrapper files and every one of
// those calls was billed a second time - two plausible rows, and daily totals
// that agreed to the token, so nothing looked wrong.
//
// The rule is deliberately simple rather than clever: manifests are processed
// in load order (LoadAdapters sorts by file name) and the first adapter to
// match a file owns it. That makes the outcome a function of the repository
// rather than of map iteration, and it means the fix is a property of the
// collector instead of a guess about which manifest is "right" - the wrapper
// sorts before the official adapter, so the wrapper keeps its own files and the
// official adapter keeps the rest.
//
// Ownership is tracked per resolved path, case-folded on Windows so the same
// file reached through two spellings of one directory is still one file.
func AssignFiles(manifests []*Manifest, ctx *Context) []FileClaim {
	out := make([]FileClaim, 0, len(manifests))
	owner := map[string]string{}

	// pathKey normalises a path for ownership comparison only; the original
	// string is what gets read.
	pathKey := func(p string) string {
		q := filepath.Clean(p)
		if runtime.GOOS == "windows" {
			q = strings.ToLower(q)
		}
		return q
	}

	for _, m := range manifests {
		files := FilesFor(m, ctx)
		claim := FileClaim{Manifest: m, Files: make([]string, 0, len(files))}
		for _, f := range files {
			k := pathKey(f)
			if prev, taken := owner[k]; taken {
				claim.Overlap = append(claim.Overlap,
					Overlap{Path: f, Owner: prev, Loser: m.ID})
				continue
			}
			owner[k] = m.ID
			claim.Files = append(claim.Files, f)
		}
		out = append(out, claim)
	}
	return out
}

// OverlapLines renders every cross-adapter overlap as one warning line, sorted
// so the output does not depend on map iteration order.
//
// An overlap is reported rather than silently resolved because the reader is
// the one who has to know: two adapters matching one file usually means one of
// the two manifests is pointing at storage it does not own, and that is a fact
// about the machine, not something to paper over.
func OverlapLines(claims []FileClaim) []string {
	var rows []Overlap
	for _, c := range claims {
		rows = append(rows, c.Overlap...)
	}
	if len(rows) == 0 {
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Loser != rows[j].Loser {
			return rows[i].Loser < rows[j].Loser
		}
		return rows[i].Path < rows[j].Path
	})
	byLoser := map[string]int{}
	for _, r := range rows {
		byLoser[r.Loser]++
	}
	losers := make([]string, 0, len(byLoser))
	for id := range byLoser {
		losers = append(losers, id)
	}
	sort.Strings(losers)

	var out []string
	for _, loser := range losers {
		var sample string
		n := 0
		for _, r := range rows {
			if r.Loser != loser {
				continue
			}
			n++
			if sample == "" {
				sample = r.Path
			}
		}
		owners := map[string]bool{}
		for _, r := range rows {
			if r.Loser == loser {
				owners[r.Owner] = true
			}
		}
		var ownerList []string
		for o := range owners {
			ownerList = append(ownerList, o)
		}
		sort.Strings(ownerList)
		out = append(out, fmt.Sprintf(
			"[warn] %s: %d 个源文件与 %s 重叠，已归 %s，跳过本适配器 —— 例如 %s",
			loser, n, strings.Join(ownerList, "、"), strings.Join(ownerList, "、"), sample))
	}
	return out
}
