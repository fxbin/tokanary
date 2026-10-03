package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExternalUsagePathIsUnderCache(t *testing.T) {
	got := externalUsagePath("/repo")
	want := filepath.Join("/repo", ".cache", "external-usage.json")
	if got != want {
		t.Fatalf("externalUsagePath = %q, want %q", got, want)
	}
}

func TestFindRepoRootFindsAdaptersDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data", "adapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	got := findRepoRoot()
	// macOS temp dirs may be reached via the /private symlink.
	if resolved, err := filepath.EvalSymlinks(got); err == nil {
		got = resolved
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		want = dir
	}
	if got != want {
		t.Fatalf("findRepoRoot = %q, want %q", got, want)
	}
}
