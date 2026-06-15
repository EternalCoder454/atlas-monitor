package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
type procRow struct {
	key  string
	proc process.Proc
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
	rows  map[string]*procRow
	order []string
	// liveCells holds a refresh closure per currently-bound (visible) cell.
	liveCells map[*gtk.Label]func()

	search      string
	grouped     bool
	needRebuild bool
	popHasPar   bool
	targetPID   int
	targetName  string
}

func newAppsView(proc *process.Collector) *appsView {
	v := &appsView{
		proc:      proc,
		rows:      make(map[string]*procRow),
		liveCells: make(map[*gtk.Label]func()),
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
		v.search = strings.ToLower(searchEntry.Text())
		v.filter.Changed(gtk.FilterChangeDifferent)
	})
	groupBtn := gtk.NewToggleButton()
	groupBtn.SetLabel("Group by app")
	groupBtn.ConnectToggled(func() {
		v.grouped = groupBtn.Active()
		v.needRebuild = true
		v.Update()
	})
	toolbar.Append(searchEntry)
	toolbar.Append(groupBtn)
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
		func(p process.Proc) string { return p.Name },
		func(a, b process.Proc) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) }))
	cv.AppendColumn(v.textColumn("PID", false, 1,
		func(p process.Proc) string { return strconv.Itoa(p.PID) },
		func(a, b process.Proc) bool { return a.PID < b.PID }))
	cpuCol := v.textColumn("CPU %", false, 1,
		func(p process.Proc) string { return fmt.Sprintf("%.1f%%", p.CPU) },
		func(a, b process.Proc) bool { return a.CPU < b.CPU })
	cv.AppendColumn(cpuCol)
	cv.AppendColumn(v.textColumn("RAM", false, 1,
		func(p process.Proc) string { return format.Bytes(p.RSS) },
		func(a, b process.Proc) bool { return a.RSS < b.RSS }))
	cv.AppendColumn(v.textColumn("GPU %", false, 1,
		gpuText,
		func(a, b process.Proc) bool { return a.GPU < b.GPU }))
	cv.AppendColumn(v.textColumn("Net ≈ In", false, 1,
		func(p process.Proc) string { return format.Rate(p.NetIn) },
		func(a, b process.Proc) bool { return a.NetIn < b.NetIn }))
	cv.AppendColumn(v.textColumn("Net ≈ Out", false, 1,
		func(p process.Proc) string { return format.Rate(p.NetOut) },
		func(a, b process.Proc) bool { return a.NetOut < b.NetOut }))
	cv.AppendColumn(v.textColumn("Disk Read", false, 1,
		func(p process.Proc) string { return format.Rate(p.DiskRead) },
		func(a, b process.Proc) bool { return a.DiskRead < b.DiskRead }))
	cv.AppendColumn(v.textColumn("Disk Write", false, 1,
		func(p process.Proc) string { return format.Rate(p.DiskWrite) },
		func(a, b process.Proc) bool { return a.DiskWrite < b.DiskWrite }))

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
// preserved and no GObjects churn.
func (v *appsView) Update() {
	if v.needRebuild {
		v.clearModel()
		v.needRebuild = false
	}

	snap := v.proc.Snapshot()
	if v.grouped {
		snap = groupByName(snap)
	}

	seen := make(map[string]bool, len(snap))
	var toAppend []*procRow
	for i := range snap {
		p := snap[i]
		key := v.keyOf(p)
		seen[key] = true
		if row, ok := v.rows[key]; ok {
			row.proc = p // update values in place
		} else {
			row := &procRow{key: key, proc: p}
			v.rows[key] = row
			v.order = append(v.order, key)
			toAppend = append(toAppend, row)
		}
	}
	if len(toAppend) > 0 {
		v.model.Splice(len(v.order)-len(toAppend), 0, toAppend...)
	}
	// Drop processes that have exited.
	if len(v.rows) > len(seen) {
		for i := 0; i < len(v.order); {
			key := v.order[i]
			if !seen[key] {
				v.model.Remove(i)
				v.order = append(v.order[:i], v.order[i+1:]...)
				delete(v.rows, key)
			} else {
				i++
			}
		}
	}

	// Refresh only the currently-visible cells (~rows on screen × columns).
	for _, refresh := range v.liveCells {
		refresh()
	}
}

func (v *appsView) clearModel() {
	if n := v.model.Len(); n > 0 {
		v.model.Splice(0, n)
	}
	v.rows = make(map[string]*procRow)
	v.order = v.order[:0]
	v.liveCells = make(map[*gtk.Label]func())
}

func (v *appsView) keyOf(p process.Proc) string {
	if v.grouped {
		return p.Name
	}
	return strconv.Itoa(p.PID)
}

// matches implements the search filter.
func (v *appsView) matches(item *coreglib.Object) bool {
	if v.search == "" {
		return true
	}
	r := gioutil.ObjectValue[*procRow](item)
	return strings.Contains(strings.ToLower(r.proc.Name), v.search) ||
		strings.Contains(strconv.Itoa(r.proc.PID), v.search)
}

// textColumn builds a sortable text column. xalign: 0 left, 1 right.
func (v *appsView) textColumn(title string, expand bool, xalign float64,
	extract func(process.Proc) string, less func(a, b process.Proc) bool) *gtk.ColumnViewColumn {

	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		label := gtk.NewLabel("")
		label.SetXAlign(float32(xalign))
		label.SetEllipsize(3) // PANGO_ELLIPSIZE_END
		cell.SetChild(label)
		v.attachContextMenu(label, cell)
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		label, _ := cell.Child().(*gtk.Label)
		if label == nil {
			return
		}
		set := func() {
			if r := rowOf(cell); r != nil {
				label.SetText(extract(r.proc))
			}
		}
		set()
		v.liveCells[label] = set
	})
	factory.ConnectUnbind(func(obj *coreglib.Object) {
		cell := obj.Cast().(*gtk.ColumnViewCell)
		if label, ok := cell.Child().(*gtk.Label); ok {
			delete(v.liveCells, label)
		}
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
		case less(ra.proc, rb.proc):
			return -1
		case less(rb.proc, ra.proc):
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
// reading the row's process at click time.
func (v *appsView) attachContextMenu(widget gtk.Widgetter, cell *gtk.ColumnViewCell) {
	click := gtk.NewGestureClick()
	click.SetButton(3) // secondary / right button
	click.ConnectPressed(func(_ int, x, y float64) {
		r := rowOf(cell)
		if r == nil {
			return
		}
		v.targetPID, v.targetName = r.proc.PID, r.proc.Name

		if v.popHasPar {
			v.popover.Unparent()
		}
		v.popover.SetParent(widget)
		v.popHasPar = true
		rect := gdk.NewRectangle(int(x), int(y), 1, 1)
		v.popover.SetPointingTo(&rect)
		v.popover.Popup()
	})
	if w, ok := widget.(*gtk.Label); ok {
		w.AddController(click)
	}
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

func gpuText(p process.Proc) string {
	if p.GPU < 0 {
		return "—" // not a GPU client
	}
	return fmt.Sprintf("%.0f%%", p.GPU)
}

// groupByName aggregates processes sharing a name into a single row.
func groupByName(procs []process.Proc) []process.Proc {
	byName := make(map[string]*process.Proc)
	var order []string
	for i := range procs {
		p := procs[i]
		g, ok := byName[p.Name]
		if !ok {
			cp := p
			byName[p.Name] = &cp
			order = append(order, p.Name)
			continue
		}
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
	out := make([]process.Proc, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPU > out[j].CPU })
	return out
}
