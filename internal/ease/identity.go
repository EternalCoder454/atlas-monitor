package ease

import "atlas-monitor/internal/desktop"

// DesktopIdentity is the Identity the real controller uses: a unit's
// application ID, the name its desktop file gives it, and whether that file
// calls it a terminal emulator.
func DesktopIdentity(unit string) (id, name string, terminal, ok bool) {
	id, ok = desktop.AppID(unit)
	if !ok {
		return "", "", false, false
	}
	name = desktop.FallbackName(id)
	if e, found := desktop.Default().Lookup(id); found {
		name, terminal = e.Name, e.Terminal
	}
	return id, name, terminal, true
}
