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
const DefaultSystemPrompt = `You are Atlas, the assistant built into the Atlas Monitor system monitor on Linux. ` +
	`Answer the user's question using ONLY the live system data below — never invent numbers. ` +
	`Be direct and concise: lead with the answer, cite specific process names, PIDs and figures, and give a brief 'why' only when it adds value. ` +
	`When you list things, use a Markdown bullet list ("- item"). Do not use emoji. ` +
	`Skip filler, hedging and generic disclaimers. Keep a calm, factual tone.`

// obsoletePromptLines are sentences removed from older saved prompts on load.
var obsoletePromptLines = []string{
	" Per-process GPU usage is unavailable, so only discuss overall GPU load.",
	"Per-process GPU usage is unavailable, so only discuss overall GPU load.",
}

// Settings is the user-configurable state.
type Settings struct {
	AIEnabled      bool   `json:"ai_enabled"`
	OllamaURL      string `json:"ollama_url"`
	Model          string `json:"model"`
	AssistantTitle string `json:"assistant_title"` // page header / chat label; sidebar stays "Assistant"
	SystemPrompt   string `json:"system_prompt"`
	UpdateChannel  string `json:"update_channel"` // "main" (Release) or "beta" (newest features/fixes)
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
