package warehouse_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fxbin/tokanary/internal/clisession"
	"github.com/fxbin/tokanary/internal/pidata"
	"github.com/fxbin/tokanary/internal/warehouse"
)

// TestFrozenParity builds the warehouse from the frozen fixture and compares
// every field against the reference payload the retired Python pipeline produced
// from the same fixture. That reference is what makes this a parity gate rather
// than a snapshot test, and it cannot be regenerated from this repository: the
// collector that wrote it is gone, so the fixture has to come from a machine
// that still has it.
//
// The fixture lives in .cache/ and is not committed (it contains this machine's
// paths, project names and session titles), so the test skips when it is
// absent. With both halves in place:
//
//	go test ./internal/warehouse -run Frozen
func TestFrozenParity(t *testing.T) {
	root := repoRoot(t)
	fixture := filepath.Join(root, ".cache", "frozen")
	if _, err := os.Stat(filepath.Join(fixture, "pi.sqlite")); err != nil {
		t.Skip("frozen fixture absent; this gate needs the reference payload from the retired Python pipeline")
	}

	work := t.TempDir()
	snap, err := pidata.Snapshot(fixture, work)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	data, err := pidata.Read(snap, filepath.Join(fixture, "pi.sqlite"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	cliDir := filepath.Join(fixture, "cli")
	var cli *clisession.Result
	if st, err := os.Stat(cliDir); err == nil && st.IsDir() {
		exclude := map[string]bool{}
		for _, s := range data.Sessions {
			exclude[s.ID] = true
		}
		res := clisession.Collect([]string{cliDir}, exclude)
		cli = &res
	}

	dbPath := filepath.Join(work, "tokanary.sqlite")
	db, err := warehouse.DB(dbPath, data, cli)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer db.Close()

	usage, err := warehouse.ExportUsage(db)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	payload, err := warehouse.LoadDashboardPayload(filepath.Join(root, ".cache", "frozen-data.js"))
	if err != nil {
		t.Fatalf("load data.js: %v", err)
	}
	diffs := warehouse.Compare(payload, usage)
	if len(diffs) > 0 {
		limit := len(diffs)
		if limit > 25 {
			limit = 25
		}
		for _, d := range diffs[:limit] {
			t.Errorf("parity %s", d)
		}
		t.Fatalf("%d parity diffs (%d shown)", len(diffs), limit)
	}
}

// TestTokenIdentity pins the accounting invariant from .agent-memory: turns
// must satisfy in+out+cacheRead+cacheWrite == total, and input must not be
// substituted for the cache columns.
func TestTokenIdentity(t *testing.T) {
	root := repoRoot(t)
	fixture := filepath.Join(root, ".cache", "frozen")
	if _, err := os.Stat(filepath.Join(fixture, "pi.sqlite")); err != nil {
		t.Skip("frozen fixture absent")
	}
	work := t.TempDir()
	snap, err := pidata.Snapshot(fixture, work)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	data, err := pidata.Read(snap, filepath.Join(fixture, "pi.sqlite"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data.Turns) == 0 {
		t.Fatal("fixture produced no turns")
	}
	for i, r := range data.Turns {
		sum := r.Input + r.Output + r.CacheRead + r.CacheWrite
		if r.Total != sum {
			t.Fatalf("turn %d: %d != %d (in=%d out=%d cr=%d cw=%d)",
				i, r.Total, sum, r.Input, r.Output, r.CacheRead, r.CacheWrite)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// go.mod lives at the repo root, so the directory holding it IS the root.
	// Stop before walking past it.
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("cannot locate repo root")
	return ""
}
