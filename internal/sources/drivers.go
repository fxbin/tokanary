package sources

import "strings"

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
	// Pending usage is bucketed by DAY, not pooled into one bucket. It used to
	// be a single bucket with no timestamp at all, and normalize only files a
	// record into byDay when Ts is a non-empty string - so every parked record
	// counted towards byModel and silently vanished from byDay. Measured on this
	// machine: codex per-model and per-day disagreed by 1.47B tokens (4.14%).
	var pending map[string]map[string]int64
	var pendingSession map[string]string
	var pendingOrder []string

	rec := func(model, sess string, ts any, tok map[string]int64) Record {
		return Record{
			Tool: m.ID, Model: model, Session: sess, Ts: ts,
			Input: tok["input"], CacheRead: tok["cacheRead"],
			CacheWrite: tok["cacheWrite"], Output: tok["output"],
			Reasoning: tok["reasoning"],
		}
	}
	flushPending := func(model string) {
		for _, day := range pendingOrder {
			var ts any
			if day != unknownDay {
				ts = day
			}
			out = append(out, rec(orUnknown(model), orQ(pendingSession[day]), ts, pending[day]))
		}
		pending, pendingSession, pendingOrder = nil, nil, nil
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
				if len(pendingOrder) > 0 {
					flushPending(curModel)
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
			// model not yet known: park the usage (18M tokens rely on this).
			// Bucketed by day so the parked tokens keep their date and still
			// reach byDay once the model shows up.
			day := dayOf(ts, o.File)
			p := pending[day]
			if p == nil {
				p = blankTokens()
				if pending == nil {
					pending = map[string]map[string]int64{}
					pendingSession = map[string]string{}
				}
				pending[day] = p
				pendingOrder = append(pendingOrder, day)
			}
			for _, k := range TokenKeys {
				p[k] += tok[k]
			}
			if pendingSession[day] == "" {
				pendingSession[day] = session
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

// unknownDay is the bucket key for usage whose date could not be determined.
// It keeps the tokens in byModel; they just cannot be placed on a day.
const unknownDay = ""

// dayOf resolves the calendar day a record belongs to. Every codex token_count
// line carries a timestamp, so the normal path is its first 10 characters. The
// rollout filename is the fallback: rollout-2026-03-04T19-26-36-<uuid>.jsonl
// opens with the session's own date.
func dayOf(ts any, file string) string {
	if s, ok := ts.(string); ok && len(s) >= 10 {
		return s[:10]
	}
	const pfx = "rollout-"
	if i := strings.Index(file, pfx); i >= 0 {
		rest := file[i+len(pfx):]
		if len(rest) >= 10 && rest[4] == '-' && rest[7] == '-' {
			return rest[:10]
		}
	}
	return unknownDay
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
