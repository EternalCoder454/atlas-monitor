package theme

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var hex = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// TestEveryThemeIsComplete: a theme missing a piece does not fail, it draws
// wrong — an empty Primary is a transparent half-circle in the picker, and a
// missing Name is a blank label under it.
func TestEveryThemeIsComplete(t *testing.T) {
	if len(Themes) != 5 {
		t.Errorf("got %d themes, want 5", len(Themes))
	}
	seen := map[string]bool{}
	for _, th := range Themes {
		if th.ID == "" || th.Name == "" || th.Summary == "" {
			t.Errorf("%+v is missing an id, name or summary", th)
		}
		if seen[th.ID] {
			t.Errorf("two themes share the id %q; the settings file could not tell them apart", th.ID)
		}
		seen[th.ID] = true

		// Lower-case six-digit hex, because SwatchCSS pastes these straight into a
		// gradient and CSS that does not parse leaves a circle with no colour at all.
		for label, c := range map[string]string{"Primary": th.Primary, "Secondary": th.Secondary} {
			if !hex.MatchString(c) {
				t.Errorf("%s: %s is %q, want lower-case #rrggbb", th.ID, label, c)
			}
		}
		if th.Primary == th.Secondary {
			t.Errorf("%s: both halves of the circle are %s, so it is not a split circle", th.ID, th.Primary)
		}
	}
}

// TestOverrideColoursAreValid: every value ends up in CSS, and a malformed one
// takes the rest of the declaration block with it.
func TestOverrideColoursAreValid(t *testing.T) {
	for _, th := range Themes {
		for name, c := range th.colors {
			if !hex.MatchString(c) {
				t.Errorf("%s: %s is %q, want lower-case #rrggbb", th.ID, name, c)
			}
			if strings.ContainsAny(name, " -") {
				t.Errorf("%s: colour name %q should be libadwaita's underscore form", th.ID, name)
			}
		}
	}
}

// TestLightAndDarkCarryNoOverrides: they are libadwaita's own palettes. Listing
// colours for them would be transcribing values that the toolkit already has and
// that change when it does.
func TestLightAndDarkCarryNoOverrides(t *testing.T) {
	for _, id := range []string{"light", "dark"} {
		th, ok := ByID(id)
		if !ok {
			t.Fatalf("%s is missing", id)
		}
		if len(th.colors) != 0 {
			t.Errorf("%s overrides %d colours; it should be libadwaita's own", id, len(th.colors))
		}
		if th.CSS() != "" {
			t.Errorf("%s produced CSS", id)
		}
	}
}

// TestThemedCSSCarriesBothSyntaxes is the one that would have shipped broken.
//
// libadwaita reads different colours through different mechanisms: the window
// background comes from the named colour @window_bg_color, while its own rule for
// links is `color: var(--accent-color)`. Emitting one form and not the other
// re-themes about half the window and leaves links on libadwaita's blue, which is
// exactly what happened before both were emitted.
func TestThemedCSSCarriesBothSyntaxes(t *testing.T) {
	for _, th := range Themes {
		if len(th.colors) == 0 {
			continue
		}
		css := th.CSS()
		if !strings.Contains(css, ":root {") {
			t.Errorf("%s: no :root block, so nothing reading var(--accent-color) follows the theme", th.ID)
		}
		if !strings.Contains(css, "--accent-color:") {
			t.Errorf("%s: no --accent-color property; links would stay libadwaita's blue", th.ID)
		}
		if !strings.Contains(css, "@define-color accent_color ") {
			t.Errorf("%s: no @define-color accent_color; assets/style.css reads that form", th.ID)
		}
		if !strings.Contains(css, "@define-color window_bg_color ") {
			t.Errorf("%s: no @define-color window_bg_color", th.ID)
		}
		// Every colour appears in both forms, not just the two checked above.
		for name, value := range th.colors {
			variable := "--" + strings.ReplaceAll(name, "_", "-") + ": " + value + ";"
			named := "@define-color " + name + " " + value + ";"
			if !strings.Contains(css, variable) {
				t.Errorf("%s: %q missing from the :root block", th.ID, variable)
			}
			if !strings.Contains(css, named) {
				t.Errorf("%s: %q missing", th.ID, named)
			}
		}
	}
}

// TestResolveFallsBackToTheDesktop: an unset setting, and one naming a theme this
// build does not have — a settings file written by a newer version — both mean
// "follow the desktop" rather than a fixed theme.
func TestResolveFallsBackToTheDesktop(t *testing.T) {
	for _, id := range []string{Follow, "", "chartreuse", "Nord"} { // note: ids are case-sensitive
		if !IsFollowing(id) {
			t.Errorf("IsFollowing(%q) = false, want true", id)
		}
		if got := Resolve(id, true); got.ID != "dark" {
			t.Errorf("Resolve(%q, dark) = %q, want dark", id, got.ID)
		}
		if got := Resolve(id, false); got.ID != "light" {
			t.Errorf("Resolve(%q, light) = %q, want light", id, got.ID)
		}
	}

	// A known id is used whatever the desktop is set to: choosing one is the
	// point at which Atlas stops following.
	for _, id := range []string{"light", "dark", "nord", "ember", "sage"} {
		if IsFollowing(id) {
			t.Errorf("IsFollowing(%q) = true", id)
		}
		for _, systemDark := range []bool{true, false} {
			if got := Resolve(id, systemDark); got.ID != id {
				t.Errorf("Resolve(%q, %v) = %q", id, systemDark, got.ID)
			}
		}
	}
}

// TestSwatchCSSCoversEveryTheme: the circles are generated from the table so that
// adding a theme cannot leave its circle blank. That only holds if this stays true.
func TestSwatchCSSCoversEveryTheme(t *testing.T) {
	css := SwatchCSS()
	for _, th := range Themes {
		selector := ".am-swatch." + SwatchClass(th.ID)
		if !strings.Contains(css, selector) {
			t.Errorf("no rule for %s", selector)
		}
		if !strings.Contains(css, th.Primary) || !strings.Contains(css, th.Secondary) {
			t.Errorf("%s: its colours are not in the generated CSS", th.ID)
		}
	}
	// Both classes in the selector on purpose: one ties with Adwaita's own button
	// rules and the background-image is the whole widget.
	if strings.Contains(css, "\n.am-swatch-") {
		t.Error("a swatch rule uses one class, which does not outrank Adwaita's button styling")
	}
}

// TestThereIsAnAlternativeToBothDefaults: the three added themes are not much use
// if they are all dark, because then somebody who prefers a light window has
// nothing to move to.
func TestThereIsAnAlternativeToBothDefaults(t *testing.T) {
	var extraLight, extraDark int
	for _, th := range Themes {
		if th.ID == "light" || th.ID == "dark" {
			continue
		}
		if th.Dark {
			extraDark++
		} else {
			extraLight++
		}
	}
	if extraLight == 0 {
		t.Error("every added theme is dark; there is no alternative to Light")
	}
	if extraDark == 0 {
		t.Error("every added theme is light; there is no alternative to Dark")
	}
}

// TestDarkThemesAreActuallyDark checks the flag against the palette.
//
// The flag is not decoration: it decides the colour scheme, and internal/graph
// picks its chart palette from the resulting foreground. A theme with a dark
// background and Dark set false would draw pale lines on a pale chart.
func TestDarkThemesAreActuallyDark(t *testing.T) {
	for _, th := range Themes {
		lum := luminance(t, th.Primary)
		if th.Dark && lum > 0.5 {
			t.Errorf("%s is marked dark but its background %s is light (%.2f)", th.ID, th.Primary, lum)
		}
		if !th.Dark && lum < 0.5 {
			t.Errorf("%s is marked light but its background %s is dark (%.2f)", th.ID, th.Primary, lum)
		}
	}
}

// luminance is a rough perceived brightness, 0..1, from a #rrggbb string.
func luminance(t *testing.T, c string) float64 {
	t.Helper()
	n, err := strconv.ParseUint(c[1:], 16, 32)
	if err != nil {
		t.Fatalf("parsing %q: %v", c, err)
	}
	r, g, b := float64(n>>16&0xff), float64(n>>8&0xff), float64(n&0xff)
	return (0.299*r + 0.587*g + 0.114*b) / 255
}
