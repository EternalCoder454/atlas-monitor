package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/stats"
)

type netView struct {
	root      *gtk.ScrolledWindow
	col       *stats.Collector
	net       *stats.NetStats
	title     *gtk.Label
	caption   *gtk.Label
	downGraph *graph.Graph
	upGraph   *graph.Graph

	vIPv4, vIPv6, vMAC, vSpeed       *gtk.Label
	vDownRate, vUpRate               *gtk.Label
	vRxTotal, vTxTotal               *gtk.Label
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
	v.title.SetText(label)
	box.Append(headBox)

	box.Append(sectionTitle("DOWNLOAD"))
	v.downGraph = graph.New("Down", graph.ColorNetDown, downHist, graph.Bytes, 130)
	box.Append(v.downGraph)

	box.Append(sectionTitle("UPLOAD"))
	v.upGraph = graph.New("Up", graph.ColorNetUp, upHist, graph.Bytes, 130)
	box.Append(v.upGraph)

	box.Append(sectionTitle("DETAILS"))
	g := newStatGrid()
	g.add("Interface").SetText(name)
	v.vIPv4 = g.add("IPv4")
	v.vIPv6 = g.add("IPv6")
	v.vMAC = g.add("MAC address")
	v.vSpeed = g.add("Link speed")
	v.vDownRate = g.add("Download")
	v.vUpRate = g.add("Upload")
	v.vRxTotal = g.add("Received total")
	v.vTxTotal = g.add("Sent total")
	box.Append(g)

	v.vMAC.SetText(orDash(mac))
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
	v.vIPv4.SetText(orDash(ipv4))
	v.vIPv6.SetText(orDash(ipv6))
	v.vSpeed.SetText(linkSpeed(speed))
	v.vDownRate.SetText(format.Rate(rxRate))
	v.vUpRate.SetText(format.Rate(txRate))
	v.vRxTotal.SetText(format.Bytes(rxTotal))
	v.vTxTotal.SetText(format.Bytes(txTotal))
	v.caption.SetText(format.Rate(rxRate) + " ↓   " + format.Rate(txRate) + " ↑")
	v.downGraph.Refresh()
	v.upGraph.Refresh()
}

func linkSpeed(mbit int) string {
	if mbit <= 0 {
		return "—"
	}
	if mbit >= 1000 {
		return fmt.Sprintf("%g Gbit/s", float64(mbit)/1000)
	}
	return fmt.Sprintf("%d Mbit/s", mbit)
}
