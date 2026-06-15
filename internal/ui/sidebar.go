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
}

// buildSidebar constructs the fixed 200px navigation panel. onSelect is called
// with a view name ("cpu", "disk:nvme0n1", ...) whenever a row is activated.
func buildSidebar(disks []*stats.DiskStats, nets []*stats.NetStats, gpuAvail bool, onSelect func(string)) *sidebar {
	outer := gtk.NewBox(gtk.OrientationVertical, 0)
	outer.AddCSSClass("am-sidebar")

	outer.Append(sectionTitle("HARDWARE"))
	hw := newSidebarList()
	appendRow(hw, "CPU", "atlas-cpu-symbolic", "cpu", onSelect)
	appendRow(hw, "Memory", "atlas-memory-symbolic", "memory", onSelect)

	diskExp := adw.NewExpanderRow()
	diskExp.SetTitle("Disk")
	diskExp.SetIconName("drive-harddisk-solidstate-symbolic")
	for _, d := range disks {
		appendSubRow(diskExp, d.Label(), d.Name, "disk:"+d.Name, onSelect)
	}
	hw.Append(diskExp)

	netExp := adw.NewExpanderRow()
	netExp.SetTitle("Network")
	netExp.SetIconName("network-wired-symbolic")
	netRows := make(map[string]*adw.ActionRow)
	for _, n := range nets {
		netRows[n.Name] = appendSubRow(netExp, n.Label(), n.Name, "net:"+n.Name, onSelect)
	}
	hw.Append(netExp)

	if gpuAvail {
		appendRow(hw, "GPU", "video-display-symbolic", "gpu", onSelect)
	}
	outer.Append(hw)

	outer.Append(sectionTitle("SYSTEM"))
	sys := newSidebarList()
	assistantRow := appendRow(sys, "Assistant", "atlas-assistant-symbolic", "assistant", onSelect)
	appendRow(sys, "Apps", "view-app-grid-symbolic", "apps", onSelect)
	appendRow(sys, "Services", "system-run-symbolic", "services", onSelect)
	outer.Append(sys)

	// Default highlight on CPU.
	hw.SelectRow(hw.RowAtIndex(0))

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(outer)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetSizeRequest(200, -1)
	scroll.SetVExpand(true)

	return &sidebar{root: scroll, assistantRow: assistantRow, netExp: netExp, netRows: netRows}
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
