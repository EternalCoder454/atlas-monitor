package ui

import (
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

type netView struct {
	root      *gtk.ScrolledWindow
	col       *stats.Collector
	net       *stats.NetStats
	title     *liveLabel
	caption   *liveLabel
	capBuf    []byte // "<down> ↓   <up> ↑", rebuilt without allocating
	downGraph *graph.Graph
	upGraph   *graph.Graph

	vIPv4, vIPv6, vMAC, vSpeed *liveLabel
	vDownRate, vUpRate         *liveLabel
	vRxTotal, vTxTotal         *liveLabel
}

func newNetView(col *stats.Collector, n *stats.NetStats) *netView {
	v := &netView{col: col, net: n}
	sw, box := newPage()
	v.root = sw

	var name, label, mac string
	var downHist, upHist *stats.RingBuffer
	col.Read(func(s *stats.Stats) {
		name, label, mac = n.Name, n.Label(), n.MAC
		downHist, upHist = n.DownHist, n.UpHist
	})

	var headBox *gtk.Box
	v.title, v.caption, headBox = newHeader()
	v.title.text(label)
	box.Append(headBox)

	box.Append(sectionTitle("DOWNLOAD"))
	v.downGraph = graph.New("Down", graph.ColorNetDown, downHist, graph.Bytes, 130)
	box.Append(v.downGraph)

	box.Append(sectionTitle("UPLOAD"))
	v.upGraph = graph.New("Up", graph.ColorNetUp, upHist, graph.Bytes, 130)
	box.Append(v.upGraph)

	box.Append(sectionTitle("DETAILS"))
	g := newStatGrid()
	g.add("Interface").text(name)
	v.vIPv4 = g.add("IPv4")
	v.vIPv6 = g.add("IPv6")
	v.vMAC = g.add("MAC address")
	v.vSpeed = g.add("Link speed")
	v.vDownRate = g.add("Download")
	v.vUpRate = g.add("Upload")
	v.vRxTotal = g.add("Received total")
	v.vTxTotal = g.add("Sent total")
	box.Append(g)

	v.vMAC.text(orDash(mac))
	return v
}

func (v *netView) Root() gtk.Widgetter { return v.root }

func (v *netView) Update() {
	var ipv4, ipv6 string
	var speed int
	var rxRate, txRate float64
	var rxTotal, txTotal uint64
	v.col.Read(func(s *stats.Stats) {
		ipv4, ipv6 = v.net.IPv4, v.net.IPv6
		speed = v.net.SpeedMbit
		rxRate, txRate = v.net.RxRate, v.net.TxRate
		rxTotal, txTotal = v.net.RxTotal, v.net.TxTotal
	})
	v.vIPv4.text(orDash(ipv4))
	v.vIPv6.text(orDash(ipv6))
	v.vSpeed.commit(appendLinkSpeed(v.vSpeed.scratch(), speed))
	v.vDownRate.rate(rxRate)
	v.vUpRate.rate(txRate)
	v.vRxTotal.bytesVal(rxTotal)
	v.vTxTotal.bytesVal(txTotal)

	v.capBuf = format.AppendRate(v.capBuf[:0], rxRate)
	v.capBuf = append(v.capBuf, " ↓   "...)
	v.capBuf = format.AppendRate(v.capBuf, txRate)
	v.capBuf = append(v.capBuf, " ↑"...)
	v.caption.commit(v.capBuf)

	v.downGraph.Refresh()
	v.upGraph.Refresh()
}

func appendLinkSpeed(dst []byte, mbit int) []byte {
	switch {
	case mbit <= 0:
		return append(dst, "—"...)
	case mbit >= 1000:
		return append(strconv.AppendFloat(dst, float64(mbit)/1000, 'g', -1, 64), " Gbit/s"...)
	default:
		return append(strconv.AppendInt(dst, int64(mbit), 10), " Mbit/s"...)
	}
}
