package clisession

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Result is the row-level output of one collect pass, shaped like the
// warehouse tables so later slices can ingest it unchanged.
type Result struct {
	Sessions      []SessionRow
	Turns         []TurnRow
	Projects      []string
	Roles         map[string][2]int
	Tools         map[string][2]int
	SkippedDup    int
	SkippedEmpty  int
	Files         int
	Dirs          []string
	SchemaWarning []string
}

type SessionRow struct {
	ID        string  `json:"id"`
	Project   string  `json:"project"`
	ModelRaw  string  `json:"modelRaw"`
	Mode      string  `json:"mode"`
	Thinking  *string `json:"thinking"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
	Turns     int     `json:"turns"`
	Messages  int     `json:"messages"`
}

type TurnRow struct {
	Session    string `json:"session"`
	Tool       string `json:"tool"`
	ModelRaw   string `json:"modelRaw"`
	ModelCanon string `json:"modelCanon"`
	Status     string `json:"status"`
	Day        string `json:"day"`
	Hour       string `json:"hour"`
	StartedAt  int64  `json:"startedAt"`
	EndedAt    int64  `json:"endedAt"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Reasoning  int64  `json:"reasoning"`
	Total      int64  `json:"total"`
	HasUsage   bool   `json:"hasUsage"`
}

func sessionFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if strings.HasSuffix(d.Name(), RevisionsSuffix) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// readRecords parses one JSONL file; truncated or malformed lines are skipped
// because pi can be killed mid-write.
func readRecords(path string) []Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Collect scans session roots, skipping ids already present in the sqlite
// source (sqlite wins when a session exists in both places).
func Collect(dirs []string, excludeIDs map[string]bool) Result {
	res := Result{Roles: map[string][2]int{}, Tools: map[string][2]int{}, Dirs: append([]string{}, dirs...)}
	if len(dirs) == 0 {
		return res
	}
	grouped := map[string][]string{}
	var stems []string
	for _, root := range dirs {
		files, err := sessionFiles(root)
		if err != nil {
			continue
		}
		for _, f := range files {
			stem := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			if _, ok := grouped[stem]; !ok {
				stems = append(stems, stem)
			}
			grouped[stem] = append(grouped[stem], f)
		}
	}
	sort.Strings(stems)

	for _, stem := range stems {
		files := grouped[stem]
		res.Files += len(files)
		var recs []Record
		for _, f := range files {
			recs = append(recs, readRecords(f)...)
		}
		s := Parse(recs)
		if s.ID == "" {
			res.SkippedEmpty++
			continue
		}
		if excludeIDs != nil && excludeIDs[s.ID] {
			res.SkippedDup++
			continue
		}
		if s.Version != 0 && s.Version != SchemaVersion {
			res.SchemaWarning = append(res.SchemaWarning,
				fmt.Sprintf("session %s schema v%d (expected v%d) - 已按 v%d 解析，请复核", s.ID, s.Version, SchemaVersion, SchemaVersion))
		}
		hasUsage := false
		for _, t := range s.Turns {
			hasUsage = hasUsage || t.HasUsage
		}
		if len(s.Turns) == 0 || !hasUsage {
			res.SkippedEmpty++
			continue
		}

		row := SessionRow{
			ID: s.ID, Project: s.Project, ModelRaw: firstNonEmpty(s.ModelRaw, "(none)"),
			Mode: s.Mode, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
			Turns: len(s.Turns), Messages: s.Messages,
		}
		if s.Thinking != "" {
			thinking := s.Thinking
			row.Thinking = &thinking
		}
		res.Sessions = append(res.Sessions, row)
		// projects are keyed by cwd and land in the same table as the sqlite
		// ones, so totals.projects grows by one per distinct CLI cwd
		res.Projects = append(res.Projects, row.Project)
		for _, t := range s.Turns {
			day, hour := LocalDay(t.StartedAt), LocalHour(t.StartedAt)
			if t.StartedAt == 0 {
				day, hour = LocalDay(t.EndedAt), LocalHour(t.EndedAt)
			}
			res.Turns = append(res.Turns, TurnRow{
				Session: s.ID, Tool: Tool, ModelRaw: firstNonEmpty(t.ModelRaw, s.ModelRaw, "(none)"),
				ModelCanon: CanonicalModel(firstNonEmpty(t.ModelRaw, s.ModelRaw)),
				Status:     t.Status, Day: day, Hour: hour,
				StartedAt: t.StartedAt, EndedAt: t.EndedAt,
				Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite,
				Reasoning: t.Reasoning, Total: t.Total, HasUsage: t.HasUsage,
			})
		}
		for role, c := range s.Roles {
			cur := res.Roles[role]
			res.Roles[role] = [2]int{cur[0] + c.N, cur[1] + c.Errors}
		}
		for name, c := range s.Tools {
			cur := res.Tools[name]
			res.Tools[name] = [2]int{cur[0] + c.N, cur[1] + c.Errors}
		}
	}
	return res
}

// Golden mirrors the shape emitted by the frozen golden fixtures in testdata/clisession.
type Golden struct {
	Sessions     []SessionRow      `json:"sessions"`
	Turns        []TurnRow         `json:"turns"`
	Roles        map[string][2]int `json:"roles"`
	Tools        map[string][2]int `json:"tools"`
	SkippedDup   int               `json:"skippedDup"`
	SkippedEmpty int               `json:"skippedEmpty"`
	Files        int               `json:"files"`
}

// Golden renders a collect result in the golden shape.
func (r Result) Golden() Golden {
	g := Golden{Sessions: r.Sessions, Turns: r.Turns, Roles: r.Roles, Tools: r.Tools,
		SkippedDup: r.SkippedDup, SkippedEmpty: r.SkippedEmpty, Files: r.Files}
	sort.SliceStable(g.Sessions, func(i, j int) bool { return g.Sessions[i].ID < g.Sessions[j].ID })
	sort.SliceStable(g.Turns, func(i, j int) bool {
		a, b := g.Turns[i], g.Turns[j]
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.StartedAt != b.StartedAt {
			return a.StartedAt < b.StartedAt
		}
		return a.ModelRaw < b.ModelRaw
	})
	return g
}
