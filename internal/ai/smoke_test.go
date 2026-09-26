package ai

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOllamaSmoke(t *testing.T) {
	c := New("http://localhost:11434", "qwen2.5:3b")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if !c.Available(ctx) {
		t.Skip("Ollama not reachable")
	}
	// Reachable is not the same as ready. A machine can be running Ollama with
	// entirely different models pulled, and asking it for one it does not have
	// is a 404 — which is the server behaving correctly, not this package
	// failing. Skip unless the model this test wants is actually installed.
	tags, err := c.Tags(ctx)
	if err != nil {
		t.Skipf("Ollama reachable but its model list could not be read: %v", err)
	}
	installed := false
	for _, tag := range tags {
		if tag == c.Model() {
			installed = true
			break
		}
	}
	if !installed {
		t.Skipf("Ollama is running but %q is not pulled (it has %d other model(s)); "+
			"run `ollama pull %s` to exercise this test", c.Model(), len(tags), c.Model())
	}

	var tokens int
	full, stats, err := c.Chat(ctx, []Message{
		{Role: "system", Content: "You are a terse assistant. Reply in one short sentence."},
		{Role: "user", Content: "Say hello and name one Linux system metric."},
	}, func(tok string) { tokens++ })
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if strings.TrimSpace(full) == "" {
		t.Fatal("empty response")
	}
	t.Logf("streamed %d chunks, %.1f tok/s (%d tokens in %s): %q",
		tokens, stats.TokensPerSec(), stats.EvalCount, stats.TotalDuration.Round(time.Millisecond), strings.TrimSpace(full))
}
