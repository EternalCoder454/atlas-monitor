package theme

import (
	"fmt"
	"strings"
	"testing"
)

// TestLinesAreSizedToEachTheme: every theme's lines reach their contrast
// targets against its own background, the row lines stay fainter than the
// dividers, and a light theme's lines are softer than a dark theme's — which is
// the point of working them out rather than using one opacity everywhere.
func TestLinesAreSizedToEachTheme(t *testing.T) {
	for _, th := range Themes {
		d, r := th.LineColors()
		bgHex, _ := th.windowColors()
		bg, _ := parseHex(bgHex)
		dc, _ := parseHex(d)
		rc, _ := parseHex(r)
		want := lightLines
		if th.Dark {
			want = darkLines
		}
		if got := contrastRatio(dc, bg); got < want.divider-0.01 || got > want.divider+0.05 {
			t.Errorf("%s: divider %s is %.2f:1 against %s, want about %.2f", th.ID, d, got, bgHex, want.divider)
		}
		if got := contrastRatio(rc, bg); got < want.row-0.01 || got > want.row+0.05 {
			t.Errorf("%s: row line %s is %.2f:1 against %s, want about %.2f", th.ID, r, got, bgHex, want.row)
		}
		if contrastRatio(rc, bg) >= contrastRatio(dc, bg) {
			t.Errorf("%s: row lines (%s) are not fainter than dividers (%s)", th.ID, r, d)
		}
	}
	if lightLines.divider >= darkLines.divider || lightLines.row >= darkLines.row {
		t.Error("light themes' lines should be softer than dark themes'")
	}
}

// TestUnreadableColourFallsBackFaint: a colour the parser cannot read must not
// turn every line into the full-strength text colour.
func TestUnreadableColourFallsBackFaint(t *testing.T) {
	for _, bad := range [][2]string{{"#fff", "#000000"}, {"#000000", "rgba(0,0,0,0.8)"}, {"", ""}} {
		if got := lineTowards(bad[0], bad[1], 1.5); got != faintLine {
			t.Errorf("lineTowards(%q, %q) = %q, want the faint fallback", bad[0], bad[1], got)
		}
	}
}

// heatAlpha reads the opacity back out of one of heatColors' rgba() values.
func heatAlpha(t *testing.T, c string) (r, g, b int, a float64) {
	t.Helper()
	if _, err := fmt.Sscanf(c, "rgba(%d, %d, %d, %f)", &r, &g, &b, &a); err != nil {
		t.Fatalf("heat colour %q is not rgba(): %v", c, err)
	}
	return
}

// TestHeatKeepsTextReadable: on every theme, and on libadwaita's Light and Dark
// with each of the desktop's accent colours, the text on every heat step keeps
// 4.5:1, and the four steps stay four distinct, rising shades.
func TestHeatKeepsTextReadable(t *testing.T) {
	// libadwaita's accent choices, as the desktop offers them.
	accents := []string{"#3584e4", "#2190a4", "#3a944a", "#c88800", "#ed5b00", "#e62d42", "#d56199", "#9141ac", "#6f8396"}
	type tc struct{ name, bg, fg, accent string }
	var cases []tc
	for _, th := range Themes {
		bg, fg := th.windowColors()
		accent := th.colors["accent_bg_color"]
		if accent == "" {
			for _, a := range accents {
				cases = append(cases, tc{th.ID + "/" + a, bg, fg, a})
			}
			continue
		}
		cases = append(cases, tc{th.ID, bg, fg, accent})
	}
	for _, c := range cases {
		b, _ := parseHex(c.bg)
		f, _ := parseHex(c.fg)
		prev := 0.0
		for i, col := range heatColors(c.bg, c.fg, c.accent) {
			r, g, bl, a := heatAlpha(t, col)
			blend := mix(b, rgb{float64(r), float64(g), float64(bl)}, a)
			if got := contrastRatio(f, blend); got < minTextContrast-0.02 {
				t.Errorf("%s: text on heat step %d (%s) is %.2f:1, want at least %.1f", c.name, i, col, got, minTextContrast)
			}
			if a <= prev {
				t.Errorf("%s: heat step %d (alpha %.3f) is not stronger than the one before (%.3f)", c.name, i, a, prev)
			}
			prev = a
		}
	}
}

// TestFollowCSSTracksTheDesktop: following the desktop loads Light's or Dark's
// table colours, with the desktop's accent in the heat shades.
func TestFollowCSSTracksTheDesktop(t *testing.T) {
	light, _ := ByID("light")
	dark, _ := ByID("dark")
	ld, _ := light.LineColors()
	dd, _ := dark.LineColors()
	if css := FollowCSS(false, "#9141ac"); !strings.Contains(css, ld) || !strings.Contains(css, "rgba(145, 65, 172,") {
		t.Errorf("light, purple accent:\n%s", css)
	}
	if css := FollowCSS(true, "#3584e4"); !strings.Contains(css, dd) || !strings.Contains(css, "rgba(53, 132, 228,") {
		t.Errorf("dark, blue accent:\n%s", css)
	}
	for _, v := range []string{"--am-divider", "--am-rowline", "--am-heat-0", "--am-heat-3"} {
		if !strings.Contains(FollowCSS(true, ""), v) {
			t.Errorf("follow CSS is missing %s", v)
		}
	}
}

// TestUnreadableAccentFallsBackToATint: an accent the parser cannot read gives
// a quiet tint of the text colour for the heat shades rather than nothing.
func TestUnreadableAccentFallsBackToATint(t *testing.T) {
	for i, c := range heatColors("#222226", "#ffffff", "rgba(1,2,3,0.5)") {
		if !strings.Contains(c, "color-mix") {
			t.Errorf("heat step %d = %q, want the color-mix fallback", i, c)
		}
	}
}
