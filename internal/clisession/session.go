// Package clisession parses the official pi CLI's session JSONL (schema version 3)
// and normalizes it into the row-level shape shared with the sqlite source.
//
// Parity contract: output must equal the official pi CLI JSONL format field-for-field, verified
// against testdata/clisession/golden.json. The python implementation is the
// temporary oracle and is deleted in the Go migration's final slice.
package clisession

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// Tool marks rows produced by this ingest (sqlite source uses "pi").
	Tool = "pi-cli"
	// SchemaVersion is the only session schema this parser accepts.
	SchemaVersion = 3
	// RevisionsSuffix marks message-revision copies whose usage would double count.
	RevisionsSuffix = ".revisions.jsonl"
)

var (
	errorStops = map[string]bool{"error": true, "aborted": true, "abort": true, "failed": true, "refusal": true}
	prefixes   = []string{
		"azure-", "openai/", "kimi/", "moonshotai/", "moonshot-", "x-ai/", "z-ai/",
		"google/", "anthropic/", "deepseek/", "qwen/", "zai-org/", "minimax/",
		"xiaomi/", "stepfun/", "subconscious/", "bothub/", "modelis/", "greenpt/",
	}
	reTrailingFlag = regexp.MustCompile(`--?(ga|preview|exp|latest|beta)[-_]?\d*$`)
	reTrailingInt  = regexp.MustCompile(`--?int$`)
	reTrailingDate = regexp.MustCompile(`-\d{6}$`)
	reTrailingExp  = regexp.MustCompile(`-(preview|exp)$`)
)

// CanonicalModel mirrors pi_common.canonical_model so model keys stay comparable
// with the sqlite source.
func CanonicalModel(modelID string) string {
	if modelID == "" {
		return "(unknown)"
	}
	s := strings.ToLower(strings.TrimSpace(modelID))
	changed := true
	for changed {
		changed = false
		for _, p := range prefixes {
			if strings.HasPrefix(s, p) {
				s, changed = s[len(p):], true
			}
		}
	}
	s = reTrailingInt.ReplaceAllString(s, "")
	s = reTrailingFlag.ReplaceAllString(s, "")
	s = reTrailingDate.ReplaceAllString(s, "")
	s = reTrailingExp.ReplaceAllString(s, "")
	if s == "" {
		return strings.ToLower(strings.TrimSpace(modelID))
	}
	return s
}

// ProjectName derives the project label from a session cwd, matching the
// desktop shell's projects.name convention.
func ProjectName(cwd string) string {
	if cwd == "" {
		return "(无项目)"
	}
	norm := strings.ReplaceAll(cwd, "\\", "/")
	norm = strings.TrimRight(norm, "/")
	if i := strings.LastIndex(norm, "/"); i >= 0 {
		norm = norm[i+1:]
	}
	if norm == "" {
		return "(无项目)"
	}
	return norm
}

func LocalDay(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

// LocalHour keeps the hours contract "YYYY-MM-DD HH" (no :00).
func LocalHour(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15")
}

func num(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// toMS accepts epoch milliseconds (number or numeric string) and ISO8601 (UTC).
func toMS(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ms
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

type Usage struct {
	Input       *int64 `json:"input"`
	Output      *int64 `json:"output"`
	CacheRead   *int64 `json:"cacheRead"`
	CacheWrite  *int64 `json:"cacheWrite"`
	Reasoning   *int64 `json:"reasoning"`
	TotalTokens *int64 `json:"totalTokens"`
}

type Block struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Message struct {
	Role          string          `json:"role"`
	Content       json.RawMessage `json:"content"`
	Model         string          `json:"model"`
	Provider      string          `json:"provider"`
	StopReason    string          `json:"stopReason"`
	RawStopReason string          `json:"rawStopReason"`
	Timestamp     json.RawMessage `json:"timestamp"`
	ToolCallID    string          `json:"toolCallId"`
	ToolName      string          `json:"toolName"`
	IsError       bool            `json:"isError"`
	Usage         *Usage          `json:"usage"`
}

// blocks decodes content only when it is an array: the real v3 system message
// carries a plain string, and assistant content mixes text/thinking/toolCall.
func (m *Message) blocks() []Block {
	if len(m.Content) == 0 || m.Content[0] != '[' {
		return nil
	}
	var out []Block
	if err := json.Unmarshal(m.Content, &out); err != nil {
		return nil
	}
	return out
}

type Record struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	Timestamp     json.RawMessage `json:"timestamp"`
	Version       *int            `json:"version"`
	CWD           string          `json:"cwd"`
	Provider      string          `json:"provider"`
	ModelID       string          `json:"modelId"`
	ProviderID    string          `json:"providerId"`
	ThinkingLevel string          `json:"thinkingLevel"`
	Usage         *Usage          `json:"usage"`
	Message       *Message        `json:"message"`
}

// Turn is one user-initiated turn; tool-loop assistant calls are summed into it.
type Turn struct {
	ModelRaw   string
	Provider   string
	Status     string
	Day        string
	Hour       string
	StartedAt  int64
	EndedAt    int64
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Reasoning  int64
	Total      int64
	HasUsage   bool
}

func (t *Turn) addUsage(u *Usage) {
	if u == nil {
		return
	}
	t.Input += num(u.Input)
	t.Output += num(u.Output)
	t.CacheRead += num(u.CacheRead)
	t.CacheWrite += num(u.CacheWrite)
	t.Reasoning += num(u.Reasoning)
	t.HasUsage = true
}

func (t *Turn) seal() {
	t.Total = t.Input + t.Output + t.CacheRead + t.CacheWrite
	if t.Status == "" {
		t.Status = "completed"
	}
}

type counter struct{ N, Errors int }

// Session is one normalized CLI session.
type Session struct {
	ID        string
	CWD       string
	Project   string
	ModelRaw  string
	Mode      string
	Thinking  string
	CreatedAt int64
	UpdatedAt int64
	Turns     []*Turn
	Messages  int
	Roles     map[string]*counter
	Tools     map[string]*counter
	Version   int
}

func (s *Session) role(name string) *counter {
	if s.Roles == nil {
		s.Roles = map[string]*counter{}
	}
	c, ok := s.Roles[name]
	if !ok {
		c = &counter{}
		s.Roles[name] = c
	}
	return c
}

func (s *Session) tool(name string) *counter {
	if s.Tools == nil {
		s.Tools = map[string]*counter{}
	}
	c, ok := s.Tools[name]
	if !ok {
		c = &counter{}
		s.Tools[name] = c
	}
	return c
}

// Parse merges every file of one session (rotation), drops revisions and
// duplicate record ids, then walks records in (timestamp, id) order.
func Parse(records []Record) *Session {
	s := &Session{Mode: "agent", Roles: map[string]*counter{}, Tools: map[string]*counter{}}

	seen := map[string]bool{}
	ordered := make([]Record, 0, len(records))
	for _, r := range records {
		if r.ID != "" {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
		}
		if r.Type == "session" {
			if s.ID == "" {
				s.ID = r.ID
			}
			if s.CWD == "" {
				s.CWD = r.CWD
			}
			if r.Version != nil && s.Version == 0 {
				s.Version = *r.Version
			}
		}
		ordered = append(ordered, r)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		ti, tj := toMS(ordered[i].Timestamp), toMS(ordered[j].Timestamp)
		if ti != tj {
			return ti < tj
		}
		return ordered[i].ID < ordered[j].ID
	})

	var curModel, curProvider, curThinking string
	var cur *Turn
	toolCalls := map[string]string{}
	toolErrors := map[string]bool{}

	for _, r := range ordered {
		rts := toMS(r.Timestamp)
		switch r.Type {
		case "model_change":
			if r.ModelID != "" {
				curModel = r.ModelID
			}
			if r.Provider != "" {
				curProvider = r.Provider
			}
			continue
		case "thinking_level_change":
			if r.ThinkingLevel != "" {
				curThinking = r.ThinkingLevel
			}
			continue
		case "session":
			if s.CreatedAt == 0 {
				s.CreatedAt = rts
			}
			continue
		case "compaction":
			t := &Turn{StartedAt: rts, EndedAt: rts, ModelRaw: firstNonEmpty(r.ModelID, curModel),
				Provider: firstNonEmpty(r.ProviderID, curProvider)}
			t.addUsage(r.Usage)
			t.Status = "completed"
			t.seal()
			s.Turns = append(s.Turns, t)
			s.touch(rts)
			continue
		case "message":
		default:
			continue
		}

		m := r.Message
		if m == nil {
			continue
		}
		s.Messages++
		mts := toMS(m.Timestamp)
		if mts == 0 {
			mts = rts
		}
		if s.CreatedAt == 0 {
			s.CreatedAt = mts
		}
		s.touch(mts)

		switch m.Role {
		case "user":
			cur = &Turn{StartedAt: mts, EndedAt: mts}
			s.Turns = append(s.Turns, cur)
			s.role("user").N++
		case "toolResult":
			c := s.role("toolResult")
			c.N++
			if m.IsError {
				c.Errors++
				toolErrors[m.ToolCallID] = true
			}
			if m.ToolName != "" && m.ToolCallID != "" {
				if _, ok := toolCalls[m.ToolCallID]; !ok {
					toolCalls[m.ToolCallID] = m.ToolName
				}
			}
		case "assistant":
			c := s.role("assistant")
			c.N++
			model := firstNonEmpty(m.Model, curModel)
			stop := firstNonEmpty(m.StopReason, m.RawStopReason)
			if errorStops[stop] {
				c.Errors++
			}
			for _, b := range m.blocks() {
				if b.Type == "toolCall" && b.Name != "" {
					toolCalls[b.ID] = b.Name
					s.tool(b.Name).N++
				}
			}
			if cur == nil {
				cur = &Turn{StartedAt: mts, EndedAt: mts}
				s.Turns = append(s.Turns, cur)
			}
			if model != "" {
				cur.ModelRaw = model
			}
			if m.Provider != "" {
				cur.Provider = m.Provider
			}
			if mts != 0 {
				cur.EndedAt = mts
			}
			switch {
			case stop != "" && stop != "toolUse" && stop != "tool_use":
				cur.Status = statusFor(stop)
			case cur.Status == "":
				cur.Status = "completed"
			}
			cur.addUsage(m.Usage)
		default:
			s.role(m.Role).N++
		}
	}

	for callID, name := range toolCalls {
		if toolErrors[callID] {
			if c, ok := s.Tools[name]; ok {
				c.Errors++
			}
		}
	}
	for _, t := range s.Turns {
		t.seal()
	}
	s.ModelRaw = curModel
	s.Thinking = curThinking
	if s.UpdatedAt == 0 {
		s.UpdatedAt = s.CreatedAt
	}
	s.Project = ProjectName(s.CWD)
	return s
}

func (s *Session) touch(ms int64) {
	if ms > s.UpdatedAt {
		s.UpdatedAt = ms
	}
}

func statusFor(stop string) string {
	switch stop {
	case "error", "failed", "refusal":
		return "error"
	case "aborted", "abort":
		return "aborted"
	default:
		return "completed"
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
