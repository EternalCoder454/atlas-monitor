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
	// GtkColumnViewCell pointer (stable for the cell's lifetime).
	cells map[uintptr]*procCell

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
	popHasPar   bool
	targetPID   int
	targetName  string
}

func newAppsView(proc *process.Collector) *appsView {
	v := &appsView{
		proc:   proc,
		byPID:  make(map[int]*procRow),
		byName: make(map[string]*procRow),
		cells:  make(map[uintptr]*procCell),
		groups: make(map[string]int),
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
	toolbar.Append(searchEntry)
	toolbar.Append(groupBtn)
	toolbar.Append(kernelBtn)
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
		func(a, b *process.Proc) bool { return lessFold(a.Name, b.Name) }))
	cv.AppendColumn(v.textColumn("PID", false, 1,
		func(dst []byte, p *process.Proc) []byte { return strconv.AppendInt(dst, int64(p.PID), 10) },
		func(a, b *process.Proc) bool { return a.PID < b.PID }))
	cpuCol := v.textColumn("CPU %", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendPercent1(dst, p.CPU) },
		func(a, b *process.Proc) bool { return a.CPU < b.CPU })
	cv.AppendColumn(cpuCol)
	cv.AppendColumn(v.textColumn("RAM", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendBytes(dst, p.RSS) },
		func(a, b *process.Proc) bool { return a.RSS < b.RSS }))
	cv.AppendColumn(v.textColumn("GPU %", false, 1, appendGPU,
		func(a, b *process.Proc) bool { return a.GPU < b.GPU }))
	// Sorted by the underlying score, not the label, so the order runs
	// Very low → High rather than alphabetically.
	cv.AppendColumn(v.textColumn("Power", false, 0,
		func(dst []byte, p *process.Proc) []byte { return append(dst, p.Impact().String()...) },
		func(a, b *process.Proc) bool { return a.PowerScore() < b.PowerScore() }))
	cv.AppendColumn(v.textColumn("Net ≈ In", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.NetIn) },
		func(a, b *process.Proc) bool { return a.NetIn < b.NetIn }))
	cv.AppendColumn(v.textColumn("Net ≈ Out", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.NetOut) },
		func(a, b *process.Proc) bool { return a.NetOut < b.NetOut }))
	cv.AppendColumn(v.textColumn("Disk Read", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.DiskRead) },
		func(a, b *process.Proc) bool { return a.DiskRead < b.DiskRead }))
	cv.AppendColumn(v.textColumn("Disk Write", false, 1,
		func(dst []byte, p *process.Proc) []byte { return format.AppendRate(dst, p.DiskWrite) },
		func(a, b *process.Proc) bool { return a.DiskWrite < b.DiskWrite }))

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
	changed := 0 // rows that moved between hidden and visible this tick

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
			changed++
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
		changed++
		v.register(row)
	}
	if len(toAppend) > 0 {
		v.model.Splice(len(v.order)-len(toAppend), 0, toAppend...)
	}
	v.appended = toAppend[:0] // keep the buffers for the next tick
	v.pending = pending[:0]

	// The filter decides visibility from row.live, which GTK cannot see change,
	// so a row appearing or retiring has to be announced.
	if changed > 0 {
		v.filter.Changed(gtk.FilterChangeDifferent)
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
	for _, row := range v.order {
		row.live = false
		row.gen = 0
		v.free = append(v.free, row)
	}
	if len(v.order) > 0 {
		v.filter.Changed(gtk.FilterChangeDifferent)
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

// textColumn builds a sortable text column. xalign: 0 left, 1 right.
func (v *appsView) textColumn(title string, expand bool, xalign float64,
	render renderFunc, less func(a, b *process.Proc) bool) *gtk.ColumnViewColumn {

	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		label := gtk.NewLabel("")
		label.SetXAlign(float32(xalign))
		label.SetEllipsize(3) // PANGO_ELLIPSIZE_END
		cell.SetChild(label)
		c := &procCell{label: label, render: render}
		v.cells[cell.Native()] = c
		v.attachContextMenu(label, c)
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
		delete(v.cells, obj.Cast().(*gtk.ColumnViewCell).Native())
	})

	col := gtk.NewColumnViewColumn(title, &factory.ListItemFactory)
	col.SetExpand(expand)
	col.SetResizable(true)

	// Sorting runs only when the user changes the sort or the process set
	// changes — never every tick — so the Take() wrappers here are not a hot path.
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
}

// attachContextMenu wires a right-click gesture on a cell to the popover,
// reading the row the cell is bound to at click time.
func (v *appsView) attachContextMenu(widget gtk.Widgetter, c *procCell) {
	click := gtk.NewGestureClick()
	click.SetButton(3) // secondary / right button
	click.ConnectPressed(func(_ int, x, y float64) {
		if c.row == nil {
			return
		}
		v.targetPID, v.targetName = c.row.proc.PID, c.row.proc.Name

		if v.popHasPar {
			v.popover.Unparent()
		}
		v.popover.SetParent(widget)
		v.popHasPar = true
		rect := gdk.NewRectangle(int(x), int(y), 1, 1)
		v.popover.SetPointingTo(&rect)
		v.popover.Popup()
	})
	gtk.BaseWidget(widget).AddController(click)
}

func (v *appsView) kill(sig syscall.Signal) {
	if v.targetPID > 0 {
		_ = syscall.Kill(v.targetPID, sig)
	}
}

func (v *appsView) openLocation() {
	if v.targetPID <= 0 {
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
