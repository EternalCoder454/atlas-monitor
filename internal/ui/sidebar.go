package ui

import (
	"strconv"

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

	// rows maps a view name to its row, and owner to the list that row sits in.
	// The sidebar is two separate GtkListBoxes — hardware and system — and a
	// selection in one says nothing about the other, so following the open page
	// means clearing both and then selecting in the right one.
	rows  map[string]*adw.ActionRow
	owner map[string]*gtk.ListBox
	lists []*gtk.ListBox
}

// buildSidebar constructs the fixed 200px navigation panel. onSelect is called
// with a view name ("cpu", "disk:nvme0n1", ...) whenever a row is activated.
func buildSidebar(disks []*stats.DiskStats, nets []*stats.NetStats, packs []string, gpuAvail, batteryAvail, withAI bool, onSelect func(string)) *sidebar {
	outer := gtk.NewBox(gtk.OrientationVertical, 0)
	outer.AddCSSClass("am-sidebar")

	outer.Append(sectionTitle("HARDWARE"))
	hw := newSidebarList()
	sb := &sidebar{
		rows:  make(map[string]*adw.ActionRow),
		owner: make(map[string]*gtk.ListBox),
	}
	// track records a row so the sidebar can follow the page that is open.
	track := func(name string, lb *gtk.ListBox, row *adw.ActionRow) *adw.ActionRow {
		sb.rows[name], sb.owner[name] = row, lb
		return row
	}
	sb.cpuVal = rowValue(track("cpu", hw, appendRow(hw, "CPU", "atlas-cpu-symbolic", "cpu", onSelect)))
	sb.memVal = rowValue(track("memory", hw, appendRow(hw, "Memory", "atlas-memory-symbolic", "memory", onSelect)))

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
		sb.gpuVal = rowValue(track("gpu", hw, appendRow(hw, "GPU", "atlas-gpu-symbolic", "gpu", onSelect)))
	}
	switch {
	case batteryAvail && len(packs) > 1:
		// Two packs, as on a ThinkPad with a hot-swap bay: the summed reading
		// stays at the top, and each pack gets a row of its own, the same shape
		// the disks and interfaces already use.
		batExp := adw.NewExpanderRow()
		batExp.SetTitle("Battery")
		batExp.SetIconName("atlas-battery-symbolic")
		appendSubRow(batExp, "All batteries", "", "power", onSelect)
		for i, name := range packs {
			appendSubRow(batExp, "Battery "+strconv.Itoa(i+1), name, "power:"+name, onSelect)
		}
		hw.Append(batExp)
	case batteryAvail:
		track("power", hw, appendRow(hw, "Battery", "atlas-battery-symbolic", "power", onSelect))
	}
	outer.Append(hw)

	outer.Append(sectionTitle("SYSTEM"))
	sys := newSidebarList()
	var assistantRow *adw.ActionRow
	if withAI {
		assistantRow = track("assistant", sys, appendRow(sys, "Assistant", "atlas-assistant-symbolic", "assistant", onSelect))
	}
	track("apps", sys, appendRow(sys, "Apps", "atlas-apps-symbolic", "apps", onSelect))
	track("startup", sys, appendRow(sys, "Startup", "atlas-startup-symbolic", "startup", onSelect))
	track("services", sys, appendRow(sys, "Services", "atlas-services-symbolic", "services", onSelect))
	outer.Append(sys)

	sb.lists = []*gtk.ListBox{hw, sys}

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

// selectView moves the highlight to the row for the open page.
//
// It is called for every page change, including the one at startup: Atlas
// reopens on whichever page was last used, and without this the highlight sat
// on CPU while the content showed something else. Both lists are cleared first
// because a GtkListBox only knows about its own rows — selecting Apps in the
// system list would otherwise leave CPU selected in the hardware list, and two
// rows would look active at once.
//
// Disk and Network pages live in expander rows, whose children belong to a list
// of their own; there is nothing to select at this level, so the highlight is
// simply cleared rather than left pointing at the wrong page.
func (s *sidebar) selectView(name string) {
	for _, lb := range s.lists {
		lb.UnselectAll()
	}
	if row, ok := s.rows[name]; ok {
		if lb := s.owner[name]; lb != nil {
			lb.SelectRow(&row.ListBoxRow)
		}
	}
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
