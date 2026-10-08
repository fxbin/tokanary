package warehouse

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestScanProjectPathsToleratesAWarehouseWithoutTheColumn keeps a stale
// artifact from blanking the dashboard.
//
// This lives in the internal test package on purpose: scanProjectPaths is not
// part of the API, and exporting it just so a test could reach it would widen
// the surface for no caller. The round-trip behaviour is covered from the
// outside in TestProjectPathsSurviveTheRoundTrip.
func TestScanProjectPathsToleratesAWarehouseWithoutTheColumn(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE projects(id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects VALUES ('1', 'demo')`); err != nil {
		t.Fatal(err)
	}

	got, err := scanProjectPaths(db)
	if err != nil {
		t.Fatalf("an old-schema warehouse must not fail the read path: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no paths from an old warehouse, got %v", got)
	}
}

// TestScanProjectPathsSkipsEmptyValues guards the difference between "no path
// known" and "path is the empty string". The latter would send ProjectGit at
// the process working directory, where it would happily read this repository's
// own history and attribute it to a project.
func TestScanProjectPathsSkipsEmptyValues(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "w.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, name, path string }{
		{"1", "good", filepath.Join("somewhere", "repo")},
		{"2", "blank", ""},
		{"3", "null", ""},
		{"4", "", filepath.Join("somewhere", "nameless")},
	} {
		if _, err := db.Exec(`INSERT INTO projects(id, name, path) VALUES (?,?,?)`,
			row.id, row.name, row.path); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanProjectPaths(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 usable path, got %v", got)
	}
	if _, ok := got["good"]; !ok {
		t.Errorf("the one usable path was dropped: %v", got)
	}
	for _, bad := range []string{"blank", "null", ""} {
		if p, ok := got[bad]; ok {
			t.Errorf("kept an unusable entry for %q -> %q", bad, p)
		}
	}
}
