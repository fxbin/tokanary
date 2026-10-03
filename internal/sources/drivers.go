package sources

// codex implements the two quirks of the codex rollout format.
//
//  1. input_tokens is the TOTAL prompt including cache. Provable from a single
//     event: total_tokens(26407) == input_tokens(25614) + output_tokens(793),
//     and cache_write_input_tokens(25546) is part of input - so it must be
//     subtracted, otherwise those 25546 get billed twice, once as input and
//     once as cache-write.
//
//  2. token_count carries CUMULATIVE values. Summing last_token_usage
//     double-counts (measured on this machine: summing `last` gives 109.2M
//     while the session total is only 94.5M). The correct approach is to
//     difference total_token_usage; only when the cumulative value goes
//     backwards (resume/fork) do we fall back to last_token_usage and reset the
//     baseline.
//
// The model name lives on a separate turn_context line and is carried forward.
func codex(raw []*rawObj, m *Manifest, ctx *Context) []Record {
	f := m.Fields
	var out []Record

	var curFile string
	var prevTotal map[string]any
	hasPrev := false
	var curModel string
	var sid string
	var pending map[string]int64
	pendingSet := false
	var pendingSid string

	rec := func(model, sess string, ts any, tok map[string]int64) Record {
		return Record{
			Tool: m.ID, Model: model, Session: sess, Ts: ts,
			Input: tok["input"], CacheRead: tok["cacheRead"],
			CacheWrite: tok["cacheWrite"], Output: tok["output"],
			Reasoning: tok["reasoning"],
		}
	}
	flushPending := func(model string) {
		if pendingSet {
			out = append(out, rec(orUnknown(model), orQ(pendingSid), nil, pending))
			pending = nil
			pendingSet = false
		}
	}

	for _, o := range raw {
		if o.File != curFile {
			// each rollout file keeps its own state
			flushPending(curModel)
			curFile = o.File
			prevTotal = nil
			hasPrev = false
			curModel = ""
			sid = ""
		}

		obj := o.Obj
		t, _ := obj["type"].(string)
		var p map[string]any
		if pm, ok := obj["payload"].(map[string]any); ok {
			p = pm
		} else {
			p = map[string]any{}
		}

		switch t {
		case "session_meta":
			if v, ok := p["session_id"].(string); ok && v != "" {
				sid = v
			} else if v, ok := p["id"].(string); ok && v != "" {
				sid = v
			}
			continue
		case "turn_context":
			mv := firstOf(p, []string{"model", "model_id"})
			if mv != nil {
				curModel = str(mv)
				// flush usage parked before the model was known
				if pendingSet {
					s := sid
					if pendingSid != "" {
						s = pendingSid
					}
					if s == "" {
						s = o.File
					}
					out = append(out, rec(curModel, s, nil, pending))
					pending = nil
					pendingSet = false
				}
			}
			if sid == "" {
				if v, ok := p["session_id"].(string); ok {
					sid = v
				}
			}
			continue
		}

		pt, _ := p["type"].(string)
		if t != "event_msg" || pt != "token_count" {
			continue
		}
		info, _ := p["info"].(map[string]any)
		last, _ := info["last_token_usage"].(map[string]any)
		total, _ := info["total_token_usage"].(map[string]any)
		if total == nil {
			continue
		}

		var delta map[string]any
		if !hasPrev {
			delta = total
		} else {
			keys := []string{"input_tokens", "output_tokens", "cached_input_tokens",
				"cache_write_input_tokens", "total_tokens"}
			monotonic := true
			for _, k := range keys {
				if toInt(total[k]) < toInt(prevTotal[k]) {
					monotonic = false
					break
				}
			}
			if monotonic {
				delta = map[string]any{}
				seen := map[string]bool{}
				for k := range total {
					seen[k] = true
				}
				for k := range prevTotal {
					seen[k] = true
				}
				for k := range seen {
					delta[k] = int64(toInt(total[k]) - toInt(prevTotal[k]))
				}
			} else {
				delta = last
				ctx.Resets++
			}
		}
		prevTotal = total
		hasPrev = true
		if len(delta) == 0 {
			continue
		}

		rawIn := toInt(firstOf(delta, firstPath(f["input"])))
		cr := toInt(firstOf(delta, firstPath(f["cacheRead"])))
		cw := toInt(firstOf(delta, firstPath(f["cacheWrite"])))
		outTok := toInt(firstOf(delta, firstPath(f["output"])))
		rs := toInt(firstOf(delta, firstPath(f["reasoning"])))

		tokIn := rawIn - cr - cw
		if tokIn < 0 {
			tokIn = 0
		}
		if rs > outTok {
			rs = outTok
		}
		tok := map[string]int64{
			"input": tokIn, "cacheRead": cr, "cacheWrite": cw,
			"output": outTok, "reasoning": rs,
		}
		sum := int64(0)
		for _, k := range TokenKeys {
			sum += tok[k]
		}
		if sum == 0 {
			continue
		}

		ctx.Calls++
		ctx.RawInput += rawIn
		ts := obj["timestamp"]

		session := sid
		if session == "" {
			session = o.Dir
		}
		if session == "" {
			session = o.File
		}
		if curModel != "" {
			out = append(out, rec(curModel, session, ts, tok))
		} else {
			// model not yet known: park the usage (18M tokens rely on this)
			if !pendingSet {
				pending = blankTokens()
				pendingSet = true
			}
			for _, k := range TokenKeys {
				pending[k] += tok[k]
			}
			if pendingSid == "" {
				pendingSid = session
			}
		}
	}
	flushPending(curModel)
	return out
}

func firstPath(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return []string{paths[0]}
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// drivers is the registry a manifest's "driver" field resolves against.
var drivers = map[string]func([]*rawObj, *Manifest, *Context) []Record{
	"codex": codex,
}

// GetDriver exposes the registry for --validate parity.
func GetDriver(name string) (func([]*rawObj, *Manifest, *Context) []Record, bool) {
	d, ok := drivers[name]
	return d, ok
}
