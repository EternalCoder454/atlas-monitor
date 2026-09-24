package ui

import (
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The Apps table connects a right-click gesture to every cell GTK sets up, and
// GTK sets up cells constantly — on scrolling, on splices, on filter changes.
// These tests establish what that costs, because the answer decides whether the
// gesture can stay per-cell at all.

// liveObjects is the live heap object count after a collection.
func liveObjects() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapObjects
}

// TestPerWidgetGestureClosuresAreRetained measures what connecting a signal to a
// short-lived widget costs once the widget is gone. gotk4 registers the Go
// callback in a registry keyed by the object; if that entry outlives the widget,
// every cell GTK ever builds is a permanent allocation.
func TestPerWidgetGestureClosuresAreRetained(t *testing.T) {
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
		// The gesture goes out of scope here, exactly as a torn-down cell's does.
	}
	after := liveObjects()

	grew := int64(after) - int64(before)
	t.Logf("%d gestures connected and dropped: live objects %+d (%.2f per gesture)",
		n, grew, float64(grew)/float64(n))
	if grew > int64(n)/4 {
		t.Logf("VERDICT: retained — a per-cell gesture leaks for the life of the process")
	} else {
		t.Logf("VERDICT: released")
	}
}

// TestDisconnectedGestureClosuresAreReleased asks whether disconnecting the
// handler is enough to get the memory back, which would make the fix as small as
// disconnecting in the factory's teardown.
func TestDisconnectedGestureClosuresAreReleased(t *testing.T) {
	const n = 2000

	for i := 0; i < 100; i++ {
		g := gtk.NewGestureClick()
		h := g.ConnectPressed(func(int, float64, float64) {})
		g.HandlerDisconnect(h)
	}
	before := liveObjects()

	for i := 0; i < n; i++ {
		g := gtk.NewGestureClick()
		g.SetButton(3)
		h := g.ConnectPressed(func(int, float64, float64) {})
		g.HandlerDisconnect(h)
	}
	after := liveObjects()

	grew := int64(after) - int64(before)
	t.Logf("%d gestures connected, disconnected and dropped: live objects %+d (%.2f per gesture)",
		n, grew, float64(grew)/float64(n))
}
