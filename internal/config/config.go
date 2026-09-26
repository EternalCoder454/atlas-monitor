// Package config persists user settings to ~/.config/atlas-monitor/settings.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"atlas-monitor/internal/gfx"
)

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

// MinimalChannel is the only branch this build updates from. The full
// application lives on main and beta; pulling either of those into a minimal
// install would quietly turn it back into the full one.
const MinimalChannel = "minimal"

// DefaultRefreshSeconds is the sampling interval when nothing is configured.
//
// Two seconds rather than one, because this is the build for machines that
// cannot spare the difference. Measured on the CPU page: 9.7 ms/s of processor
// at 1s, 5.0 ms/s at 2s, 3.2 ms/s at 3s — the work is simply proportional to
// the tick rate. A second of resolution is not worth halving a laptop's idle
// cost for, and anyone who wants it back has the setting.
const DefaultRefreshSeconds = 2

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

type Settings struct {
	TextRendering string `json:"text_rendering"`
	UpdateCheck   bool   `json:"update_check"`
	ShowIOColumns bool   `json:"show_io_columns"`
	UpdateChannel string `json:"update_channel"` // always MinimalChannel in this build; see UpdateChannel handling
	RenderMode    string `json:"render_mode"`    // see gfx: "software" (default), "gpu", "system"

	// RefreshSeconds is how often every collector samples and the visible page
	// redraws. It also stretches the graphs: they keep 60 samples either way, so
	// 1s shows the last minute and 5s the last five.
	RefreshSeconds int `json:"refresh_seconds"`

	// Window state, so Atlas reopens where it was left.
	WindowWidth     int    `json:"window_width"`
	WindowHeight    int    `json:"window_height"`
	WindowMaximized bool   `json:"window_maximized"`
	LastView        string `json:"last_view"`
}

// Defaults returns the built-in defaults.
func Defaults() Settings {
	return Settings{
		TextRendering:  gfx.TextSharp,
		UpdateCheck:    true,
		UpdateChannel:  MinimalChannel,
		RenderMode:     gfx.ModeSoftware,
		RefreshSeconds: DefaultRefreshSeconds,
		WindowWidth:    DefaultWindowWidth,
		WindowHeight:   DefaultWindowHeight,
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
	// This build tracks one branch and no other. A settings file carried over
	// from a full install will name main or beta, and honouring that would pull
	// the full application over the top of this one on the next update — the
	// assistant back, from a build that was installed to be without it. Anything
	// that is not the minimal channel is repaired to it.
	if s.UpdateChannel != MinimalChannel {
		s.UpdateChannel = MinimalChannel
	}
	s.RenderMode = gfx.Normalize(s.RenderMode)
	s.TextRendering = gfx.NormalizeText(s.TextRendering)
	s.RefreshSeconds = NormalizeRefresh(s.RefreshSeconds)
	if s.WindowWidth < MinWindowWidth {
		s.WindowWidth = DefaultWindowWidth
	}
	if s.WindowHeight < MinWindowHeight {
		s.WindowHeight = DefaultWindowHeight
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
	// Write to a sibling and rename over the target, so the settings file is
	// never observed half-written. Atlas saves on window close, which is exactly
	// when the process is most likely to be killed mid-write — and a truncated
	// file reads back as no settings at all, silently resetting the window size,
	// the last view and the refresh interval.
	// 0600: nothing else needs to read it, and there is no reason for one
	// user's window geometry and preferences to be legible to everybody else
	// on a shared machine.
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
