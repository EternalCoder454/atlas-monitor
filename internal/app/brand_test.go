package app

import "testing"

// TestDecorationShowsIcon: only an "icon" among the buttons at the start of the
// title bar means GTK draws the window icon where Atlas's name goes.
func TestDecorationShowsIcon(t *testing.T) {
	cases := map[string]bool{
		"icon:minimize,maximize,close": true, // KDE's default
		"appmenu:close":                false,
		":minimize,maximize,close":     false,
		"close,minimize:icon":          false, // at the end, not the start
		"menu, icon:close":             true,
		"":                             false,
		"icon":                         true, // no colon: all of it is the start
	}
	for layout, want := range cases {
		if got := decorationShowsIcon(layout); got != want {
			t.Errorf("decorationShowsIcon(%q) = %v, want %v", layout, got, want)
		}
	}
}
