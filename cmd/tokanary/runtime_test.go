package main

import (
	"os"
	"path/filepath"
	"runtime"
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

// outsideAnyRoot returns a path that cannot sit under os.TempDir(),
// <UserCacheDir>/go-build or GOROOT, on whichever platform the test runs on.
//
// The naive spelling - filepath.Join("C:", "tools", ...) - does not work:
// Join drops the separator after a volume name, yielding the drive-relative
// "C:tools\...", which filepath.Abs resolves against that drive's current
// directory. A checkout under %TEMP% then makes the "stable install" case
// report ephemeral, and the suite goes red for a reason that has nothing to do
// with the code. A volume root is never under a cache or temp tree, and on
// POSIX the filesystem root plays the same role.
func outsideAnyRoot(t *testing.T, elems ...string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := string(filepath.Separator)
	if vol := filepath.VolumeName(wd); vol != "" {
		root = vol + string(filepath.Separator)
	}
	return filepath.Join(append([]string{root}, elems...)...)
}

func TestEphemeralBinaryRejectsBuildCache(t *testing.T) {
	// The exact reason `go run ./cmd/tokanary schedule` must not register:
	// its own path is inside the build cache, which `go clean -cache` empties.
	//
	// The cache is deliberately NOT under t.TempDir(): os.TempDir() is itself
	// one of the roots, so a temp-based fixture would pass even with the
	// GOCACHE root deleted, and the assertion would prove nothing.
	cache := outsideAnyRoot(t, "gocache-fixture")
	t.Setenv("GOCACHE", cache)
	exe := filepath.Join(cache, "b001", "exe", "tokanary.exe")
	why, ephemeral := ephemeralBinary(exe)
	if !ephemeral {
		t.Fatalf("ephemeralBinary(%q) = false, want true", exe)
	}
	if why != "GOCACHE" {
		t.Errorf("ephemeralBinary(%q) blamed %q, want GOCACHE - only that root is in play here", exe, why)
	}
}

func TestEphemeralBinaryAllowsStableInstall(t *testing.T) {
	t.Setenv("GOCACHE", outsideAnyRoot(t, "gocache-fixture"))
	exe := outsideAnyRoot(t, "tools", "tokanary.exe")
	if why, ephemeral := ephemeralBinary(exe); ephemeral {
		t.Errorf("ephemeralBinary(%q) = true (%s), want false", exe, why)
	}
}

// TestWithinDirIsNotAFuzzyPrefixMatch covers the containment check on its own,
// where the case-folding rule cannot interfere.
func TestWithinDirIsNotAFuzzyPrefixMatch(t *testing.T) {
	// A sibling that shares the prefix must not count as inside, or
	// ".../tokanary-evil" would inherit the verdict of ".../tokanary".
	dir := filepath.Join(string(filepath.Separator), "opt", "tokanary")
	if withinDir(filepath.Join(string(filepath.Separator), "opt", "tokanary-evil", "x"), dir) {
		t.Errorf("withinDir accepted a sibling of %q", dir)
	}
	if !withinDir(filepath.Join(dir, "bin", "tokanary"), dir) {
		t.Errorf("withinDir rejected a child of %q", dir)
	}
}

// TestWithinDirCaseFoldingFollowsThePlatform pins the OS-conditional folding.
// Folding everywhere would let a build cache that differs from the reported
// root only by case slip through; not folding on Windows would miss the real
// matches this check exists for.
func TestWithinDirCaseFoldingFollowsThePlatform(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive comparison is Windows-only behaviour")
	}
	if !withinDir(`C:\Users\Me\AppData\Local\Temp\go-build\x.exe`,
		`c:\users\me\appdata\local\temp`) {
		t.Error("withinDir missed a Windows path that differs only by case")
	}
}
