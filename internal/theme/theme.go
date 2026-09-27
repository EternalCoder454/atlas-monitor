// Package theme is Atlas's colour themes.
//
// Atlas used to take whatever the desktop was set to and follow it, which is the
// right default and the only behaviour anybody needs most of the time. This adds
// the choice: five themes, two of them the ordinary light and dark that libadwaita
// already provides, and three with palettes of their own.
//
// A theme is two things. It forces a light or dark colour scheme, which is what
// makes the widgets and the chart palette pick the right side — internal/graph
// decides from the foreground colour, so getting the scheme right is what keeps a
// pale line visible on a pale background. And it overrides libadwaita's named
// colours, which is where the palette actually comes from: the stylesheet in
// assets/style.css never names a colour of its own except in the graphs, so
// redefining the names is enough to re-theme the whole window.
//
// The overrides are written in both of libadwaita's syntaxes, because it uses
// both. Its stylesheet — extractable with
//
//	gresource extract /usr/lib64/libadwaita-1.so.0 /org/gnome/Adwaita/styles/gtk.css
//
// defines 55 CSS custom properties and over a hundred named colours, and different
// rules read different ones: the window background comes through @window_bg_color,
// while `link { color: var(--accent-color) }` reads the property. Overriding only
// one form re-themes about half the window. assets/style.css is written in the
// named-colour syntax, so that form is needed regardless.
package theme

import (
	"fmt"
	"sort"
	"strings"
)

// Theme is one palette.
type Theme struct {
	// ID is what goes in the settings file. It is never translated and never
	// changes, because a renamed one would silently reset everybody's choice.
	ID string
	// Name is what the picker shows.
	Name string
	// Summary is one short line about what it is for.
	Summary string

	// Dark says which colour scheme to force. It is not cosmetic: the chart
	// palette and several parts of the stylesheet key off whether the background
	// is dark, so a theme with a dark palette and a light scheme would draw
	// unreadable graphs.
	Dark bool

	// Primary and Secondary are the two halves of the circle in the picker: the
	// window background and the accent. Together they are a fair preview of what
	// choosing it does, which a single colour would not be.
	Primary   string
	Secondary string

	// colors are libadwaita's named colours to override, keyed by the name in its
	// underscore form — "window_bg_color". Empty for the two themes that are
	// libadwaita's own palette, where the scheme is the whole of the change.
	colors map[string]string
}

// Themes is every theme, in the order the picker shows them.
//
// Light and dark come first because they are what most people want and what the
// desktop itself offers. The other three are dark, warm-dark and light, so that
// whichever of the two ordinary ones somebody prefers, there is an alternative to
// it rather than three variations on the other one.
var Themes = []Theme{
	{
		ID:      "light",
		Name:    "Light",
		Summary: "libadwaita's own light palette",
		Dark:    false,
		// Adwaita's light window background, and its blue.
		Primary:   "#ffffff",
		Secondary: "#3584e4",
	},
	{
		ID:      "dark",
		Name:    "Dark",
		Summary: "libadwaita's own dark palette",
		Dark:    true,
		// Adwaita's dark window background, and the same blue.
		Primary:   "#242424",
		Secondary: "#3584e4",
	},
	{
		ID:      "nord",
		Name:    "Nord",
		Summary: "Cool blue-grey, low contrast",
		Dark:    true,
		// The Nord palette: polar night for the backgrounds, snow storm for text,
		// frost for the accent. Its whole point is that nothing in it is fully
		// saturated, which is easy to read for a long time and unusually kind to
		// a screen full of charts.
		Primary:   "#2e3440",
		Secondary: "#88c0d0",
		colors: map[string]string{
			"window_bg_color":    "#2e3440",
			"window_fg_color":    "#eceff4",
			"view_bg_color":      "#272c36",
			"view_fg_color":      "#eceff4",
			"sidebar_bg_color":   "#292e39",
			"sidebar_fg_color":   "#eceff4",
			"headerbar_bg_color": "#2e3440",
			"headerbar_fg_color": "#eceff4",
			"card_bg_color":      "#3b4252",
			"card_fg_color":      "#eceff4",
			"dialog_bg_color":    "#2e3440",
			"dialog_fg_color":    "#eceff4",
			"popover_bg_color":   "#3b4252",
			"popover_fg_color":   "#eceff4",
			"accent_bg_color":    "#5e81ac",
			"accent_fg_color":    "#eceff4",
			// accent_color is the one used for text and icons rather than for
			// filled buttons, so it has to carry against the background on its
			// own — the lighter frost shade, not the one the buttons use.
			"accent_color":  "#88c0d0",
			"warning_color": "#ebcb8b",
			"error_color":   "#bf616a",
			"success_color": "#a3be8c",
		},
	},
	{
		ID:      "ember",
		Name:    "Ember",
		Summary: "Warm dark, no blue light",
		Dark:    true,
		// Warm greys rather than neutral ones, and an amber accent, so there is
		// almost no blue in it. That is the point: this is the one to leave Atlas
		// open in at night.
		Primary:   "#1c1917",
		Secondary: "#e8913a",
		colors: map[string]string{
			"window_bg_color":    "#1c1917",
			"window_fg_color":    "#ede4dc",
			"view_bg_color":      "#231f1d",
			"view_fg_color":      "#ede4dc",
			"sidebar_bg_color":   "#211d1b",
			"sidebar_fg_color":   "#ede4dc",
			"headerbar_bg_color": "#1c1917",
			"headerbar_fg_color": "#ede4dc",
			"card_bg_color":      "#2a2523",
			"card_fg_color":      "#ede4dc",
			"dialog_bg_color":    "#231f1d",
			"dialog_fg_color":    "#ede4dc",
			"popover_bg_color":   "#2a2523",
			"popover_fg_color":   "#ede4dc",
			"accent_bg_color":    "#b86f28",
			"accent_fg_color":    "#ffffff",
			"accent_color":       "#e8913a",
			"warning_color":      "#e0a458",
			"error_color":        "#d4675a",
			"success_color":      "#a3a55c",
		},
	},
	{
		ID:      "sage",
		Name:    "Sage",
		Summary: "Light, warm paper and green",
		Dark:    false,
		// The light alternative. Paper rather than white, which takes the glare
		// off a bright room without going grey, and a muted green accent that
		// does not fight the chart colours.
		Primary:   "#f4f3ec",
		Secondary: "#5c8a5e",
		colors: map[string]string{
			"window_bg_color":    "#f4f3ec",
			"window_fg_color":    "#2d3a2e",
			"view_bg_color":      "#fbfaf5",
			"view_fg_color":      "#2d3a2e",
			"sidebar_bg_color":   "#eae9df",
			"sidebar_fg_color":   "#2d3a2e",
			"headerbar_bg_color": "#eae9df",
			"headerbar_fg_color": "#2d3a2e",
			"card_bg_color":      "#ffffff",
			"card_fg_color":      "#2d3a2e",
			"dialog_bg_color":    "#fbfaf5",
			"dialog_fg_color":    "#2d3a2e",
			"popover_bg_color":   "#ffffff",
			"popover_fg_color":   "#2d3a2e",
			"accent_bg_color":    "#4e7850",
			"accent_fg_color":    "#ffffff",
			"accent_color":       "#3f6641",
			"warning_color":      "#a1661b",
			"error_color":        "#b3392f",
			"success_color":      "#3f6641",
		},
	},
}

// Follow is the setting's value when no theme has been chosen: Atlas tracks the
// desktop, which is what it did before any of this existed. Choosing a theme
// replaces it, and there is no way back to it from the picker on purpose — a
// sixth circle for "whatever the desktop says" would be a different kind of thing
// from the five beside it.
const Follow = ""

// ByID returns a theme by its settings value.
func ByID(id string) (Theme, bool) {
	for _, t := range Themes {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

// Resolve turns a setting into the theme to actually draw.
//
// An unknown ID resolves the same way an unset one does rather than falling back
// to a fixed theme: a settings file from a newer version naming a theme this build
// does not have should leave the app looking like the desktop, not force it light.
func Resolve(id string, systemDark bool) Theme {
	if t, ok := ByID(id); ok {
		return t
	}
	if systemDark {
		dark, _ := ByID("dark")
		return dark
	}
	light, _ := ByID("light")
	return light
}

// IsFollowing reports whether a setting means "track the desktop".
func IsFollowing(id string) bool {
	_, ok := ByID(id)
	return !ok
}

// CSS is the colour overrides for this theme, or "" for the two that are
// libadwaita's own palette.
func (t Theme) CSS() string {
	if len(t.colors) == 0 {
		return ""
	}
	return colorCSS(t.colors)
}

// colorCSS writes one set of overrides.
//
// Sorted, so the output is stable and a change to it is readable in a diff.
func colorCSS(colors map[string]string) string {
	names := make([]string, 0, len(colors))
	for name := range colors {
		names = append(names, name)
	}
	sort.Strings(names) // stable output, so the CSS is diffable

	var b strings.Builder

	// The custom properties. libadwaita's own rules read several colours only
	// through these — links are `var(--accent-color)` — so a theme that set the
	// named colours alone would leave them on libadwaita's blue.
	b.WriteString(":root {\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  --%s: %s;\n", strings.ReplaceAll(name, "_", "-"), colors[name])
	}
	b.WriteString("}\n")

	// And the named colours, which are what assets/style.css is written in and
	// what most of libadwaita's own rules still use.
	for _, name := range names {
		fmt.Fprintf(&b, "@define-color %s %s;\n", name, colors[name])
	}
	return b.String()
}

// SwatchCSS is the styling for every theme's circle in the picker.
//
// It is generated from the table rather than written in assets/style.css so that
// adding a theme cannot leave its circle the wrong colour, or absent. Each is a
// disc split on the diagonal: background on one side, accent on the other.
func SwatchCSS() string {
	var b strings.Builder
	b.WriteString("/* Generated by internal/theme. See SwatchCSS. */\n")
	for _, t := range Themes {
		// Both classes in the selector on purpose. A single class ties with
		// Adwaita's own `.toggle` rules and loses to anything more specific, and
		// the background-image is the whole point of the widget.
		fmt.Fprintf(&b, ".am-swatch.%s {\n", SwatchClass(t.ID))
		// A hard stop at the midpoint rather than a blend: the two colours are
		// two facts about the theme, and a gradient between them would invent a
		// third that is not in it.
		fmt.Fprintf(&b, "  background-image: linear-gradient(135deg, %s 0%%, %s 50%%, %s 50%%, %s 100%%);\n",
			t.Primary, t.Primary, t.Secondary, t.Secondary)
		b.WriteString("}\n")
	}
	return b.String()
}

// SwatchClass is the CSS class carrying one theme's colours.
func SwatchClass(id string) string { return "am-swatch-" + id }
