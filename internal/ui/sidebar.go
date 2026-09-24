package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/stats"
)

// sidebar holds references the window needs after construction.
type sidebar struct {
	root         gtk.Widgetter
	assistantRow *adw.ActionRow
	netExp       *adw.ExpanderRow
	netRows      map[string]*adw.ActionRow

	// Live readings shown on the right of the hardware rows, so the headline
	// numbers are visible without opening each page. Nil where the machine has
	// no such device.
	cpuVal, memVal, gpuVal *liveLabel
}

// buildSidebar constructs the fixed 200px navigation panel. onSelect is called
// with a view name ("cpu", "disk:nvme0n1", ...) whenever a row is activated.
func buildSidebar(disks []*stats.DiskStats, nets []*stats.NetStats, gpuAvail, batteryAvail, withAI bool, onSelect func(string)) *sidebar {
	outer := gtk.NewBox(gtk.OrientationVertical, 0)
	outer.AddCSSClass("am-sidebar")

	outer.Append(sectionTitle("HARDWARE"))
	hw := newSidebarList()
	sb := &sidebar{}
	sb.cpuVal = rowValue(appendRow(hw, "CPU", "atlas-cpu-symbolic", "cpu", onSelect))
	sb.memVal = rowValue(appendRow(hw, "Memory", "atlas-memory-symbolic", "memory", onSelect))

	diskExp := adw.NewExpanderRow()
	diskExp.SetTitle("Disk")
	diskExp.SetIconName("atlas-disk-symbolic")
	for _, d := range disks {
		appendSubRow(diskExp, d.Label(), d.Name, "disk:"+d.Name, onSelect)
	}
	hw.Append(diskExp)

	netExp := adw.NewExpanderRow()
	netExp.SetTitle("Network")
	netExp.SetIconName("atlas-network-symbolic")
	netRows := make(map[string]*adw.ActionRow)
	for _, n := range nets {
		netRows[n.Name] = appendSubRow(netExp, n.Label(), n.Name, "net:"+n.Name, onSelect)
	}
	hw.Append(netExp)

	if gpuAvail {
		sb.gpuVal = rowValue(appendRow(hw, "GPU", "atlas-gpu-symbolic", "gpu", onSelect))
	}
	if batteryAvail {
		appendRow(hw, "Battery", "atlas-battery-symbolic", "power", onSelect)
	}
	outer.Append(hw)

	outer.Append(sectionTitle("SYSTEM"))
	sys := newSidebarList()
	var assistantRow *adw.ActionRow
	if withAI {
		assistantRow = appendRow(sys, "Assistant", "atlas-assistant-symbolic", "assistant", onSelect)
	}
	appendRow(sys, "Apps", "atlas-apps-symbolic", "apps", onSelect)
	appendRow(sys, "Services", "atlas-services-symbolic", "services", onSelect)
	outer.Append(sys)

	// Default highlight on CPU.
	hw.SelectRow(hw.RowAtIndex(0))

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(outer)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetSizeRequest(200, -1)
	scroll.SetVExpand(true)

	sb.root, sb.assistantRow, sb.netExp, sb.netRows = scroll, assistantRow, netExp, netRows
	return sb
}

func newSidebarList() *gtk.ListBox {
	lb := gtk.NewListBox()
	lb.SetSelectionMode(gtk.SelectionSingle)
	lb.AddCSSClass("navigation-sidebar")
	return lb
}

func appendRow(lb *gtk.ListBox, title, icon, name string, onSelect func(string)) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(title)
	if icon != "" {
		row.SetIconName(icon)
	}
	row.SetActivatable(true)
	row.ConnectActivated(func() { onSelect(name) })
	lb.Append(row)
	return row
}

// rowValue attaches a live reading to the right-hand end of a sidebar row.
func rowValue(row *adw.ActionRow) *liveLabel {
	l := gtk.NewLabel("")
	l.AddCSSClass("am-sidebar-value")
	l.SetVAlign(gtk.AlignCenter)
	row.AddSuffix(l)
	return newLiveLabel(l)
}

// update refreshes the readings beside the hardware rows. It runs every tick
// whatever page is open, which is the point of them, and costs three label
// comparisons — liveLabel only touches GTK when the text actually changes.
func (s *sidebar) update(st *stats.Stats) {
	if s.cpuVal != nil {
		s.cpuVal.percent(st.CPU.Usage)
	}
	if s.memVal != nil && st.Mem.Total > 0 {
		s.memVal.percent(float64(st.Mem.Used) / float64(st.Mem.Total) * 100)
	}
	if s.gpuVal != nil && st.GPU.Available {
		s.gpuVal.percent(st.GPU.Usage)
	}
}

func appendSubRow(exp *adw.ExpanderRow, title, subtitle, name string, onSelect func(string)) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(title)
	if subtitle != "" {
		row.SetSubtitle(subtitle)
	}
	row.SetActivatable(true)
	row.AddCSSClass("am-subrow")
	row.ConnectActivated(func() { onSelect(name) })
	exp.AddRow(row)
	return row
}
