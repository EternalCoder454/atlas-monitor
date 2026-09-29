// Package ui builds the sidebar, the content stack, and the individual
// resource views.
package ui

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-monitor/internal/config"
	"atlas-monitor/internal/process"
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

// newHeadline builds the large current-value number with a muted caption on
// the same row, pushed to the right edge, as Task Manager's Performance pages
// do: the reading on the left and what is being read on the right. The caption
// is the one that gives way when the window is narrow, so it ellipsizes rather
// than pushing the number out of view.
func newHeadline() (number, caption *liveLabel, box *gtk.Box) {
	n := gtk.NewLabel("—")
	n.AddCSSClass("am-headline")
	n.SetXAlign(0)
	return newTitleRow(n)
}

// newTitleRow lays out lead on the left and a right-aligned, ellipsizing
// caption beside it. newHeadline and newDeviceHeader differ only in the lead
// label's style, so the layout is written once.
func newTitleRow(lead *gtk.Label) (first, caption *liveLabel, box *gtk.Box) {
	box = gtk.NewBox(gtk.OrientationHorizontal, 12)
	c := gtk.NewLabel("")
	c.AddCSSClass("am-headline-caption")
	c.SetHExpand(true)
	c.SetXAlign(1)
	c.SetEllipsize(pango.EllipsizeEnd)
	c.SetVAlign(gtk.AlignCenter)
	box.Append(lead)
	box.Append(c)
	return newLiveLabel(lead), newLiveLabel(c), box
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

// newDeviceHeader is the title row of a page about one device: the device's
// own name on the left and a muted line about it on the right, in the same
// layout as newHeadline. newHeader stays as it was because the pages that use
// it put a sentence under their title, which does not fit on one row.
func newDeviceHeader() (title, caption *liveLabel, box *gtk.Box) {
	t := gtk.NewLabel("—")
	t.AddCSSClass("am-device-title")
	t.SetXAlign(0)
	return newTitleRow(t)
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
	kept := s.s.CollapsedSections[:0:0]
	for _, t := range s.s.CollapsedSections {
		if !strings.EqualFold(t, s.title) {
			kept = append(kept, t)
		}
	}
	s.s.CollapsedSections = kept
	if !s.exp.Expanded() {
		s.s.CollapsedSections = append(s.s.CollapsedSections, s.title)
	}
	_ = config.Save(*s.s)
}

func sectionCollapsed(s *config.Settings, title string) bool {
	if s == nil {
		return false
	}
	// Case-insensitively: the headings were upper case before the Task Manager
	// restyle ("CORES", now "Cores"), and a section folded under its old name
	// should still be folded.
	for _, t := range s.CollapsedSections {
		if strings.EqualFold(t, title) {
			return true
		}
	}
	return false
}

// without returns names minus one entry, keeping order. Both the hidden
// columns and the folded sections are stored as lists of titles, and both need
// to take one out.
func without(names []string, drop string) []string {
	out := names[:0:0]
	for _, n := range names {
		if n != drop {
			out = append(out, n)
		}
	}
	return out
}

// procIdent identifies a process across ticks.
//
// A pid on its own is not an identity: Linux reuses them, and both the places
// that hold on to one across time — the context menu that will send a signal,
// and the Energy Saver page that remembers what it has eased off — would
// otherwise act on whatever inherited the number. The start time from
// /proc/[pid]/stat pins it down.
type procIdent struct {
	pid   int
	start uint64
}

// identOf reads a process's identity now.
func identOf(pid int) procIdent {
	id := procIdent{pid: pid}
	id.start, _ = process.StartTime(pid)
	return id
}

// same reports whether the process behind this pid is still the one that was
// there when the identity was taken. A start time of nought means it could not
// be read on one side or the other, and it refuses rather than guessing.
func (id procIdent) same() bool {
	if id.pid <= 0 || id.start == 0 {
		return false
	}
	now, ok := process.StartTime(id.pid)
	return ok && now == id.start
}

// wanter is a view that needs particular per-process figures gathered while it
// is the one on screen.
//
// The scan only collects the expensive figures something is showing, and the
// Apps table decides that from which of its columns are hidden. Energy Saver
// scores processors, graphics, disk and network together, so without this it
// would quietly lose a term whenever a column was put away on a different page
// — the same list, silently ranked on less.
type wanter interface {
	applyWants()
}

// searcher is a view with a search box that typing should reach without the
// box having to be focused first.
//
// Nothing on these pages takes focus on purpose. A focused text box blinks its
// cursor, and GTK 4 fades the cursor in and out rather than toggling it, which
// redraws the window at the display's frame rate for as long as the blinking
// lasts — ten seconds after the last keystroke, by default. When the window
// opened on Apps, GTK handed its initial focus to the search box, and every
// launch there spent its first ten seconds drawing thirty-odd full frames a
// second: 150 to 490 ms of CPU each second, against about 35 once it stopped.
//
// Capturing keys instead keeps typing-to-search, which is all that focus was
// giving anyone. The capture only takes keys the focused widget does not use
// itself — letters that land on a sidebar row or the table, say — and the box
// takes focus the moment it receives one.
type searcher interface {
	// captureKeysFrom routes unclaimed typing inside from to the search box,
	// or stops routing it when from is nil.
	captureKeysFrom(from gtk.Widgetter)
}

// Commands, the way Task Manager's command bar has them: flat, text first, with
// a small glyph before the words. A row of framed buttons reads as a form to
// fill in; a row of flat commands reads as things the page can do, and leaves
// the table below as the thing being looked at.

// commandContent is the icon-and-label face shared by every kind of command.
func commandContent(icon, label string) *adw.ButtonContent {
	c := adw.NewButtonContent()
	c.SetIconName(icon)
	c.SetLabel(label)
	return c
}

// newCommandButton is a flat command that does something once.
func newCommandButton(icon, label string) *gtk.Button {
	b := gtk.NewButton()
	b.SetChild(commandContent(icon, label))
	b.AddCSSClass("flat")
	b.AddCSSClass("am-command")
	return b
}

// newCommandToggle is a flat command that stays on, for a way of looking at
// the page rather than an action on it. The face is returned for a label that
// changes: setting the button's own label would replace the icon with it.
func newCommandToggle(icon, label string) (*gtk.ToggleButton, *adw.ButtonContent) {
	face := commandContent(icon, label)
	b := gtk.NewToggleButton()
	b.SetChild(face)
	b.AddCSSClass("flat")
	b.AddCSSClass("am-command")
	return b, face
}

// newCommandSeparator divides a command bar into groups, as Task Manager's
// thin upright rule does between "Run new task" and the rest.
func newCommandSeparator() *gtk.Separator {
	sep := gtk.NewSeparator(gtk.OrientationVertical)
	sep.AddCSSClass("am-command-separator")
	return sep
}
