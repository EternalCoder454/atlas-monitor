package ui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

// cpuColumns is the number of columns in the per-core usage grid.
const cpuColumns = 8

type cpuView struct {
	root    *gtk.ScrolledWindow
	col     *stats.Collector
	number  *liveLabel
	caption *liveLabel
	usage   *graph.Graph
	cores   *coreGrid
	nCores  int
	coreBuf []float64 // reused each Update; avoids a per-tick alloc on the GTK thread

	vBase, vCur, vSockets, vCores, vLogical *liveLabel
	vL1d, vL1i, vL2, vL3, vTemp             *liveLabel
}

func newCPUView(col *stats.Collector) *cpuView {
	v := &cpuView{col: col}
	sw, box := newPage()
	v.root = sw

	var headBox *gtk.Box
	v.number, v.caption, headBox = newHeadline()
	box.Append(headBox)

	var hist *stats.RingBuffer
	var logical int
	col.Read(func(s *stats.Stats) {
		hist = s.CPU.UsageHist
		logical = s.CPU.Logical
	})
	v.usage = graph.New("CPU", graph.ColorCPU, hist, graph.Percent, 160)
	box.Append(v.usage)

	// Per-core usage bars, drawn in a single Cairo pass.
	box.Append(sectionTitle("CORES"))
	v.nCores = logical
	v.coreBuf = make([]float64, logical)
	v.cores = newCoreGrid(logical)
	box.Append(v.cores)

	// Stats grid.
	box.Append(sectionTitle("DETAILS"))
	g := newStatGrid()
	v.vBase = g.add("Base speed")
	v.vCur = g.add("Current speed")
	v.vSockets = g.add("Sockets")
	v.vCores = g.add("Cores")
	v.vLogical = g.add("Logical processors")
	v.vL1d = g.add("L1 data cache")
	v.vL1i = g.add("L1 instruction cache")
	v.vL2 = g.add("L2 cache")
	v.vL3 = g.add("L3 cache")
	v.vTemp = g.add("Temperature")
	box.Append(g)

	// Static fields, set once.
	col.Read(func(s *stats.Stats) {
		v.caption.text(s.CPU.Model)
		v.vBase.mhz(s.CPU.BaseFreq)
		v.vSockets.intVal(s.CPU.Sockets)
		v.vCores.intVal(s.CPU.PhysCores)
		v.vLogical.intVal(s.CPU.Logical)
		v.vL1d.text(orDash(s.CPU.L1d))
		v.vL1i.text(orDash(s.CPU.L1i))
		v.vL2.text(orDash(s.CPU.L2))
		v.vL3.text(orDash(s.CPU.L3))
	})
	return v
}

func (v *cpuView) Root() gtk.Widgetter { return v.root }

func (v *cpuView) Update() {
	var usage, cur, temp float64
	cores := v.coreBuf
	v.col.Read(func(s *stats.Stats) {
		usage = s.CPU.Usage
		cur = s.CPU.CurFreq
		temp = s.CPU.Temp
		for i := 0; i < v.nCores && i < len(s.CPU.Cores); i++ {
			cores[i] = s.CPU.Cores[i].Usage
		}
	})
	v.number.percent(usage)
	v.vCur.mhz(cur)
	v.vTemp.temp(temp)
	v.cores.set(cores)
	v.usage.Refresh()
}
