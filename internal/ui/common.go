// Package ui builds the sidebar, the content stack, and the individual
// resource views.
package ui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// View is one page in the content stack. Update refreshes it from the latest
// collected stats and is only called while the view is visible.
type View interface {
	Root() gtk.Widgetter
	Update()
}

// newPage returns a vertically scrolling page and its content box. Views append
// their widgets to the returned box.
func newPage() (*gtk.ScrolledWindow, *gtk.Box) {
	box := gtk.NewBox(gtk.OrientationVertical, 14)
	box.SetMarginTop(18)
	box.SetMarginBottom(18)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)

	sw := gtk.NewScrolledWindow()
	sw.SetChild(box)
	sw.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sw.SetHExpand(true)
	sw.SetVExpand(true)
	return sw, box
}

// newHeadline builds the large current-value number plus a muted caption below.
func newHeadline() (number, caption *gtk.Label, box *gtk.Box) {
	box = gtk.NewBox(gtk.OrientationVertical, 0)
	number = gtk.NewLabel("—")
	number.AddCSSClass("am-headline")
	number.SetXAlign(0)
	caption = gtk.NewLabel("")
	caption.AddCSSClass("am-subtle")
	caption.SetXAlign(0)
	box.Append(number)
	box.Append(caption)
	return number, caption, box
}

// newTitle is a medium bold heading (used by disk/network/gpu views).
func newTitle(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.AddCSSClass("am-title")
	l.SetXAlign(0)
	return l
}

// newHeader builds a title plus a muted caption beneath it.
func newHeader() (title, caption *gtk.Label, box *gtk.Box) {
	box = gtk.NewBox(gtk.OrientationVertical, 2)
	title = newTitle("—")
	caption = gtk.NewLabel("")
	caption.AddCSSClass("am-subtle")
	caption.SetXAlign(0)
	box.Append(title)
	box.Append(caption)
	return title, caption, box
}

// sectionTitle is a small bold heading used between blocks within a view.
func sectionTitle(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.AddCSSClass("am-section-label")
	l.SetXAlign(0)
	return l
}

// statGrid lays out label/value pairs across two columns (so detail panes read
// as a compact overview rather than a long scroll): pairs fill left-to-right,
// wrapping to a new row every two entries.
type statGrid struct {
	*gtk.Grid
	n int
}

func newStatGrid() *statGrid {
	g := gtk.NewGrid()
	g.SetRowSpacing(8)
	g.SetColumnSpacing(12)
	g.SetHExpand(true)
	g.AddCSSClass("am-stats")
	return &statGrid{Grid: g}
}

// add appends a label/value pair and returns the value label for later updates.
func (g *statGrid) add(label string) *gtk.Label {
	col := (g.n % 2) * 2 // 0 (left pair) or 2 (right pair)
	row := g.n / 2
	g.n++

	l := gtk.NewLabel(label)
	l.AddCSSClass("am-stat-label")
	l.SetXAlign(0)
	v := gtk.NewLabel("—")
	v.AddCSSClass("am-stat-value")
	v.SetXAlign(0)
	v.SetHExpand(true) // values share the extra width, spreading the two pairs apart
	v.SetSelectable(true)
	g.Grid.Attach(l, col, row, 1, 1)
	g.Grid.Attach(v, col+1, row, 1, 1)
	return v
}
