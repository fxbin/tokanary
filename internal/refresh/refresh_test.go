package refresh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTouchReusesUnchangedTools(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data", "adapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal jsonl adapter over a tiny log file.
	log := filepath.Join(root, "log.jsonl")
	if err := os.WriteFile(log, []byte(`{"type":"assistant","model":"m","session":"s1","input_tokens":1,"output_tokens":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ad := `{
  "id": "t1", "label": "T1",
  "paths": ["` + filepath.ToSlash(log) + `"],
  "fields": {
    "model": ["model"], "session": ["session"],
    "input": ["input_tokens"], "output": ["output_tokens"]
  }
}`
	if err := os.WriteFile(filepath.Join(root, "data", "adapters", "t1.json"), []byte(ad), 0o644); err != nil {
		t.Fatal(err)
	}

	r1, err := Touch(root)
	if err != nil {
		t.Fatalf("first Touch: %v", err)
	}
	if r1.ToolsParsed < 1 {
		t.Fatalf("first Touch should parse, got %+v", r1)
	}

	r2, err := Touch(root)
	if err != nil {
		t.Fatalf("second Touch: %v", err)
	}
	if r2.ToolsReused != 1 || r2.ToolsParsed != 0 {
		t.Fatalf("second Touch should reuse unchanged tool, got %+v", r2)
	}

	// Change the log → tool must reparse.
	if err := os.WriteFile(log, []byte(`{"type":"assistant","model":"m","session":"s1","input_tokens":1,"output_tokens":2}`+"\n"+
		`{"type":"assistant","model":"m","session":"s1","input_tokens":3,"output_tokens":4}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r3, err := Touch(root)
	if err != nil {
		t.Fatalf("third Touch: %v", err)
	}
	if r3.ToolsParsed < 1 {
		t.Fatalf("changed log must reparse, got %+v", r3)
	}
}
