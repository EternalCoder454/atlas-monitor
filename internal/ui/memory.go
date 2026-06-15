package ui

import (
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

type memView struct {
	root      *gtk.ScrolledWindow
	col       *stats.Collector
	number    *gtk.Label
	caption   *gtk.Label
	ramGraph  *graph.Graph
	swapGraph *graph.Graph
	breakdown *gtk.DrawingArea

	// Current breakdown values (bytes), read by the draw func on the main thread.
	bUsed, bCached, bFree, bTotal float64

	vTotal, vUsed, vCached, vAvail *gtk.Label
	vSwapTotal, vSwapUsed          *gtk.Label
}

func newMemView(col *stats.Collector) *memView {
	v := &memView{col: col}
	sw, box := newPage()
	v.root = sw

	var headBox *gtk.Box
	v.number, v.caption, headBox = newHeadline()
	box.Append(headBox)

	var ramHist, swapHist *stats.RingBuffer
	col.Read(func(s *stats.Stats) {
		ramHist = s.Mem.UsageHist
		swapHist = s.Mem.SwapHist
	})
	v.ramGraph = graph.New("RAM", graph.ColorMemory, ramHist, graph.Percent, 140)
	box.Append(v.ramGraph)

	// Used / Cached / Free breakdown bar with a legend.
	v.breakdown = gtk.NewDrawingArea()
	v.breakdown.SetContentHeight(24)
	v.breakdown.SetHExpand(true)
	v.breakdown.AddCSSClass("am-breakdown")
	v.breakdown.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
		v.drawBreakdown(cr, w, h)
	})
	box.Append(v.breakdown)
	box.Append(memLegend())

	box.Append(sectionTitle("SWAP"))
	v.swapGraph = graph.New("Swap", graph.ColorGPU, swapHist, graph.Percent, 80)
	box.Append(v.swapGraph)

	box.Append(sectionTitle("DETAILS"))
	g := newStatGrid()
	v.vTotal = g.add("Total")
	v.vUsed = g.add("Used")
	v.vCached = g.add("Cached")
	v.vAvail = g.add("Available")
	v.vSwapTotal = g.add("Swap total")
	v.vSwapUsed = g.add("Swap used")
	box.Append(g)
	return v
}

func (v *memView) Root() gtk.Widgetter { return v.root }

func (v *memView) Update() {
	var total, used, cached, avail, free, swapT, swapU uint64
	v.col.Read(func(s *stats.Stats) {
		total, used, cached, avail, free = s.Mem.Total, s.Mem.Used, s.Mem.Cached, s.Mem.Available, s.Mem.Free
		swapT, swapU = s.Mem.SwapTotal, s.Mem.SwapUsed
	})

	v.number.SetText(format.GiB(used))
	v.caption.SetText("of " + format.GiB(total) + " in use")
	v.vTotal.SetText(format.GiB(total))
	v.vUsed.SetText(format.GiB(used))
	v.vCached.SetText(format.GiB(cached))
	v.vAvail.SetText(format.GiB(avail))
	v.vSwapTotal.SetText(format.GiB(swapT))
	v.vSwapUsed.SetText(format.GiB(swapU))

	// Breakdown: app-used | cached | free, summing to total.
	appUsed := float64(total) - float64(free) - float64(cached)
	if appUsed < 0 {
		appUsed = 0
	}
	v.bUsed, v.bCached, v.bFree, v.bTotal = appUsed, float64(cached), float64(free), float64(total)
	v.breakdown.QueueDraw()

	v.ramGraph.Refresh()
	v.swapGraph.Refresh()
}

func (v *memView) drawBreakdown(cr *cairo.Context, w, h int) {
	if v.bTotal <= 0 {
		return
	}
	width, height := float64(w), float64(h)
	segs := []struct {
		val        float64
		r, g, b    float64
	}{
		{v.bUsed, 0xe0 / 255.0, 0x1b / 255.0, 0x24 / 255.0},   // used: red
		{v.bCached, 0xf5 / 255.0, 0xc2 / 255.0, 0x11 / 255.0}, // cached: yellow
		{v.bFree, 0x2e / 255.0, 0xc2 / 255.0, 0x7e / 255.0},   // free: green
	}
	x := 0.0
	for _, s := range segs {
		segW := width * s.val / v.bTotal
		cr.SetSourceRGBA(s.r, s.g, s.b, 0.9)
		cr.Rectangle(x, 0, segW, height)
		cr.Fill()
		x += segW
	}
}

// memLegend builds a compact coloured legend for the breakdown bar.
func memLegend() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 16)
	box.Append(legendItem("#e01b24", "Used"))
	box.Append(legendItem("#f5c211", "Cached"))
	box.Append(legendItem("#2ec27e", "Free"))
	return box
}

func legendItem(color, text string) *gtk.Box {
	b := gtk.NewBox(gtk.OrientationHorizontal, 6)
	dot := gtk.NewLabel("")
	dot.SetMarkup("<span color='" + color + "'>■</span>")
	lbl := gtk.NewLabel(text)
	lbl.AddCSSClass("am-subtle")
	b.Append(dot)
	b.Append(lbl)
	return b
}
