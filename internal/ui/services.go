package ui

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/services"
)

// svcRow is a stable row object, kept for the lifetime of a unit and mutated in
// place. Replacing the model wholesale on every refresh — which is what this
// view used to do — makes GTK tear down and rebuild every realised list item,
// and that costs several megabytes each time that are never given back. Units
// almost never come and go between refreshes, so merging the new list into the
// old one usually changes nothing at all.
type svcRow struct {
	svc services.Service
}

// svcCell is one realised cell. GTK recycles cells as the table scrolls, so
// bind/unbind track which row each is currently showing.
type svcCell interface{ refresh() }

type svcTextCell struct {
	label  *gtk.Label
	render func(services.Service) string
	row    *svcRow
	cur    string
	set    bool
}

func (c *svcTextCell) refresh() {
	if c.row == nil {
		return
	}
	text := c.render(c.row.svc)
	if c.set && text == c.cur {
		return
	}
	c.cur, c.set = text, true
	c.label.SetText(text)
}

// svcDotCell is the coloured status dot.
type svcDotCell struct {
	dot *gtk.Box
	row *svcRow
	cur services.Status
	set bool
}

var statusClass = map[services.Status]string{
	services.Running: "running",
	services.Failed:  "failed",
	services.Stopped: "stopped",
}

func (c *svcDotCell) refresh() {
	if c.row == nil {
		return
	}
	s := c.row.svc
	if c.set && s.Status == c.cur {
		return
	}
	if c.set {
		c.dot.RemoveCSSClass(statusClass[c.cur])
	}
	c.cur, c.set = s.Status, true
	c.dot.AddCSSClass(statusClass[s.Status])
	c.dot.SetTooltipText(s.Active + " / " + s.Sub)
}

type servicesView struct {
	root      *gtk.Box
	client    *services.Client
	model     *gioutil.ListModel[*svcRow]
	filter    *gtk.CustomFilter
	selection *gtk.SingleSelection
	banner    *gtk.Label

	// order mirrors the model, sorted by unit name; cells holds every realised
	// cell, keyed by its native GtkColumnViewCell pointer.
	order []*svcRow
	cells map[uintptr]svcCell

	search       string
	servicesOnly bool
	loaded       bool
}

func newServicesView() *servicesView {
	v := &servicesView{servicesOnly: true, cells: make(map[uintptr]svcCell)}
	v.client, _ = services.NewClient()

	v.root = gtk.NewBox(gtk.OrientationVertical, 8)
	v.root.SetMarginTop(12)
	v.root.SetMarginBottom(12)
	v.root.SetMarginStart(12)
	v.root.SetMarginEnd(12)

	v.root.Append(v.buildToolbar())

	v.banner = gtk.NewLabel("")
	v.banner.AddCSSClass("am-subtle")
	v.banner.SetXAlign(0)
	v.banner.SetVisible(false)
	v.root.Append(v.banner)

	// Model chain: base -> filter (search) -> single selection.
	v.model = gioutil.NewListModel[*svcRow]()
	v.filter = gtk.NewCustomFilter(v.matches)
	filterModel := gtk.NewFilterListModel(v.model, &v.filter.Filter)
	v.selection = gtk.NewSingleSelection(filterModel)

	cv := gtk.NewColumnView(v.selection)
	cv.SetShowRowSeparators(true)
	cv.AppendColumn(v.statusColumn())
	cv.AppendColumn(v.textColumn("Service", true, func(s services.Service) string { return s.Name }))
	cv.AppendColumn(v.textColumn("Description", true, func(s services.Service) string { return s.Description }))
	cv.AppendColumn(v.textColumn("Startup", false, func(s services.Service) string { return s.Enabled }))

	scroller := gtk.NewScrolledWindow()
	scroller.SetChild(cv)
	scroller.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	scroller.SetVExpand(true)
	v.root.Append(scroller)

	if v.client == nil {
		v.setBanner("systemd D-Bus unavailable — service control is disabled.")
	}
	return v
}

func (v *servicesView) buildToolbar() *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 6)

	mkBtn := func(label string, fn func(string) error) *gtk.Button {
		b := gtk.NewButtonWithLabel(label)
		b.ConnectClicked(func() { v.doAction(fn) })
		bar.Append(b)
		return b
	}
	mkBtn("Start", func(n string) error { return v.client.Start(n) })
	mkBtn("Stop", func(n string) error { return v.client.Stop(n) })
	mkBtn("Restart", func(n string) error { return v.client.Restart(n) })
	mkBtn("Enable", func(n string) error { return v.client.Enable(n) })
	mkBtn("Disable", func(n string) error { return v.client.Disable(n) })

	refresh := gtk.NewButtonWithLabel("Refresh")
	refresh.ConnectClicked(func() { v.refresh() })
	bar.Append(refresh)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	bar.Append(spacer)

	allToggle := gtk.NewToggleButton()
	allToggle.SetLabel("All unit types")
	allToggle.ConnectToggled(func() {
		v.servicesOnly = !allToggle.Active()
		v.refresh()
	})
	bar.Append(allToggle)

	search := gtk.NewSearchEntry()
	search.SetHExpand(false)
	search.ConnectSearchChanged(func() {
		v.search = strings.ToLower(search.Text())
		v.filter.Changed(gtk.FilterChangeDifferent)
	})
	bar.Append(search)
	return bar
}

func (v *servicesView) Root() gtk.Widgetter { return v.root }

// Update lazily loads the service list the first time the view is shown.
func (v *servicesView) Update() {
	if v.loaded {
		return
	}
	v.loaded = true
	v.refresh()
}

func (v *servicesView) refresh() {
	if v.client == nil {
		return
	}
	go func() {
		svcs, err := v.client.List(v.servicesOnly)
		glib.IdleAdd(func() {
			if err != nil {
				v.setBanner("D-Bus error: " + err.Error())
				return
			}
			v.setBanner("")
			v.apply(svcs)
		})
	}()
}

// apply merges a freshly listed set of units into the model. Both sides are
// sorted by name, so this is a linear merge: unchanged units keep their row
// object (updated in place), and only genuine arrivals and departures splice
// the model.
func (v *servicesView) apply(list []services.Service) {
	i := 0
	for _, s := range list {
		for i < len(v.order) && v.order[i].svc.Name < s.Name {
			v.removeAt(i)
		}
		if i < len(v.order) && v.order[i].svc.Name == s.Name {
			v.order[i].svc = s // same unit, new state
			i++
			continue
		}
		row := &svcRow{svc: s}
		v.order = append(v.order, nil)
		copy(v.order[i+1:], v.order[i:])
		v.order[i] = row
		v.model.Splice(i, 0, row)
		i++
	}
	for i < len(v.order) {
		v.removeAt(i)
	}
	for _, c := range v.cells {
		c.refresh()
	}
}

func (v *servicesView) removeAt(i int) {
	v.model.Remove(i)
	v.order = append(v.order[:i], v.order[i+1:]...)
}

func (v *servicesView) doAction(fn func(string) error) {
	if v.client == nil {
		v.setBanner("systemd D-Bus unavailable.")
		return
	}
	item := v.selection.SelectedItem()
	if item == nil {
		v.setBanner("Select a service first.")
		return
	}
	name := gioutil.ObjectValue[*svcRow](item).svc.Name
	go func() {
		err := fn(name)
		glib.IdleAdd(func() {
			if err != nil {
				v.setBanner(name + ": " + err.Error())
			} else {
				v.setBanner("")
				v.refresh()
			}
		})
	}()
}

func (v *servicesView) matches(item *coreglib.Object) bool {
	if v.search == "" {
		return true
	}
	s := gioutil.ObjectValue[*svcRow](item).svc
	return containsFold(s.Name, v.search) || containsFold(s.Description, v.search)
}

func (v *servicesView) setBanner(text string) {
	v.banner.SetText(text)
	v.banner.SetVisible(text != "")
}

func (v *servicesView) textColumn(title string, expand bool, render func(services.Service) string) *gtk.ColumnViewColumn {
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		label := gtk.NewLabel("")
		label.SetXAlign(0)
		label.SetEllipsize(3) // PANGO_ELLIPSIZE_END
		cell.SetChild(label)
		v.cells[cell.Native()] = &svcTextCell{label: label, render: render}
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		c, _ := v.cells[cell.Native()].(*svcTextCell)
		if c == nil {
			return
		}
		c.row = rowOfService(cell)
		c.refresh()
	})
	factory.ConnectUnbind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		if c, ok := v.cells[cell.Native()].(*svcTextCell); ok {
			c.row = nil
		}
	})
	factory.ConnectTeardown(func(obj *coreglib.Object) {
		delete(v.cells, obj.Cast().(*gtk.ColumnViewCell).Native())
	})

	col := gtk.NewColumnViewColumn(title, &factory.ListItemFactory)
	col.SetExpand(expand)
	col.SetResizable(true)
	return col
}

func (v *servicesView) statusColumn() *gtk.ColumnViewColumn {
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		dot := gtk.NewBox(gtk.OrientationHorizontal, 0)
		dot.AddCSSClass("am-dot")
		dot.SetVAlign(gtk.AlignCenter)
		dot.SetHAlign(gtk.AlignCenter)
		dot.SetSizeRequest(12, 12)
		cell.SetChild(dot)
		v.cells[cell.Native()] = &svcDotCell{dot: dot}
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		c, _ := v.cells[cell.Native()].(*svcDotCell)
		if c == nil {
			return
		}
		c.row = rowOfService(cell)
		c.refresh()
	})
	factory.ConnectUnbind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		if c, ok := v.cells[cell.Native()].(*svcDotCell); ok {
			c.row = nil
		}
	})
	factory.ConnectTeardown(func(obj *coreglib.Object) {
		delete(v.cells, obj.Cast().(*gtk.ColumnViewCell).Native())
	})

	col := gtk.NewColumnViewColumn("", &factory.ListItemFactory)
	col.SetFixedWidth(36)
	return col
}

func rowOfService(cell *gtk.ColumnViewCell) *svcRow {
	item := cell.Item()
	if item == nil {
		return nil
	}
	return gioutil.ObjectValue[*svcRow](item)
}
