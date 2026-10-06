// Package yield correlates local git activity with usage days so the
// dashboard can answer "what did this spend ship?" without leaving the machine.
package yield

import (
	"database/sql"
	"os/exec"

	_ "modernc.org/sqlite"
	"path/filepath"
	"strings"
)

// DayCommits is one calendar day of git activity in a workspace.
type DayCommits struct {
	D      string `json:"d"`
	Count  int    `json:"count"`
	Subjects []string `json:"subjects,omitempty"`
}

// WorkspaceYield is per-repo commit activity.
type WorkspaceYield struct {
	Project string       `json:"project"`
	Root    string       `json:"root"`
	Days    []DayCommits `json:"days"`
	Total   int          `json:"total"`
}

// ProjectGit scans git log for each workspace path and returns commits per day.
func ProjectGit(projects map[string]string) []WorkspaceYield {
	var out []WorkspaceYield
	for name, root := range projects {
		if root == "" {
			continue
		}
		if !isGitRepo(root) {
			continue
		}
		days := gitCommitsByDay(root, 60)
		total := 0
		for _, d := range days {
			total += d.Count
		}
		out = append(out, WorkspaceYield{Project: name, Root: root, Days: days, Total: total})
	}
	return out
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

// DiscoverRoots walks well-known local agent DBs for workspace paths that are
// git repos (ZCode session.directory, mimocode session.directory).
func DiscoverRoots(home string) map[string]string {
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
