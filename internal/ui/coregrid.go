package ui

import (
	"fmt"
	"math"
	"strconv"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"

	"atlas-monitor/internal/graph"
)

// coreGrid renders all per-core usage bars in a single DrawingArea (one Cairo
// pass), instead of one GtkLevelBar widget per core. With 32 cores this keeps
// the CPU view's idle cost low.
type coreGrid struct {
	*gtk.DrawingArea
	usages []float64
	labels []string // "Core N", built once (kept out of the draw hot path)
	cols   int
	dirty  bool // a reading changed, so its layout needs new text

	// Core labels and readings are drawn through Pango rather than Cairo's
	// "toy" text API. That API picks a font by family name with no reference to
	// the desktop's own, and gets none of GTK's hinting configuration, which is
	// why these labels looked soft next to every other piece of text in the
	// window.
	//
	// There is a layout per cell rather than one reused for all of them, because
	// setting a layout's text re-shapes it: sharing one would re-shape sixty-four
	// strings on every frame. The names never change, so they are shaped once;
	// a reading is re-shaped only on the tick its value actually moves.
	names []*pango.Layout
	pcts  []*pango.Layout
	pct   []string
}

const coreRowHeight = 34

// minCoreCellWidth is the narrowest a core cell can be and still fit "Core 31"
// and a percentage on one line, measured against the grid's own font size.
const minCoreCellWidth = 108

// fitColumns picks how many cores sit across a grid of the given width.
func fitColumns(width, cores int) int {
	cols := width / minCoreCellWidth
	if cols > cpuColumns {
		cols = cpuColumns
	}
	if cols > cores {
		cols = cores
	}
	if cols < 2 {
		cols = 2
	}
	return cols
}

func newCoreGrid(n int) *coreGrid {
	g := &coreGrid{
		DrawingArea: gtk.NewDrawingArea(),
		usages:      make([]float64, n),
		labels:      make([]string, n),
		cols:        cpuColumns,
	}
	for i := range g.labels {
		g.labels[i] = fmt.Sprintf("Core %d", i)
	}
	// Seeded to match the zeroed usages above, because set() only writes a
	// label when a reading changes: a core that has been idle since the page
	// opened never changes, and would otherwise sit there with no figure at all
	// while its busier neighbours were labelled.
	g.pct = make([]string, n)
	for i := range g.pct {
		g.pct[i] = "0%"
	}
	rows := (n + g.cols - 1) / g.cols
	g.SetContentHeight(rows * coreRowHeight)
	g.SetHExpand(true)
	g.AddCSSClass("am-core-grid")
	g.SetDrawFunc(g.draw)
	return g
}

// set updates the usages and requests a redraw, skipping the redraw when every
// core reads the same as last tick (an idle machine, mostly).
func (g *coreGrid) set(usages []float64) {
	changed := false
	for i := range g.usages {
		if i < len(usages) && g.usages[i] != usages[i] {
			g.usages[i] = usages[i]
			if p := strconv.Itoa(int(usages[i]+0.5)) + "%"; p != g.pct[i] {
				g.pct[i] = p
				g.dirty = true // re-shape this reading on the next draw
			}
			changed = true
		}
	}
	if changed {
		g.QueueDraw()
	}
}

func (g *coreGrid) draw(area *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	n := len(g.usages)
	if n == 0 || w <= 0 {
		return
	}
	fr, fg, fb := graph.Foreground(area)

	// Columns follow the width rather than a constant. Eight of them across a
	// 560px window leaves each core about 65px, which is not enough for a name
	// and a reading side by side: "Core 0" and "4%" printed straight through
	// each other. The grid re-flows instead, down to two columns.
	if cols := fitColumns(w, len(g.usages)); cols != g.cols {
		g.cols = cols
		rows := (len(g.usages) + cols - 1) / cols
		g.SetContentHeight(rows * coreRowHeight)
	}
	cellW := float64(w) / float64(g.cols)
	if g.names == nil {
		g.names = make([]*pango.Layout, n)
		g.pcts = make([]*pango.Layout, n)
		for i := range g.names {
			g.names[i] = area.CreatePangoLayout(g.labels[i])
			g.pcts[i] = area.CreatePangoLayout("")
		}
		g.dirty = true
	}
	if g.dirty {
		for i := range g.pcts {
			g.pcts[i].SetText(g.pct[i])
		}
		g.dirty = false
	}

	for i := 0; i < n; i++ {
		x := float64(i%g.cols) * cellW
		y := float64(i/g.cols) * coreRowHeight
		barX, barY := x+2, y+18.0
		barW, barH := cellW-8, 7.0

		// Name on the left, reading on the right, on one line above the bar.
		cr.SetSourceRGBA(fr, fg, fb, 0.75)
		cr.MoveTo(barX, y+1)
		pangocairo.ShowLayout(cr, g.names[i])

		if g.pct[i] != "" {
			tw, _ := g.pcts[i].PixelSize()
			cr.SetSourceRGBA(fr, fg, fb, 0.55)
			cr.MoveTo(barX+barW-float64(tw), y+1)
			pangocairo.ShowLayout(cr, g.pcts[i])
		}

		u := g.usages[i]
		if u < 0 {
			u = 0
		} else if u > 100 {
			u = 100
		}

		// Rounded track and fill: square ends read as unfinished at this size.
		cr.SetSourceRGBA(fr, fg, fb, 0.14)
		roundedBar(cr, barX, barY, barW, barH)
		cr.Fill()
		if u > 0 {
			fw := barW * u / 100
			if fw < barH { // keep a very small reading from becoming a sliver
				fw = barH
			}
			cr.SetSourceRGBA(graph.ColorCPU.R, graph.ColorCPU.G, graph.ColorCPU.B, 0.95)
			roundedBar(cr, barX, barY, fw, barH)
			cr.Fill()
		}
	}
}

// roundedBar traces a pill-shaped rectangle, the radius being half its height.
func roundedBar(cr *cairo.Context, x, y, w, h float64) {
	r := h / 2
	if w < h {
		w = h
	}
	cr.NewPath()
	cr.Arc(x+r, y+r, r, math.Pi/2, 3*math.Pi/2)
	cr.Arc(x+w-r, y+r, r, 3*math.Pi/2, math.Pi/2)
	cr.ClosePath()
}
