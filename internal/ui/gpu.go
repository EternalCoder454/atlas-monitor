package ui

import (
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

type gpuView struct {
	root       *gtk.ScrolledWindow
	col        *stats.Collector
	number     *liveLabel
	caption    *liveLabel
	usageGraph *graph.Graph
	vramGraph  *graph.Graph

	vGpuClock, vMemClock, vTemp, vFan, vPower *liveLabel
	vVram, vGtt                               *liveLabel
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
	v.caption.text(name)

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
	var fanPct float64
	var vramUsed, vramTotal, gtt uint64
	v.col.Read(func(s *stats.Stats) {
		usage = s.GPU.Usage
		temp, power = s.GPU.Temp, s.GPU.PowerW
		gclk, mclk = s.GPU.GpuClockMHz, s.GPU.MemClockMHz
		fan, fanPct = s.GPU.FanRPM, s.GPU.FanPercent
		vramUsed, vramTotal, gtt = s.GPU.VramUsed, s.GPU.VramTotal, s.GPU.GttUsed
	})
	v.number.percent(usage)
	v.vGpuClock.mhz(gclk)
	v.vMemClock.mhz(mclk)
	v.vTemp.temp(temp)
	v.vFan.commit(appendFan(v.vFan.scratch(), fan, fanPct))
	v.vPower.commit(appendWatts(v.vPower.scratch(), power))
	v.vVram.gibOf(vramUsed, vramTotal)
	v.vGtt.gib(gtt)
	v.usageGraph.Refresh()
	v.vramGraph.Refresh()
}

// appendFan renders whichever figure the driver gives us: amdgpu reports tacho
// RPM, NVML reports a percentage of maximum.
func appendFan(dst []byte, rpm int, pct float64) []byte {
	switch {
	case rpm > 0:
		return append(strconv.AppendInt(dst, int64(rpm), 10), " RPM"...)
	case pct > 0:
		return append(strconv.AppendFloat(dst, pct, 'f', 0, 64), "%"...)
	default:
		return append(dst, "—"...)
	}
}

func appendWatts(dst []byte, w float64) []byte {
	if w <= 0 {
		return append(dst, "—"...)
	}
	return append(strconv.AppendFloat(dst, w, 'f', 0, 64), " W"...)
}
