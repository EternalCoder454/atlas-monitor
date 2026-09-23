package ui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

type diskView struct {
	root       *gtk.ScrolledWindow
	col        *stats.Collector
	disk       *stats.DiskStats
	title      *liveLabel
	caption    *liveLabel
	readGraph  *graph.Graph
	writeGraph *graph.Graph

	vSize, vUsed, vFree     *liveLabel
	vReadTotal, vWriteTotal *liveLabel
	vReadRate, vWriteRate   *liveLabel
}

func newDiskView(col *stats.Collector, disk *stats.DiskStats) *diskView {
	v := &diskView{col: col, disk: disk}
	sw, box := newPage()
	v.root = sw

	var node, label string
	var size uint64
	var isSwap bool
	var readHist, writeHist *stats.RingBuffer
	col.Read(func(s *stats.Stats) {
		node = disk.Name
		label = disk.Label()
		size = disk.SizeBytes
		isSwap = disk.IsSwap
		readHist, writeHist = disk.ReadHist, disk.WriteHist
	})

	var headBox *gtk.Box
	v.title, v.caption, headBox = newHeader()
	v.title.text(label)
	if isSwap {
		v.caption.text(format.Bytes(size) + " · compressed-RAM swap (" + node + ")")
	} else {
		v.caption.text(format.Bytes(size) + " · " + node)
	}
	box.Append(headBox)

	if isSwap {
		note := gtk.NewLabel("Swap (zram) is a compressed pool carved out of your RAM that acts as overflow " +
			"memory: when RAM fills up, the kernel compresses rarely-used pages and parks them here instead of " +
			"writing to your SSD, which keeps the system responsive under pressure and avoids disk wear.")
		note.AddCSSClass("am-subtle")
		note.SetWrap(true)
		note.SetXAlign(0)
		box.Append(note)
	}

	box.Append(sectionTitle("READ SPEED"))
	v.readGraph = graph.New("Read", graph.ColorDiskRead, readHist, graph.Bytes, 130)
	box.Append(v.readGraph)

	box.Append(sectionTitle("WRITE SPEED"))
	v.writeGraph = graph.New("Write", graph.ColorDiskWr, writeHist, graph.Bytes, 130)
	box.Append(v.writeGraph)

	box.Append(sectionTitle("DETAILS"))
	g := newStatGrid()
	v.vSize = g.add("Total size")
	v.vUsed = g.add("Used")
	v.vFree = g.add("Free")
	v.vReadRate = g.add("Read speed")
	v.vWriteRate = g.add("Write speed")
	v.vReadTotal = g.add("Read total")
	v.vWriteTotal = g.add("Written total")
	box.Append(g)

	v.vSize.bytesVal(size)
	return v
}

func (v *diskView) Root() gtk.Widgetter { return v.root }

func (v *diskView) Update() {
	var used, free, rTotal, wTotal uint64
	var rRate, wRate float64
	v.col.Read(func(s *stats.Stats) {
		used, free = v.disk.Used, v.disk.Free
		rTotal, wTotal = v.disk.ReadTotal, v.disk.WriteTotal
		rRate, wRate = v.disk.ReadRate, v.disk.WriteRate
	})
	v.vUsed.bytesVal(used)
	v.vFree.bytesVal(free)
	v.vReadRate.rate(rRate)
	v.vWriteRate.rate(wRate)
	v.vReadTotal.bytesVal(rTotal)
	v.vWriteTotal.bytesVal(wTotal)
	v.readGraph.Refresh()
	v.writeGraph.Refresh()
}
