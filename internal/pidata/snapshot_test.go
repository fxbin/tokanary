package pidata

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// writeSource builds a minimal but valid pi.sqlite so the snapshot tests do
// not depend on a frozen copy of somebody's real data.
func writeSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, DBName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table t (id integer primary key, v text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into t (v) values ('a'), ('b')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func countRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`select count(*) from t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSnapshotsDoNotShareADirectory pins the reason the snapshot directory
// carries a random suffix. The desktop window refreshes on read while a
// `tokanary refresh` or the scheduled task may be running: with one shared
// name, the second writer deletes the first one's database out from under it.
func TestSnapshotsDoNotShareADirectory(t *testing.T) {
	src := writeSource(t)
	work := t.TempDir()

	first, err := Snapshot(src, work)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	second, err := Snapshot(src, work)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if first == second {
		t.Fatalf("two snapshots returned the same path %q; they must not share a directory", first)
	}
	for _, p := range []string{first, second} {
		if n := countRows(t, p); n != 2 {
			t.Errorf("%s has %d rows, want 2", p, n)
		}
	}
}

func TestRemoveSnapshotLeavesSiblingsAlone(t *testing.T) {
	src := writeSource(t)
	work := t.TempDir()

	keep, err := Snapshot(src, work)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := Snapshot(src, work)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveSnapshot(gone); err != nil {
		t.Fatalf("RemoveSnapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(gone)); !os.IsNotExist(err) {
		t.Errorf("snapshot dir %s survived RemoveSnapshot (err=%v)", filepath.Dir(gone), err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("RemoveSnapshot took the sibling snapshot with it: %v", err)
	}
}

// TestConcurrentSnapshotsAllSucceed is the Windows failure in its purest form:
// the old code ran os.RemoveAll on a fixed directory before every snapshot, so
// one process holding the file turned another's refresh into an error. Every
// goroutine here must get its own database, whatever the platform.
func TestConcurrentSnapshotsAllSucceed(t *testing.T) {
	src := writeSource(t)
	work := t.TempDir()

	const n = 4
	var wg sync.WaitGroup
	paths := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = Snapshot(src, work)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("snapshot %d failed: %v", i, err)
		}
		if seen[paths[i]] {
			t.Fatalf("snapshot %d reused path %q", i, paths[i])
		}
		seen[paths[i]] = true
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		t.Errorf("work dir holds %d snapshot dirs, want %d", len(entries), n)
	}
	for _, p := range paths {
		if err := RemoveSnapshot(p); err != nil {
			t.Errorf("RemoveSnapshot(%s): %v", p, err)
		}
	}
	entries, err = os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("work dir still holds %d entries after cleanup", len(entries))
	}
}

func TestRemoveSnapshotIgnoresEmptyPath(t *testing.T) {
	// Callers defer this before checking the snapshot error, so the empty
	// string has to be a no-op rather than a panic or a stray RemoveAll("").
	if err := RemoveSnapshot(""); err != nil {
		t.Fatalf("RemoveSnapshot(\"\") = %v, want nil", err)
	}
}

// TestRemoveSnapshotRefusesForeignPaths guards the destructive edge of the API.
// It deletes the parent of whatever it is given, so an ordinary path would take
// a real directory with it and still report success.
func TestRemoveSnapshotRefusesForeignPaths(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "important")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveSnapshot(filepath.Join(victim, "snapshot.sqlite")); err == nil {
		t.Fatal("RemoveSnapshot accepted a path outside a snapshot directory")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("RemoveSnapshot deleted a directory it was never given: %v", err)
	}
}

// TestSweepStaleSnapshots pins the property that per-process directory names
// took away from the old fixed-name implementation: a snapshot orphaned by a
// killed process is a full copy of the source db, and nothing else reclaims it.
func TestSweepStaleSnapshots(t *testing.T) {
	work := t.TempDir()

	stale := filepath.Join(work, snapshotDirPrefix+"stale")
	fresh := filepath.Join(work, snapshotDirPrefix+"fresh")
	unrelated := filepath.Join(work, "something-else")
	for _, d := range []string{stale, fresh, unrelated} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleSnapshotAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	sweepStaleSnapshots(work)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale snapshot survived the sweep (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("sweep removed a snapshot young enough to still be in use: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("sweep removed an unrelated directory: %v", err)
	}
}
