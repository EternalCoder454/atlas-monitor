package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

type gpuView struct {
	root       *gtk.ScrolledWindow
	col        *stats.Collector
	number     *gtk.Label
	caption    *gtk.Label
	usageGraph *graph.Graph
	vramGraph  *graph.Graph

	vGpuClock, vMemClock, vTemp, vFan, vPower *gtk.Label
	vVram, vGtt                               *gtk.Label
}

func newGPUView(col *stats.Collector) *gpuView {
	v := &gpuView{col: col}
	sw, box := newPage()
	v.root = sw

	var headBox *gtk.Box
	v.number, v.caption, headBox = newHeadline()
	box.Append(headBox)

	var name string
	var usageHist, vramHist *stats.RingBuffer
	col.Read(func(s *stats.Stats) {
		name = s.GPU.Name
		usageHist, vramHist = s.GPU.UsageHist, s.GPU.VramHist
	})
	v.caption.SetText(name)

	box.Append(sectionTitle("GPU UTILISATION"))
	v.usageGraph = graph.New("GPU", graph.ColorGPU, usageHist, graph.Percent, 150)
	box.Append(v.usageGraph)

	box.Append(sectionTitle("VRAM USAGE"))
	v.vramGraph = graph.New("VRAM", graph.ColorGPU, vramHist, graph.Percent, 130)
	box.Append(v.vramGraph)

	box.Append(sectionTitle("DETAILS"))
	g := newStatGrid()
	v.vGpuClock = g.add("GPU clock")
	v.vMemClock = g.add("Memory clock")
	v.vTemp = g.add("Temperature")
	v.vFan = g.add("Fan speed")
	v.vPower = g.add("Power draw")
	v.vVram = g.add("VRAM used")
	v.vGtt = g.add("GTT used")
	box.Append(g)
	return v
}

func (v *gpuView) Root() gtk.Widgetter { return v.root }

func (v *gpuView) Update() {
	var usage, temp, power, gclk, mclk float64
	var fan int
	var vramUsed, vramTotal, gtt uint64
	v.col.Read(func(s *stats.Stats) {
		usage = s.GPU.Usage
		temp, power = s.GPU.Temp, s.GPU.PowerW
		gclk, mclk = s.GPU.GpuClockMHz, s.GPU.MemClockMHz
		fan = s.GPU.FanRPM
		vramUsed, vramTotal, gtt = s.GPU.VramUsed, s.GPU.VramTotal, s.GPU.GttUsed
	})
	v.number.SetText(format.Percent(usage))
	v.vGpuClock.SetText(format.MHz(gclk))
	v.vMemClock.SetText(format.MHz(mclk))
	v.vTemp.SetText(format.Temp(temp))
	v.vFan.SetText(rpm(fan))
	v.vPower.SetText(watts(power))
	v.vVram.SetText(format.GiB(vramUsed) + " / " + format.GiB(vramTotal))
	v.vGtt.SetText(format.GiB(gtt))
	v.usageGraph.Refresh()
	v.vramGraph.Refresh()
}

func rpm(r int) string {
	if r <= 0 {
		return "0 RPM"
	}
	return fmt.Sprintf("%d RPM", r)
}

func watts(w float64) string {
	if w <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f W", w)
}
