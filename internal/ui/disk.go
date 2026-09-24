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
	capacity   *capacityBar
	capSection *gtk.Box
	capNote    *gtk.Label
	capUsedDot *colorDot
	capUsed    *gtk.Label
	capFree    *gtk.Label

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

	// Capacity first: how full the drive is, which is the thing you open this
	// page to find out. Throughput matters less often and reads below.
	if !isSwap {
		box.Append(sectionTitle("CAPACITY"))
		v.capSection = gtk.NewBox(gtk.OrientationVertical, 0)
		v.capacity = newCapacityBar(24)
		v.capSection.Append(v.capacity)
		v.capUsed = gtk.NewLabel("")
		v.capFree = gtk.NewLabel("")
		v.capUsedDot = newColorDot(graph.ColorCPU, 0.95)
		v.capSection.Append(legendRow(
			legendEntry(v.capUsedDot, v.capUsed),
			legendEntry(newColorDot(graph.ColorFree, 0.30), v.capFree),
		))
		box.Append(v.capSection)

		// A drive with no mounted filesystem has no usage to report. Drawn as
		// numbers it came out as "0 B used · 0 B free" under an empty bar,
		// which reads as an empty disk, or a broken sensor, rather than as a
		// question Atlas cannot answer. The throughput below is still real —
		// the kernel counts blocks whether or not anything is mounted — so only
		// this section stands down.
		v.capNote = gtk.NewLabel("Not mounted, so there is no usage to show. Mount the drive in your " +
			"file manager and its capacity will appear here.")
		v.capNote.AddCSSClass("am-subtle")
		v.capNote.SetWrap(true)
		v.capNote.SetXAlign(0)
		v.capNote.SetVisible(false)
		box.Append(v.capNote)
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
	var usedBytes, free, rTotal, wTotal uint64
	var rRate, wRate float64
	v.col.Read(func(s *stats.Stats) {
		usedBytes, free = v.disk.Used, v.disk.Free
		rTotal, wTotal = v.disk.ReadTotal, v.disk.WriteTotal
		rRate, wRate = v.disk.ReadRate, v.disk.WriteRate
	})
	switch {
	case v.capacity == nil: // swap: no filesystem, and none expected
		v.vUsed.bytesVal(usedBytes)
		v.vFree.bytesVal(free)
	case usedBytes+free == 0: // nothing mounted, so nothing to measure
		v.capSection.SetVisible(false)
		v.capNote.SetVisible(true)
		v.vUsed.text("—")
		v.vFree.text("—")
	default:
		v.capSection.SetVisible(true)
		v.capNote.SetVisible(false)
		v.vUsed.bytesVal(usedBytes)
		v.vFree.bytesVal(free)
		total := float64(usedBytes + free)
		frac := float64(usedBytes) / total
		used := capacityColor(frac)
		v.capacity.set(total,
			capSeg{float64(usedBytes), used, 0.95},
			capSeg{float64(free), graph.ColorFree, 0.14})
		v.capUsedDot.setColor(used, 0.95)
		v.capUsed.SetText(format.Bytes(usedBytes) + " used")
		v.capFree.SetText(format.Bytes(free) + " free")
	}
	v.vReadRate.rate(rRate)
	v.vWriteRate.rate(wRate)
	v.vReadTotal.bytesVal(rTotal)
	v.vWriteTotal.bytesVal(wTotal)
	v.readGraph.Refresh()
	v.writeGraph.Refresh()
}
