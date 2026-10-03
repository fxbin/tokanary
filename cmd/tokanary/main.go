// Command tokanary is the single binary for the Tokanary pipeline.
// Subcommands land slice by slice; clisession-selftest is the G1 parity gate
// and warehouse is the G2 parity gate.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fxbin/tokanary/internal/clisession"
)

// externalUsagePath locates the collect -> build handoff file.
//
// It is an intermediate artefact, so it lives under .cache/ alongside the
// consistency snapshot and the sqlite warehouse instead of in data/, which
// holds only committed declarations and price tables.
//
// The path is anchored to repoRoot, never to the process cwd. Resolving
// pipeline paths against cwd is a bug this project already paid for once -
// see the comment above repoRoot resolution in build.go.
func externalUsagePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".cache", "external-usage.json")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "clisession-selftest":
		os.Exit(cliSessionSelfTest(os.Args[2:]))
	case "warehouse":
		os.Exit(runWarehouse(os.Args[2:]))
	case "build":
		os.Exit(runBuild(os.Args[2:]))
	case "collect":
		os.Exit(runCollect(os.Args[2:]))
	case "prices":
		os.Exit(runPrices(os.Args[2:]))
	case "refresh":
		os.Exit(runRefresh(os.Args[2:]))
	case "schedule":
		os.Exit(runSchedule(os.Args[2:]))
	case "validate":
		os.Exit(runCollect(append([]string{"--validate"}, os.Args[2:]...)))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `tokanary - local AI usage & cost pipeline (desktop dashboard)

usage:
  tokanary refresh [<pi-dir>] [--no-web] [--quiet]
                                 collect + rebuild warehouse + write
                                 .cache/dashboard.json + build web/dist
  tokanary schedule [<minutes>] [--remove] [--show]
                                 register a background refresh every N minutes
                                 (default 60)
  tokanary clisession-selftest [--dir <sessions-root>] [--json]
                                 parse official pi CLI session JSONL (schema v3)
  tokanary warehouse [--pi-dir <dir>] [--db <path>] [--cli-dir <dir>]...
                      [--no-cli] [--check [--against <json>]] [--json]
                                 build the SQLite warehouse and export the
                                 usage payload (parity gate)
  tokanary build [--pi-dir <dir>] [--out <.cache/dashboard.json>] [--db <path>]
                 [--adapters <dir>] [--work-dir <dir>] [--no-external]
                 [--check [--against <json>]]
                                 assemble the dashboard JSON: usage +
                                 pricing + external
  tokanary collect [--adapters <dir>] [--out <.cache/external-usage.json>]
                   [--tools a,b] [--home <dir>] [--work-dir <dir>]
                   [--include-aggregators] [--full]
                                 run the adapter engine over every external
                                 tool and write .cache/external-usage.json
                                 (skip tools whose source files are unchanged
                                 unless --full)
  tokanary validate                    adapter declaration check only
  tokanary collect --list              list adapters and their state
  tokanary prices [--out .cache/prices-raw.json] [--models a,b] [--pi-dir <dir>]
                  [--no-pi] [--url <models.dev/api.json>] [--force]
                  [--report <file.json>]
                                 generate .cache/prices-raw.json from the public
                                 models.dev catalogue, matching local model ids
                                 through the same normalisation the pricer uses.
                                 Refuses to overwrite unless --force (backs up
                                 to <out>.bak first).

Desktop window: build and run ./tokanary-desktop (see desktop.go).
pi data directory resolution: --pi-dir > PI_HOME > auto-detect.
`)
}

func cliSessionSelfTest(args []string) int {
	root := "testdata/clisession"
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 < len(args) {
				i++
				root = args[i]
			}
		case "--json":
			asJSON = true
		}
	}
	res := clisession.Collect([]string{root}, nil)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res.Golden())
		return 0
	}

	bad := 0
	ck := func(cond bool, label, detail string) {
		mark := "  ok  "
		if !cond {
			mark = "  FAIL"
			bad++
		}
		fmt.Printf("%s %s  %s\n", mark, label, detail)
	}

	fmt.Printf("sessions root: %s\n", root)
	for _, w := range res.SchemaWarning {
		fmt.Printf("  [warn] %s\n", w)
	}

	var in, out, cr, cw, tot int64
	usageTurns := 0
	for _, tr := range res.Turns {
		in += tr.Input
		out += tr.Output
		cr += tr.CacheRead
		cw += tr.CacheWrite
		tot += tr.Total
		if tr.HasUsage {
			usageTurns++
		}
		ck(tr.Total == tr.Input+tr.Output+tr.CacheRead+tr.CacheWrite,
			"turn total = 4 classes", fmt.Sprintf("%s/%s total=%d", tr.Session, tr.ModelCanon, tr.Total))
		ck(tr.Day != "" && tr.Hour != "", "turn has local day/hour",
			fmt.Sprintf("%s %s %s", tr.Session, tr.Day, tr.Hour))
	}

	ck(len(res.Sessions) > 0, "sessions parsed", fmt.Sprintf("%d", len(res.Sessions)))
	known := map[string]bool{}
	for _, s := range res.Sessions {
		known[s.ID] = true
	}
	orphan := 0
	for _, tr := range res.Turns {
		if !known[tr.Session] {
			orphan++
		}
	}
	ck(orphan == 0, "every turn belongs to an emitted session", fmt.Sprintf("orphans=%d", orphan))
	ck(len(res.Sessions)+res.SkippedDup+res.SkippedEmpty >= 1 && res.Files >= len(res.Sessions),
		"files/sessions accounted",
		fmt.Sprintf("files=%d sessions=%d dup=%d empty=%d", res.Files, len(res.Sessions), res.SkippedDup, res.SkippedEmpty))
	ck(in+out+cr+cw == tot, "sum(4 classes) == sum(turn totals)",
		fmt.Sprintf("in=%d out=%d cr=%d cw=%d total=%d", in, out, cr, cw, tot))
	fmt.Printf("  roles: %v\n  tools: %v\n  files: %d  dupSkipped: %d  usageTurns: %d\n",
		res.Roles, res.Tools, res.Files, res.SkippedDup, usageTurns)

	if bad > 0 {
		fmt.Printf("selftest FAILED: %d\n", bad)
		return 1
	}
	fmt.Println("selftest OK")
	return 0
}
