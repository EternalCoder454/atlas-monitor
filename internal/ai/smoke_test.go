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
