package pricing

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactHome(t *testing.T) {
	cases := []struct {
		home, in, want string
	}{
		{`C:\Users\ab`, `C:\Users\ab\.pi-desktop\pi.sqlite`, `~/.pi-desktop/pi.sqlite`},
		{`C:\Users\ab`, `C:\Users\ab`, `~`},
		{`/home/ab`, `/home/ab/.pi/sessions`, `~/.pi/sessions`},
		// Outside the home directory: left alone, because folding it would lie
		// about where the data came from.
		{`C:\Users\ab`, `D:\backup\pi-home`, `D:\backup\pi-home`},
		{`C:\Users\ab`, `C:\Users\abc\.pi`, `C:\Users\abc\.pi`},
		{`/home/ab`, `/home/abc/.pi`, `/home/abc/.pi`},
		// No home to fold against, or nothing to fold.
		{``, `C:\Users\ab\.pi`, `C:\Users\ab\.pi`},
		{`C:\Users\ab`, ``, ``},
		{`C:\Users\ab`, `C:\Users\ab\`, `~`},
	}
	for _, c := range cases {
		if got := RedactHome(c.in, c.home); got != c.want {
			t.Errorf("RedactHome(%q, %q) = %q, want %q", c.in, c.home, got, c.want)
		}
	}
}

// The account name must not survive anywhere in the meta block.
func TestBuildMetaRedactsEveryPath(t *testing.T) {
	const home = `/home/alice`
	const marker = "alice"
	cli := map[string]any{
		"dirs": "/home/alice/.pi/agent/sessions, /var/shared/other",
	}
	m := BuildMeta("2026-01-01T00:00:00+08:00",
		home+"/.pi-desktop/pi.sqlite", home+"/.pi-desktop", nil, 19,
		1024, 0, nil, nil, cli, home)

	if strings.Contains(m.DBPath, marker) {
		t.Errorf("DBPath still names the account: %q", m.DBPath)
	}
	if strings.Contains(m.PiDir, marker) {
		t.Errorf("PiDir still names the account: %q", m.PiDir)
	}
	if m.DBPath != "~/.pi-desktop/pi.sqlite" {
		t.Errorf("DBPath = %q", m.DBPath)
	}
	if m.PiDir != "~/.pi-desktop" {
		t.Errorf("PiDir = %q", m.PiDir)
	}
	// The external path in the same list must survive untouched.
	dirs, _ := m.CLI.(map[string]any)["dirs"].(string)
	if strings.Contains(dirs, marker) {
		t.Errorf("cli.dirs still names the account: %q", dirs)
	}
	if !strings.Contains(dirs, "/var/shared/other") {
		t.Errorf("a path outside home was mangled: %q", dirs)
	}
	if !strings.Contains(dirs, "~/.pi/agent/sessions") {
		t.Errorf("home-relative cli dir was not folded: %q", dirs)
	}
}

// Passing an empty home must leave everything verbatim - some callers may
// legitimately want the real path.
func TestBuildMetaWithoutHomeKeepsPaths(t *testing.T) {
	m := BuildMeta("t", `C:\Users\ab\pi.sqlite`, `C:\Users\ab`, nil, 1, 0, 0, nil, nil, nil, "")
	if m.DBPath != `C:\Users\ab\pi.sqlite` {
		t.Errorf("DBPath = %q, want the original", m.DBPath)
	}
	if m.PiDir != `C:\Users\ab` {
		t.Errorf("PiDir = %q, want the original", m.PiDir)
	}
}

// warehouse.CLIStats returns map[string]string. Asserting only on
// map[string]any passed the field through untouched, and the account name
// survived into data.js - so this case is pinned explicitly.
func TestBuildMetaRedactsCLIDirsInTheRealShape(t *testing.T) {
	const home = `/home/alice`
	cli := map[string]string{
		"dirs":       "/home/alice/.pi/agent/sessions",
		"files":      "1",
		"tokenTotal": "444",
	}
	m := BuildMeta("t", home+"/pi.sqlite", home, nil, 19, 0, 0, nil, nil, cli, home)
	got, ok := m.CLI.(map[string]string)
	if !ok {
		t.Fatalf("CLI shape changed to %T", m.CLI)
	}
	if strings.Contains(got["dirs"], "alice") {
		t.Errorf("cli.dirs still names the account: %q", got["dirs"])
	}
	if got["dirs"] != "~/.pi/agent/sessions" {
		t.Errorf("cli.dirs = %q", got["dirs"])
	}
	if got["tokenTotal"] != "444" {
		t.Errorf("a non-path field was mangled: %q", got["tokenTotal"])
	}
}

func TestBuildMetaRedactsACommaJoinedCLIDirList(t *testing.T) {
	const home = `/home/alice`
	for _, shape := range []any{
		map[string]string{"dirs": "/home/alice/.a, /var/shared/b"},
		map[string]any{"dirs": "/home/alice/.a, /var/shared/b"},
	} {
		m := BuildMeta("t", home+"/pi.sqlite", home, nil, 19, 0, 0, nil, nil, shape, home)
		var dirs string
		switch v := m.CLI.(type) {
		case map[string]string:
			dirs = v["dirs"]
		case map[string]any:
			dirs, _ = v["dirs"].(string)
		}
		if strings.Contains(dirs, "alice") {
			t.Errorf("%T: still names the account: %q", m.CLI, dirs)
		}
		if !strings.Contains(dirs, "/var/shared/b") {
			t.Errorf("%T: a path outside home was mangled: %q", m.CLI, dirs)
		}
	}
}

func TestRedactHomeIsIdempotent(t *testing.T) {
	once := RedactHome(`/home/alice/.pi/pi.sqlite`, `/home/alice`)
	if twice := RedactHome(once, `/home/alice`); twice != once {
		t.Errorf("second pass changed %q to %q", once, twice)
	}
	if strings.Contains(once, "alice") {
		t.Errorf("still names the account: %q", once)
	}
}

func TestRedactHomeTrailingSeparatorHome(t *testing.T) {
	// A home with a trailing separator must not produce a double slash.
	home := `/home/alice` + string(filepath.Separator)
	got := RedactHome(`/home/alice/.pi/pi.sqlite`, home)
	if got != "~/.pi/pi.sqlite" {
		t.Errorf("got %q", got)
	}
}
