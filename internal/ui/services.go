package ui

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/services"
)

type servicesView struct {
	root      *gtk.Box
	client    *services.Client
	model     *gioutil.ListModel[services.Service]
	filter    *gtk.CustomFilter
	selection *gtk.SingleSelection
	banner    *gtk.Label

	search       string
	servicesOnly bool
	loaded       bool
}

func newServicesView() *servicesView {
	v := &servicesView{servicesOnly: true}
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
	v.model = gioutil.NewListModel[services.Service]()
	v.filter = gtk.NewCustomFilter(v.matches)
	filterModel := gtk.NewFilterListModel(v.model, &v.filter.Filter)
	v.selection = gtk.NewSingleSelection(filterModel)

	cv := gtk.NewColumnView(v.selection)
	cv.SetShowRowSeparators(true)
	cv.AppendColumn(svcStatusColumn())
	cv.AppendColumn(svcTextColumn("Service", true, func(s services.Service) string { return s.Name }))
	cv.AppendColumn(svcTextColumn("Description", true, func(s services.Service) string { return s.Description }))
	cv.AppendColumn(svcTextColumn("Startup", false, func(s services.Service) string { return s.Enabled }))

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
			v.model.Splice(0, v.model.Len(), svcs...)
		})
	}()
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
	s := gioutil.ObjectValue[services.Service](item)
	go func() {
		err := fn(s.Name)
		glib.IdleAdd(func() {
			if err != nil {
				v.setBanner(s.Name + ": " + err.Error())
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
	s := gioutil.ObjectValue[services.Service](item)
	return strings.Contains(strings.ToLower(s.Name), v.search) ||
		strings.Contains(strings.ToLower(s.Description), v.search)
}

func (v *servicesView) setBanner(text string) {
	v.banner.SetText(text)
	v.banner.SetVisible(text != "")
}

func svcTextColumn(title string, expand bool, extract func(services.Service) string) *gtk.ColumnViewColumn {
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		li := obj.Cast().(*gtk.ColumnViewCell)
		label := gtk.NewLabel("")
		label.SetXAlign(0)
		label.SetEllipsize(3) // PANGO_ELLIPSIZE_END
		li.SetChild(label)
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		li := obj.Cast().(*gtk.ColumnViewCell)
		if label, ok := li.Child().(*gtk.Label); ok {
			label.SetText(extract(gioutil.ObjectValue[services.Service](li.Item())))
		}
	})
	col := gtk.NewColumnViewColumn(title, &factory.ListItemFactory)
	col.SetExpand(expand)
	col.SetResizable(true)
	return col
}

func svcStatusColumn() *gtk.ColumnViewColumn {
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		li := obj.Cast().(*gtk.ColumnViewCell)
		dot := gtk.NewBox(gtk.OrientationHorizontal, 0)
		dot.AddCSSClass("am-dot")
		dot.SetVAlign(gtk.AlignCenter)
		dot.SetHAlign(gtk.AlignCenter)
		dot.SetSizeRequest(12, 12)
		li.SetChild(dot)
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		li := obj.Cast().(*gtk.ColumnViewCell)
		dot, ok := li.Child().(*gtk.Box)
		if !ok {
			return
		}
		s := gioutil.ObjectValue[services.Service](li.Item())
		dot.RemoveCSSClass("running")
		dot.RemoveCSSClass("stopped")
		dot.RemoveCSSClass("failed")
		switch s.Status {
		case services.Running:
			dot.AddCSSClass("running")
		case services.Failed:
			dot.AddCSSClass("failed")
		default:
			dot.AddCSSClass("stopped")
		}
		dot.SetTooltipText(s.Active + " / " + s.Sub)
	})
	col := gtk.NewColumnViewColumn("", &factory.ListItemFactory)
	col.SetFixedWidth(36)
	return col
}
