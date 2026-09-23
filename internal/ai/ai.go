// Package ai is a minimal streaming client for a local Ollama server.
package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client talks to an Ollama HTTP endpoint. Requests go over a small HTTP/1.1
// implementation (see http.go) rather than net/http.
type Client struct {
	url   string
	model string
}

// chatTimeout bounds a whole generation; probeTimeout bounds a liveness check.
const (
	chatTimeout  = 3 * time.Minute
	probeTimeout = 10 * time.Second
)

// New returns a client for the given endpoint and model.
func New(url, model string) *Client {
	return &Client{url: url, model: model}
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
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

// chatTemperature is kept low: Atlas extracts and reports live figures, so
// determinism and grounding matter far more than creativity.
const chatTemperature = 0.3

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
	body, _ := json.Marshal(chatReq{
		Model:    c.model,
		Messages: msgs,
		Stream:   true,
		Options:  map[string]any{"temperature": chatTemperature},
	})
	ctx, cancel := timeoutContext(ctx, chatTimeout)
	defer cancel()

	resp, err := c.do(ctx, "POST", "/api/chat", body)
	if err != nil {
		return "", stats, err
	}
	defer resp.Close()
	if resp.status != 200 {
		return "", stats, statusError(resp)
	}

	sc := bufio.NewScanner(resp.body)
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
	ctx, cancel := timeoutContext(ctx, probeTimeout)
	defer cancel()
	resp, err := c.do(ctx, "GET", "/api/tags", nil)
	if err != nil {
		return false
	}
	defer resp.Close()
	return resp.status == 200
}

// statusError turns a non-200 response into an error carrying the server's
// explanation, which Ollama returns as a short JSON or text body.
func statusError(resp *response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.body, 2048))
	return fmt.Errorf("ollama returned %d: %s", resp.status, strings.TrimSpace(string(b)))
}

type tagsResp struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// Tags returns the names of the models installed on the Ollama server. A nil
// error means the server is reachable; the slice may still be empty if no models
// are pulled. Used by the UI to tell "Ollama down" from "model not installed".
func (c *Client) Tags(ctx context.Context) ([]string, error) {
	ctx, cancel := timeoutContext(ctx, probeTimeout)
	defer cancel()
	resp, err := c.do(ctx, "GET", "/api/tags", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	if resp.status != 200 {
		return nil, fmt.Errorf("ollama returned %d", resp.status)
	}
	var tr tagsResp
	if err := json.NewDecoder(resp.body).Decode(&tr); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tr.Models))
	for _, m := range tr.Models {
		switch {
		case m.Name != "":
			names = append(names, m.Name)
		case m.Model != "":
			names = append(names, m.Model)
		}
	}
	return names, nil
}
