// Package yield correlates local git activity with usage days so the
// dashboard can answer "what did this spend ship?" without leaving the machine.
package yield

import (
	"database/sql"
	"os/exec"
	"sort"
	"time"

	_ "modernc.org/sqlite"
	"path/filepath"
	"strings"
)

// DayCommits is one calendar day of git activity in a workspace.
type DayCommits struct {
	D        string   `json:"d"`
	Count    int      `json:"count"`
	Subjects []string `json:"subjects,omitempty"`
}

// WorkspaceYield is per-repo commit activity.
type WorkspaceYield struct {
	Project string `json:"project"`
	Root    string `json:"root"`
	// AlsoProjects are the other project names that resolve to this same
	// repository. They ride along so a project's own cost can still join this
	// repo's commits when pi recorded it under a second name for the same path.
	AlsoProjects []string     `json:"alsoProjects,omitempty"`
	Days         []DayCommits `json:"days"`
	Total        int          `json:"total"`
}

// ProjectGit scans git log for each workspace path and returns one row per
// repository.
//
// One row per repository, not per path, and the path is resolved to the
// repository that owns it. A directory inside someone else's work tree has no
// commits of its own and git answers for it with the enclosing history, so
// taking a repo and a directory inside it used to count that repo twice and put
// two identical rows in the panel - two projects, one commit count, one last
// subject. A nested repository of its own still gets its own row, because
// asking git where the commits live is what decides it, not a .git probe.
//
// A project whose working directory is inside a repository does not vanish: it
// joins that repository's row as an alias, so its spend still has commits to be
// measured against instead of falling into "no commits to attribute". Names are
// sorted before grouping, so the primary name does not depend on map order.
func ProjectGit(projects map[string]string) []WorkspaceYield {
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)

	type row struct {
		ws   WorkspaceYield
		also []string
	}
	byRoot := map[string]*row{}
	var order []string
	for _, name := range names {
		root := projects[name]
		if root == "" {
			continue
		}
		top, ok := repoRoot(root)
		if !ok {
			continue
		}
		if r, seen := byRoot[top]; seen {
			r.also = append(r.also, name)
			continue
		}
		days := gitCommitsByDay(top, 60)
		total := 0
		for _, d := range days {
			total += d.Count
		}
		byRoot[top] = &row{ws: WorkspaceYield{Project: name, Root: top, Days: days, Total: total}}
		order = append(order, top)
	}
	out := make([]WorkspaceYield, 0, len(order))
	for _, top := range order {
		r := byRoot[top]
		r.ws.AlsoProjects = r.also
		out = append(out, r.ws)
	}
	return out
}

// repoRoot reports the repository a directory belongs to, by asking git rather
// than by looking for a .git directory. A directory inside a work tree resolves
// upward to that repository, which is the point: the subdirectory has no
// commits of its own, and the enclosing repository is the thing that shipped
// them.
func repoRoot(dir string) (string, bool) {
	b, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(b))
	if top == "" {
		return "", false
	}
	return top, true
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	b, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(b)) == "true"
}

func gitCommitsByDay(root string, days int) []DayCommits {
	// %ad short date, %s subject
	cmd := exec.Command("git", "-C", root, "log",
		"--since="+itoa(days)+" days ago",
		"--pretty=format:%ad%x09%s", "--date=short")
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	byDay := map[string]*DayCommits{}
	var order []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 1 {
			continue
		}
		d := strings.TrimSpace(parts[0])
		if d == "" {
			continue
		}
		e := byDay[d]
		if e == nil {
			e = &DayCommits{D: d}
			byDay[d] = e
			order = append(order, d)
		}
		e.Count++
		if len(parts) > 1 && len(e.Subjects) < 3 {
			sub := strings.TrimSpace(parts[1])
			if len(sub) > 60 {
				sub = sub[:60] + "…"
			}
			e.Subjects = append(e.Subjects, sub)
		}
	}
	out := make([]DayCommits, 0, len(order))
	for _, d := range order {
		out = append(out, *byDay[d])
	}
	// sort asc by day
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].D < out[j-1].D; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
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

// Ensure filepath used for nothing critical — keep import for future roots.
func openSQLite(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

var _ = filepath.Join

var cachedRoots map[string]string
var cachedRootsAt time.Time

// DiscoverRoots walks well-known local agent DBs for workspace paths that are
// git repos (ZCode session.directory, mimocode session.directory).
//
// This is a SUPPLEMENT, not the source. Those databases belong to optional
// third-party agents that are usually not installed, so on a typical machine
// this returns nothing and the panel stayed empty - even though pi records the
// working directory of every project in its own schema. Callers must prefer
// their own project-to-path mapping and use this only to fill gaps; on a machine
// with neither agent installed, an empty result is the normal case, not a
// failure.
//
// Errors are intentionally not reported: "that optional database is not there"
// is the common case and has nothing actionable in it.
func DiscoverRoots(home string) map[string]string {
	if cachedRoots != nil && time.Since(cachedRootsAt) < 10*time.Minute {
		return cachedRoots
	}
	out := map[string]string{}
	add := func(name, dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if isGitRepo(dir) {
			out[name] = dir
		}
	}
	// ZCode
	if db, err := openSQLite(home + "/.zcode/cli/db/db.sqlite"); err == nil {
		rows, err := db.Query("select directory from session group by directory")
		if err == nil {
			for rows.Next() {
				var dir string
				if rows.Scan(&dir) == nil {
					add(filepath.Base(dir), dir)
				}
			}
			rows.Close()
		}
		db.Close()
	}
	// MiMo mimocode
	if db, err := openSQLite(home + "/.local/share/mimocode/mimocode.db"); err == nil {
		rows, err := db.Query("select directory from session group by directory")
		if err == nil {
			for rows.Next() {
				var dir string
				if rows.Scan(&dir) == nil {
					add(filepath.Base(dir), dir)
				}
			}
			rows.Close()
		}
		db.Close()
	}
	return out
}
