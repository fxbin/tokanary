package warehouse

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// Diff is one parity mismatch between the Go export and the snapshot payload.
type Diff struct {
	Path string
	Go   any
	JS   any
}

func (d Diff) String() string {
	return fmt.Sprintf("%s: go=%v js=%v", d.Path, d.Go, d.JS)
}

// Compare walks the reference snapshot payload and the Go Usage side by side.
// Every leaf integer must be equal, or the gate fails.
//
// Scope note: data.js is produced by build_data.py, which reshapes export_usage
// before writing. Two transformations are deliberately out of scope here:
//
//   - models is a dict in export_usage but a sorted list in data.js, and each
//     entry gains priced plus a rawUsage list. Compare() rebuilds
//     the dict shape from the data.js list so the numbers still line up.
//   - sessions is capped at TOP_SESSIONS; sessionsAll carries the full set, so
//     sessions is compared as a prefix of sessionsAll.
func Compare(payload map[string]any, u *Usage) []Diff {
	cmp := &comparer{payload: payload, u: u}
	cmp.totals()
	cmp.models()
	cmp.days()
	cmp.dayModel()
	cmp.hours()
	cmp.projects()
	cmp.sessions()
	cmp.roles()
	cmp.tools()
	cmp.scalars()
	return cmp.d
}

type comparer struct {
	payload map[string]any
	u       *Usage
	d       []Diff
}

func (c *comparer) add(path string, got, want any) {
	c.d = append(c.d, Diff{Path: path, Go: got, JS: want})
}

// numEq compares numerically, tolerating the float/int and json.Number
// mismatches that come from parsing arbitrary JSON into any.
func numEq(a, b any) bool {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if aok && bok {
		if af == bf {
			return true
		}
		return math.Abs(af-bf) < 1e-9
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// asFloat unwraps the nullable wrappers and accepts every numeric encoding
// that json.Unmarshal into any produces. Without the NullInt64 case a valid
// timestamp reads as a struct and every scalar comparison fails.
func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case sql.NullInt64:
		if !t.Valid {
			return 0, false
		}
		return float64(t.Int64), true
	}
	return 0, false
}

// eq is the leaf comparison: numbers compare numerically, everything else by
// exact string. nil matches nil.
func (c *comparer) eq(path string, got, want any) {
	if got == nil && want == nil {
		return
	}
	if numEq(got, want) {
		return
	}
	if got == nil || want == nil {
		c.add(path, got, want)
		return
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		c.add(path, got, want)
	}
}

func (c *comparer) get(key string) any {
	v, ok := c.payload[key]
	if !ok {
		c.add("<missing key "+key+">", nil, nil)
		return nil
	}
	return v
}

func (c *comparer) totals() {
	t, ok := c.get("totals").(map[string]any)
	if !ok {
		c.add("totals", "(not an object)", c.get("totals"))
		return
	}
	c.eq("totals.turns", c.u.Totals.Turns, t["turns"])
	c.eq("totals.turnsWithUsage", c.u.Totals.TurnsWithUsage, t["turnsWithUsage"])
	c.eq("totals.cacheRead", c.u.Totals.CacheRead, t["cacheRead"])
	c.eq("totals.cacheWrite", c.u.Totals.CacheWrite, t["cacheWrite"])
	c.eq("totals.input", c.u.Totals.Input, t["input"])
	c.eq("totals.output", c.u.Totals.Output, t["output"])
	c.eq("totals.reasoning", c.u.Totals.Reasoning, t["reasoning"])
	c.eq("totals.total", c.u.Totals.Total, t["total"])
	c.eq("totals.sessions", c.u.Totals.Sessions, t["sessions"])
	c.eq("totals.messages", c.u.Totals.Messages, t["messages"])
	c.eq("totals.projects", c.u.Totals.Projects, t["projects"])
	c.eq("totals.cacheHitPct", c.u.Totals.CacheHitPct, t["cacheHitPct"])
}

// models rebuilds the export_usage dict from data.js's sorted list. The list
// carries extra priced fields we ignore; the numbers are what must match.
func (c *comparer) models() {
	list, _ := c.get("models").([]any)
	byKey := map[string]map[string]any{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, _ := m["key"].(string)
		byKey[key] = m
	}
	if len(byKey) != len(c.u.Models) {
		c.add("models.count", len(c.u.Models), len(byKey))
	}
	keys := make([]string, 0, len(c.u.Models))
	for k := range c.u.Models {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		gm := c.u.Models[k]
		jm, ok := byKey[k]
		if !ok {
			c.add("models."+k, "(present)", "(absent)")
			continue
		}
		c.eq("models."+k+".turns", gm.Turns, jm["turns"])
		c.eq("models."+k+".missingUsage", gm.MissingUage, jm["missingUsage"])
		c.eq("models."+k+".cacheRead", gm.CacheRead, jm["cacheRead"])
		c.eq("models."+k+".cacheWrite", gm.CacheWrite, jm["cacheWrite"])
		c.eq("models."+k+".input", gm.Input, jm["input"])
		c.eq("models."+k+".output", gm.Output, jm["output"])
		c.eq("models."+k+".reasoning", gm.Reasoning, jm["reasoning"])
		c.eq("models."+k+".total", gm.Total, jm["total"])
		c.eq("models."+k+".firstTs", gm.FirstTs, jm["firstTs"])
		c.eq("models."+k+".lastTs", gm.LastTs, jm["lastTs"])
		// rawIds: python wrote a sorted set
		jsIDs := strSet(jm["rawIds"])
		goIDs := strSet(anySlice(gm.RawIDs))
		for id := range jsIDs {
			if !goIDs[id] {
				c.add("models."+k+".rawIds", gm.RawIDs, jm["rawIds"])
				break
			}
		}
		// rawUsage: data.js has a sorted list, export_usage a dict
		jsRaw, _ := jm["rawUsage"].([]any)
		if len(jsRaw) != len(gm.RawUsage) {
			c.add("models."+k+".rawUsage.count", len(gm.RawUsage), len(jsRaw))
		}
		for _, item := range jsRaw {
			rm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			rid, _ := rm["id"].(string)
			gr, ok := gm.RawUsage[rid]
			if !ok {
				c.add("models."+k+".rawUsage."+rid, "(present)", "(absent)")
				continue
			}
			c.eq("models."+k+".rawUsage."+rid+".turns", gr.Turns, rm["turns"])
			c.eq("models."+k+".rawUsage."+rid+".cacheRead", gr.CacheRead, rm["cacheRead"])
			c.eq("models."+k+".rawUsage."+rid+".cacheWrite", gr.CacheWrite, rm["cacheWrite"])
			c.eq("models."+k+".rawUsage."+rid+".input", gr.Input, rm["input"])
			c.eq("models."+k+".rawUsage."+rid+".output", gr.Output, rm["output"])
			c.eq("models."+k+".rawUsage."+rid+".total", gr.Total, rm["total"])
			// reasoning is deliberately absent: build_data.py:334 never emits it
			// into rawUsage, so comparing it would compare against nil.
		}
		// statuses
		jsSt, _ := jm["statuses"].(map[string]any)
		if len(jsSt) != len(gm.Statuses) {
			c.add("models."+k+".statuses.count", len(gm.Statuses), len(jsSt))
		}
		for name, n := range jsSt {
			gn, ok := gm.Statuses[name]
			if !ok {
				c.add("models."+k+".statuses."+name, "(present)", "(absent)")
				continue
			}
			c.eq("models."+k+".statuses."+name, gn, n)
		}
	}
}

func (c *comparer) days() {
	list, _ := c.get("days").([]any)
	if len(list) != len(c.u.Days) {
		c.add("days.count", len(c.u.Days), len(list))
	}
	n := min(len(list), len(c.u.Days))
	for i := 0; i < n; i++ {
		jm, _ := list[i].(map[string]any)
		gm := c.u.Days[i]
		p := fmt.Sprintf("days[%d]", i)
		c.eq(p+".d", gm.D, jm["d"])
		c.eq(p+".turns", gm.Turns, jm["turns"])
		c.eq(p+".cacheRead", gm.CacheRead, jm["cacheRead"])
		c.eq(p+".cacheWrite", gm.CacheWrite, jm["cacheWrite"])
		c.eq(p+".input", gm.Input, jm["input"])
		c.eq(p+".output", gm.Output, jm["output"])
		c.eq(p+".reasoning", gm.Reasoning, jm["reasoning"])
		c.eq(p+".total", gm.Total, jm["total"])
	}
}

func (c *comparer) dayModel() {
	list, _ := c.get("dayModel").([]any)
	if len(list) != len(c.u.DayModel) {
		c.add("dayModel.count", len(c.u.DayModel), len(list))
	}
	n := min(len(list), len(c.u.DayModel))
	for i := 0; i < n; i++ {
		jm, _ := list[i].(map[string]any)
		gm := c.u.DayModel[i]
		p := fmt.Sprintf("dayModel[%d]", i)
		c.eq(p+".d", gm.D, jm["d"])
		c.eq(p+".key", gm.Key, jm["key"])
		c.eq(p+".turns", gm.Turns, jm["turns"])
		c.eq(p+".total", gm.Total, jm["total"])
		c.eq(p+".output", gm.Output, jm["output"])
	}
}

func (c *comparer) hours() {
	list, _ := c.get("hours").([]any)
	if len(list) != len(c.u.Hours) {
		c.add("hours.count", len(c.u.Hours), len(list))
	}
	n := min(len(list), len(c.u.Hours))
	for i := 0; i < n; i++ {
		jm, _ := list[i].(map[string]any)
		gm := c.u.Hours[i]
		p := fmt.Sprintf("hours[%d]", i)
		c.eq(p+".h", gm.H, jm["h"])
		c.eq(p+".turns", gm.Turns, jm["turns"])
		c.eq(p+".cacheRead", gm.CacheRead, jm["cacheRead"])
		c.eq(p+".cacheWrite", gm.CacheWrite, jm["cacheWrite"])
		c.eq(p+".input", gm.Input, jm["input"])
		c.eq(p+".output", gm.Output, jm["output"])
		c.eq(p+".total", gm.Total, jm["total"])
	}
}

func (c *comparer) projects() {
	list, _ := c.get("projects").([]any)
	if len(list) != len(c.u.Projects) {
		c.add("projects.count", len(c.u.Projects), len(list))
	}
	n := min(len(list), len(c.u.Projects))
	for i := 0; i < n; i++ {
		jm, _ := list[i].(map[string]any)
		gm := c.u.Projects[i]
		p := fmt.Sprintf("projects[%d]", i)
		c.eq(p+".name", gm.Name, jm["name"])
		c.eq(p+".sessions", gm.Sessions, jm["sessions"])
		c.eq(p+".turns", gm.Turns, jm["turns"])
		c.eq(p+".cacheRead", gm.CacheRead, jm["cacheRead"])
		c.eq(p+".cacheWrite", gm.CacheWrite, jm["cacheWrite"])
		c.eq(p+".input", gm.Input, jm["input"])
		c.eq(p+".output", gm.Output, jm["output"])
		c.eq(p+".reasoning", gm.Reasoning, jm["reasoning"])
		c.eq(p+".total", gm.Total, jm["total"])
	}
}

func (c *comparer) sessions() {
	all, _ := c.get("sessionsAll").([]any)
	if len(all) != len(c.u.SessionsAll) {
		c.add("sessionsAll.count", len(c.u.SessionsAll), len(all))
	}
	n := min(len(all), len(c.u.SessionsAll))
	for i := 0; i < n; i++ {
		jm, _ := all[i].(map[string]any)
		gm := c.u.SessionsAll[i]
		p := fmt.Sprintf("sessionsAll[%d]", i)
		c.eq(p+".id", gm.ID, jm["id"])
		c.eq(p+".title", gm.Title, jm["title"])
		c.eq(p+".project", gm.Project, jm["project"])
		c.eq(p+".model", gm.Model, jm["model"])
		c.eq(p+".createdAt", gm.CreatedAt, jm["createdAt"])
		c.eq(p+".updatedAt", gm.UpdatedAt, jm["updatedAt"])
		c.eq(p+".turns", gm.Turns, jm["turns"])
		c.eq(p+".messages", gm.Messages, jm["messages"])
		c.eq(p+".cacheRead", gm.CacheRead, jm["cacheRead"])
		c.eq(p+".cacheWrite", gm.CacheWrite, jm["cacheWrite"])
		c.eq(p+".input", gm.Input, jm["input"])
		c.eq(p+".output", gm.Output, jm["output"])
		c.eq(p+".total", gm.Total, jm["total"])
	}
	// sessions is the capped prefix
	capped, _ := c.get("sessions").([]any)
	wantLen := TopSessions
	if len(all) < wantLen {
		wantLen = len(all)
	}
	if len(capped) != wantLen {
		c.add("sessions.len", len(c.u.Sessions), len(capped))
	}
	// sessions is the capped prefix of sessionsAll. Every field is compared
	// here, not just id and total: a field that drifts only in the capped slice
	// is still a field the dashboard renders.
	m := min(len(capped), wantLen, len(c.u.Sessions))
	for i := 0; i < m; i++ {
		jm, _ := capped[i].(map[string]any)
		gm := c.u.Sessions[i]
		p := fmt.Sprintf("sessions[%d]", i)
		c.eq(p+".id", gm.ID, jm["id"])
		c.eq(p+".title", gm.Title, jm["title"])
		c.eq(p+".project", gm.Project, jm["project"])
		c.eq(p+".model", gm.Model, jm["model"])
		c.eq(p+".createdAt", gm.CreatedAt, jm["createdAt"])
		c.eq(p+".updatedAt", gm.UpdatedAt, jm["updatedAt"])
		c.eq(p+".turns", gm.Turns, jm["turns"])
		c.eq(p+".messages", gm.Messages, jm["messages"])
		c.eq(p+".cacheRead", gm.CacheRead, jm["cacheRead"])
		c.eq(p+".cacheWrite", gm.CacheWrite, jm["cacheWrite"])
		c.eq(p+".input", gm.Input, jm["input"])
		c.eq(p+".output", gm.Output, jm["output"])
		c.eq(p+".total", gm.Total, jm["total"])
	}
	// anything past the compared prefix is still a rendering surface
	for i := m; i < len(capped) && i < len(c.u.Sessions); i++ {
		c.add(fmt.Sprintf("sessions[%d]", i), "present", "absent")
	}
	c.eq("sessionCount", c.u.SessionCount, c.get("sessionCount"))
}

func (c *comparer) roles() {
	jr, _ := c.get("roles").(map[string]any)
	if len(jr) != len(c.u.Roles) {
		c.add("roles.count", len(c.u.Roles), len(jr))
	}
	for role, js := range jr {
		gr, ok := c.u.Roles[role]
		if !ok {
			c.add("roles."+role, "(present)", "(absent)")
			continue
		}
		m, _ := js.(map[string]any)
		c.eq("roles."+role+".n", gr.N, m["n"])
		c.eq("roles."+role+".errors", gr.Errors, m["errors"])
	}
}

// tools matches by name, not by position. The duckdb original ordered by
// count(*) desc only, so rows with equal counts come out in an arbitrary order
// and the two engines legitimately disagree on position. The numbers still
// have to match exactly.
func (c *comparer) tools() {
	list, _ := c.get("tools").([]any)
	byName := map[string]map[string]any{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		byName[name] = m
	}
	if len(byName) != len(c.u.Tools) {
		c.add("tools.count", len(c.u.Tools), len(byName))
	}
	names := make([]string, 0, len(c.u.Tools))
	for _, t := range c.u.Tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	for _, name := range names {
		gm := ToolRow{}
		for _, t := range c.u.Tools {
			if t.Name == name {
				gm = t
				break
			}
		}
		jm, ok := byName[name]
		if !ok {
			c.add("tools."+name, "(present)", "(absent)")
			continue
		}
		c.eq("tools."+name+".n", gm.N, jm["n"])
		c.eq("tools."+name+".errors", gm.Errors, jm["errors"])
	}
}

// scalars reads firstTs/lastTs/dbVersion out of meta. build_data.py lifts them
// into the meta block; export_usage returns them at the top of the usage
// payload, so the data.js shape is one level deeper.
func (c *comparer) scalars() {
	meta, _ := c.get("meta").(map[string]any)
	if meta == nil {
		c.add("meta", "(missing)", c.get("meta"))
		return
	}
	c.eq("meta.dbVersion", c.u.DBVersion, meta["dbVersion"])
	// data.js stores these formatted as local time strings, not raw ms. A
	// missing key used to skip the comparison entirely, which is exactly the
	// "gate stays green" failure mode; an absent key is now a reported diff.
	jsStart, hasStart := meta["rangeStart"]
	jsEnd, hasEnd := meta["rangeEnd"]
	switch {
	case !hasStart && c.u.FirstTs.Valid:
		c.add("meta.rangeStart", formatMS(c.u.FirstTs.Int64), nil)
	case hasStart && c.u.FirstTs.Valid:
		c.eq("meta.rangeStart", formatMS(c.u.FirstTs.Int64), jsStart)
	}
	switch {
	case !hasEnd && c.u.LastTs.Valid:
		c.add("meta.rangeEnd", formatMS(c.u.LastTs.Int64), nil)
	case hasEnd && c.u.LastTs.Valid:
		c.eq("meta.rangeEnd", formatMS(c.u.LastTs.Int64), jsEnd)
	}
}

func strSet(v any) map[string]bool {
	out := map[string]bool{}
	for _, s := range anySlice(v) {
		out[fmt.Sprint(s)] = true
	}
	return out
}

// formatMS renders a millisecond timestamp the way build_data.py's meta.rangeStart
// does: local time, "YYYY-MM-DD HH:MM".
func formatMS(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04")
}

func anySlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, s := range t {
			out = append(out, fmt.Sprint(s))
		}
		return out
	}
	return nil
}
