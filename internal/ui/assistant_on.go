//go:build !noai

package ui

import (
	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/process"
	"atlas-monitor/internal/stats"
)

// aiCompiledIn reports whether this build includes the Assistant. Building with
// `-tags noai` (see `make build-lean`) drops the assistant page, the Ollama
// client and the Markdown renderer from the binary entirely, for people who
// want a pure system monitor.
const aiCompiledIn = true

// assistant is the slice of the Assistant page the window needs. Keeping it
// behind an interface lets the noai build drop the implementation without the
// window knowing.
type assistant interface {
	View
	RefreshQuickPrompts()
}

func newAssistant(col *stats.Collector, proc *process.Collector, client *ai.Client, settings *config.Settings) assistant {
	return newAssistantView(col, proc, client, settings)
}
