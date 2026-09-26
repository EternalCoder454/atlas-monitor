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

// TestCoreGridReflowsWhenNarrow is the regression for cores printed through
// their own readings. The grid used a fixed eight columns whatever the width,
// so a 560px window gave each core 65px for a name and a percentage that need
// about 108 together, and they overlapped.
func TestCoreGridReflowsWhenNarrow(t *testing.T) {
	for _, c := range []struct {
		width, cores, want int
		why                string
	}{
		{1100, 32, 8, "a wide window keeps the full eight"},
		{560, 32, 5, "a narrow one takes what fits"},
		{300, 32, 2, "and never drops below two"},
		{1100, 4, 4, "a four-thread laptop is not padded out to eight"},
	} {
		if got := fitColumns(c.width, c.cores); got != c.want {
			t.Errorf("fitColumns(%d, %d) = %d, want %d — %s", c.width, c.cores, got, c.want, c.why)
		}
	}
	// Whatever it picks, a cell is wide enough for the text.
	for w := 300; w <= 2000; w += 7 {
		if cell := w / fitColumns(w, 32); cell < minCoreCellWidth-1 && fitColumns(w, 32) > 2 {
			t.Fatalf("width %d: %dpx per cell is too narrow", w, cell)
		}
	}
}
