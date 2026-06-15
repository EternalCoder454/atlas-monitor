package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/graph"
)

// coreGrid renders all per-core usage bars in a single DrawingArea (one Cairo
// pass), instead of one GtkLevelBar widget per core. With 32 cores this keeps
// the CPU view's idle cost low.
type coreGrid struct {
	*gtk.DrawingArea
	usages []float64
	cols   int
}

const coreRowHeight = 34

func newCoreGrid(n int) *coreGrid {
	g := &coreGrid{
		DrawingArea: gtk.NewDrawingArea(),
		usages:      make([]float64, n),
		cols:        cpuColumns,
	}
	rows := (n + g.cols - 1) / g.cols
	g.SetContentHeight(rows * coreRowHeight)
	g.SetHExpand(true)
	g.AddCSSClass("am-core-grid")
	g.SetDrawFunc(g.draw)
	return g
}

// set updates the usages and requests a redraw. Call on the GTK main thread.
func (g *coreGrid) set(usages []float64) {
	copy(g.usages, usages)
	g.QueueDraw()
}

func (g *coreGrid) draw(area *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	n := len(g.usages)
	if n == 0 || w <= 0 {
		return
	}
	fr, fg, fb := 0.5, 0.5, 0.5
	if sc := area.StyleContext(); sc != nil {
		c := sc.Color()
		fr, fg, fb = float64(c.Red()), float64(c.Green()), float64(c.Blue())
	}

	cellW := float64(w) / float64(g.cols)
	cr.SelectFontFace("sans-serif", cairo.FontSlantNormal, cairo.FontWeightNormal)
	cr.SetFontSize(10)

	for i := 0; i < n; i++ {
		x := float64(i%g.cols) * cellW
		y := float64(i/g.cols) * coreRowHeight

		cr.SetSourceRGBA(fr, fg, fb, 0.6)
		cr.MoveTo(x+2, y+12)
		cr.ShowText(fmt.Sprintf("Core %d", i))

		barX, barY := x+2, y+18.0
		barW, barH := cellW-8, 7.0
		cr.SetSourceRGBA(fr, fg, fb, 0.12)
		cr.Rectangle(barX, barY, barW, barH)
		cr.Fill()

		u := g.usages[i]
		if u < 0 {
			u = 0
		} else if u > 100 {
			u = 100
		}
		cr.SetSourceRGBA(graph.ColorCPU.R, graph.ColorCPU.G, graph.ColorCPU.B, 0.9)
		cr.Rectangle(barX, barY, barW*u/100, barH)
		cr.Fill()
	}
}
