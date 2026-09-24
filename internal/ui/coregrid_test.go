package ui

import (
	"os"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// TestEveryCoreIsLabelledFromTheStart is the regression for cores that were
// drawn with no figure beside them at all.
//
// The grid only re-shapes a reading when it changes, which is what keeps an
// idle machine from re-laying out thirty-two labels every second. The labels
// started empty, though, so a core sitting at exactly 0% from the moment the
// page opened never changed and never got one — on a 32-thread desktop that
// meant three or four cores with a name, a bar, and a blank where the number
// should be, while every core that had been busy once was labelled.
func TestEveryCoreIsLabelledFromTheStart(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("needs a display to construct a drawing area")
	}
	if !gtk.InitCheck() {
		t.Skip("GTK could not initialise")
	}
	g := newCoreGrid(4)
	for i, p := range g.pct {
		if p == "" {
			t.Errorf("core %d has no label before the first reading", i)
		}
	}

	// A core that stays at zero keeps its label; the others follow their value.
	g.set([]float64{0, 12.4, 0, 99.6})
	want := []string{"0%", "12%", "0%", "100%"}
	for i := range want {
		if g.pct[i] != want[i] {
			t.Errorf("core %d: got %q, want %q", i, g.pct[i], want[i])
		}
	}

	// And a core that goes back to zero says so rather than going blank.
	g.set([]float64{0, 0, 0, 0})
	for i, p := range g.pct {
		if p != "0%" {
			t.Errorf("core %d: got %q after falling idle, want %q", i, p, "0%")
		}
	}
}
