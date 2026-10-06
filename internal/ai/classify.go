// Package ai optionally classifies session titles with an LLM. Disabled by
// default: callers must pass enable=true, and env must configure an endpoint.
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config from environment. Empty endpoint means "AI off".
type Config struct {
	Endpoint string // e.g. https://api.openai.com/v1/chat/completions
	APIKey   string
	Model    string
}

func LoadConfig() Config {
	return Config{
		Endpoint: os.Getenv("TOKANARY_AI_ENDPOINT"),
		APIKey:   os.Getenv("TOKANARY_AI_KEY"),
		Model:    os.Getenv("TOKANARY_AI_MODEL"),
	}
}

func (c Config) Ready() bool {
	return c.Endpoint != "" && c.Model != ""
}

// Category is one labeled bucket.
type Category struct {
	Label  string `json:"label"`
	Count  int    `json:"count"`
	Tokens int64  `json:"tokens"`
}

// ClassifyTitles batch-tags titles into a fixed taxonomy. Returns nil when
// disabled/unconfigured so the UI can hide the panel.
func ClassifyTitles(cfg Config, enable bool, items []TitleTokens) ([]Category, error) {
	if !enable || !cfg.Ready() || len(items) == 0 {
		return nil, nil
	}
	// keep the prompt tiny to control cost
	const max = 40
	if len(items) > max {
		items = items[:max]
	}
	lines := make([]string, 0, len(items))
	for i, it := range items {
		t := strings.TrimSpace(it.Title)
		if t == "" {
			t = it.Tool
		}
		if len(t) > 80 {
			t = t[:80]
		}
		lines = append(lines, fmt.Sprintf("%d\t%s", i, t))
	}
	prompt := "Classify each numbered line into exactly one of: coding, debug, research, write, plan, review, other.\n" +
		"Reply ONLY with JSON array [{\"i\":0,\"cat\":\"coding\"},...].\n" +
		strings.Join(lines, "\n")

	body := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", cfg.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("no choices")
	}
	text := out.Choices[0].Message.Content
	// strip code fences if any
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	var tags []struct {
		I   int    `json:"i"`
		Cat string `json:"cat"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &tags); err != nil {
		return nil, fmt.Errorf("bad classify JSON: %w", err)
	}
	labels := map[string]string{
		"coding": "编码", "debug": "调试", "research": "研究",
		"write": "写作", "plan": "方案", "review": "评审", "other": "其他",
	}
	agg := map[string]*Category{}
	for _, t := range tags {
		key := t.Cat
		if key == "" {
			key = "other"
		}
		c := agg[key]
		if c == nil {
			c = &Category{Label: labels[key]}
			if c.Label == "" {
				c.Label = key
			}
			agg[key] = c
		}
		c.Count++
		if t.I >= 0 && t.I < len(items) {
			c.Tokens += items[t.I].Tokens
		}
	}
	var cats []Category
	for _, c := range agg {
		cats = append(cats, *c)
	}
	// stable order
	order := []string{"coding", "debug", "research", "write", "plan", "review", "other"}
	by := map[string]Category{}
	for _, c := range cats {
		for _, k := range order {
			if c.Label == labels[k] {
				by[k] = c
			}
		}
	}
	outCats := make([]Category, 0, len(cats))
	for _, k := range order {
		if c, ok := by[k]; ok {
			outCats = append(outCats, c)
		}
	}
	return outCats, nil
}

// TitleTokens is one row to classify.
type TitleTokens struct {
	Title  string
	Tool   string
	Tokens int64
}
