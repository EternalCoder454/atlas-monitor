// Package config persists user settings to ~/.config/atlas-monitor/settings.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"atlas-monitor/internal/gfx"
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

// Window geometry. The minimums match the window's own size request, so a
// corrupt or hand-edited settings file cannot produce an unusable window.
const (
	DefaultWindowWidth  = 1100
	DefaultWindowHeight = 720
	// Low enough that the layout's narrow mode is reachable: below 700px the
	// sidebar collapses into an overlay, and a 900px floor meant nobody could
	// ever get there. These are the smallest sizes the content still works at.
	MinWindowWidth  = 360
	MinWindowHeight = 400
)

// DefaultRefreshSeconds is the sampling interval when nothing is configured.
const DefaultRefreshSeconds = 1

// RefreshChoices are the intervals offered in Settings, in seconds.
var RefreshChoices = []int{1, 2, 3, 5, 10}

// NormalizeRefresh snaps an interval onto the nearest offered choice, so an
// out-of-range or hand-edited value can never stop the collectors.
func NormalizeRefresh(seconds int) int {
	for _, c := range RefreshChoices {
		if seconds == c {
			return seconds
		}
	}
	return DefaultRefreshSeconds
}

// Update channels: the branch an update pulls. Release and Beta are the full
// application, the first stable and the second ahead of it; Minimal is Atlas
// without the assistant. Choosing another one and pressing Update moves this
// copy onto it.
const (
	ChannelRelease = "main"
	ChannelBeta    = "beta"
	ChannelMinimal = "minimal"
)

// Channels are the channels offered in Settings, in its order.
var Channels = []string{ChannelRelease, ChannelBeta, ChannelMinimal}

// NormalizeChannel maps anything that is not a channel onto this build's
// default.
func NormalizeChannel(ch string) string {
	for _, c := range Channels {
		if ch == c {
			return ch
		}
	}
	return DefaultChannel
}

// Window transparency levels, as stored in settings. Off is the default and
// leaves the window opaque, which is how Atlas has always looked.
const (
	TransparencyOff    = "off"
	TransparencySubtle = "subtle"
	TransparencyMedium = "medium"
	TransparencyStrong = "strong"
)

// TransparencyChoices are the levels offered in Settings, weakest first.
var TransparencyChoices = []string{
	TransparencyOff, TransparencySubtle, TransparencyMedium, TransparencyStrong,
}

// NormalizeTransparency maps anything that is not one of the offered levels onto
// off, so a hand-edited value can never leave the window half see-through in a
// way the Settings row cannot show or undo.
func NormalizeTransparency(level string) string {
	for _, c := range TransparencyChoices {
		if level == c {
			return level
		}
	}
	return TransparencyOff
}

// Frame styles, as stored in settings: how the sidebar and title bar look while
// the window is see-through. See-through, the default, lets the desktop show
// through them as well as the page. Solid sidebar keeps the sidebar opaque and
// the title bar see-through, which is how the first transparent release looked.
// Solid keeps both opaque, so only the page shows the desktop.
const (
	FrameSeeThrough   = "see-through"
	FrameSolidSidebar = "solid-sidebar"
	FrameSolid        = "solid"
)

// FrameChoices are the frame styles offered in Settings, in its order.
var FrameChoices = []string{FrameSeeThrough, FrameSolidSidebar, FrameSolid}

// NormalizeFrame maps anything that is not one of the offered styles onto
// see-through, for the same reason as NormalizeTransparency.
func NormalizeFrame(style string) string {
	for _, c := range FrameChoices {
		if style == c {
			return style
		}
	}
	return FrameSeeThrough
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
	TextRendering  string `json:"text_rendering"`
	UpdateCheck    bool   `json:"update_check"`
	AssistantTitle string `json:"assistant_title"` // page header / chat label; sidebar stays "Assistant"
	SystemPrompt   string `json:"system_prompt"`
	UpdateChannel  string `json:"update_channel"` // one of the Channel constants
	RenderMode     string `json:"render_mode"`    // see gfx: "software" (default), "gpu", "system"

	// Theme is the colour theme's id — see internal/theme. Empty means Atlas
	// follows the desktop's light/dark preference, which is what it did before
	// there was a choice, and is the default for that reason.
	Theme string `json:"theme"`

	// WindowTransparency is how see-through the window's backgrounds are: one of
	// the Transparency constants. Text and charts stay solid at every level. It
	// only takes effect where the display composites; see ui.TransparencyAvailable.
	WindowTransparency string `json:"window_transparency"`

	// FrameStyle is how the sidebar and title bar look while the window is
	// see-through: one of the Frame constants. It does nothing while
	// transparency is off.
	FrameStyle string `json:"frame_style"`

	// RefreshSeconds is how often every collector samples and the visible page
	// redraws. It also stretches the graphs: they keep 60 samples either way, so
	// 1s shows the last minute and 5s the last five.
	RefreshSeconds int `json:"refresh_seconds"`

	// Window state, so Atlas reopens where it was left.
	WindowWidth     int    `json:"window_width"`
	WindowHeight    int    `json:"window_height"`
	WindowMaximized bool   `json:"window_maximized"`
	LastView        string `json:"last_view"`

	// CollapsedSections are the page sections folded shut, by title. A folded
	// section is not drawn and, where the work is its own, not done either.
	CollapsedSections []string `json:"collapsed_sections"`

	// EnergyAuto lets Energy Saver ease off busy applications by itself, and put
	// them back when they calm down or start playing sound. See internal/ease.
	EnergyAuto bool `json:"energy_auto"`
	// EnergyNever are applications, by ID, never to ease off automatically.
	EnergyNever []string `json:"energy_never"`

	// HiddenColumns are the Apps table columns put away, by their titles. The
	// two disk ones start hidden: they count blocks that reach the drive, which
	// on anything with a page cache is nothing for nearly every process.
	HiddenColumns []string `json:"hidden_columns"`

	QuickPrompts []QuickPrompt `json:"quick_prompts"` // exactly 3, shown in the assistant dropdown
}

// Defaults returns the built-in defaults.
func Defaults() Settings {
	return Settings{
		AIEnabled:      true,
		OllamaURL:      "http://localhost:11434",
		Model:          "qwen3.5:9b",
		TextRendering:  gfx.TextSharp,
		UpdateCheck:    true,
		AssistantTitle: "Assistant",
		SystemPrompt:   DefaultSystemPrompt,
		UpdateChannel:  DefaultChannel,
		RenderMode:     gfx.ModeSoftware,
		RefreshSeconds: DefaultRefreshSeconds,
		WindowWidth:    DefaultWindowWidth,
		WindowHeight:   DefaultWindowHeight,
		HiddenColumns:  []string{"Disk Read", "Disk Write"},
		EnergyAuto:     true,
		QuickPrompts:   DefaultQuickPrompts(),

		WindowTransparency: TransparencyOff,
		FrameStyle:         FrameSeeThrough,
	}
}

// dir is where settings.json lives.
//
// An explicit XDG_CONFIG_HOME wins on every platform. On Linux that is what
// os.UserConfigDir does anyway. On Windows it would be ignored in favour of
// %AppData%, which had two consequences: a portable install unpacked into a folder
// had no way to keep its settings beside itself, and these tests — which isolate
// themselves by setting XDG_CONFIG_HOME — were reading and writing the real
// settings file instead of their own fixture. That is how four of them failed the
// first time they ran on Windows.
func dir() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "atlas-monitor")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		// UserConfigDir only fails when it cannot find a home directory at all.
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "atlas-monitor" // beside the working directory: nowhere better
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "atlas-monitor")
}

func path() string { return filepath.Join(dir(), "settings.json") }

// Load reads settings from disk, falling back to defaults for missing fields.
func Load() Settings {
	s := Defaults()
	if b, err := os.ReadFile(path()); err == nil {
		_ = json.Unmarshal(b, &s)
		// 0.9.0 had one switch for the pair of disk columns. Anyone who turned
		// it on meant "show me those", and a rename should not quietly put them
		// away again.
		var old struct {
			ShowIOColumns *bool `json:"show_io_columns"`
		}
		if json.Unmarshal(b, &old) == nil && old.ShowIOColumns != nil && *old.ShowIOColumns {
			s.HiddenColumns = without(s.HiddenColumns, "Disk Read", "Disk Write")
		}
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
	s.UpdateChannel = NormalizeChannel(s.UpdateChannel)
	s.RenderMode = gfx.Normalize(s.RenderMode)
	s.TextRendering = gfx.NormalizeText(s.TextRendering)
	s.RefreshSeconds = NormalizeRefresh(s.RefreshSeconds)
	s.WindowTransparency = NormalizeTransparency(s.WindowTransparency)
	s.FrameStyle = NormalizeFrame(s.FrameStyle)
	if s.WindowWidth < MinWindowWidth {
		s.WindowWidth = DefaultWindowWidth
	}
	if s.WindowHeight < MinWindowHeight {
		s.WindowHeight = DefaultWindowHeight
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
	b, err := merged(s)
	if err != nil {
		return err
	}
	// Write to a sibling and rename over the target, so the settings file is
	// never observed half-written. Atlas saves on window close, which is exactly
	// when the process is most likely to be killed mid-write — and a truncated
	// file reads back as no settings at all, silently resetting the window size,
	// the last view and the refresh interval.
	// 0600: nothing else needs to read this, and it carries the assistant's
	// endpoint and system prompt. There is no reason for it to be world
	// readable on a shared machine.
	tmp := path() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	// Flush before the rename: rename only orders the directory entry, so
	// without this the new name can be visible while its contents are not.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// without returns names minus any of drop, keeping order.
func without(names []string, drop ...string) []string {
	out := names[:0:0]
	for _, n := range names {
		skip := false
		for _, d := range drop {
			if n == d {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, n)
		}
	}
	return out
}

// merged is the settings as JSON, on top of whatever the file already holds that
// this build does not know about.
//
// The full application and the minimal one share a settings file, and the
// minimal one has no assistant, so it knows nothing of the model, the prompt or
// the quick prompts. Saving only what it knows would drop them, and someone who
// tried the Minimal channel and came back would find the assistant reset. Keys
// this build does not have are kept as they were; the ones it has are written
// fresh.
func merged(s Settings) ([]byte, error) {
	fresh, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	if old, err := os.ReadFile(path()); err == nil {
		// A damaged file has nothing to keep — and `null` would leave the map
		// nil, which could not be written to.
		if json.Unmarshal(old, &out) != nil || out == nil {
			out = map[string]json.RawMessage{}
		}
	}
	var mine map[string]json.RawMessage
	if err := json.Unmarshal(fresh, &mine); err != nil {
		return nil, err
	}
	for k, v := range mine {
		out[k] = v
	}
	return json.MarshalIndent(out, "", "  ")
}
