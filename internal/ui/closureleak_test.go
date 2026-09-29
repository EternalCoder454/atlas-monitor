//go:build !race

// This file measures what the heap holds once the main context has run, and
// the race detector changes when finalizers run; see rowleak_test.go.

package ui

import (
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// settle lets everything that is going to be released be released: collections
// to queue finalizers, and turns of GLib's default main context, where gotk4
// 0.4.1 carries out the release they request. The application runs the main
// loop all the time; a test has to do it by hand.
func settle() {
	ctx := glib.MainContextDefault()
	for round := 0; round < 20; round++ {
		runtime.GC()
		runtime.Gosched()
		for i := 0; i < 10000 && ctx.Iteration(false); i++ {
		}
	}
}

// liveObjects is the live heap object count once everything releasable has been.
func liveObjects() uint64 {
	settle()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapObjects
}

// TestDroppedGestureClosuresAreReleased checks that a widget with a Go callback
// connected to it is released with its callback when nothing holds it any more.
//
// Before gotk4 0.4.1 neither was: gotk4 registered every callback in a registry
// keyed by the object and never removed the entry, so each dropped gesture kept
// about ten objects alive for the life of the process (10.5 on the machine this
// was measured on; 1.6 once the main context was allowed to run) — which is why
// the Apps table has one right-click gesture for the whole table rather than one
// per cell. Under 0.4.1 it is 0.01, which is noise. This fails if a later gotk4
// brings the leak back.
func TestDroppedGestureClosuresAreReleased(t *testing.T) {
	const n = 2000

	// Warm up so one-time allocations are not counted as growth.
	for i := 0; i < 100; i++ {
		g := gtk.NewGestureClick()
		g.SetButton(3)
		g.ConnectPressed(func(int, float64, float64) {})
	}
	before := liveObjects()

	for i := 0; i < n; i++ {
		g := gtk.NewGestureClick()
		g.SetButton(3)
		g.ConnectPressed(func(int, float64, float64) {})
		// The gesture goes out of scope here, as a torn-down cell's does.
	}
	after := liveObjects()

	grew := int64(after) - int64(before)
	t.Logf("%d gestures connected and dropped: live objects %+d (%.2f per gesture)",
		n, grew, float64(grew)/float64(n))
	if grew > n/10 {
		t.Errorf("dropped gestures are being retained: %.2f live objects each", float64(grew)/float64(n))
	}
}
