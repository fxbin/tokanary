package yield

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProjectGitOnTempRepo(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Skip("git init unavailable")
	}
	_ = exec.Command("git", "-C", dir, "config", "user.email", "t@t").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "t").Run()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", dir, "add", "a.txt").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	out := ProjectGit(map[string]string{"demo": dir, "notgit": t.TempDir()})
	if len(out) != 1 {
		t.Fatalf("want 1 workspace, got %d", len(out))
	}
	if out[0].Project != "demo" || out[0].Total < 1 {
		t.Fatalf("unexpected yield %+v", out[0])
	}
}

// commitOnce builds a one-commit repository and returns its path.
func commitOnce(t *testing.T, dir, msg string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Skip("git init unavailable")
	}
	_ = exec.Command("git", "-C", dir, "config", "user.email", "t@t").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "t").Run()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", dir, "add", "a.txt").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", msg).Run()
	return dir
}

// A directory inside a repository is not a repository. It used to pass the
// inside-work-tree check, and git then answered with the enclosing repo's
// history, so the panel showed one repo twice under two project names with the
// same commit count and the same last subject.
func TestProjectGitMergesSubdirectoryIntoItsRepo(t *testing.T) {
	repo := commitOnce(t, filepath.Join(t.TempDir(), "demo"), "init")
	inner := filepath.Join(repo, "web")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}

	out := ProjectGit(map[string]string{
		"demo":     repo,
		"demo-web": inner,
	})
	if len(out) != 1 {
		t.Fatalf("a subdirectory must not become a second row, got %d: %+v", len(out), out)
	}
	row := out[0]
	if row.Project != "demo" {
		t.Fatalf("primary name = %q, want demo", row.Project)
	}
	// The subdirectory is not dropped either: its spend has these commits to be
	// measured against, so it joins the row as an alias.
	if len(row.AlsoProjects) != 1 || row.AlsoProjects[0] != "demo-web" {
		t.Fatalf("AlsoProjects = %v, want [demo-web]", row.AlsoProjects)
	}
	if row.Total < 1 || len(row.Days) == 0 {
		t.Fatalf("the row must carry the repository's commits, got %+v", row)
	}
}

// A nested repository of its own keeps its own row: the fix is "is this path a
// root", not "does a .git exist somewhere above".
func TestProjectGitKeepsNestedRepoAsItsOwnRow(t *testing.T) {
	base := t.TempDir()
	repo := commitOnce(t, filepath.Join(base, "demo"), "outer")
	nested := commitOnce(t, filepath.Join(repo, "sub"), "inner")

	out := ProjectGit(map[string]string{
		"demo": repo,
		"sub":  nested,
	})
	if len(out) != 2 {
		t.Fatalf("want 2 rows (outer + nested repo), got %d: %+v", len(out), out)
	}
	seen := map[string]bool{}
	for _, w := range out {
		seen[w.Project] = true
	}
	if !seen["demo"] || !seen["sub"] {
		t.Fatalf("rows = %+v, want one per repository", out)
	}
}

// Two project names for one path are one repository, and the name that lost must
// stay joinable or its spend has nowhere to land.
func TestProjectGitMergesTwoNamesForOneRepo(t *testing.T) {
	repo := commitOnce(t, filepath.Join(t.TempDir(), "demo"), "init")

	out := ProjectGit(map[string]string{"zeta": repo, "alpha": repo})
	if len(out) != 1 {
		t.Fatalf("two names for one repo must merge, got %d: %+v", len(out), out)
	}
	// Sorted first, so the row does not depend on map order.
	if out[0].Project != "alpha" {
		t.Fatalf("primary name = %q, want alpha", out[0].Project)
	}
	if len(out[0].AlsoProjects) != 1 || out[0].AlsoProjects[0] != "zeta" {
		t.Fatalf("AlsoProjects = %v, want [zeta]", out[0].AlsoProjects)
	}
}
