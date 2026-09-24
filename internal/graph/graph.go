// Package graph is a reusable GtkDrawingArea + Cairo widget that renders a live
// 60-sample ring buffer as a filled area chart with a line on top.
package graph

import (
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/stats"
)

// Mode selects the Y-axis scaling and value formatting.
type Mode int

const (
	// Percent fixes the Y-axis to 0..100 and formats values as "NN%".
	Percent Mode = iota
	// Bytes auto-scales the Y-axis and formats values as byte rates.
	Bytes
	// Watts auto-scales the Y-axis and formats values as a power draw.
	Watts
)

// autoScaled reports whether the mode picks its Y-axis from the data rather
// than pinning it to 0..100.
func (m Mode) autoScaled() bool { return m == Bytes || m == Watts }

// Graph is a single live chart bound to a ring buffer.
type Graph struct {
	*gtk.DrawingArea
	label   string
	color   Color
	rb      *stats.RingBuffer
	mode    Mode
	scratch []float64 // pre-allocated read buffer, never grows

	// Current-value text, cached so a steady reading costs no allocation.
	valBuf  []byte
	valText string
	peakBuf []byte

	// Text is drawn through Pango rather than Cairo's "toy" API, so the chart
	// labels use the desktop font at the desktop's hinting settings instead of
	// whatever cairo_select_font_face picks. The layouts are built once from the
	// widget and reused: creating one per frame would both re-shape the text and
	// churn a gotk4 wrapper each time. A font change at runtime needs a restart
	// to take effect here, which is the same deal the rendering mode has.
	labelLayout *pango.Layout
	valueLayout *pango.Layout
	peakLayout  *pango.Layout
	shownValue  string
	shownPeak   string
}

// New builds a graph for the given ring buffer. height is the requested
// content height in pixels.
func New(label string, color Color, rb *stats.RingBuffer, mode Mode, height int) *Graph {
	g := &Graph{
		DrawingArea: gtk.NewDrawingArea(),
		label:       label,
		color:       color,
		rb:          rb,
		mode:        mode,
		scratch:     make([]float64, stats.HistLen),
	}
	// Deliberately a fixed height rather than one that grows with the window.
	// Letting the charts expand was tried and is worse: a percentage chart draws
	// its line at the reading, so on an idle machine a taller chart is simply a
	// larger empty area, and on the Storage page the growth pushed the details
	// below the bottom edge. What the space needed was meaning, not more of it —
	// hence the peak marker on the auto-scaled charts.
	g.SetContentHeight(height)
	g.SetHExpand(true)
	g.AddCSSClass("am-graph")
	g.SetDrawFunc(g.draw)
	return g
}

// Refresh requests a redraw. Call from the GTK main thread.
func (g *Graph) Refresh() { g.QueueDraw() }

func (g *Graph) draw(area *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	if g.rb == nil || w <= 0 || h <= 0 {
		return
	}
	width, height := float64(w), float64(h)

	fr, fg, fb := Foreground(area)

	n := g.rb.ReadInto(g.scratch)

	// Determine vertical scale.
	scale := 100.0
	if g.mode.autoScaled() {
		scale = g.rb.Max() * 1.25
		if scale < 1 {
			scale = 1
		}
	}

	// Horizontal grid lines (subtle).
	cr.SetLineWidth(1)
	cr.SetSourceRGBA(fr, fg, fb, 0.10)
	for i := 0; i <= 4; i++ {
		y := height * float64(i) / 4
		cr.MoveTo(0, y)
		cr.LineTo(width, y)
	}
	cr.Stroke()

	if n >= 2 {
		dx := width / float64(stats.HistLen-1)
		px := func(i int) float64 { return width - dx*float64(n-1-i) }
		py := func(v float64) float64 {
			r := v / scale
			if r > 1 {
				r = 1
			}
			if r < 0 {
				r = 0
			}
			return height - r*height
		}

		// Filled area under the curve, fading out towards the baseline. A flat
		// wash reads as a solid block on a chart that is mostly empty — which a
		// percentage chart on an idle machine always is — where a gradient reads
		// as depth under the line.
		cr.MoveTo(px(0), height)
		for i := 0; i < n; i++ {
			cr.LineTo(px(i), py(g.scratch[i]))
		}
		cr.LineTo(px(n-1), height)
		cr.ClosePath()
		if grad, err := cairo.NewPatternLinear(0, 0, 0, height); err == nil {
			grad.AddColorStopRGBA(0, g.color.R, g.color.G, g.color.B, 0.34)
			grad.AddColorStopRGBA(1, g.color.R, g.color.G, g.color.B, 0.02)
			cr.SetSource(grad)
			cr.Fill()
		} else {
			cr.SetSourceRGBA(g.color.R, g.color.G, g.color.B, 0.20)
			cr.Fill()
		}

		// Solid line on top.
		cr.SetSourceRGBA(g.color.R, g.color.G, g.color.B, 1)
		cr.SetLineWidth(2)
		cr.MoveTo(px(0), py(g.scratch[0]))
		for i := 1; i < n; i++ {
			cr.LineTo(px(i), py(g.scratch[i]))
		}
		cr.Stroke()
	}

	if g.labelLayout == nil {
		g.labelLayout = area.CreatePangoLayout(g.label)
		g.valueLayout = area.CreatePangoLayout("")
		g.peakLayout = area.CreatePangoLayout("")
	}

	// Label, top-left.
	if g.label != "" {
		cr.SetSourceRGBA(fr, fg, fb, 0.66)
		cr.MoveTo(8, 5)
		pangocairo.ShowLayout(cr, g.labelLayout)
	}

	// Current value, top-right. Pango measures the text, so it lands where it
	// should instead of where a character-count estimate guessed.
	cur := 0.0
	if n > 0 {
		cur = g.scratch[n-1]
	}
	text := g.formatValue(cur)
	if text != g.shownValue {
		g.valueLayout.SetText(text)
		g.shownValue = text
	}
	tw, th := g.valueLayout.PixelSize()
	cr.SetSourceRGBA(fr, fg, fb, 0.92)
	cr.MoveTo(width-float64(tw)-8, 5)
	pangocairo.ShowLayout(cr, g.valueLayout)

	// On an auto-scaled chart the vertical axis means nothing on its own: the
	// same picture describes kilobytes and gigabytes. The highest reading still
	// on screen says what the height is worth, and gives the empty space above
	// an idle line something to say. A percentage chart needs no such note —
	// its axis is always nought to a hundred.
	if g.mode.autoScaled() && n > 0 {
		if peak := g.rb.Max(); peak > 0 {
			text := "peak " + string(g.formatInto(peak))
			if text != g.shownPeak {
				g.peakLayout.SetText(text)
				g.shownPeak = text
			}
			pw, _ := g.peakLayout.PixelSize()
			cr.SetSourceRGBA(fr, fg, fb, 0.45)
			cr.MoveTo(width-float64(pw)-8, 5+float64(th))
			pangocairo.ShowLayout(cr, g.peakLayout)
		}
	}
}

// formatInto renders v the way this chart formats its readings, into a scratch
// buffer kept apart from the current-value one so the two cannot clobber each
// other mid-draw.
func (g *Graph) formatInto(v float64) []byte {
	switch g.mode {
	case Bytes:
		g.peakBuf = format.AppendRate(g.peakBuf[:0], v)
	case Watts:
		g.peakBuf = format.AppendWatts(g.peakBuf[:0], v)
	default:
		g.peakBuf = format.AppendPercent(g.peakBuf[:0], v)
	}
	return g.peakBuf
}

// formatValue renders v into the graph's scratch buffer and returns the text,
// reusing the previous string whenever the reading is unchanged (the common
// case for an idle interface or a pinned percentage).
func (g *Graph) formatValue(v float64) string {
	switch g.mode {
	case Bytes:
		g.valBuf = format.AppendRate(g.valBuf[:0], v)
	case Watts:
		g.valBuf = format.AppendWatts(g.valBuf[:0], v)
	default:
		g.valBuf = format.AppendPercent(g.valBuf[:0], v)
	}
	if g.valText != string(g.valBuf) { // compares without allocating
		g.valText = string(g.valBuf)
	}
	return g.valText
}

// Foreground returns the widget's themed text colour, used for grid lines and
// labels. gtk_widget_get_color is a plain struct read; the GtkStyleContext it
// replaces is deprecated and allocated a wrapper object on every frame.
func Foreground(w *gtk.DrawingArea) (r, g, b float64) {
	c := w.Color()
	if c == nil {
		return 0.5, 0.5, 0.5
	}
	return float64(c.Red()), float64(c.Green()), float64(c.Blue())
}
