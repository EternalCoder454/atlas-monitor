//go:build noai

package ui

import (
	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/process"
	"atlas-monitor/internal/stats"
)

// aiCompiledIn is false in the lean build: no assistant page, no Ollama client,
// no Markdown renderer.
const aiCompiledIn = false

type assistant interface {
	View
	RefreshQuickPrompts()
}

func newAssistant(*stats.Collector, *process.Collector, *ai.Client, *config.Settings) assistant {
	return nil
}
