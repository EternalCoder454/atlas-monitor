package ui

import (
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/graph"
)

// capSeg is one slice of a capacity bar: how much, in what colour, at what
// strength. Segments are drawn in order from the left.
type capSeg struct {
	val   float64
	color graph.Color
	alpha float64
}

// capacityBar is a rounded bar divided into coloured segments — the "what is
// this space made of" readout shared by the Memory and Storage pages.
//
// It is one DrawingArea rather than a box of coloured children: at this size the
// whole thing is three filled rectangles behind a rounded clip, and doing it in
// widgets would cost a handful of GObjects per page for no gain.
type capacityBar struct {
	*gtk.DrawingArea
	segs  []capSeg
	total float64
	last  []float64 // previous values, so an unchanged reading skips the redraw
}

func newCapacityBar(height int) *capacityBar {
	b := &capacityBar{DrawingArea: gtk.NewDrawingArea()}
	b.SetContentHeight(height)
	b.SetHExpand(true)
	b.AddCSSClass("am-breakdown")
	b.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) { b.draw(cr, w, h) })
	return b
}

// set replaces the segments, redrawing only when something actually moved.
func (b *capacityBar) set(total float64, segs ...capSeg) {
	changed := total != b.total || len(segs) != len(b.last)
	if !changed {
		for i, s := range segs {
			if s.val != b.last[i] {
				changed = true
				break
			}
		}
	}
	if !changed {
		return
	}
	b.total = total
	b.segs = append(b.segs[:0], segs...)
	b.last = b.last[:0]
	for _, s := range segs {
		b.last = append(b.last, s.val)
	}
	b.QueueDraw()
}

func (b *capacityBar) draw(cr *cairo.Context, w, h int) {
	if b.total <= 0 || w <= 0 || h <= 0 {
		return
	}
	width, height := float64(w), float64(h)

	// Everything is clipped to one rounded outline, so the bar reads as a single
	// object rather than as touching rectangles.
	cr.Save()
	roundedBar(cr, 0, 0, width, height)
	cr.Clip()
	x := 0.0
	for _, s := range b.segs {
		segW := width * s.val / b.total
		cr.SetSourceRGBA(s.color.R, s.color.G, s.color.B, s.alpha)
		cr.Rectangle(x, 0, segW+0.5, height) // half a pixel of overlap: no seams
		cr.Fill()
		x += segW
	}
	cr.Restore()
}

// capacityColor picks the colour for the "in use" slice of a capacity bar. It
// is the app's own accent until the space is nearly gone, which is the one place
// on these pages where a warning colour earns its keep.
func capacityColor(usedFraction float64) graph.Color {
	switch {
	case usedFraction >= 0.95:
		return graph.ColorDiskWr // almost full
	case usedFraction >= 0.85:
		return graph.ColorGPU // getting tight
	default:
		return graph.ColorCPU
	}
}

// colorDot is the swatch beside a legend entry. It tracks the colour of the
// segment it stands for, which for "in use" changes as the space fills.
type colorDot struct {
	*gtk.DrawingArea
	c graph.Color
	a float64
}

func newColorDot(c graph.Color, a float64) *colorDot {
	d := &colorDot{DrawingArea: gtk.NewDrawingArea(), c: c, a: a}
	d.SetContentWidth(10)
	d.SetContentHeight(10)
	d.SetVAlign(gtk.AlignCenter)
	d.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
		cr.SetSourceRGBA(d.c.R, d.c.G, d.c.B, d.a)
		roundedBar(cr, 0, 0, float64(w), float64(h))
		cr.Fill()
	})
	return d
}

func (d *colorDot) setColor(c graph.Color, a float64) {
	if d.c == c && d.a == a {
		return
	}
	d.c, d.a = c, a
	d.QueueDraw()
}

// legendEntry pairs a swatch with its label.
func legendEntry(dot *colorDot, lbl *gtk.Label) *gtk.Box {
	b := gtk.NewBox(gtk.OrientationHorizontal, 6)
	lbl.AddCSSClass("am-subtle")
	b.Append(dot)
	b.Append(lbl)
	return b
}

// legendRow lays legend entries out in a line.
func legendRow(items ...*gtk.Box) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 16)
	for _, it := range items {
		box.Append(it)
	}
	return box
}
