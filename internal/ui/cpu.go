package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

// cpuColumns is the number of columns in the per-core usage grid.
const cpuColumns = 8

type cpuView struct {
	root    *gtk.ScrolledWindow
	col     *stats.Collector
	number  *gtk.Label
	caption *gtk.Label
	usage   *graph.Graph
	cores   *coreGrid
	nCores  int

	vBase, vCur, vSockets, vCores, vLogical *gtk.Label
	vL1d, vL1i, vL2, vL3, vTemp             *gtk.Label
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
		v.caption.SetText(s.CPU.Model)
		v.vBase.SetText(format.MHz(s.CPU.BaseFreq))
		v.vSockets.SetText(fmt.Sprintf("%d", s.CPU.Sockets))
		v.vCores.SetText(fmt.Sprintf("%d", s.CPU.PhysCores))
		v.vLogical.SetText(fmt.Sprintf("%d", s.CPU.Logical))
		v.vL1d.SetText(orDash(s.CPU.L1d))
		v.vL1i.SetText(orDash(s.CPU.L1i))
		v.vL2.SetText(orDash(s.CPU.L2))
		v.vL3.SetText(orDash(s.CPU.L3))
	})
	return v
}

func (v *cpuView) Root() gtk.Widgetter { return v.root }

func (v *cpuView) Update() {
	var usage, cur, temp float64
	cores := make([]float64, v.nCores)
	v.col.Read(func(s *stats.Stats) {
		usage = s.CPU.Usage
		cur = s.CPU.CurFreq
		temp = s.CPU.Temp
		for i := 0; i < v.nCores && i < len(s.CPU.Cores); i++ {
			cores[i] = s.CPU.Cores[i].Usage
		}
	})
	v.number.SetText(format.Percent(usage))
	v.vCur.SetText(format.MHz(cur))
	v.vTemp.SetText(format.Temp(temp))
	v.cores.set(cores)
	v.usage.Refresh()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
