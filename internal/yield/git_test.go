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
