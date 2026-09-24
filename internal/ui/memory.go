package ui

import (
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
	breakdown *capacityBar

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

	// In use / Cached / Free breakdown bar with a legend.
	v.breakdown = newCapacityBar(24)
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

	// Breakdown: app-used | cached | free, summing to total. Shades of the one
	// memory colour rather than red/amber/green — a machine with a quarter of
	// its RAM in use is not in a warning state.
	appUsed := float64(total) - float64(free) - float64(cached)
	if appUsed < 0 {
		appUsed = 0
	}
	v.breakdown.set(float64(total),
		capSeg{appUsed, graph.ColorMemory, 0.95},
		capSeg{float64(cached), graph.ColorMemory, 0.38},
		capSeg{float64(free), graph.ColorFree, 0.14})

	v.ramGraph.Refresh()
	v.swapGraph.Refresh()
}

// memLegend builds the swatch-and-label row under the breakdown bar.
func memLegend() *gtk.Box {
	return legendRow(
		legendEntry(newColorDot(graph.ColorMemory, 0.95), gtk.NewLabel("In use")),
		legendEntry(newColorDot(graph.ColorMemory, 0.38), gtk.NewLabel("Cached")),
		legendEntry(newColorDot(graph.ColorFree, 0.30), gtk.NewLabel("Free")),
	)
}
