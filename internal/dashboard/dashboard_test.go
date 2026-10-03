package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAssembleFromExistingWarehouse exercises the live desktop path against a
// real warehouse when one is present (.cache/tokanary.sqlite). It also writes
// .cache/dashboard.json so the frontend live-data suites can pick it up.
func TestAssembleFromExistingWarehouse(t *testing.T) {
	root := filepath.Join("..", "..")
	dbPath := filepath.Join(root, DefaultDBPath)
	if _, err := os.Stat(dbPath); err != nil {
		t.Skip("no local warehouse; run tokanary refresh first")
	}
	payload, err := Assemble(Options{RepoRoot: root})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if payload == nil || payload.Totals == nil {
		t.Fatal("empty payload")
	}
	out := filepath.Join(root, ".cache", "dashboard.json")
	if err := WriteJSON(out, payload); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	t.Logf("wrote %s", out)
}
