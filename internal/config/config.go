// Package config persists user settings to ~/.config/atlas-monitor/settings.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DefaultSystemPrompt is the instruction text sent to the model before the live
// system data. It is user-editable via Settings.
const DefaultSystemPrompt = `You are Atlas, the assistant built into Atlas Monitor, a Linux system monitor. Answer using ONLY the live system data below; never invent or estimate numbers. If the data can't answer the question, say so in one sentence and stop.

Reading the data:
- [SUMMARY] holds the key overall figures (CPU %, RAM, GPU, swap, network, uptime), already extracted for you — use it for any question about overall usage or status, and quote its numbers exactly.
- The "Alerts:" line lists the genuine problems the monitor detected (high temperatures, low memory, heavy swap, failed services, full disks). For health questions like "is anything wrong", report each of those; if it says "none", tell the user the system is healthy. Never report a problem that is not listed there.
- [HARDWARE], [PROCESSES] and [SERVICES] hold the detail. A process's CPU% counts every core and can exceed 100%; it is never the overall CPU usage, and a process's memory is never the total RAM used.

Style:
- Lead with the answer. No preamble, no restating the question.
- Be concise. Use short Markdown bullets ("- item") for lists; cite exact names, PIDs and figures. Keep overviews to a few bullets per area.
- No emoji, no hedging, no filler. Never end with an offer or a follow-up question.
- Write for the user; don't mention the data's internal section names. Busy is not broken: high CPU or GPU usage on its own is normal activity, not a problem. Never call ordinary values (10% CPU, 40% RAM, a load average below the thread count) "high" or "concerning".`

// obsoletePromptLines are sentences removed from older saved prompts on load.
var obsoletePromptLines = []string{
	" Per-process GPU usage is unavailable, so only discuss overall GPU load.",
	"Per-process GPU usage is unavailable, so only discuss overall GPU load.",
}

// QuickPrompt is one entry in the assistant's quick-prompts dropdown: a display
// name and the message sent when it is chosen. Both are user-editable.
type QuickPrompt struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// DefaultQuickPrompts are the three built-in quick prompts.
func DefaultQuickPrompts() []QuickPrompt {
	return []QuickPrompt{
		{
			Name:   "Detailed Overview",
			Prompt: "Give me a concise overview of this machine right now — CPU, memory, GPU, disks, and network — a few bullet points each, and call out anything that needs attention.",
		},
		{
			Name:   "Top Processes",
			Prompt: "List the 10 processes using the most CPU right now — name, PID and CPU% each, highest first.",
		},
		{
			Name:   "Quick Check",
			Prompt: "Quick status: overall CPU usage, RAM used of total, network up and down, and uptime. One short line each.",
		},
	}
}

// Settings is the user-configurable state.
type Settings struct {
	AIEnabled      bool   `json:"ai_enabled"`
	OllamaURL      string `json:"ollama_url"`
	Model          string `json:"model"`
	AssistantTitle string `json:"assistant_title"` // page header / chat label; sidebar stays "Assistant"
	SystemPrompt   string `json:"system_prompt"`
	UpdateChannel  string `json:"update_channel"` // "main" (Release) or "beta" (newest features/fixes)

	QuickPrompts []QuickPrompt `json:"quick_prompts"` // exactly 3, shown in the assistant dropdown
}

// Defaults returns the built-in defaults.
func Defaults() Settings {
	return Settings{
		AIEnabled:      true,
		OllamaURL:      "http://localhost:11434",
		Model:          "qwen2.5:3b",
		AssistantTitle: "Assistant",
		SystemPrompt:   DefaultSystemPrompt,
		UpdateChannel:  "main",
		QuickPrompts:   DefaultQuickPrompts(),
	}
}

func dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "atlas-monitor")
}

func path() string { return filepath.Join(dir(), "settings.json") }

// Load reads settings from disk, falling back to defaults for missing fields.
func Load() Settings {
	s := Defaults()
	if b, err := os.ReadFile(path()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.OllamaURL == "" {
		s.OllamaURL = Defaults().OllamaURL
	}
	if s.Model == "" {
		s.Model = Defaults().Model
	}
	if s.AssistantTitle == "" {
		s.AssistantTitle = Defaults().AssistantTitle
	}
	if s.SystemPrompt == "" {
		s.SystemPrompt = DefaultSystemPrompt
	} else {
		// Migrate older saved prompts: drop the now-obsolete per-process GPU caveat.
		for _, line := range obsoletePromptLines {
			s.SystemPrompt = strings.ReplaceAll(s.SystemPrompt, line, "")
		}
		s.SystemPrompt = strings.TrimSpace(s.SystemPrompt)
	}
	if s.UpdateChannel != "main" && s.UpdateChannel != "beta" {
		s.UpdateChannel = "main" // default/repair: Release channel
	}
	// Quick prompts: keep exactly three, filling any missing slot from defaults.
	if def := DefaultQuickPrompts(); len(s.QuickPrompts) != len(def) {
		s.QuickPrompts = def
	} else {
		for i := range s.QuickPrompts {
			if strings.TrimSpace(s.QuickPrompts[i].Name) == "" {
				s.QuickPrompts[i].Name = def[i].Name
			}
			if strings.TrimSpace(s.QuickPrompts[i].Prompt) == "" {
				s.QuickPrompts[i].Prompt = def[i].Prompt
			}
		}
	}
	return s
}

// Save writes settings to disk.
func Save(s Settings) error {
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(), b, 0o644)
}
