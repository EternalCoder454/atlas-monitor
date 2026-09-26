// Package ui builds the sidebar, the content stack, and the individual
// resource views.
package ui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/config"
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
func newHeadline() (number, caption *liveLabel, box *gtk.Box) {
	box = gtk.NewBox(gtk.OrientationVertical, 0)
	n := gtk.NewLabel("—")
	n.AddCSSClass("am-headline")
	n.SetXAlign(0)
	c := gtk.NewLabel("")
	c.AddCSSClass("am-subtle")
	c.SetXAlign(0)
	box.Append(n)
	box.Append(c)
	return newLiveLabel(n), newLiveLabel(c), box
}

// newTitle is a medium bold heading (used by disk/network/gpu views).
func newTitle(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.AddCSSClass("am-title")
	l.SetXAlign(0)
	return l
}

// newHeader builds a title plus a muted caption beneath it.
func newHeader() (title, caption *liveLabel, box *gtk.Box) {
	box = gtk.NewBox(gtk.OrientationVertical, 2)
	t := newTitle("—")
	c := gtk.NewLabel("")
	c.AddCSSClass("am-subtle")
	c.SetXAlign(0)
	box.Append(t)
	box.Append(c)
	return newLiveLabel(t), newLiveLabel(c), box
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
func (g *statGrid) add(label string) *liveLabel {
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
	return newLiveLabel(v)
}

// orDash returns s, or an em dash when s is empty.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// section is a page heading that folds away what is under it.
//
// The headings used to be plain labels, so every page showed everything it had
// whether or not the reader wanted it — and a 32-core grid is a third of the
// CPU page and the most expensive thing on it to draw. Folded, a section costs
// nothing: expanded() is false, so the view skips updating what is inside as
// well as GTK skipping drawing it.
type section struct {
	exp   *gtk.Expander
	title string
	s     *config.Settings
}

// newSection wraps child in a fold, remembering whether it was left open.
func newSection(title string, child gtk.Widgetter, s *config.Settings) *section {
	lbl := gtk.NewLabel(title)
	lbl.AddCSSClass("am-section-label")
	lbl.SetXAlign(0)

	exp := gtk.NewExpander("")
	exp.SetLabelWidget(lbl)
	exp.SetChild(child)
	exp.SetExpanded(!sectionCollapsed(s, title))

	sec := &section{exp: exp, title: title, s: s}
	exp.NotifyProperty("expanded", func() { sec.save() })
	return sec
}

// widget is what the page appends.
func (s *section) widget() gtk.Widgetter { return s.exp }

// expanded reports whether what is inside is worth updating.
func (s *section) expanded() bool { return s.exp.Expanded() }

func (s *section) save() {
	if s.s == nil {
		return
	}
	s.s.CollapsedSections = withoutSection(s.s.CollapsedSections, s.title)
	if !s.exp.Expanded() {
		s.s.CollapsedSections = append(s.s.CollapsedSections, s.title)
	}
	_ = config.Save(*s.s)
}

func sectionCollapsed(s *config.Settings, title string) bool {
	if s == nil {
		return false
	}
	for _, t := range s.CollapsedSections {
		if t == title {
			return true
		}
	}
	return false
}

func withoutSection(names []string, drop string) []string {
	out := names[:0:0]
	for _, n := range names {
		if n != drop {
			out = append(out, n)
		}
	}
	return out
}
