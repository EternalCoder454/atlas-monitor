package ui

import (
	"testing"

	"atlas-monitor/internal/config"
)

func TestTransparencyAvailable(t *testing.T) {
	cases := []struct {
		goos       string
		composited bool
		ok         bool
		why        string
	}{
		{"linux", true, true, ""},
		{"linux", false, false, "Needs a desktop that composites windows"},
		{"windows", true, false, "Not available on Windows"},
		// Windows is named first: that is the reason that would still hold on a
		// display that did composite.
		{"windows", false, false, "Not available on Windows"},
		{"freebsd", true, true, ""},
		{"darwin", true, true, ""},
	}
	for _, c := range cases {
		ok, why := transparencyAvailable(c.goos, c.composited)
		if ok != c.ok || why != c.why {
			t.Errorf("transparencyAvailable(%q, %v) = %v, %q; want %v, %q",
				c.goos, c.composited, ok, why, c.ok, c.why)
		}
	}
}

// TestTransparencyLevelsMatchConfig keeps the dropdown in step with the values
// config accepts: a level offered here that config normalises away would not
// survive a restart, and one config accepts that is not offered could never be
// chosen.
func TestTransparencyLevelsMatchConfig(t *testing.T) {
	if len(transparencyLevels) != len(config.TransparencyChoices) {
		t.Fatalf("%d levels in the dropdown, %d in config", len(transparencyLevels), len(config.TransparencyChoices))
	}
	for i, l := range transparencyLevels {
		if l.Value != config.TransparencyChoices[i] {
			t.Errorf("level %d is %q, config has %q there", i, l.Value, config.TransparencyChoices[i])
		}
		if l.Label == "" {
			t.Errorf("level %q has no label", l.Value)
		}
	}
}
