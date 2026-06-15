// Package graph is a reusable GtkDrawingArea + Cairo widget that renders a live
// 60-sample ring buffer as a filled area chart with a line on top.
package graph

import (
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

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
)

// Graph is a single live chart bound to a ring buffer.
type Graph struct {
	*gtk.DrawingArea
	label   string
	color   Color
	rb      *stats.RingBuffer
	mode    Mode
	scratch []float64 // pre-allocated read buffer, never grows
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

	// Theme foreground colour for grid lines and text.
	fr, fg, fb := 0.5, 0.5, 0.5
	if sc := area.StyleContext(); sc != nil {
		c := sc.Color()
		fr, fg, fb = float64(c.Red()), float64(c.Green()), float64(c.Blue())
	}

	n := g.rb.ReadInto(g.scratch)

	// Determine vertical scale.
	scale := 100.0
	if g.mode == Bytes {
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

		// Filled area under the curve.
		cr.MoveTo(px(0), height)
		for i := 0; i < n; i++ {
			cr.LineTo(px(i), py(g.scratch[i]))
		}
		cr.LineTo(px(n-1), height)
		cr.ClosePath()
		cr.SetSourceRGBA(g.color.R, g.color.G, g.color.B, 0.20)
		cr.Fill()

		// Solid line on top.
		cr.SetSourceRGBA(g.color.R, g.color.G, g.color.B, 1)
		cr.SetLineWidth(2)
		cr.MoveTo(px(0), py(g.scratch[0]))
		for i := 1; i < n; i++ {
			cr.LineTo(px(i), py(g.scratch[i]))
		}
		cr.Stroke()
	}

	cr.SelectFontFace("sans-serif", cairo.FontSlantNormal, cairo.FontWeightNormal)
	cr.SetFontSize(11)

	// Label, top-left.
	if g.label != "" {
		cr.SetSourceRGBA(fr, fg, fb, 0.6)
		cr.MoveTo(6, 15)
		cr.ShowText(g.label)
	}

	// Current value, top-right (width estimated to avoid TextExtents).
	cur := 0.0
	if n > 0 {
		cur = g.scratch[n-1]
	}
	text := g.formatValue(cur)
	cr.SetSourceRGBA(fr, fg, fb, 0.85)
	cr.MoveTo(width-float64(len(text))*6.5-6, 15)
	cr.ShowText(text)
}

func (g *Graph) formatValue(v float64) string {
	if g.mode == Bytes {
		return format.Rate(v)
	}
	return format.Percent(v)
}
