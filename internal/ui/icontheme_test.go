package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// atlasIcons is every icon the app asks GTK for by name. Keeping the list here
// rather than deriving it means a rename has to be made in two places, which is
// the point: the other place is the code that would silently show a broken-image
// glyph instead.
var atlasIcons = []string{
	"atlas-cpu-symbolic",
	"atlas-memory-symbolic",
	"atlas-disk-symbolic",
	"atlas-gpu-symbolic",
	"atlas-assistant-symbolic",
	"atlas-network-symbolic",
	"atlas-wifi-symbolic",
	"atlas-battery-symbolic",
	"atlas-apps-symbolic",
	"atlas-services-symbolic",
	"atlas-settings-symbolic",
	"atlas-prompts-symbolic",
	"atlas-update-symbolic",
	"atlas-trash-symbolic",
	"atlas-reset-symbolic",
	"atlas-menu-symbolic",
}

// TestIconThemeResolvesEveryIcon checks GTK can actually find the icons by name
// once they are installed.
//
// This is the half that static checks cannot cover. An icon file can be perfectly
// well-formed and still never appear: installed under the wrong name, missing
// from the Makefile's list, or — the one that bit this project's design — given a
// generic name that the system theme also defines, in which case the system's
// wins because hicolor is searched last. GTK does not complain about any of
// that; it quietly draws a broken-image glyph.
//
// The icons live in the user's installed icon theme, not in the repository, so
// this can only run on a machine where `make install` has been run. When none of
// them resolve the test skips rather than failing, because that means they are
// not installed rather than broken — but if some resolve and others do not, that
// is a real fault and it fails.
func TestIconThemeResolvesEveryIcon(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("needs a display to open an icon theme")
	}
	if !gtk.InitCheck() {
		t.Skip("GTK could not initialise")
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		t.Skip("no display")
	}
	theme := gtk.IconThemeGetForDisplay(display)
	if theme == nil {
		t.Skip("no icon theme")
	}

	var found, missing []string
	for _, name := range atlasIcons {
		if theme.HasIcon(name) {
			found = append(found, name)
		} else {
			missing = append(missing, name)
		}
	}

	switch {
	case len(found) == 0:
		t.Skipf("none of the %d icons are installed; run `make install` to check them", len(atlasIcons))
	case len(missing) > 0:
		t.Errorf("%d of %d icons are installed but these are not: %s\n"+
			"an icon GTK cannot resolve is drawn as a broken image, silently",
			len(found), len(atlasIcons), strings.Join(missing, ", "))
	default:
		t.Logf("all %d icons resolve through the icon theme", len(found))
	}
}
