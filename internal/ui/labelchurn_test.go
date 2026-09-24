package ui

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/sysmem"
)

// rssKiB reads this process's resident set size. The Apps table's remaining
// growth is on the C heap, where Go's profiler cannot see it, so these tests
// measure the only thing that is visible: what the kernel says the process is
// holding.
func rssKiB(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("cannot read /proc/self/status: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			f := strings.Fields(rest)
			if len(f) == 0 {
				break
			}
			n, err := strconv.Atoi(f[0])
			if err != nil {
				break
			}
			return n
		}
	}
	t.Skip("VmRSS not reported")
	return 0
}

// TestLabelTextChurnIsCheap covers the Apps table's hottest write path. Every
// tick hands a few hundred rows entirely new text — new pids, new names — and
// each is a SetText on a GtkLabel with a string never seen before. That has to
// stay close to free, because it happens across every realised cell.
//
// The second variant is not something Atlas does, and is here as a warning:
// asking a label for its PangoLayout leaks about 1.7 kB per call through the
// gotk4 wrapper, which is roughly seventy times what the SetText itself costs.
// It made an earlier version of this very test "prove" a leak that was entirely
// the test's own doing.
func TestLabelTextChurnIsCheap(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("needs a display")
	}
	if !gtk.InitCheck() {
		t.Skip("GTK could not initialise")
	}

	const warm, n = 5000, 100000
	measure := func(fn func(*gtk.Label, string)) float64 {
		label := gtk.NewLabel("")
		for i := 0; i < warm; i++ {
			fn(label, "warm "+strconv.Itoa(i))
		}
		runtime.GC()
		before := rssKiB(t)
		for i := 0; i < n; i++ {
			fn(label, "proc"+strconv.Itoa(i)+" 12.3% 456.7 MB")
		}
		runtime.GC()
		sysmem.Release() // hand back whatever the allocator is merely holding
		return float64(rssKiB(t)-before) / float64(n)
	}

	setText := measure(func(l *gtk.Label, s string) { l.SetText(s) })
	t.Logf("SetText alone:      %.4f kB per update", setText)
	if setText > 0.5 {
		t.Errorf("SetText costs %.3f kB per update; the table does ~1800 of these a second", setText)
	}

	withLayout := measure(func(l *gtk.Label, s string) {
		l.SetText(s)
		if lay := l.Layout(); lay != nil {
			_, _ = lay.PixelSize()
		}
	})
	t.Logf("SetText + Layout(): %.4f kB per update", withLayout)
	if withLayout < setText*4 {
		t.Logf("NOTE: Layout() no longer dominates — gotk4 may have fixed the wrapper it used to retain")
	}
}
