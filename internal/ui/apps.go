package ui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/config"
	"atlas-monitor/internal/format"
	"atlas-monitor/internal/process"
)

// procRow is a stable row object. The same pointer is kept in the list model
// for the lifetime of a process (or group), and its values are mutated in place
// each tick. This avoids recreating ~700 GObjects every second.
//
// gen is the tick number in which the row was last seen; rows left behind by an
// older tick have exited. A generation stamp replaces the "seen" set the diff
// used to allocate on every refresh.
type procRow struct {
	proc process.Proc
	gen  uint64

	// live is false while the row is vacant: its process has exited and the row
	// is waiting in the free list to be reused for the next one. A vacant row
	// stays in the list model and is hidden by the filter — see the free list in
	// appsView for why rows are recycled rather than removed.
	live bool

	// shown is what the filter last decided about this row. The two differ only
	// between a row changing state and GTK being told, which is what makes it
	// possible to notice that a row retired and reused within one tick never
	// changed visibility at all.
	shown bool
}

// renderFunc appends a cell's text to dst. Working in bytes lets a cell compare
// the new text against what it already shows and skip the update entirely —
// which is what most cells do most seconds.
type renderFunc func(dst []byte, p *process.Proc) []byte

// procCell is the state behind one realised table cell. GTK recycles cells as
// the table scrolls, so setup/bind/unbind track which row (if any) a cell is
// currently showing.
//
// GTK keeps widgets realised for roughly 200 rows of a list — it measures that
// many to size the view — so a 700-process table holds about 1800 cells however
// few are on screen. Refreshing them is therefore the app's hottest path, and
// each cell skipping an unchanged value is what keeps it nearly free.
type procCell struct {
	label  *gtk.Label
	render renderFunc
	row    *procRow // nil while the cell is unbound (off screen)
	buf    []byte   // render scratch
	cur    []byte   // text currently displayed
	set    bool

	// dim decides whether this reading is the boring one — a rate of zero, a
	// process with no GPU handle, a power draw of "Very low". Those are worth
	// showing, since a blank cell would look like a collector failure, but a
	// table where nine columns in ten read zero hides the rows that are
	// actually doing something. dimmed tracks what the label currently wears so
	// the CSS class is only touched when it changes.
	dim    func([]byte) bool
	dimmed bool

	// heat marks a reading as worth noticing. Dimming answers "which rows can I
	// ignore"; this answers the opposite question, which is the one you open the
	// page to ask. Only the CPU column uses it: it is the one column with a
	// scale that means the same thing on every machine.
	heat   func(*process.Proc) int
	heated int
}

// refresh re-renders the cell, touching GTK only when the text really changed.
func (c *procCell) refresh() {
	if c.row == nil {
		return
	}
	c.buf = c.render(c.buf[:0], &c.row.proc)
	if c.set && bytes.Equal(c.buf, c.cur) {
		return
	}
	c.cur = append(c.cur[:0], c.buf...)
	c.set = true
	c.label.SetText(string(c.buf))
	if c.dim != nil {
		if d := c.dim(c.buf); d != c.dimmed {
			c.dimmed = d
			if d {
				c.label.AddCSSClass("am-zero")
			} else {
				c.label.RemoveCSSClass("am-zero")
			}
		}
	}
	if c.heat != nil {
		if h := c.heat(&c.row.proc); h != c.heated {
			for _, cls := range heatClasses {
				c.label.RemoveCSSClass(cls)
			}
			if h > 0 && h <= len(heatClasses) {
				c.label.AddCSSClass(heatClasses[h-1])
			}
			c.heated = h
		}
	}
}

// heatClasses are the styles for a busy reading, in increasing order.
var heatClasses = []string{"am-warm", "am-hot"}

// cpuHeat grades a process's CPU share. The thresholds are per core, matching
// what the column shows: a thread pinned to one core reads 100 whatever the
// machine has. Below a quarter of a core nothing is marked, because on a busy
// desktop that would mark half the table and mean nothing.
func cpuHeat(p *process.Proc) int {
	switch {
	case p.CPU >= 60:
		return 2
	case p.CPU >= 25:
		return 1
	}
	return 0
}

// resortActiveColumn asks the column currently being sorted on to compare its
// rows again. Nothing happens when the table is unsorted.
func (v *appsView) resortActiveColumn() {
	if v.columnView == nil {
		return
	}
	// ColumnView.Sorter returns the base type; the concrete object is the
	// column-view sorter that knows which column is primary.
	cvs, ok := v.columnView.Sorter().Cast().(*gtk.ColumnViewSorter)
	if !ok || cvs == nil {
		return
	}
	col := cvs.PrimarySortColumn()
	if col == nil {
		return
	}
	if so := v.sortFor[col]; so != nil {
		so.Changed(gtk.SorterChangeDifferent)
	}
}

// isIdleReading reports whether a numeric cell shows nothing of interest: no
// digit above zero anywhere in it. That covers "0 B/s", "0.0%", "0.00 GiB" and
// the em dash used where a figure does not apply.
func isIdleReading(b []byte) bool {
	for _, ch := range b {
		if ch >= '1' && ch <= '9' {
			return false
		}
	}
	return true
}

type appsView struct {
	root       *gtk.Box
	proc       *process.Collector
	model      *gioutil.ListModel[*procRow]
	filter     *gtk.CustomFilter
	scroller   *gtk.ScrolledWindow
	columnView *gtk.ColumnView
	popover    *gtk.PopoverMenu

	// Stable row registry and current model order (parallel to the model).
	// Ungrouped rows are keyed by pid, grouped rows by process name, so the
	// per-tick diff never has to build a key string.
	byPID  map[int]*procRow
	byName map[string]*procRow
	order  []*procRow
	gen    uint64

	// sortFor maps a column to its comparer. The table is sorted by GTK, which
	// re-sorts when the model changes — but the rows are mutated in place, so
	// nothing tells it the numbers moved. Without a nudge the order is whatever
	// it was when the rows were first added, which meant a table headed "CPU %"
	// that never actually put the busy process at the top.
	sortFor map[*gtk.ColumnViewColumn]*gtk.Sorter

	// free holds rows whose process has exited. They stay in the list model and
	// are reused for the next process that appears, so the model's item set only
	// ever grows to the high-water mark of concurrent processes and never churns.
	//
	// This is not an optimisation, it is a leak fix. Attaching any Go callback to
	// a list model — the search GtkCustomFilter here, the column GtkCustomSorters
	// below — makes gotk4 take a reference on every item the callback is handed
	// and never give it back, so an item spliced out of the model is pinned for
	// the life of the process. On a machine with ordinary process churn that is
	// around a megabyte a minute, for as long as this page is open. The pinning
	// is per item rather than per call, so holding the item set steady is what
	// bounds it; TestAppsRowsAreRecycled and TestRowsAreReleasedWhenSplicedOut
	// cover both halves of that.
	free []*procRow

	// cells holds the state of every realised cell, keyed by the native
	// GtkColumnViewCell pointer (stable for the cell's lifetime). byLabel is the
	// same set keyed by the label inside each cell, which is what a click on the
	// table resolves to.
	cells   map[uintptr]*procCell
	byLabel map[uintptr]*procCell

	// Scratch reused across ticks.
	snap     []process.Proc
	grouping []process.Proc
	groups   map[string]int
	appended []*procRow
	pending  []int // indices into the snapshot with no row yet

	search      string
	grouped     bool
	showKernel  bool
	needRebuild bool
	targetPID   int
	targetName  string
	// targetStart is the start time of the process the context menu was opened
	// on. PIDs are reused, and the menu acts some seconds after it was opened,
	// so the signal is only sent if this still matches — otherwise Atlas would
	// eventually kill a process that merely inherited the number.
	targetStart uint64
}

func newAppsView(proc *process.Collector, gpuAvail bool, settings *config.Settings) *appsView {
	v := &appsView{
		proc:    proc,
		byPID:   make(map[int]*procRow),
		byName:  make(map[string]*procRow),
		cells:   make(map[uintptr]*procCell),
		sortFor: make(map[*gtk.ColumnViewColumn]*gtk.Sorter),
		byLabel: make(map[uintptr]*procCell),
		groups:  make(map[string]int),
	}

	v.root = gtk.NewBox(gtk.OrientationVertical, 8)
	v.root.SetMarginTop(12)
	v.root.SetMarginBottom(12)
	v.root.SetMarginStart(12)
	v.root.SetMarginEnd(12)

	// Toolbar: search + group toggle.
	toolbar := gtk.NewBox(gtk.OrientationHorizontal, 8)
	searchEntry := gtk.NewSearchEntry()
	searchEntry.SetHExpand(true)
	searchEntry.SetPlaceholderText("Search by name or PID")
	searchEntry.ConnectSearchChanged(func() {
		v.search = lowerASCII(searchEntry.Text())
		v.filter.Changed(gtk.FilterChangeDifferent)
	})
	groupBtn := gtk.NewToggleButton()
	groupBtn.SetLabel("Group by app")
	groupBtn.ConnectToggled(func() {
		v.grouped = groupBtn.Active()
		v.needRebuild = true
		v.Update()
	})
	// Kernel threads are three quarters of /proc on a typical machine and there
	// is nothing a user can do with them, so they start hidden — which also
	// keeps the table (and the widgets GTK realises for it) a quarter the size.
	kernelBtn := gtk.NewToggleButton()
	kernelBtn.SetLabel("Kernel threads")
	kernelBtn.SetTooltipText("Show kernel worker threads (kworker, ksoftirqd, …)")
	kernelBtn.ConnectToggled(func() {
		v.showKernel = kernelBtn.Active()
		// Tell the collector too: with them hidden it can drop a kernel thread
		// the moment the stat line identifies one, and skip the rest of the
		// files it would otherwise open for it.
		v.proc.SetIncludeKernel(v.showKernel)
		v.needRebuild = true
		v.Update()
	})
	colBtn := gtk.NewMenuButton()
	colBtn.SetLabel("Columns")
	colBtn.SetTooltipText("Choose which columns the table shows")

	toolbar.Append(searchEntry)
	toolbar.Append(groupBtn)
	toolbar.Append(kernelBtn)
	toolbar.Append(colBtn)
	v.root.Append(toolbar)

	// Model chain: base -> filter (search) -> sort (column headers) -> selection.
	v.model = gioutil.NewListModel[*procRow]()
	v.filter = gtk.NewCustomFilter(v.matches)
	filterModel := gtk.NewFilterListModel(v.model, &v.filter.Filter)

	cv := gtk.NewColumnView(nil)
	cv.SetShowRowSeparators(true)
	cv.SetReorderable(false)
	v.columnView = cv

	cv.AppendColumn(v.textColumn("Name", true, 0,
		func(dst []byte, p *process.Proc) []byte { return append(dst, p.Name...) },
		func(a, b *process.Proc) bool { return lessFold(a.Name, b.Name) },
		colOpts{minChars: 16}))
	cv.AppendColumn(v.textColumn("PID", false, 1,
		func(dst []byte, p *process.Proc) []byte { return strconv.AppendInt(dst, int64(p.PID), 10) },
		func(a, b *process.Proc) bool { return a.PID < b.PID }))
	cpuCol := v.textColumn("CPU %", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendPercent1(dst, p.CPU) },
		func(a, b *process.Proc) bool { return a.CPU < b.CPU },
		colOpts{heat: cpuHeat})
	cv.AppendColumn(cpuCol)
	cv.AppendColumn(v.textColumn("RAM", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendBytes(dst, p.RSS) },
		func(a, b *process.Proc) bool { return a.RSS < b.RSS }))
	if gpuAvail {
		cv.AppendColumn(v.textColumn("GPU %", false, 1, appendGPU,
			func(a, b *process.Proc) bool { return a.GPU < b.GPU }))
	}
	// Sorted by the underlying score, not the label, so the order runs
	// Very low → High rather than alphabetically.
	cv.AppendColumn(v.textColumn("Power", false, 0,
		func(dst []byte, p *process.Proc) []byte { return append(dst, p.Impact().String()...) },
		func(a, b *process.Proc) bool { return a.PowerScore() < b.PowerScore() },
		colOpts{dim: func(b []byte) bool { return string(b) == process.ImpactVeryLow.String() }}))
	cv.AppendColumn(v.textColumn("Net ≈ In", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.NetIn) },
		func(a, b *process.Proc) bool { return a.NetIn < b.NetIn }))
	cv.AppendColumn(v.textColumn("Net ≈ Out", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.NetOut) },
		func(a, b *process.Proc) bool { return a.NetOut < b.NetOut }))
	// Per-process disk figures count blocks that actually reach the drive, and
	// on any machine with room for a page cache that is almost nothing: two
	// columns of "0 B/s" holding width that the name column needed. They are
	// still there for whoever wants them, behind the Columns button.
	readCol := v.textColumn("Disk Read", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.DiskRead) },
		func(a, b *process.Proc) bool { return a.DiskRead < b.DiskRead })
	writeCol := v.textColumn("Disk Write", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.DiskWrite) },
		func(a, b *process.Proc) bool { return a.DiskWrite < b.DiskWrite })
	readCol.SetVisible(settings.ShowIOColumns)
	writeCol.SetVisible(settings.ShowIOColumns)
	cv.AppendColumn(readCol)
	cv.AppendColumn(writeCol)

	ioChk := gtk.NewCheckButtonWithLabel("Disk read and write")
	ioChk.SetActive(settings.ShowIOColumns)
	ioChk.ConnectToggled(func() {
		on := ioChk.Active()
		if on == settings.ShowIOColumns {
			return
		}
		readCol.SetVisible(on)
		writeCol.SetVisible(on)
		settings.ShowIOColumns = on
		_ = config.Save(*settings)
	})
	ioNote := gtk.NewLabel("Counts only reads and writes that reach the drive, so most\nprograms sit at zero whatever they are doing.")
	ioNote.SetXAlign(0)
	ioNote.AddCSSClass("dim-label")
	ioNote.AddCSSClass("caption")

	colBox := gtk.NewBox(gtk.OrientationVertical, 6)
	colBox.SetMarginTop(10)
	colBox.SetMarginBottom(10)
	colBox.SetMarginStart(12)
	colBox.SetMarginEnd(12)
	colBox.Append(ioChk)
	colBox.Append(ioNote)

	colPop := gtk.NewPopover()
	colPop.SetChild(colBox)
	colBtn.SetPopover(colPop)

	sortModel := gtk.NewSortListModel(filterModel, cv.Sorter())
	cv.SetModel(gtk.NewNoSelection(sortModel))
	cv.SortByColumn(cpuCol, gtk.SortDescending)

	// When the user changes the sort (clicks a header), jump back to the top so
	// the new ordering is shown from its start. This is deferred to an idle
	// callback so it runs *after* the model has finished re-sorting — doing it
	// synchronously in the signal lands on the pre-sort layout (and GTK's own
	// post-sort scroll then wins, dumping you at the bottom).
	cv.Sorter().ConnectChanged(func(_ gtk.SorterChange) {
		glib.IdleAdd(func() {
			if v.model.Len() > 0 {
				v.columnView.ScrollTo(0, nil, gtk.ListScrollNone, nil)
			}
		})
	})

	v.scroller = gtk.NewScrolledWindow()
	v.scroller.SetChild(cv)
	v.scroller.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	v.scroller.SetVExpand(true)
	v.root.Append(v.scroller)

	v.buildContextMenu(cv)
	return v
}

func (v *appsView) Root() gtk.Widgetter { return v.root }

// Update diffs the latest process snapshot against the stable row set, mutating
// existing rows in place and only splicing the model when processes appear or
// disappear. No per-tick model rebuild, so sort order and scroll position are
// preserved and no GObjects churn. Every container it uses is reused, so a
// machine with a steady process list allocates essentially nothing per second.
func (v *appsView) Update() {
	if v.needRebuild {
		v.clearModel()
		v.needRebuild = false
	}

	v.snap = v.proc.SnapshotInto(v.snap)
	snap := v.snap
	if !v.showKernel {
		snap = withoutKernelThreads(snap)
	}
	if v.grouped {
		snap = v.groupByName(snap)
	}
	v.applyRows(snap)

	// Refresh only the currently-visible cells (~rows on screen × columns).
	for _, c := range v.cells {
		c.refresh()
	}
}

// applyRows merges one snapshot into the table's stable rows: values are updated
// in place, a process that has appeared takes a recycled row (or a new one if
// there is none to recycle), and a process that has exited retires its row to
// the free list. It is split out from Update so the diff can be driven directly
// by the tests, which have neither a collector nor a display.
func (v *appsView) applyRows(snap []process.Proc) {
	v.gen++

	// Three passes, in this order for a reason. Processes that are still here
	// are stamped first; then rows whose process is gone are retired, which is
	// what fills the free list; only then are new processes given rows. Adding
	// before retiring would find the free list empty every tick and leave the
	// model at twice the high-water mark.
	pending := v.pending[:0]
	for i := range snap {
		p := &snap[i]
		if row := v.lookup(p); row != nil {
			row.proc = *p // same process, update values in place
			row.gen = v.gen
			continue
		}
		pending = append(pending, i)
	}

	for _, row := range v.order {
		if row.live && row.gen != v.gen {
			row.live = false
			v.unregister(row)
			v.free = append(v.free, row)
		}
	}

	toAppend := v.appended[:0]
	for _, i := range pending {
		p := &snap[i]
		var row *procRow
		if n := len(v.free); n > 0 {
			// Reuse a row whose process exited: it is already in the model, so
			// nothing is spliced and nothing is pinned.
			row, v.free = v.free[n-1], v.free[:n-1]
			row.proc = *p
			row.live = true
		} else {
			row = &procRow{proc: *p, live: true}
			v.order = append(v.order, row)
			toAppend = append(toAppend, row)
		}
		row.gen = v.gen
		v.register(row)
	}
	if len(toAppend) > 0 {
		v.model.Splice(len(v.order)-len(toAppend), 0, toAppend...)
	}
	v.appended = toAppend[:0] // keep the buffers for the next tick
	v.pending = pending[:0]

	// Tell the sorter the readings moved. Every row's values are rewritten in
	// place each tick and GTK has no way to know, so without this the order
	// never changes after the rows are first added — a table headed "CPU %"
	// that never put the busy process at the top.
	//
	// Only the column actually being sorted on is nudged. Nudging all ten made
	// GTK re-sort the whole table once per column per tick, which measured at
	// half again the page's cost for nine sorts nobody asked for.
	v.resortActiveColumn()

	// The filter decides visibility from row.live, which GTK cannot see change,
	// so a row appearing or retiring has to be announced — but only if the
	// visible set really is different now. On a machine with busy process churn
	// most ticks retire a row and immediately reuse it for a new process, which
	// changes that row's contents but not whether it is shown. Announcing those
	// ticks anyway made GTK tear down and rebuild the table's cells every
	// second, which is most of what the page cost and most of what it leaked.
	var hidden, revealed int
	for _, row := range v.order {
		if row.live == row.shown {
			continue
		}
		row.shown = row.live
		if row.live {
			revealed++
		} else {
			hidden++
		}
	}
	switch {
	case hidden > 0 && revealed > 0:
		v.filter.Changed(gtk.FilterChangeDifferent)
	case hidden > 0:
		v.filter.Changed(gtk.FilterChangeMoreStrict)
	case revealed > 0:
		v.filter.Changed(gtk.FilterChangeLessStrict)
	}
}

// withoutKernelThreads compacts the snapshot in place, dropping kernel threads.
// The caller owns the backing array and refills it every tick, so this costs
// nothing beyond the walk.
func withoutKernelThreads(procs []process.Proc) []process.Proc {
	kept := procs[:0]
	for _, p := range procs {
		if !p.Kernel {
			kept = append(kept, p)
		}
	}
	return kept
}

// lookup finds the stable row for p under the current grouping mode.
func (v *appsView) lookup(p *process.Proc) *procRow {
	if v.grouped {
		return v.byName[p.Name]
	}
	return v.byPID[p.PID]
}

func (v *appsView) register(row *procRow) {
	if v.grouped {
		v.byName[row.proc.Name] = row
	} else {
		v.byPID[row.proc.PID] = row
	}
}

func (v *appsView) unregister(row *procRow) {
	if v.grouped {
		delete(v.byName, row.proc.Name)
	} else {
		delete(v.byPID, row.proc.PID)
	}
}

// clearModel empties the table without emptying the list model. Every row is
// retired to the free list instead of being spliced out, for the same reason the
// per-tick diff recycles rows: an item removed from a model that has a Go
// callback attached is pinned for the life of the process, so toggling "Group by
// app" a few dozen times would otherwise cost as much as an hour on the page.
func (v *appsView) clearModel() {
	clear(v.byPID)
	clear(v.byName)
	v.free = v.free[:0]
	hidden := false
	for _, row := range v.order {
		if row.live {
			hidden = true
		}
		row.live = false
		row.shown = false
		row.gen = 0
		v.free = append(v.free, row)
	}
	if hidden {
		v.filter.Changed(gtk.FilterChangeMoreStrict)
	}
	for _, c := range v.cells {
		c.row = nil
		c.set = false
	}
}

// matches implements the search filter. It works directly on the row's fields,
// with no lower-casing copy or PID-to-string conversion per item.
func (v *appsView) matches(item *coreglib.Object) bool {
	return v.matchesRow(gioutil.ObjectValue[*procRow](item))
}

// matchesRow is the predicate itself, separate from unwrapping the list item so
// the tests can exercise it without a GObject.
func (v *appsView) matchesRow(r *procRow) bool {
	if !r.live {
		return false // a retired row waiting on the free list
	}
	if v.search == "" {
		return true
	}
	if containsFold(r.proc.Name, v.search) {
		return true
	}
	var digits [20]byte
	return containsBytes(strconv.AppendInt(digits[:0], int64(r.proc.PID), 10), v.search)
}

// textColumn builds a sortable text column. xalign: 0 left, 1 right. A
// right-aligned column is a numeric one, which is what decides both the tabular
// figures and the dimming of idle readings; dimWhen overrides that test for
// columns whose "nothing happening" value is not a zero.
// colOpts are the behaviours only some columns want.
type colOpts struct {
	// dim overrides the default test for an uninteresting reading, for a column
	// whose quiet value is not a zero.
	dim func([]byte) bool
	// heat grades a reading as worth noticing. Only the CPU column sets it.
	heat func(*process.Proc) int
	// minChars is a floor on the column's width, in characters.
	//
	// Every cell label ellipsises, which means its minimum width is next to
	// nothing, which means GTK squeezes it before any column whose content has
	// a natural size. The name is the widest column and the only one that
	// expands, so it was always the one that collapsed — a table of "electr…"
	// and "youtu…" beside four columns of zeroes with room to spare.
	minChars int
}

func (v *appsView) textColumn(title string, expand bool, xalign float64,
	render renderFunc, less func(a, b *process.Proc) bool,
	opt ...colOpts) *gtk.ColumnViewColumn {

	var o colOpts
	if len(opt) > 0 {
		o = opt[0]
	}
	numeric := xalign == 1
	dim := o.dim
	if dim == nil && numeric {
		dim = isIdleReading
	}

	// GTK builds and discards list-item cells as the table changes, and each
	// setup used to make a fresh GtkLabel. gotk4 keeps a reference to every
	// GObject it hands a Go callback and never gives it back, so those labels —
	// and the accessibility context GTK creates alongside each one — were never
	// freed. Measured with heaptrack under heavy process churn: 4905 labels in
	// 150 seconds on this page against zero on a page with no table, about a
	// kilobyte each.
	//
	// The labels are pooled per column instead. A column only ever needs as many
	// as GTK realises at once, so after the table has filled the pool no more
	// are created, and the pinning stops growing with them. The pool is a plain
	// slice in this closure: one per column, only ever touched from the UI
	// thread.
	var pool []*gtk.Label

	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		var label *gtk.Label
		if n := len(pool); n > 0 {
			label, pool = pool[n-1], pool[:n-1]
			label.SetText("")
		} else {
			label = gtk.NewLabel("")
			label.SetXAlign(float32(xalign))
			label.SetEllipsize(3)
			if o.minChars > 0 {
				label.SetWidthChars(o.minChars)
			} // PANGO_ELLIPSIZE_END
			if numeric {
				label.AddCSSClass("am-num")
			}
		}
		cell.SetChild(label)
		c := &procCell{label: label, render: render, dim: dim, heat: o.heat}
		v.cells[cell.Native()] = c
		v.byLabel[label.Object.Native()] = c
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		c := v.cells[cell.Native()]
		if c == nil {
			return
		}
		c.row = rowOf(cell)
		c.refresh()
	})
	factory.ConnectUnbind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		if c := v.cells[cell.Native()]; c != nil {
			c.row = nil
		}
	})
	factory.ConnectTeardown(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		if c := v.cells[cell.Native()]; c != nil && c.label != nil {
			delete(v.byLabel, c.label.Object.Native())
			// Take the label off the cell before the cell goes, and keep it for
			// the next one. Without the unparent GTK would complain about a
			// widget with a parent being added elsewhere.
			cell.SetChild(nil)
			if c.dimmed {
				c.label.RemoveCSSClass("am-zero")
				c.dimmed = false
			}
			pool = append(pool, c.label)
		}
		delete(v.cells, cell.Native())
	})

	col := gtk.NewColumnViewColumn(title, &factory.ListItemFactory)
	col.SetExpand(expand)
	col.SetResizable(true)

	// This runs on every tick now, because the rows' values change in place and
	// the sorter has to be told (see applyRows). The Take() wrappers below are
	// therefore a warm path: they are safe because the set of row objects is
	// bounded — rows are recycled rather than replaced — so the references gotk4
	// keeps on them do not accumulate.
	sorter := gtk.NewCustomSorter(func(a, b unsafe.Pointer) int {
		ra := gioutil.ObjectValue[*procRow](coreglib.Take(a))
		rb := gioutil.ObjectValue[*procRow](coreglib.Take(b))
		switch {
		case less(&ra.proc, &rb.proc):
			return -1
		case less(&rb.proc, &ra.proc):
			return 1
		default:
			return 0
		}
	})
	col.SetSorter(&sorter.Sorter)
	v.sortFor[col] = &sorter.Sorter
	return col
}

func rowOf(cell *gtk.ColumnViewCell) *procRow {
	item := cell.Item()
	if item == nil {
		return nil
	}
	return gioutil.ObjectValue[*procRow](item)
}

func (v *appsView) buildContextMenu(parent gtk.Widgetter) {
	group := gio.NewSimpleActionGroup()
	add := func(name string, fn func()) {
		act := gio.NewSimpleAction(name, nil)
		act.ConnectActivate(func(_ *glib.Variant) { fn() })
		group.AddAction(act)
	}
	add("term", func() { v.kill(syscall.SIGTERM) })
	add("kill", func() { v.kill(syscall.SIGKILL) })
	add("stop", func() { v.kill(syscall.SIGSTOP) })
	add("cont", func() { v.kill(syscall.SIGCONT) })
	add("open", v.openLocation)

	if w, ok := parent.(*gtk.ColumnView); ok {
		w.InsertActionGroup("proc", group)
	}

	menu := gio.NewMenu()
	menu.Append("End Task", "proc.term")
	menu.Append("Kill", "proc.kill")
	menu.Append("Stop", "proc.stop")
	menu.Append("Continue", "proc.cont")
	menu.Append("Open file location", "proc.open")

	v.popover = gtk.NewPopoverMenuFromModel(menu)
	v.popover.SetHasArrow(false)

	// One gesture for the whole table, parented once. Anything per-cell here
	// would be registered for the life of the process — see attachContextMenu.
	if w, ok := parent.(*gtk.ColumnView); ok {
		v.attachContextMenu(w)
	}
}

// attachContextMenu wires one right-click gesture to the whole table, resolving
// the row under the pointer at click time.
//
// It used to be a gesture per cell, connected in the factory's setup handler.
// That leaks: gotk4 registers every Go callback in a process-wide registry and
// does not release the entry when the widget goes away — around eleven live
// objects per gesture, and disconnecting the handler first only halves it (see
// TestPerWidgetGestureClosuresAreRetained). GTK builds cells constantly, on
// scrolling as much as on refreshes, so on a machine with busy process churn the
// Apps page grew by roughly a megabyte a minute for as long as it was open.
// One gesture for the table costs one registration for the life of the process.
func (v *appsView) attachContextMenu(cv *gtk.ColumnView) {
	v.popover.SetParent(cv)
	click := gtk.NewGestureClick()
	click.SetButton(3) // secondary / right button
	click.ConnectPressed(func(_ int, x, y float64) {
		c := v.cellAt(cv, x, y)
		if c == nil || c.row == nil {
			return
		}
		v.targetPID, v.targetName = c.row.proc.PID, c.row.proc.Name
		v.targetStart, _ = process.StartTime(v.targetPID)
		rect := gdk.NewRectangle(int(x), int(y), 1, 1)
		v.popover.SetPointingTo(&rect)
		v.popover.Popup()
	})
	cv.AddController(click)
}

// cellAt finds the cell under a point in the table. Pick returns the deepest
// widget there, which is the label a cell owns (or something inside it), so the
// walk goes up a few parents looking for one this view put there.
func (v *appsView) cellAt(cv *gtk.ColumnView, x, y float64) *procCell {
	w := cv.Pick(x, y, gtk.PickDefault)
	for i := 0; w != nil && i < 4; i++ {
		if c := v.byLabel[gtk.BaseWidget(w).Object.Native()]; c != nil {
			return c
		}
		w = gtk.BaseWidget(w).Parent()
	}
	return nil
}

func (v *appsView) kill(sig syscall.Signal) {
	if !v.targetIsStillTheSameProcess() {
		return
	}
	_ = syscall.Kill(v.targetPID, sig)
}

// targetIsStillTheSameProcess re-checks that the PID the context menu was opened
// on is the same run of the same process now that the menu item has been
// chosen. Without this, a process that exits in the seconds between the two —
// and a machine that gets back round to its number — would put the signal on
// something innocent.
func (v *appsView) targetIsStillTheSameProcess() bool {
	if v.targetPID <= 0 {
		return false
	}
	now, ok := process.StartTime(v.targetPID)
	if !ok {
		return false // gone: nothing to signal
	}
	// A start time of zero means the field could not be read, on either side;
	// refuse rather than guess.
	return v.targetStart != 0 && now == v.targetStart
}

func (v *appsView) openLocation() {
	if !v.targetIsStillTheSameProcess() {
		return
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", v.targetPID))
	if err != nil {
		return
	}
	_ = exec.Command("xdg-open", filepath.Dir(exe)).Start()
}

// appendGPU renders the GPU column: a dash for processes that hold no GPU
// handle, a percentage for DRM clients.
func appendGPU(dst []byte, p *process.Proc) []byte {
	if p.GPU < 0 {
		return append(dst, "—"...)
	}
	return format.AppendPercent(dst, p.GPU)
}

// groupByName aggregates processes sharing a name into a single row, reusing
// the view's scratch slice and index map.
func (v *appsView) groupByName(procs []process.Proc) []process.Proc {
	out := v.grouping[:0]
	clear(v.groups)
	for i := range procs {
		p := &procs[i]
		idx, ok := v.groups[p.Name]
		if !ok {
			v.groups[p.Name] = len(out)
			out = append(out, *p)
			continue
		}
		g := &out[idx]
		g.CPU += p.CPU
		g.RSS += p.RSS
		g.NetIn += p.NetIn
		g.NetOut += p.NetOut
		g.DiskRead += p.DiskRead
		g.DiskWrite += p.DiskWrite
		if p.GPU > 0 {
			if g.GPU < 0 {
				g.GPU = 0
			}
			g.GPU += p.GPU
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPU > out[j].CPU })
	v.grouping = out
	return out
}

// lowerASCII lower-cases a search string. Process names are ASCII.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for ; i < len(b); i++ {
				if b[i] >= 'A' && b[i] <= 'Z' {
					b[i] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// containsFold reports whether s contains the already-lower-cased sub,
// comparing ASCII case-insensitively without allocating.
func containsFold(s, sub string) bool {
	n := len(sub)
	if n == 0 {
		return true
	}
	for i := 0; i+n <= len(s); i++ {
		if equalFold(s[i:i+n], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, lower string) bool {
	for i := 0; i < len(a); i++ {
		c := a[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

// containsBytes is strings.Contains for a byte slice haystack, without the
// allocation a []byte→string conversion would cost.
func containsBytes(b []byte, sub string) bool {
	n := len(sub)
	if n == 0 {
		return true
	}
	for i := 0; i+n <= len(b); i++ {
		if string(b[i:i+n]) == sub {
			return true
		}
	}
	return false
}

// lessFold orders names case-insensitively without allocating.
func lessFold(a, b string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		ca, cb := foldByte(a[i]), foldByte(b[i])
		if ca != cb {
			return ca < cb
		}
	}
	return len(a) < len(b)
}

func foldByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
