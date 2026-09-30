package app

import (
	"strings"
	"testing"

	"atlas-monitor/internal/config"
)

// TestGlassClassesFor: transparency off means no classes whatever the frame
// style, and each style adds exactly its own class to the level's.
func TestGlassClassesFor(t *testing.T) {
	for _, frame := range config.FrameChoices {
		if got := glassClassesFor(config.TransparencyOff, frame); got != nil {
			t.Errorf("off with %q: got %v, want none", frame, got)
		}
	}
	cases := map[string]string{
		config.FrameSeeThrough:   "am-glass am-glass-medium",
		config.FrameSolidSidebar: "am-glass am-glass-medium am-sidebar-solid",
		config.FrameSolid:        "am-glass am-glass-medium am-frame-solid",
		"nonsense":               "am-glass am-glass-medium",
	}
	for frame, want := range cases {
		if got := strings.Join(glassClassesFor(config.TransparencyMedium, frame), " "); got != want {
			t.Errorf("medium with %q: got %q, want %q", frame, got, want)
		}
	}
	// Every class it can hand out is one applyTransparency knows to take off.
	known := map[string]bool{}
	for _, c := range glassClasses {
		known[c] = true
	}
	for _, level := range config.TransparencyChoices {
		for _, frame := range config.FrameChoices {
			for _, c := range glassClassesFor(level, frame) {
				if !known[c] {
					t.Errorf("%s/%s gives %q, which glassClasses does not list", level, frame, c)
				}
			}
		}
	}
}
