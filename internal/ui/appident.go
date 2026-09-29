package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/desktop"
)

// appIdent is an application as the Apps and Energy Saver pages show it: the
// name people know it by and its icon. See internal/desktop for where both come
// from.
type appIdent struct {
	id   string
	name string
	// icon is an icon name the theme has, an absolute path, or "" for none.
	icon     string
	terminal bool
}

// appResolver turns a process's systemd unit into its application, caching the
// answer per unit: a unit's application never changes, a desktop has a few dozen
// of them, and the Apps table asks about every visible row every second.
//
// Only the UI thread uses one. The desktop index underneath is shared and locked.
type appResolver struct {
	index  *desktop.Index
	byUnit map[string]*appIdent // a nil value means "not an application"
	// hasIcon asks the icon theme. Nil in tests, and when there is no display,
	// in which case every named icon is taken on trust.
	hasIcon func(string) bool
}

func newAppResolver(index *desktop.Index) *appResolver {
	r := &appResolver{index: index, byUnit: map[string]*appIdent{}}
	if display := gdk.DisplayGetDefault(); display != nil {
		theme := gtk.IconThemeGetForDisplay(display)
		addFlatpakIconDirs(theme)
		r.hasIcon = theme.HasIcon
	}
	return r
}

// of returns the application a unit belongs to, or nil when the unit is not an
// application's — a system service, the session, a kernel thread's empty unit.
func (r *appResolver) of(unit string) *appIdent {
	if r == nil || unit == "" {
		return nil
	}
	if a, ok := r.byUnit[unit]; ok {
		return a
	}
	id, ok := desktop.AppID(unit)
	if !ok {
		r.byUnit[unit] = nil
		return nil
	}
	a := &appIdent{id: id, name: desktop.FallbackName(id)}
	e, found := r.index.Lookup(id)
	if found {
		a.name, a.terminal = e.Name, e.Terminal
	}
	a.icon = r.pickIcon(e.Icon, id, a.name)
	r.byUnit[unit] = a
	return a
}

// pickIcon chooses the first icon that will actually draw: the desktop file's,
// then the application ID (which is what Flatpak apps name their icons), then
// the plain lower-case name ("chromium" for org.chromium.Chromium, which runs
// without a desktop file when a browser-automation tool starts it).
func (r *appResolver) pickIcon(declared, id, name string) string {
	if filepath.IsAbs(declared) {
		if _, err := os.Stat(declared); err == nil {
			return declared
		}
		declared = ""
	}
	for _, c := range []string{declared, id, strings.ToLower(name)} {
		if c == "" {
			continue
		}
		if r.hasIcon == nil || r.hasIcon(c) {
			return c
		}
	}
	return ""
}

// addFlatpakIconDirs puts Flatpak's exported icons on the theme's search path
// when the session did not, for the same reason desktop.DataDirs adds its
// applications: started from somewhere that never read Flatpak's profile
// script, Atlas would find Discord's name and not its icon.
func addFlatpakIconDirs(theme *gtk.IconTheme) {
	have := map[string]bool{}
	for _, p := range theme.SearchPath() {
		have[filepath.Clean(p)] = true
	}
	for _, d := range desktop.DataDirs() {
		if !strings.Contains(d, "flatpak") {
			continue
		}
		icons := filepath.Join(d, "icons")
		if have[icons] {
			continue
		}
		if st, err := os.Stat(icons); err == nil && st.IsDir() {
			theme.AddSearchPath(icons)
		}
	}
}
