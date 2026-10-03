package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Cross-platform resolution for the Windows-shaped path variables the adapter
// manifests use.
//
// The manifests are written with %VAR% because that is what the original Python
// collector accepted, and a Windows-only path set is a poor fit for a tool that
// also builds a macOS and Linux binary. Rather than fork every manifest per
// platform, a Windows variable resolves to its nearest equivalent elsewhere:
//
//	%APPDATA%       Windows roaming app data   ->  XDG_DATA_HOME, else the
//	                                               platform app-data dir
//	%LOCALAPPDATA%  Windows local app data      ->  XDG_STATE_HOME, else same
//
// A real environment variable always wins, so setting APPDATA explicitly on
// Linux (for a Wine install, say) still works.

// windowsVarFallbacks maps a Windows variable to the environment variables that
// carry the same meaning elsewhere, most specific first. An empty list means the
// variable has no meaningful counterpart and must stay unresolved.
var windowsVarFallbacks = map[string][]string{
	"APPDATA":       {"XDG_DATA_HOME", "XDG_CONFIG_HOME"},
	"LOCALAPPDATA":  {"XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME"},
	"USERPROFILE":   {"HOME"},
	"HOMEDRIVE":     nil, // meaningless off Windows
	"HOMEPATH":      nil,
	"PROGRAMDATA":   {"XDG_DATA_HOME"},
	"PROGRAMDATA32": {"XDG_DATA_HOME"},
}

// platformAppData is where an app keeps its data on the given OS when no XDG
// variable is set. goos is a parameter rather than a runtime.GOOS lookup so the
// mapping can be tested from any platform - otherwise these cases would only
// ever run on the machine that happened to run the suite.
func platformAppData(goos, home string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	default:
		// The freedesktop layout: XDG_DATA_HOME defaults to ~/.local/share,
		// which is the closest analogue of Windows roaming app data.
		return filepath.Join(home, ".local", "share")
	}
}

// lookupPathVar resolves a path-bearing variable, falling back to the platform
// equivalent when the variable is not set in the environment.
func lookupPathVar(name, home string) (string, bool) {
	return lookupPathVarFor(runtime.GOOS, name, home)
}

// lookupPathVarFor is lookupPathVar with an explicit OS, so the cross-platform
// mapping is verifiable from any machine.
func lookupPathVarFor(goos, name, home string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v, true
	}
	if goos == "windows" {
		return "", false
	}
	chain, known := windowsVarFallbacks[strings.ToUpper(name)]
	if !known {
		return "", false
	}
	for _, alt := range chain {
		if v, ok := os.LookupEnv(alt); ok && v != "" {
			return v, true
		}
	}
	switch strings.ToUpper(name) {
	case "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "PROGRAMDATA32":
		if home != "" {
			return platformAppData(goos, home), true
		}
	}
	return "", false
}

// UnresolvedVars lists the %VAR% references in a pattern that this machine
// cannot satisfy.
//
// A path that still contains one of these is a literal string such as
// "%APPDATA%/sessions": it will never match a file, and the adapter will
// silently contribute nothing. Reporting it turns a confusing "my numbers are
// missing" into a diagnosable cause.
//
// home must be the same value subst will use, otherwise a machine with no XDG
// variable set would be reported as unresolvable even though the real
// expansion falls back to the platform app-data dir and succeeds.
func UnresolvedVars(pattern, home string) []string {
	var out []string
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '%' {
			continue
		}
		end := strings.IndexByte(pattern[i+1:], '%')
		if end < 0 {
			break
		}
		name := pattern[i+1 : i+1+end]
		i += end + 1
		if name == "" {
			continue
		}
		if _, ok := lookupPathVar(name, home); !ok {
			out = append(out, name)
		}
	}
	return out
}

// MissingVarsFor reports the path variables that would leave an adapter with no
// usable path at all.
//
// The bar is deliberately high. A manifest may legitimately carry an optional
// override - deepseek-harness lists %DSH_HOME% alongside {home}/.dsh - and an
// unresolved override just means that one path is skipped. Warning about it
// would be noise on every machine that has not set it.
//
// What actually hurts is a variable that EVERY path depends on: 13 of
// deepseek-harness-wrapper's 14 paths are %APPDATA%-relative, so if that
// variable cannot be resolved the adapter silently contributes nothing. That
// is the case worth reporting, because otherwise the only symptom is a tool
// whose numbers quietly went missing.
func MissingVarsFor(m *Manifest, home string) map[string][]string {
	if len(m.Paths) == 0 {
		return nil
	}
	counts := map[string]int{}
	unresolved := map[string]bool{}
	for _, p := range m.Paths {
		for _, v := range UnresolvedVars(p, home) {
			counts[v]++
			unresolved[v] = true
		}
	}
	var blocking []string
	for v := range unresolved {
		if counts[v] == len(m.Paths) {
			blocking = append(blocking, v)
		}
	}
	if len(blocking) == 0 {
		return nil
	}
	sortStrings(blocking)
	return map[string][]string{m.ID: blocking}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
