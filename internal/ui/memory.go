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
	number    *liveLabel
	caption   *liveLabel
	capBuf    []byte // "of N GiB in use", rebuilt without allocating
	ramGraph  *graph.Graph
	swapGraph *graph.Graph
	breakdown *gtk.DrawingArea

	// Current breakdown values (bytes), read by the draw func on the main thread.
	bUsed, bCached, bFree, bTotal float64
	lastBreakdown                 [4]float64 // last drawn values; skips redundant redraws

	vTotal, vUsed, vCached, vAvail *liveLabel
	vSwapTotal, vSwapUsed          *liveLabel
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

	v.number.gib(used)
	v.capBuf = append(v.capBuf[:0], "of "...)
	v.capBuf = format.AppendGiB(v.capBuf, total)
	v.capBuf = append(v.capBuf, " in use"...)
	v.caption.commit(v.capBuf)

	v.vTotal.gib(total)
	v.vUsed.gib(used)
	v.vCached.gib(cached)
	v.vAvail.gib(avail)
	v.vSwapTotal.gib(swapT)
	v.vSwapUsed.gib(swapU)

	// Breakdown: app-used | cached | free, summing to total.
	appUsed := float64(total) - float64(free) - float64(cached)
	if appUsed < 0 {
		appUsed = 0
	}
	v.bUsed, v.bCached, v.bFree, v.bTotal = appUsed, float64(cached), float64(free), float64(total)
	if next := [4]float64{v.bUsed, v.bCached, v.bFree, v.bTotal}; next != v.lastBreakdown {
		v.lastBreakdown = next
		v.breakdown.QueueDraw()
	}

	v.ramGraph.Refresh()
	v.swapGraph.Refresh()
}

func (v *memView) drawBreakdown(cr *cairo.Context, w, h int) {
	if v.bTotal <= 0 {
		return
	}
	width, height := float64(w), float64(h)
	// used: red, cached: yellow, free: green — matching memLegend below.
	segs := [3]struct {
		val     float64
		r, g, b float64
	}{
		{v.bUsed, 0xe0 / 255.0, 0x1b / 255.0, 0x24 / 255.0},
		{v.bCached, 0xf5 / 255.0, 0xc2 / 255.0, 0x11 / 255.0},
		{v.bFree, 0x2e / 255.0, 0xc2 / 255.0, 0x7e / 255.0},
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
