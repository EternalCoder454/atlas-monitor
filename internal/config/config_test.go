package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"atlas-monitor/internal/gfx"
)

func TestDefaultsAreValid(t *testing.T) {
	d := Defaults()
	if d.RefreshSeconds != DefaultRefreshSeconds {
		t.Errorf("RefreshSeconds = %d, want %d", d.RefreshSeconds, DefaultRefreshSeconds)
	}
	if d.WindowWidth < MinWindowWidth || d.WindowHeight < MinWindowHeight {
		t.Errorf("default window %dx%d is below the minimum %dx%d",
			d.WindowWidth, d.WindowHeight, MinWindowWidth, MinWindowHeight)
	}
	if gfx.Normalize(d.RenderMode) != d.RenderMode {
		t.Errorf("default RenderMode %q is not a valid mode", d.RenderMode)
	}
	if len(d.QuickPrompts) != 3 {
		t.Errorf("got %d quick prompts, want 3", len(d.QuickPrompts))
	}
}

func TestNormalizeRefresh(t *testing.T) {
	for _, sec := range RefreshChoices {
		if got := NormalizeRefresh(sec); got != sec {
			t.Errorf("NormalizeRefresh(%d) = %d, want it unchanged", sec, got)
		}
	}
	for _, bad := range []int{0, -5, 4, 7, 999} {
		if got := NormalizeRefresh(bad); got != DefaultRefreshSeconds {
			t.Errorf("NormalizeRefresh(%d) = %d, want the default %d", bad, got, DefaultRefreshSeconds)
		}
	}
}

// TestLoadRepairsBadValues: a hand-edited or truncated settings file must never
// leave the app with an unusable window or a stopped collector.
func TestLoadRepairsBadValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	bad := map[string]any{
		"refresh_seconds": 0,
		"window_width":    12,
		"window_height":   -400,
		"render_mode":     "holographic",
		"update_channel":  "nightly",
		"ollama_url":      "",
		"model":           "",
	}
	raw, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "atlas-monitor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "atlas-monitor", "settings.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	s := Load()
	if s.RefreshSeconds != DefaultRefreshSeconds {
		t.Errorf("RefreshSeconds = %d, want repaired to %d", s.RefreshSeconds, DefaultRefreshSeconds)
	}
	if s.WindowWidth != DefaultWindowWidth || s.WindowHeight != DefaultWindowHeight {
		t.Errorf("window = %dx%d, want repaired to %dx%d",
			s.WindowWidth, s.WindowHeight, DefaultWindowWidth, DefaultWindowHeight)
	}
	if s.RenderMode != gfx.ModeSoftware {
		t.Errorf("RenderMode = %q, want repaired to %q", s.RenderMode, gfx.ModeSoftware)
	}
	if s.UpdateChannel != "main" {
		t.Errorf("UpdateChannel = %q, want repaired to main", s.UpdateChannel)
	}
	if s.OllamaURL == "" || s.Model == "" {
		t.Error("empty Ollama URL/model were not repaired")
	}
}

// TestSaveLoadRoundTrip covers the window state and the new interval.
func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	want := Defaults()
	want.RefreshSeconds = 5
	want.WindowWidth, want.WindowHeight = 1440, 900
	want.WindowMaximized = true
	want.LastView = "disk:nvme0n1"
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := Load()
	if got.RefreshSeconds != 5 {
		t.Errorf("RefreshSeconds = %d, want 5", got.RefreshSeconds)
	}
	if got.WindowWidth != 1440 || got.WindowHeight != 900 || !got.WindowMaximized {
		t.Errorf("window = %dx%d max=%v, want 1440x900 max=true",
			got.WindowWidth, got.WindowHeight, got.WindowMaximized)
	}
	if got.LastView != "disk:nvme0n1" {
		t.Errorf("LastView = %q, want disk:nvme0n1", got.LastView)
	}
}

// TestLoadWithNoFile: a first run must produce usable settings, not zeroes.
func TestLoadWithNoFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := Load()
	if s.RefreshSeconds == 0 || s.WindowWidth == 0 || s.RenderMode == "" {
		t.Errorf("first run produced unusable settings: %+v", s)
	}
}
