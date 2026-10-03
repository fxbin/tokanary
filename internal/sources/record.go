// Package sources ports the cross-tool collection engine from
// the original python adapter engine.
//
// A tool's logs are cut apart declaratively: an adapter manifest says where the
// files are, how to filter them, which field holds which number, and how to
// dedup. Only genuine quirks need code - cumulative-value differencing and
// cross-line state, which is the codex driver.
//
// Every tool produces the same canonical record:
//
//	{Tool, Model, Session, Ts, Input, CacheRead, CacheWrite, Output, Reasoning}
//
// The accounting rules are project-wide and non-negotiable:
//
//   - Input counts NON-CACHE input only. Codex's input_tokens includes cache, so
//     it must be subtracted or cost is inflated several times over.
//   - Output ALREADY CONTAINS reasoning; never add them. OpenCode's reasoning is
//     additive, so outputAdd folds it in.
//   - Cost is always input + cacheRead + cacheWrite + output. Never use total.
package sources

import (
	"fmt"
	"strconv"
	"strings"
)

// Record is one canonical usage row.
type Record struct {
	Tool       string
	Model      string
	Session    string
	Ts         any
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	Reasoning  int64
}

// TokenKeys are the five canonical counters, in the order the payload uses.
var TokenKeys = [5]string{"input", "cacheRead", "cacheWrite", "output", "reasoning"}

// blankTokens is a zeroed counter set.
func blankTokens() map[string]int64 {
	m := make(map[string]int64, len(TokenKeys))
	for _, k := range TokenKeys {
		m[k] = 0
	}
	return m
}

// missing distinguishes "key absent" from "key present and nil"; python used a
// module-level sentinel for exactly this.
type missingType struct{}

var missing = missingType{}

// dig reads a dotted path with optional array indexes, e.g.
// "message.usage.output_tokens_details.thinking_tokens" or "usage.tokens[0]".
func dig(obj any, path string) any {
	if path == "" {
		return nil
	}
	cur := obj
	for _, part := range strings.Split(path, ".") {
		if cur == nil {
			return nil
		}
		if strings.HasSuffix(part, "]") && strings.Contains(part, "[") {
			name := part[:len(part)-1]
			idx := part[len(name)+1:]
			if name != "" {
				cur = mapGet(cur, name)
			}
			list, ok := cur.([]any)
			if !ok {
				return nil
			}
			n, err := strconv.Atoi(idx)
			if err != nil || n < 0 || n >= len(list) {
				return nil
			}
			cur = list[n]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := m[part]
		if !ok {
			return nil
		}
		cur = v
	}
	return cur
}

func mapGet(obj any, key string) any {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

// firstOf returns the first path that yields a non-empty value. Used for the
// model field, which has several candidate locations per tool.
func firstOf(obj any, paths []string) any {
	for _, p := range paths {
		v := dig(obj, p)
		if !isEmptyValue(v) {
			return v
		}
	}
	return nil
}

// isEmptyValue mirrors python's `v not in (None, "", _MISSING)`.
func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case missingType:
		return true
	}
	return false
}

// toInt mirrors engine.to_int: int(v or 0) with any failure collapsing to 0.
func toInt(v any) int64 {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			f, err2 := strconv.ParseFloat(s, 64)
			if err2 != nil {
				return 0
			}
			return int64(f)
		}
		return n
	}
	return 0
}

// equalValue compares like python's `!=` for the where clause: numbers compare
// numerically, everything else structurally.
func equalValue(a, b any) bool {
	af, aok := numericValue(a)
	bf, bok := numericValue(b)
	if aok && bok {
		return af == bf
	}
	return deepEqual(a, b)
}

func numericValue(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	}
	return 0, false
}

func deepEqual(a, b any) bool {
	switch at := a.(type) {
	case nil:
		return b == nil
	case string:
		bs, ok := b.(string)
		return ok && at == bs
	case bool:
		bb, ok := b.(bool)
		return ok && at == bb
	case map[string]any:
		bm, ok := b.(map[string]any)
		if !ok || len(at) != len(bm) {
			return false
		}
		for k, av := range at {
			bv, ok := bm[k]
			if !ok || !deepEqual(av, bv) {
				return false
			}
		}
		return true
	case []any:
		bl, ok := b.([]any)
		if !ok || len(at) != len(bl) {
			return false
		}
		for i := range at {
			if !deepEqual(at[i], bl[i]) {
				return false
			}
		}
		return true
	}
	return false
}

var _ = fmt.Sprint
