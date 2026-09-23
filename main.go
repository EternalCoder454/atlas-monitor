// Command atlas-monitor is a native Linux system monitor for GNOME/Fedora.
package main

import (
	_ "embed"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"atlas-monitor/internal/app"
	"atlas-monitor/internal/sysmem"
)

//go:embed assets/style.css
var styleCSS string

//go:embed VERSION
var version string

const (
	// gcPercent trades a little more GC work for a smaller heap. Atlas keeps
	// only a few megabytes live, so the default 100% headroom is pure resident
	// memory we never use; at 50% a collection still costs well under a
	// millisecond.
	gcPercent = 50

	// maxProcs caps the scheduler. The work here is five collectors blocked on
	// /proc reads once a second plus a GTK main loop — nothing that benefits
	// from a P per core, while each extra P and its thread carries a runtime
	// cache and a stack. On a 32-thread machine this is ~10 fewer OS threads.
	maxProcs = 4
)

func main() {
	// Before anything allocates: cap glibc's arenas and stop the C heap from
	// growing in large steps. GTK, Pango and cairo do the bulk of the allocating
	// in this process and all of it goes through malloc.
	sysmem.Tune()
	if _, set := os.LookupEnv("GOGC"); !set {
		debug.SetGCPercent(gcPercent)
	}
	if _, set := os.LookupEnv("GOMAXPROCS"); !set {
		runtime.GOMAXPROCS(min(maxProcs, runtime.NumCPU()))
	}

	// The renderer is chosen inside app.New, once settings are loaded, because
	// it has to be decided before GTK opens the display. See internal/gfx.
	os.Exit(app.New(styleCSS, strings.TrimSpace(version)).Run(os.Args))
}
