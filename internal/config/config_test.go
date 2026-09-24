package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		"text_rendering":  "crispy",
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
	if s.TextRendering != gfx.TextSharp {
		t.Errorf("TextRendering = %q, want repaired to %q", s.TextRendering, gfx.TextSharp)
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

// TestLoadSurvivesACorruptFile covers a settings file that is not valid JSON at
// all: a truncated write from a machine that lost power, a file someone edited
// by hand, or bytes from an entirely different program. None of it may stop
// Atlas starting — the worst acceptable outcome is falling back to defaults.
func TestLoadSurvivesACorruptFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"truncated object", `{"refresh_seconds": 2, "window_wid`},
		{"not json", "this is not json at all\n"},
		{"binary", "\x00\x01\x02\xff\xfe"},
		{"json array", `[1, 2, 3]`},
		{"json string", `"settings"`},
		{"null", `null`},
		{"nested garbage", `{"refresh_seconds": {"deeply": ["wrong"]}}`},
		{"wrong types", `{"refresh_seconds": "fast", "window_width": "wide", "render_mode": 7}`},
		{"duplicate keys", `{"refresh_seconds": 2, "refresh_seconds": 99}`},
		{"huge numbers", `{"refresh_seconds": 999999999999999999999, "window_width": 1e400}`},
		{"only whitespace", "   \n\t  "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			if err := os.MkdirAll(filepath.Join(dir, "atlas-monitor"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "atlas-monitor", "settings.json"), []byte(c.content), 0o644); err != nil {
				t.Fatal(err)
			}

			s := Load()
			// Every field the app reads without checking must be usable.
			if s.RefreshSeconds != NormalizeRefresh(s.RefreshSeconds) {
				t.Errorf("RefreshSeconds = %d, not a normalized value", s.RefreshSeconds)
			}
			if s.RefreshSeconds < 1 {
				t.Errorf("RefreshSeconds = %d, would spin the collectors", s.RefreshSeconds)
			}
			if s.WindowWidth < MinWindowWidth || s.WindowHeight < MinWindowHeight {
				t.Errorf("window %dx%d is below the minimum", s.WindowWidth, s.WindowHeight)
			}
			if s.RenderMode != gfx.Normalize(s.RenderMode) {
				t.Errorf("RenderMode = %q, not a value gfx accepts", s.RenderMode)
			}
			if s.TextRendering != gfx.NormalizeText(s.TextRendering) {
				t.Errorf("TextRendering = %q, not a value gfx accepts", s.TextRendering)
			}
			if s.UpdateChannel != "main" && s.UpdateChannel != "beta" {
				t.Errorf("UpdateChannel = %q", s.UpdateChannel)
			}
			if s.OllamaURL == "" || s.Model == "" || s.AssistantTitle == "" || s.SystemPrompt == "" {
				t.Error("a text field the assistant needs came back empty")
			}
		})
	}
}

// TestLoadWithAnUnreadableFile covers the settings file being a directory, which
// is what a botched install or a stray mkdir leaves behind.
func TestLoadWithAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "atlas-monitor", "settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Load()
	if s.RefreshSeconds != DefaultRefreshSeconds {
		t.Errorf("RefreshSeconds = %d, want the default %d", s.RefreshSeconds, DefaultRefreshSeconds)
	}
	if s.RenderMode != gfx.ModeSoftware {
		t.Errorf("RenderMode = %q, want the default", s.RenderMode)
	}
}

// TestSaveIsAtomic checks the settings file is never left half-written. Atlas
// saves on window close, so an interrupted save is the realistic failure, and a
// truncated file silently resets the window size and last view.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	want := Defaults()
	want.RefreshSeconds = 3
	want.WindowWidth = 1400
	want.WindowHeight = 900
	want.LastView = "apps"
	if err := Save(want); err != nil {
		t.Fatal(err)
	}

	// Nothing may be left beside the settings file.
	entries, err := os.ReadDir(filepath.Join(dir, "atlas-monitor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "settings.json" {
			t.Errorf("Save left %q behind", e.Name())
		}
	}

	// A save over an existing file must replace it completely, not merge into
	// whatever bytes were there — a shorter document written in place would
	// leave the tail of the old one.
	long := want
	long.SystemPrompt = strings.Repeat("x", 4096)
	if err := Save(long); err != nil {
		t.Fatal(err)
	}
	short := want
	short.SystemPrompt = "short"
	if err := Save(short); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.SystemPrompt != "short" {
		t.Errorf("SystemPrompt = %q (%d bytes), want %q — a shorter save did not replace the longer one",
			truncate(got.SystemPrompt), len(got.SystemPrompt), "short")
	}
	if got.RefreshSeconds != 3 || got.WindowWidth != 1400 || got.LastView != "apps" {
		t.Errorf("round trip lost values: %+v", got)
	}
}

// TestSaveOverAReadOnlyDirectoryFails checks Save reports an error rather than
// pretending to have written, so the caller is not told settings were kept when
// they were not.
func TestSaveOverAReadOnlyDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := filepath.Join(dir, "atlas-monitor")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o500); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(cfg, 0o755) })
	if err := os.WriteFile(filepath.Join(cfg, "probe"), []byte("x"), 0o644); err == nil {
		t.Skip("running with privileges that ignore mode bits")
	}
	if err := Save(Defaults()); err == nil {
		t.Error("Save reported success writing into a read-only directory")
	}
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// TestSaveIsNotWorldReadable checks the settings file's mode. It carries the
// assistant's endpoint and system prompt, nothing else needs to read it, and on
// a shared machine there is no reason for other users to be able to.
func TestSaveIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "atlas-monitor", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("settings.json is mode %#o; group and other should have no access", mode)
	}

	// A rewrite must not widen it either — the rename has to carry the mode of
	// the file that was written, not of whatever was there before.
	if err := Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(filepath.Join(dir, "atlas-monitor", "settings.json"))
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("after a second save settings.json is mode %#o", mode)
	}
}

// TestUpdateCheckDefaultsOn covers the launch-time update check being on unless
// the user turns it off. The Defaults-then-unmarshal pattern matters here: a
// settings file written before this option existed has no key for it, and must
// come back with the check enabled rather than with Go's zero value.
func TestUpdateCheckDefaultsOn(t *testing.T) {
	if !Defaults().UpdateCheck {
		t.Error("Defaults has the update check off")
	}

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "atlas-monitor"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "atlas-monitor", "settings.json")

	// An older file, with no update_check key at all.
	if err := os.WriteFile(settings, []byte(`{"refresh_seconds": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Load().UpdateCheck {
		t.Error("a settings file predating the option came back with the check off")
	}

	// An explicit false has to survive, or the switch would not stay off.
	if err := os.WriteFile(settings, []byte(`{"update_check": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if Load().UpdateCheck {
		t.Error("an explicit update_check=false was ignored")
	}
}
