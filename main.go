// Command atlas-monitor is a native Linux system monitor for GNOME/Fedora.
package main

import (
	_ "embed"
	"os"
	"strings"

	"atlas-monitor/internal/app"
)

//go:embed assets/style.css
var styleCSS string

//go:embed VERSION
var version string

func main() {
	// Use GTK's default GSK renderer (GPU-accelerated). It syncs to the
	// display's frame clock, so window resizing stays smooth at any refresh
	// rate — software rendering ghosts on high-Hz screens. Users can still
	// force a renderer via the standard GSK_RENDERER environment variable.
	os.Exit(app.New(styleCSS, strings.TrimSpace(version)).Run(os.Args))
}
