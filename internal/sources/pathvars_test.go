package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real environment variable must always win, so setting APPDATA on Linux (for
// a Wine install, or just to point at a test directory) keeps working.
func TestExpandWinEnvPrefersRealEnv(t *testing.T) {
	t.Setenv("APPDATA", `/explicit/appdata`)
	got := expandWinEnv("%APPDATA%/sessions", "/home/u")
	if want := "/explicit/appdata/sessions"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandWinEnvLeavesEscapedPercent(t *testing.T) {
	if got := expandWinEnv("100%% done", ""); got != "100% done" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandWinEnvUnmatchedPercent(t *testing.T) {
	// A single trailing % is not a variable reference.
	if got := expandWinEnv("a%", ""); got != "a%" {
		t.Fatalf("got %q", got)
	}
}

// %VAR% for a variable nobody defines must stay literal, which is what
// UnresolvedVars then reports.
func TestExpandWinEnvKeepsUnknownVarLiteral(t *testing.T) {
	const name = "TK_DEFINITELY_NOT_SET_VAR"
	if _, ok := os.LookupEnv(name); ok {
		t.Skip("environment unexpectedly defines " + name)
	}
	got := expandWinEnv("%"+name+"%/x", "/home/u")
	if want := "%" + name + "%/x"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnresolvedVarsReportsUnknownOnly(t *testing.T) {
	const name = "TK_DEFINITELY_NOT_SET_VAR"
	got := UnresolvedVars("%"+name+"%/x", "")
	if len(got) != 1 || got[0] != name {
		t.Fatalf("UnresolvedVars = %v, want [%s]", got, name)
	}
}

// APPDATA is the variable the manifests lean on hardest: 13 of the DSH
// Desktop adapter's 14 paths are %APPDATA%-relative. It must resolve to
// something real off Windows, or that adapter contributes nothing there.
//
// The OS is passed explicitly so this runs everywhere, not only where the suite
// happens to execute.
func TestAppDataResolvesOffWindows(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "u")
	for _, goos := range []string{"linux", "darwin"} {
		t.Setenv("APPDATA", "")
		for _, v := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
			t.Setenv(v, "")
		}
		got, ok := lookupPathVarFor(goos, "APPDATA", home)
		if !ok {
			t.Fatalf("%s: APPDATA did not resolve", goos)
		}
		// filepath.IsAbs is deliberately not used: the point is the mapping
		// from goos to a layout, and that is host-independent by construction.
		if strings.Contains(got, "%") {
			t.Errorf("%s: APPDATA still contains a percent sign: %q", goos, got)
		}
		if !strings.HasPrefix(got, home) {
			t.Errorf("%s: APPDATA = %q, want it under %q", goos, got, home)
		}
	}
}

func TestPlatformAppDataLayouts(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "u")
	if got, want := platformAppData("darwin", home), filepath.Join(home, "Library", "Application Support"); got != want {
		t.Errorf("darwin = %q, want %q", got, want)
	}
	if got, want := platformAppData("linux", home), filepath.Join(home, ".local", "share"); got != want {
		t.Errorf("linux = %q, want %q", got, want)
	}
}

func TestAppDataPrefersXDGDataHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("APPDATA", "") // force the fallback chain
	got, ok := lookupPathVarFor("linux", "APPDATA", "/home/u")
	if !ok || got != xdg {
		t.Fatalf("APPDATA = %q ok=%v, want XDG_DATA_HOME %q", got, ok, xdg)
	}
}

func TestLocalAppDataPrefersXDGStateHome(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("LOCALAPPDATA", "")
	got, ok := lookupPathVarFor("linux", "LOCALAPPDATA", "/home/u")
	if !ok || got != state {
		t.Fatalf("LOCALAPPDATA = %q ok=%v, want XDG_STATE_HOME %q", got, ok, state)
	}
}

func TestVariablesWithoutACounterpartStayUnresolved(t *testing.T) {
	// HOMEDRIVE/HOMEPATH have no meaning off Windows, so they must NOT be
	// faked: a bogus value would send a collector to a nonexistent root.
	for _, name := range []string{"HOMEDRIVE", "HOMEPATH"} {
		t.Setenv(name, "")
		for _, goos := range []string{"linux", "darwin"} {
			if _, ok := lookupPathVarFor(goos, name, "/home/u"); ok {
				t.Errorf("%s resolved on %s but has no counterpart there", name, goos)
			}
		}
	}
}

// On Windows there is nothing to fall back to, so an unset APPDATA stays
// unresolved and the diagnostic in collect is what tells the user.
func TestWindowsHasNoFallbackForUnsetVar(t *testing.T) {
	t.Setenv("TK_DEFINITELY_NOT_SET_VAR", "")
	if _, ok := lookupPathVarFor("windows", "TK_DEFINITELY_NOT_SET_VAR", "/home/u"); ok {
		t.Fatal("windows must not invent a value")
	}
}

// No shipped adapter may depend entirely on a variable this machine cannot
// resolve - that is the condition that makes an adapter contribute nothing.
func TestShippedManifestsHaveNoBlockingVars(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "adapters")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("adapter dir absent")
	}
	manifests, err := LoadAdapters(dir, nil)
	if err != nil {
		t.Fatalf("LoadAdapters: %v", err)
	}
	if len(manifests) == 0 {
		t.Fatalf("no manifests loaded from %s", dir)
	}
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	for _, m := range manifests {
		if missing := MissingVarsFor(m, home); len(missing) > 0 {
			t.Errorf("%s would contribute nothing: %v", m.ID, missing[m.ID])
		}
	}
}

// An optional override that resolves nowhere is normal and must stay quiet:
// deepseek-harness pairs %DSH_HOME% with {home}/.dsh, so the adapter still has
// a usable path.
func TestOptionalOverrideDoesNotTriggerAWarning(t *testing.T) {
	const name = "TK_DEFINITELY_NOT_SET_VAR"
	m := &Manifest{ID: "opt", Paths: []string{
		"%" + name + "%/sessions/**",
		"{home}/.opt/sessions/**",
	}}
	if got := MissingVarsFor(m, "/home/u"); len(got) != 0 {
		t.Fatalf("an optional override should not warn, got %v", got)
	}
}

func TestMissingVarsForIsEmptyForCleanManifest(t *testing.T) {
	m := &Manifest{ID: "x", Paths: []string{"{home}/.x/sessions/**/*.jsonl"}}
	if got := MissingVarsFor(m, "/home/u"); len(got) != 0 {
		t.Fatalf("MissingVarsFor = %v, want none", got)
	}
}

// A variable every path depends on leaves the adapter with nothing to collect,
// so that is the case that must be reported.
func TestMissingVarsForNamesTheAdapter(t *testing.T) {
	const name = "TK_DEFINITELY_NOT_SET_VAR"
	m := &Manifest{ID: "x", Paths: []string{"%" + name + "%/a", "%" + name + "%/b"}}
	got := MissingVarsFor(m, "/home/u")
	if len(got) != 1 {
		t.Fatalf("MissingVarsFor = %v", got)
	}
	if vars := got["x"]; len(vars) != 1 || vars[0] != name {
		t.Fatalf("vars = %v, want [%s]", vars, name)
	}
}

// subst is what actually feeds the glob, so exercise the whole chain once.
func TestSubstExpandsHomeThenVars(t *testing.T) {
	t.Setenv("APPDATA", "/appdata")
	ctx := &Context{Home: "/home/u"}
	got := subst("{home}/a %APPDATA%/b", ctx)
	if want := "/home/u/a /appdata/b"; got != want {
		t.Fatalf("subst = %q, want %q", got, want)
	}
}
