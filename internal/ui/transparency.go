package ui

import (
	"runtime"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"

	"atlas-monitor/internal/config"
)

// transparencyLevels are the window transparency choices in the order Settings
// lists them, each with the value stored for it. The values are config's; only the
// labels are the dialog's own.
var transparencyLevels = []struct{ Value, Label string }{
	{config.TransparencyOff, "Off"},
	{config.TransparencySubtle, "Subtle"},
	{config.TransparencyMedium, "Medium"},
	{config.TransparencyStrong, "Strong"},
}

// transparencyAvailable says whether the window can be made see-through, and if
// not, why in words the Settings row can show.
//
// Windows is ruled out outright, whatever the display reports. Elsewhere it takes
// a display that composites: without a compositor there is nothing behind the
// window for its see-through parts to show, so the setting could not do what its
// name promises.
//
// It takes its inputs as arguments so that it can be tested for every platform
// from any one of them; TransparencyAvailable supplies the real ones.
func transparencyAvailable(goos string, composited bool) (ok bool, why string) {
	if goos == "windows" {
		return false, "Not available on Windows"
	}
	if !composited {
		return false, "Needs a desktop that composites windows"
	}
	return true, ""
}

// TransparencyAvailable reports whether window transparency can work here, and
// when it cannot, why. No display at all counts as not compositing.
func TransparencyAvailable() (bool, string) {
	composited := false
	if d := gdk.DisplayGetDefault(); d != nil {
		composited = d.IsComposited()
	}
	return transparencyAvailable(runtime.GOOS, composited)
}
