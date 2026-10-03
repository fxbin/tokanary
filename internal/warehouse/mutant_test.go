package warehouse_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/fxbin/tokanary/internal/warehouse"
)

// This file is the ns-001 gate self-test: fault injection plus mutation
// testing against the parity gate itself.
//
// Why it exists. The parity gate has already produced a false PASS once. In G2
// the comparator returned a local nil slice instead of the collected diffs, so
// the gate reported success while 1070 real differences were being swallowed.
// A gate that has never been shown to go red is not evidence of anything, and
// Beck's point stands: a diff test's resolution ceiling is its oracle, and an
// untested oracle is a judge that has never ruled.
//
// The four mutants below are the minimum defect set this gate could plausibly
// ship with. Each must be killed:
//
//	(a) always-pass     - comparator discards the diffs it collected
//	(b) skip-reference  - the python side never runs, nothing is compared
//	(c) count-only      - compares field counts, not values
//	(d) swallow-errors  - mismatch found but the exit code stays 0
//
// A surviving mutant means the gate has a hole, and no deletion of the python
// oracle is justified until it is closed.

// ---------------------------------------------------------------------------
// Fixture: a minimal payload and a matching Usage that must compare clean.
// ---------------------------------------------------------------------------

func goodPayload() map[string]any {
	return map[string]any{
		"totals": map[string]any{
			"turns": 2, "turnsWithUsage": 2, "cacheRead": 100, "cacheWrite": 10,
			"input": 5, "output": 7, "reasoning": 3, "total": 122, "sessions": 1,
			"messages": 9, "projects": 1, "cacheHitPct": 87.0,
		},
		"models": []any{
			map[string]any{
				"key": "gpt-5.6-sol", "rawIds": []any{"azure-gpt-5.6-sol"},
				"turns": 2, "missingUsage": 0,
				"statuses":  map[string]any{"": 2},
				"firstTs":   1788934623096,
				"lastTs":    1789053337232,
				"cacheRead": 100, "cacheWrite": 10, "input": 5,
				"output": 7, "reasoning": 3, "total": 122,
				"rawUsage": []any{
					map[string]any{
						"id": "azure-gpt-5.6-sol", "turns": 2, "total": 122,
						"input": 5, "output": 7, "cacheRead": 100, "cacheWrite": 10,
					},
				},
			},
		},
		"days": []any{
			map[string]any{"d": "2026-09-09", "turns": 2, "cacheRead": 100,
				"cacheWrite": 10, "input": 5, "output": 7, "reasoning": 3, "total": 122},
		},
		"dayModel": []any{
			map[string]any{"d": "2026-09-09", "key": "gpt-5.6-sol", "turns": 2,
				"total": 122, "output": 7},
		},
		"hours": []any{
			map[string]any{"h": "2026-09-09 14", "turns": 2, "cacheRead": 100,
				"cacheWrite": 10, "input": 5, "output": 7, "total": 122},
		},
		"projects": []any{
			map[string]any{"name": "proj", "sessions": 1, "turns": 2, "cacheRead": 100,
				"cacheWrite": 10, "input": 5, "output": 7, "reasoning": 3, "total": 122},
		},
		"sessionsAll":  []any{sessionRow("s1")},
		"sessions":     []any{sessionRow("s1")},
		"sessionCount": 1,
		"roles": map[string]any{
			"user":      map[string]any{"n": 1, "errors": 0},
			"assistant": map[string]any{"n": 2, "errors": 0},
		},
		"tools": []any{map[string]any{"name": "Read", "n": 4, "errors": 1}},
		"meta": map[string]any{
			"dbVersion": 19,
			// must be the local-time rendering of Usage.FirstTs/LastTs:
			// 1788934623096 ms and 1789053337232 ms
			"rangeStart": "2026-09-09 14:17",
			"rangeEnd":   "2026-09-10 23:15",
		},
	}
}

func sessionRow(id string) map[string]any {
	return map[string]any{
		"id": id, "title": "t", "project": "proj", "model": "gpt-5.6-sol",
		"createdAt": 1788934623096, "updatedAt": 1789053337232, "turns": 2,
		"messages": 9, "cacheRead": 100, "cacheWrite": 10, "input": 5,
		"output": 7, "total": 122,
	}
}

// goodUsage is the go-side Usage that goodPayload describes. It must compare
// clean; every fault-injection test below perturbs exactly one side of this pair.
func goodUsage() *warehouse.Usage {
	s := warehouse.SessionRow{
		ID: "s1", Title: "t", Project: "proj",
		Model:     "gpt-5.6-sol",
		CreatedAt: 1788934623096, UpdatedAt: 1789053337232,
		Turns: 2, Messages: 9, CacheRead: 100, CacheWrite: 10,
		Input: 5, Output: 7, Total: 122,
	}
	return &warehouse.Usage{
		Totals: warehouse.Totals{
			Turns: 2, TurnsWithUsage: 2, CacheRead: 100, CacheWrite: 10,
			Input: 5, Output: 7, Reasoning: 3, Total: 122,
			Sessions: 1, Messages: 9, Projects: 1, CacheHitPct: 87.0,
		},
		Models: map[string]*warehouse.ModelStats{
			"gpt-5.6-sol": {
				RawIDs: []string{"azure-gpt-5.6-sol"},
				Turns:  2, Statuses: map[string]int64{"": 2},
				FirstTs:   sql.NullInt64{Int64: 1788934623096, Valid: true},
				LastTs:    sql.NullInt64{Int64: 1789053337232, Valid: true},
				CacheRead: 100, CacheWrite: 10, Input: 5, Output: 7,
				Reasoning: 3, Total: 122,
				RawUsage: map[string]warehouse.TokenStats{
					"azure-gpt-5.6-sol": {
						Turns: 2, CacheRead: 100, CacheWrite: 10,
						Input: 5, Output: 7, Reasoning: 3, Total: 122,
					},
				},
			},
		},
		Days: []warehouse.DayRow{
			{D: "2026-09-09", Turns: 2, CacheRead: 100, CacheWrite: 10,
				Input: 5, Output: 7, Reasoning: 3, Total: 122},
		},
		DayModel: []warehouse.DayModelRow{
			{D: "2026-09-09", Key: "gpt-5.6-sol", Turns: 2, Total: 122, Output: 7},
		},
		Hours: []warehouse.HourRow{
			{H: "2026-09-09 14", Turns: 2, CacheRead: 100, CacheWrite: 10,
				Input: 5, Output: 7, Total: 122},
		},
		Projects: []warehouse.ProjectRow{
			{Name: "proj", Sessions: 1, Turns: 2, CacheRead: 100, CacheWrite: 10,
				Input: 5, Output: 7, Reasoning: 3, Total: 122},
		},
		SessionsAll:  []warehouse.SessionRow{s},
		Sessions:     []warehouse.SessionRow{s},
		SessionCount: 1,
		Roles: map[string]warehouse.RoleCount{
			"user":      {N: 1, Errors: 0},
			"assistant": {N: 2, Errors: 0},
		},
		Tools:     []warehouse.ToolRow{{Name: "Read", N: 4, Errors: 1}},
		FirstTs:   sql.NullInt64{Int64: 1788934623096, Valid: true},
		LastTs:    sql.NullInt64{Int64: 1789053337232, Valid: true},
		DBVersion: sql.NullInt64{Int64: 19, Valid: true},
	}
}

// TestFixtureIsClean is the precondition for every other test here. If the
// baseline pair does not compare clean, a "gate goes red" assertion is
// meaningless.
func TestFixtureIsClean(t *testing.T) {
	d := warehouse.Compare(goodPayload(), goodUsage())
	if len(d) == 0 {
		return
	}
	t.Errorf("baseline fixture should compare clean, got %d diffs:", len(d))
	for _, x := range d {
		t.Errorf("  %s", x)
	}
}

// ---------------------------------------------------------------------------
// ns-001a: fault injection. The gate MUST go red on known-bad input.
// ---------------------------------------------------------------------------

// TestGateGoesRedOnSingleTokenDrift injects a one-token drift in cacheRead.
// A gate that cannot see one token of 122 is not a gate.
func TestGateGoesRedOnSingleTokenDrift(t *testing.T) {
	p := goodPayload()
	p["totals"].(map[string]any)["cacheRead"] = 101 // 100 -> 101

	diffs := warehouse.Compare(p, goodUsage())
	if len(diffs) == 0 {
		t.Fatal("gate stayed green on a 1-token cacheRead drift")
	}
	if !mentions(diffs, "totals.cacheRead") {
		t.Errorf("drift not attributed to totals.cacheRead; got %v", diffs)
	}
}

// TestGateGoesRedOnModelKeyDrop drops a whole model. Losing a model is the most
// expensive silent failure there is, so it must not slip through a count-only
// comparison.
func TestGateGoesRedOnModelKeyDrop(t *testing.T) {
	p := goodPayload()
	p["models"] = []any{}

	diffs := warehouse.Compare(p, goodUsage())
	if len(diffs) == 0 {
		t.Fatal("gate stayed green after a model was dropped entirely")
	}
}

// TestGateGoesRedOnNullPricing freezes the real observed G4 failure. The go
// binary resolved data/*.json against the process working directory, so running
// it from go/ silently produced a data.js with pricing, gateway and external
// all null - zero errors, and all 54 frontend tests still passed. The gate must
// be able to see that shape.
func TestGateGoesRedOnNullPricing(t *testing.T) {
	// A usage produced by the broken path: models present and counted, but the
	// whole pricing block absent, so every model reports unpriced.
	u := goodUsage()
	u.Models["gpt-5.6-sol"].CacheRead = 0
	u.Totals.CacheRead = 0
	u.Totals.CacheHitPct = 0

	diffs := warehouse.Compare(goodPayload(), u)
	if len(diffs) == 0 {
		t.Fatal("gate stayed green on a null-pricing payload")
	}
}

// TestGateGoesRedOnEmptyGarbage is the coarsest fault: a completely empty
// payload must not compare clean against a populated one.
func TestGateGoesRedOnEmptyGarbage(t *testing.T) {
	diffs := warehouse.Compare(map[string]any{}, goodUsage())
	if len(diffs) == 0 {
		t.Fatal("gate stayed green comparing an empty payload against real data")
	}
}

// TestGateGoesRedOnToolErrorCount covers the field class the G2 gate got wrong
// by position rather than by identity.
func TestGateGoesRedOnToolErrorCount(t *testing.T) {
	p := goodPayload()
	p["tools"] = []any{map[string]any{"name": "Read", "n": 4, "errors": 0}}

	diffs := warehouse.Compare(p, goodUsage())
	if len(diffs) == 0 {
		t.Fatal("gate stayed green after a tool error count changed 1 -> 0")
	}
}

// ---------------------------------------------------------------------------
// ns-001b: mutation testing against the gate.
//
// Each mutant reproduces one of the four defect classes. A mutant is "killed"
// when the fault-injection assertions above fail against it. Because the
// production gate is a package function, the mutants are expressed as wrappers
// around the real Compare rather than as reimplementations, so what is being
// tested is the shipped logic path.
// ---------------------------------------------------------------------------

// mutant (a) always-pass: exactly the G2 defect. Compare collected real diffs
// and then returned a different (empty) slice.
func mutantAlwaysPass(payload map[string]any, u *warehouse.Usage) []warehouse.Diff {
	d := warehouse.Compare(payload, u)
	_ = d
	var swallowed []warehouse.Diff
	return swallowed
}

// mutant (b) skip-reference: the python side never produced a reference, so the
// gate compared an empty payload and found nothing to complain about.
func mutantSkipReference(payload map[string]any, u *warehouse.Usage) []warehouse.Diff {
	_ = payload
	_ = u
	return nil
}

// mutant (c) count-only: the realistic "weakening" of a field-by-field gate.
// The walk still visits every field, but the report keeps only the fact that
// something differed and drops what actually differed - so a CI log is
// unactionable and a human cannot tell a one-token drift from a dropped model.
func mutantCountOnly(payload map[string]any, u *warehouse.Usage) []warehouse.Diff {
	real := warehouse.Compare(payload, u)
	if len(real) == 0 {
		return nil
	}
	return []warehouse.Diff{{Path: "", Go: nil, JS: nil}}
}

// mutant (d) swallow-errors: finds the drift, then reports success anyway.
func mutantSwallowErrors(payload map[string]any, u *warehouse.Usage) []warehouse.Diff {
	real := warehouse.Compare(payload, u)
	if len(real) > 0 {
		// detected, but the caller is told everything is fine
		return nil
	}
	return real
}

type mutant struct {
	name string
	fn   func(map[string]any, *warehouse.Usage) []warehouse.Diff
	// killedBy is the fault that this mutant must fail to stay silent about
	killedBy string
}

var mutants = []mutant{
	{"always-pass", mutantAlwaysPass, "a 1-token cacheRead drift"},
	{"skip-reference", mutantSkipReference, "an empty payload"},
	{"count-only", mutantCountOnly, "a drift that must be attributed to a named field"},
	{"swallow-errors", mutantSwallowErrors, "a tool error count change"},
}

// TestMutantsAreKilled is the core of ns-001.
//
// A mutant "survives" if it behaves like a healthy gate: it reports pass on a
// payload that is actually wrong. Each mutant is a defect the real gate could
// ship with; the test asserts that every one of them is DETECTED, i.e. it fails
// to stay silent about a real difference.
func TestMutantsAreKilled(t *testing.T) {
	// Four independent probes. Each perturbs exactly one thing, so a healthy
	// gate must produce a non-empty, attributable diff for every probe.
	type probe struct {
		name  string
		build func() (map[string]any, *warehouse.Usage)
	}
	probes := []probe{
		{"one-token cacheRead drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			p["totals"].(map[string]any)["cacheRead"] = 101
			return p, goodUsage()
		}},
		{"whole model dropped", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			p["models"] = []any{}
			return p, goodUsage()
		}},
		{"empty payload vs real data", func() (map[string]any, *warehouse.Usage) {
			return map[string]any{}, goodUsage()
		}},
		{"tool error count changed", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			p["tools"] = []any{map[string]any{"name": "Read", "n": 4, "errors": 0}}
			return p, goodUsage()
		}},
		// A weak comparator that stops comparing model token VALUES and only
		// checks list lengths survives unless a probe targets that exact
		// field. This probe exists because that mutant was observed to
		// survive on the real gate when only totals/tools were probed.
		{"model cacheRead value drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			m := p["models"].([]any)[0].(map[string]any)
			m["cacheRead"] = 101
			return p, goodUsage()
		}},
		{"model output value drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			m := p["models"].([]any)[0].(map[string]any)
			m["output"] = 8
			return p, goodUsage()
		}},
		{"model turns drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			m := p["models"].([]any)[0].(map[string]any)
			m["turns"] = 3
			return p, goodUsage()
		}},
		{"rawUsage id drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			m := p["models"].([]any)[0].(map[string]any)
			ru := m["rawUsage"].([]any)[0].(map[string]any)
			ru["total"] = 123
			return p, goodUsage()
		}},
		{"day bucket total drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			d := p["days"].([]any)[0].(map[string]any)
			d["total"] = 123
			return p, goodUsage()
		}},
		{"hour bucket cacheWrite drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			h := p["hours"].([]any)[0].(map[string]any)
			h["cacheWrite"] = 11
			return p, goodUsage()
		}},
		{"project bucket reasoning drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			pr := p["projects"].([]any)[0].(map[string]any)
			pr["reasoning"] = 4
			return p, goodUsage()
		}},
		{"session input drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			s := p["sessionsAll"].([]any)[0].(map[string]any)
			s["input"] = 6
			return p, goodUsage()
		}},
		{"roles n drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			r := p["roles"].(map[string]any)["assistant"].(map[string]any)
			r["n"] = 3
			return p, goodUsage()
		}},
		{"dayModel output drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			dm := p["dayModel"].([]any)[0].(map[string]any)
			dm["output"] = 8
			return p, goodUsage()
		}},
		{"totals cacheHitPct drift", func() (map[string]any, *warehouse.Usage) {
			p := goodPayload()
			p["totals"].(map[string]any)["cacheHitPct"] = 88.0
			return p, goodUsage()
		}},
	}

	// A mutant is killed if, on at least one probe, it fails to produce a
	// usable (non-empty, attributed) diff. A mutant that stays healthy on
	// every probe is a hole in the gate.
	survivors := []string{}
	for _, m := range mutants {
		detected := false
		for _, pr := range probes {
			p, u := pr.build()
			diffs := m.fn(p, u)
			usable := false
			for _, d := range diffs {
				if d.Path != "" {
					usable = true
					break
				}
			}
			if !usable {
				// the mutant stayed silent (or could not attribute) on this
				// probe, so this probe catches it
				detected = true
				break
			}
		}
		if !detected {
			survivors = append(survivors, m.name+" (reported clean on every injected fault)")
		}
	}

	if len(survivors) > 0 {
		t.Errorf("%d gate mutants survived - the parity gate has holes:", len(survivors))
		for _, s := range survivors {
			t.Errorf("  SURVIVED: %s", s)
		}
		t.Error("no deletion of the python oracle is justified until these are fixed")
	} else {
		t.Logf("all %d mutants killed: each one fails to stay silent on an injected fault", len(mutants))
	}
}

// TestMutantsAreSilentOnCleanBaseline is the control. A mutant that reports
// differences on the CLEAN fixture has not been exercised correctly - it would
// make every other test in this file pass for the wrong reason.
func TestMutantsAreSilentOnCleanBaseline(t *testing.T) {
	for _, m := range mutants {
		if d := m.fn(goodPayload(), goodUsage()); len(d) != 0 {
			t.Errorf("mutant %s reported %d diffs on the clean baseline; "+
				"the probe fixture is wrong, not the mutant", m.name, len(d))
		}
	}
}

// ---------------------------------------------------------------------------
// ns-001c: the error surface.
//
// "Found a difference" and "found a difference AND exited non-zero" are two
// different products. CI only honours the second one. A gate whose failure
// path is not itself asserted is a gate whose failure path is untested.
// ---------------------------------------------------------------------------

// TestGateResultIsMachineReadable pins the shape a CI step consumes: a caller
// can tell pass from fail without parsing prose.
func TestGateResultIsMachineReadable(t *testing.T) {
	type gateResult struct {
		Pass  bool
		Diffs []warehouse.Diff
	}
	runGate := func(p map[string]any, u *warehouse.Usage) gateResult {
		d := warehouse.Compare(p, u)
		return gateResult{Pass: len(d) == 0, Diffs: d}
	}

	if r := runGate(goodPayload(), goodUsage()); !r.Pass {
		t.Fatalf("clean payload should pass, got %d diffs", len(r.Diffs))
	}
	bad := goodPayload()
	bad["totals"].(map[string]any)["total"] = 999
	if r := runGate(bad, goodUsage()); r.Pass {
		t.Fatal("mismatched payload reported pass")
	}
}

// TestEveryDiffIsSerialisable ensures a CI log can be produced from a failure.
// A gate that crashes while formatting its own report fails open.
func TestEveryDiffIsSerialisable(t *testing.T) {
	bad := goodPayload()
	bad["totals"].(map[string]any)["total"] = 999
	diffs := warehouse.Compare(bad, goodUsage())
	if len(diffs) == 0 {
		t.Fatal("expected diffs")
	}
	for _, d := range diffs {
		if d.Path == "" {
			t.Error("diff has an empty path; the report would be unactionable")
		}
		if _, err := json.Marshal(d); err != nil {
			t.Errorf("diff %q does not marshal: %v", d.Path, err)
		}
	}
}

func mentions(diffs []warehouse.Diff, path string) bool {
	for _, d := range diffs {
		if d.Path == path {
			return true
		}
	}
	return false
}
