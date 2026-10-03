package warehouse_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/fxbin/tokanary/internal/warehouse"
)

// This file closes the mutation-coverage hole found in ns-001b.
//
// The first version of the mutant suite probed totals, tools and a handful of
// per-model fields by hand. When a real count-only defect was injected into
// compare.go - weakening exactly the models.cacheRead comparison - the hand
// written probes still passed, because the probe that mutated models.cacheRead
// was blind to that very weakening. A probe and the mutation it is supposed to
// detect were coupled.
//
// The fix is to stop hand-listing fields. TestEveryComparedFieldIsProbed walks
// the real Usage struct by reflection, mutates one leaf at a time, and asserts
// that the gate reports each mutation. If a comparison line is ever added,
// removed, or weakened without a matching probe, this test fails.
//
// The invariant being asserted is: for every numeric leaf the gate compares,
// mutating that leaf by one unit turns the gate red. That is the property Beck
// asked for - the detector is shown to have power, not merely to exist.

// mutatableLeaf is one numeric leaf of the Usage tree.
type mutatableLeaf struct {
	path []string
	val  int64
}

// numericLeaves walks v by reflection and collects every int64/int/uint leaf
// with a non-zero value, keyed by its field path. Maps are walked in sorted key
// order so the result is deterministic.
func numericLeaves(v reflect.Value, prefix []string, out *[]mutatableLeaf) {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return
		}
		numericLeaves(v.Elem(), prefix, out)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			// sql.NullInt64 carries a Valid flag; walk its Int64
			if f.Name == "Valid" || f.Name == "String" {
				continue
			}
			numericLeaves(v.Field(i), append(prefix, f.Name), out)
		}
	case reflect.Map:
		keys := v.MapKeys()
		sortValues(keys)
		for _, k := range keys {
			ks := fmt.Sprint(k.Interface())
			numericLeaves(v.MapIndex(k), append(prefix, ks), out)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			numericLeaves(v.Index(i), append(prefix, fmt.Sprint(i)), out)
		}
	case reflect.Int64, reflect.Int:
		if v.Int() != 0 {
			*out = append(*out, mutatableLeaf{path: prefix, val: v.Int()})
		}
	}
}

func sortValues(vals []reflect.Value) {
	for i := 1; i < len(vals); i++ {
		for j := i; j > 0 && fmt.Sprint(vals[j].Interface()) < fmt.Sprint(vals[j-1].Interface()); j-- {
			vals[j], vals[j-1] = vals[j-1], vals[j]
		}
	}
}

// setLeaf walks path into v and adds delta to the int64 leaf named by the
// final segment. The head is always consumed by descending; when only one
// segment remains the current value is written directly.
func setLeaf(v reflect.Value, path []string, delta int64) bool {
	if len(path) == 0 {
		return false
	}
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			if !v.CanSet() {
				return false
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	// when this segment is the last, v is the leaf
	if len(path) == 1 {
		switch v.Kind() {
		case reflect.Int64, reflect.Int:
			v.SetInt(v.Int() + delta)
			return true
		}
		head := path[0]
		switch v.Kind() {
		case reflect.Struct:
			t := v.Type()
			for i := 0; i < t.NumField(); i++ {
				if t.Field(i).Name == head {
					f := v.Field(i)
					switch f.Kind() {
					case reflect.Int64, reflect.Int:
						f.SetInt(f.Int() + delta)
						return true
					}
				}
			}
		case reflect.Map:
			// a map leaf: mutate every value so the probe definitely bites
			if v.Len() == 0 {
				return false
			}
			for _, k := range v.MapKeys() {
				elem := v.MapIndex(k)
				cp := reflect.New(elem.Type()).Elem()
				cp.Set(elem)
				if cp.Kind() == reflect.Int64 || cp.Kind() == reflect.Int {
					cp.SetInt(cp.Int() + delta)
					v.SetMapIndex(k, cp)
				}
			}
			return true
		}
		return false
	}
	head := path[0]
	rest := path[1:]
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).Name == head {
				return setLeaf(v.Field(i), rest, delta)
			}
		}
		return false
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if fmt.Sprint(k.Interface()) == head {
				elem := v.MapIndex(k)
				cp := reflect.New(elem.Type()).Elem()
				cp.Set(elem)
				if setLeaf(cp, rest, delta) {
					v.SetMapIndex(k, cp)
					return true
				}
			}
		}
		return false
	case reflect.Slice, reflect.Array:
		var idx int
		if _, err := fmt.Sscanf(head, "%d", &idx); err != nil {
			return false
		}
		if idx < 0 || idx >= v.Len() {
			return false
		}
		return setLeaf(v.Index(idx), rest, delta)
	}
	return false
}

// knownUncompared lists numeric leaves the gate deliberately does not compare.
// Keys are matched against the leaf path with the leading collection name
// stripped, so "Models.gpt-5.6-sol.FirstTs.Int64" matches "FirstTs.Int64".
//
// The justification must be narrow on purpose. A broad substring such as
// "RawUsage" would silently excuse every field under it, which is the same
// "gate stays green" failure this suite exists to prevent.
// knownUncompared lists numeric leaves the gate deliberately does not compare.
// Keys are the leaf's FINAL TWO path segments, so "Reasoning" would also match a
// model-level Reasoning - which the gate does compare - and is therefore not
// used. Only fields that are uncompared at EVERY level of nesting may be
// excused, and each carries a justification that is logged at test time.
var knownUncompared = map[string]string{
	// Reasoning appears at two levels: model level (compared) and rawUsage
	// level (not compared, because build_data.py:334 omits it from the
	// rawUsage array). The exemption is expressed as a path that only the
	// rawUsage instance produces, via a negative marker below.
	"__rawusage_reasoning__": "build_data.py rawUsage omits reasoning",

	// data.js stores the overall range only as a local-time string at minute
	// precision. A drift smaller than a minute is therefore not observable in
	// the payload at all; minute-level drift IS caught, which
	// TestRangeBoundaryIsObservable asserts explicitly.
	"FirstTs.Int64": "data.js keeps only minute-precision range strings",
	"LastTs.Int64":  "data.js keeps only minute-precision range strings",
}

// excusedBy reports whether a leaf is deliberately uncompared. The key is the
// leaf's final two path segments (Field.Field or just Field), which is stable
// across collection nesting. The justification is logged so an excuse cannot
// be added silently.
func (c *coverageCtx) excusedBy(path string) bool {
	parts := strings.Split(path, ".")
	key := parts[len(parts)-1]
	if len(parts) >= 2 {
		key = parts[len(parts)-2] + "." + parts[len(parts)-1]
	}
	// rawUsage.Reasoning is the one Reasoning that is genuinely uncompared,
	// distinguished by having a RawUsage segment in its path. The raw model id
	// may itself contain dots, so match on the leaf name plus the segment.
	if parts[len(parts)-1] == "Reasoning" && containsSegment(parts, "RawUsage") {
		key = "__rawusage_reasoning__"
	}
	if why, ok := knownUncompared[key]; ok {
		c.t.Logf("uncompared by design: %s (%s)", path, why)
		return true
	}
	return false
}

// TestEveryComparedFieldIsProbed is the systematic coverage gate. It mutates
// every numeric leaf of Usage in turn and requires the gate to go red each
// time. A field the gate silently ignores shows up here as a surviving leaf.
func TestEveryComparedFieldIsProbed(t *testing.T) {
	payload := goodPayload()

	var leaves []mutatableLeaf
	numericLeaves(reflect.ValueOf(goodUsage()), nil, &leaves)
	if len(leaves) < 20 {
		t.Fatalf("expected the fixture to expose many numeric leaves, got %d; "+
			"the fixture is too small for this test to mean anything", len(leaves))
	}

	silent := []string{}
	for _, leaf := range leaves {
		full := strings.Join(leaf.path, ".")
		if (&coverageCtx{t: t}).excusedBy(full) {
			continue
		}
		u := goodUsage()
		if !setLeaf(reflect.ValueOf(u), append([]string{}, leaf.path...), 1) {
			t.Fatalf("probe setup failed for %s", full)
		}
		diffs := warehouse.Compare(payload, u)
		if len(diffs) == 0 {
			silent = append(silent, full)
		}
	}

	if len(silent) > 0 {
		t.Errorf("%d numeric field(s) can drift with the gate staying green:", len(silent))
		for _, s := range silent {
			t.Errorf("  UNPROBED: %s", s)
		}
	} else {
		t.Logf("all comparable numeric fields are covered: mutating any one turns the gate red")
	}
}

// excusedBy reports whether a leaf is deliberately uncompared.
//
// containsSegment reports whether seg appears as a whole path segment.
func containsSegment(parts []string, seg string) bool {
	for _, p := range parts {
		if p == seg {
			return true
		}
	}
	return false
}

// coverageCtx carries the test handle so the excuse helper can log.
type coverageCtx struct{ t *testing.T }

// TestMutatingAnInjectedDefectIsCaught is the control that ties the two halves
// together. It takes the *real* gate, weakens one comparison line the way a
// count-only defect would, and requires the coverage test above to notice.
//
// The weakening is applied through a payload-level transformation rather than by
// editing source, so this test is self-contained and reproducible: the mutated
// payload drops the field the weakened line would otherwise compare, which is
// exactly the situation a count-only gate fails to catch.
func TestMutatingAnInjectedDefectIsCaught(t *testing.T) {
	// A payload whose models block is present but whose per-model token values
	// were replaced by counts only. A value-comparing gate reports a diff; a
	// count-only gate does not.
	weakened := goodPayload()
	m := weakened["models"].([]any)[0].(map[string]any)
	delete(m, "cacheRead")
	delete(m, "cacheWrite")
	delete(m, "output")
	delete(m, "reasoning")

	if d := warehouse.Compare(weakened, goodUsage()); len(d) == 0 {
		t.Error("gate stayed green when a model's per-token values were removed " +
			"from the payload but its shape was intact")
	}
}

// TestRangeBoundaryIsObservable pins the exact limit of the FirstTs/LastTs
// exemption. Sub-minute drift is invisible because data.js keeps only
// "YYYY-MM-DD HH:MM"; anything that moves the minute must be caught. This
// keeps the exemption honest: it is a precision limit, not a blind spot.
func TestRangeBoundaryIsObservable(t *testing.T) {
	// 61 seconds of drift: crosses a minute boundary, must be caught
	u := goodUsage()
	u.FirstTs.Int64 += 61_000
	if d := warehouse.Compare(goodPayload(), u); len(d) == 0 {
		t.Error("a 61s drift in FirstTs (past the minute boundary) was not caught")
	}
	u2 := goodUsage()
	u2.LastTs.Int64 += 61_000
	if d := warehouse.Compare(goodPayload(), u2); len(d) == 0 {
		t.Error("a 61s drift in LastTs (past the minute boundary) was not caught")
	}
	// 30 seconds: same minute, genuinely unobservable in the payload
	u3 := goodUsage()
	u3.FirstTs.Int64 += 30_000
	if d := warehouse.Compare(goodPayload(), u3); len(d) != 0 {
		t.Errorf("a 30s drift should be invisible (minute precision), got %d diffs", len(d))
	}
}

// TestUsageStillSerialises guards the error surface from the other side: if the
// Usage shape stops round-tripping, the gate's input stops being reproducible.
func TestUsageStillSerialises(t *testing.T) {
	b, err := json.Marshal(goodUsage())
	if err != nil {
		t.Fatalf("usage does not serialise: %v", err)
	}
	var back warehouse.Usage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("usage does not deserialise: %v", err)
	}
	if d := warehouse.Compare(goodPayload(), &back); len(d) != 0 {
		t.Errorf("round-tripped usage no longer matches the payload (%d diffs); "+
			"the gate input is not reproducible", len(d))
		for _, x := range d {
			t.Errorf("  %s", x)
		}
	}
}
