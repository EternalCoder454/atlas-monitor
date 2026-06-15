// Package ai is a minimal streaming client for a local Ollama server.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client talks to an Ollama HTTP endpoint.
type Client struct {
	url   string
	model string
	hc    *http.Client
}

// New returns a client for the given endpoint and model.
func New(url, model string) *Client {
	return &Client{url: url, model: model, hc: &http.Client{Timeout: 3 * time.Minute}}
}

// SetConfig updates the endpoint and model (e.g. after a settings change).
func (c *Client) SetConfig(url, model string) {
	c.url, c.model = url, model
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.model }

// Stats holds the generation metrics Ollama reports in its final chunk.
type Stats struct {
	EvalCount          int           // tokens generated
	PromptEvalCount    int           // tokens in the prompt
	EvalDuration       time.Duration // time spent generating
	PromptEvalDuration time.Duration // time spent on the prompt
	TotalDuration      time.Duration // wall time for the whole request
}

// TokensPerSec is the generation rate (output tokens / generation time).
func (s Stats) TokensPerSec() float64 {
	if s.EvalDuration > 0 {
		return float64(s.EvalCount) / s.EvalDuration.Seconds()
	}
	return 0
}

type chatReq struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatChunk struct {
	Message            Message `json:"message"`
	Done               bool    `json:"done"`
	Error              string  `json:"error,omitempty"`
	EvalCount          int     `json:"eval_count"`
	PromptEvalCount    int     `json:"prompt_eval_count"`
	EvalDuration       int64   `json:"eval_duration"`        // ns
	PromptEvalDuration int64   `json:"prompt_eval_duration"` // ns
	TotalDuration      int64   `json:"total_duration"`       // ns
}

// Chat streams a completion for msgs. onToken is invoked (on this goroutine)
// with each content chunk as it arrives. It returns the full response text and
// the generation stats reported by Ollama.
func (c *Client) Chat(ctx context.Context, msgs []Message, onToken func(string)) (string, Stats, error) {
	var stats Stats
	body, _ := json.Marshal(chatReq{Model: c.model, Messages: msgs, Stream: true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", stats, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", stats, fmt.Errorf("cannot reach Ollama at %s: %w", c.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", stats, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var full strings.Builder
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ch chatChunk
		if err := json.Unmarshal(line, &ch); err != nil {
			continue
		}
		if ch.Error != "" {
			return full.String(), stats, fmt.Errorf("ollama: %s", ch.Error)
		}
		if ch.Message.Content != "" {
			full.WriteString(ch.Message.Content)
			if onToken != nil {
				onToken(ch.Message.Content)
			}
		}
		if ch.Done {
			stats = Stats{
				EvalCount:          ch.EvalCount,
				PromptEvalCount:    ch.PromptEvalCount,
				EvalDuration:       time.Duration(ch.EvalDuration),
				PromptEvalDuration: time.Duration(ch.PromptEvalDuration),
				TotalDuration:      time.Duration(ch.TotalDuration),
			}
			break
		}
	}
	if err := sc.Err(); err != nil {
		return full.String(), stats, err
	}
	return full.String(), stats, nil
}

// Available reports whether the Ollama API responds.
func (c *Client) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
