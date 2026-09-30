package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The colours the tables and lists draw with: the upright dividers between
// columns, the fainter lines between rows, and the four shades a busy cell
// takes.
//
// All of them used to be fixed opacities of the text or accent colour, and a
// fixed opacity is not a fixed strength. Seven per cent of Dracula's text over
// its blue-grey all but vanished; the same share of dark text on a light
// background is noticeably heavier than on a dark one; and Nord's pale accent
// at 62% over its dark background left light text on the hottest cells at
// 3.2:1. So each is worked out from the colours it is drawn with instead.
//
// Lines: the colour partway from the background towards the text that reaches a
// set contrast against the background. The targets are lower for light themes
// on purpose. On white, a line of the same measured contrast reads as heavier
// than it does on black, and a light theme that looks ruled like a ledger has
// lost the airiness it was chosen for. Row lines are always fainter than the
// dividers: the columns are the structure, and the rows only have to be
// followed across.
//
// Heat: the accent at four rising opacities, scaled down together — so the four
// steps stay four — as far as it takes for the theme's text to keep WCAG's
// 4.5:1 on the hottest one.

// lineTarget is the contrast against the background each kind of line reaches.
type lineTarget struct{ divider, row float64 }

var (
	darkLines  = lineTarget{divider: 1.55, row: 1.22}
	lightLines = lineTarget{divider: 1.28, row: 1.11}
)

// heatAlphas are the accent's opacity for the resting tint and the three busy
// steps, before any scaling for contrast.
var heatAlphas = [4]float64{0.10, 0.24, 0.42, 0.62}

// minTextContrast is what the text on the hottest cell has to keep.
const minTextContrast = 4.5

// libadwaita's own window colours and default accent, which the Light and Dark
// themes use as they are: #fafafb with text of 80% near-black, and #222226 with
// white. The light text is given here already blended onto its background.
const (
	adwLightBG = "#fafafb"
	adwLightFG = "#323237"
	adwDarkBG  = "#222226"
	adwDarkFG  = "#ffffff"
	adwAccent  = "#3584e4"
)

// faintLine is what a line falls back to when a colour cannot be read: a
// quiet one, never the full-strength text colour.
const faintLine = "color-mix(in srgb, currentColor 12%, transparent)"

// LineColors returns the divider and row-line colours for the theme.
func (t Theme) LineColors() (divider, row string) {
	bg, fg := t.windowColors()
	target := lightLines
	if t.Dark {
		target = darkLines
	}
	return lineTowards(bg, fg, target.divider), lineTowards(bg, fg, target.row)
}

// windowColors is the theme's window background and text, libadwaita's own for
// the two themes that do not set them.
func (t Theme) windowColors() (bg, fg string) {
	bg, fg = t.colors["window_bg_color"], t.colors["window_fg_color"]
	if bg == "" || fg == "" {
		bg, fg = adwLightBG, adwLightFG
		if t.Dark {
			bg, fg = adwDarkBG, adwDarkFG
		}
	}
	return bg, fg
}

// tableCSS is the custom properties the stylesheet draws the tables with, for a
// theme whose accent is accent.
func (t Theme) tableCSS(accent string) string {
	if a := t.colors["accent_bg_color"]; a != "" {
		accent = a
	}
	if accent == "" {
		accent = adwAccent
	}
	d, r := t.LineColors()
	bg, fg := t.windowColors()
	var b strings.Builder
	b.WriteString(":root {\n")
	fmt.Fprintf(&b, "  --am-divider: %s;\n  --am-rowline: %s;\n", d, r)
	for i, c := range heatColors(bg, fg, accent) {
		fmt.Fprintf(&b, "  --am-heat-%d: %s;\n", i, c)
	}
	b.WriteString("}\n")
	return b.String()
}

// FollowCSS is the table colours for following the desktop, where no theme is
// forced: libadwaita's Light or Dark, with the desktop's own accent colour.
// The application reloads it whenever either changes.
func FollowCSS(dark bool, accent string) string {
	id := "light"
	if dark {
		id = "dark"
	}
	t, _ := ByID(id)
	return t.tableCSS(accent)
}

// heatColors is the accent at each of heatAlphas, scaled so the text keeps
// minTextContrast on the strongest, as rgba() so the shading stays translucent
// over a see-through window.
func heatColors(bg, fg, accent string) [4]string {
	var out [4]string
	b, errB := parseHex(bg)
	f, errF := parseHex(fg)
	a, errA := parseHex(accent)
	if errB != nil || errF != nil || errA != nil {
		for i, alpha := range heatAlphas {
			out[i] = fmt.Sprintf("color-mix(in srgb, currentColor %d%%, transparent)", int(alpha*25))
		}
		return out
	}
	scale := 1.0
	top := heatAlphas[len(heatAlphas)-1]
	if contrastRatio(f, mix(b, a, top)) < minTextContrast {
		lo, hi := 0.0, 1.0
		for i := 0; i < 30; i++ {
			mid := (lo + hi) / 2
			if contrastRatio(f, mix(b, a, top*mid)) >= minTextContrast {
				lo = mid
			} else {
				hi = mid
			}
		}
		scale = lo
	}
	for i, alpha := range heatAlphas {
		out[i] = fmt.Sprintf("rgba(%d, %d, %d, %.3f)",
			int(math.Round(a.r)), int(math.Round(a.g)), int(math.Round(a.b)), alpha*scale)
	}
	return out
}

// HexOf formats a colour given as 0..1 channels, the way GTK hands them over.
func HexOf(r, g, b float32) string {
	return toHex(rgb{float64(r) * 255, float64(g) * 255, float64(b) * 255})
}

// lineTowards finds the colour between bg and fg, as close to bg as it can be,
// whose contrast against bg is at least target. It searches the blend rather
// than solving for it, because contrast is not linear in the blend.
func lineTowards(bg, fg string, target float64) string {
	b, errB := parseHex(bg)
	f, errF := parseHex(fg)
	if errB != nil || errF != nil {
		return faintLine
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 30; i++ {
		mid := (lo + hi) / 2
		if contrastRatio(mix(b, f, mid), b) >= target {
			hi = mid
		} else {
			lo = mid
		}
	}
	return toHex(mix(b, f, hi))
}

type rgb struct{ r, g, b float64 } // 0..255

func parseHex(s string) (rgb, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return rgb{}, fmt.Errorf("not a #rrggbb colour: %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgb{}, err
	}
	return rgb{float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)}, nil
}

func toHex(c rgb) string {
	clamp := func(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(255, v)))) }
	return fmt.Sprintf("#%02x%02x%02x", clamp(c.r), clamp(c.g), clamp(c.b))
}

// mix is a blend of a towards b by t, in sRGB, the way the stylesheet's own
// mixes and a colour drawn at partial opacity both blend.
func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// contrastRatio is WCAG 2.1's: (lighter + 0.05) / (darker + 0.05).
func contrastRatio(a, b rgb) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relLuminance(c rgb) float64 {
	ch := func(v float64) float64 {
		v /= 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.r) + 0.7152*ch(c.g) + 0.0722*ch(c.b)
}
