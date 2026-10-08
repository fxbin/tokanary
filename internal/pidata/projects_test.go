package pidata

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// writePiSource builds the smallest pi.sqlite that Read accepts.
//
// The frozen fixture would cover this, but it only exists on machines that
// still have the retired Python pipeline, and a skipped test is not coverage.
// The schema below is exactly the columns Read selects - no more - so a change
// to its queries breaks this loudly instead of silently skipping.
func writePiSource(t *testing.T, projects [][3]string) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, DBName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`create table projects(id integer primary key, path text, name text)`,
		`create table sessions(id text primary key, title text, project_id integer,
			model_id text, mode text, thinking_level text,
			created_at integer, updated_at integer)`,
		`create table turns(session_id text, model_id text, started_at integer,
			ended_at integer, status text, usage_json text)`,
		`create table messages(id integer primary key, session_id text, role text,
			tool_name text, is_error integer)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range projects {
		if _, err := db.Exec(`insert into projects(id, path, name) values (?,?,?)`,
			p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`insert into sessions
		(id, title, project_id, model_id, mode, created_at, updated_at)
		values ('s1','t',1,'m','agent',100,200)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestReadKeepsProjectPaths is the regression for the dropped column.
//
// projects.path was selected and then discarded, so the pipeline had the
// project-to-directory mapping in hand and threw it away; the git panel then
// went looking for it in two optional third-party agent databases and found
// nothing. That made an empty panel look like a missing-path problem on every
// platform, including the ones where the path was sitting in the query result.
func TestReadKeepsProjectPaths(t *testing.T) {
	src := writePiSource(t, [][3]string{
		{"1", filepath.Join("D:", "Project", "XiaoIce", "ClipInjector"), "ClipInjector"},
		{"2", filepath.Join("D:", "Project", "GitHub", "skill-hub"), "skill-hub"},
	})
	d, err := Read(filepath.Join(src, DBName), filepath.Join(src, DBName))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(d.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(d.Projects))
	}
	want := map[string]string{
		"ClipInjector": filepath.Join("D:", "Project", "XiaoIce", "ClipInjector"),
		"skill-hub":    filepath.Join("D:", "Project", "GitHub", "skill-hub"),
	}
	for _, p := range d.Projects {
		if p.Path != want[p.Name] {
			t.Errorf("project %q path = %q, want %q", p.Name, p.Path, want[p.Name])
		}
	}
}

// TestReadSurvivesNullProjectPath keeps a missing path from becoming a
// filesystem probe. An empty root would send the git scanner at the process
// working directory, where it would find a repository and attribute its
// history to a project that has none.
func TestReadSurvivesNullProjectPath(t *testing.T) {
	src := writePiSource(t, [][3]string{{"1", "", "NoPath"}})
	d, err := Read(filepath.Join(src, DBName), filepath.Join(src, DBName))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(d.Projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(d.Projects))
	}
	if d.Projects[0].Path != "" {
		t.Errorf("path = %q, want empty", d.Projects[0].Path)
	}
	if d.Projects[0].Name != "NoPath" {
		t.Errorf("name = %q, want NoPath", d.Projects[0].Name)
	}
}
