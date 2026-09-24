package ui

import (
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/process"
)

// newTestAppsView builds enough of the Apps table to drive its row diff. Like the
// Services harness, the list model and the filter are plain GObjects, so no
// display is needed.
func newTestAppsView() *appsView {
	v := &appsView{
		model:  gioutil.NewListModel[*procRow](),
		byPID:  make(map[int]*procRow),
		byName: make(map[string]*procRow),
		cells:  make(map[uintptr]*procCell),
		groups: make(map[string]int),
	}
	v.filter = gtk.NewCustomFilter(v.matches)
	return v
}

// procs builds a snapshot from a list of pids.
func procs(pids ...int) []process.Proc {
	out := make([]process.Proc, len(pids))
	for i, pid := range pids {
		out[i] = process.Proc{PID: pid, Name: "p" + itoa(pid), GPU: -1}
	}
	return out
}

// livePIDs lists the pids the table currently shows, in model order.
func livePIDs(v *appsView) []int {
	var out []int
	for i := 0; i < v.model.Len(); i++ {
		if r := v.model.At(i); r.live {
			out = append(out, r.proc.PID)
		}
	}
	return out
}

// TestAppsRowsAreRecycled is the leak fix. A process that exits must leave its
// row in the list model for the next process to reuse, because an item removed
// from a model with a Go callback attached is pinned for the life of the
// process — see the free list in appsView.
func TestAppsRowsAreRecycled(t *testing.T) {
	v := newTestAppsView()

	v.applyRows(procs(1, 2, 3))
	if got := v.model.Len(); got != 3 {
		t.Fatalf("model holds %d rows after the first tick, want 3", got)
	}

	// Replace the whole process set, over and over. Without recycling this would
	// splice three rows out and three in every tick.
	for round := 0; round < 100; round++ {
		base := 100 + round*3
		v.applyRows(procs(base, base+1, base+2))
		if got := v.model.Len(); got != 3 {
			t.Fatalf("round %d: model holds %d rows, want 3 — rows are not being reused", round, got)
		}
		if got := len(livePIDs(v)); got != 3 {
			t.Fatalf("round %d: %d rows visible, want 3", round, got)
		}
	}
	if got := len(v.free); got != 0 {
		t.Errorf("free list holds %d rows with every row in use", got)
	}
	if got := len(v.byPID); got != 3 {
		t.Errorf("byPID holds %d entries, want 3 — exited processes are not being unregistered", got)
	}
}

// TestAppsModelStopsAtTheHighWaterMark checks the model grows to the largest
// number of concurrent processes and no further, which is what bounds the
// pinning.
func TestAppsModelStopsAtTheHighWaterMark(t *testing.T) {
	v := newTestAppsView()

	// Ramp up to 50, drop to 5, then churn 5 at a time for a long while.
	var all []int
	for i := 1; i <= 50; i++ {
		all = append(all, i)
	}
	v.applyRows(procs(all...))
	if got := v.model.Len(); got != 50 {
		t.Fatalf("model holds %d rows, want 50", got)
	}

	v.applyRows(procs(1, 2, 3, 4, 5))
	if got := v.model.Len(); got != 50 {
		t.Errorf("model shrank to %d rows; retired rows should be kept for reuse", got)
	}
	if got := len(livePIDs(v)); got != 5 {
		t.Errorf("%d rows visible, want 5", got)
	}
	if got := len(v.free); got != 45 {
		t.Errorf("free list holds %d rows, want 45", got)
	}

	for round := 0; round < 200; round++ {
		base := 1000 + round*5
		v.applyRows(procs(base, base+1, base+2, base+3, base+4))
		if got := v.model.Len(); got > 50 {
			t.Fatalf("round %d: model grew to %d rows, above the high-water mark of 50", round, got)
		}
	}
	if got := v.model.Len(); got != 50 {
		t.Errorf("model holds %d rows after 200 rounds of churn, want 50", got)
	}
}

// TestAppsRetiredRowsAreHidden checks a recycled row is not shown between its
// process exiting and the row being reused.
func TestAppsRetiredRowsAreHidden(t *testing.T) {
	v := newTestAppsView()
	v.applyRows(procs(1, 2, 3))
	v.applyRows(procs(2))

	live := livePIDs(v)
	if len(live) != 1 || live[0] != 2 {
		t.Errorf("visible pids = %v, want [2]", live)
	}
	for i := 0; i < v.model.Len(); i++ {
		r := v.model.At(i)
		if !r.live && v.matchesRow(r) {
			t.Errorf("retired row for pid %d is not hidden by the filter", r.proc.PID)
		}
	}
	// Reviving it must make it visible again.
	v.applyRows(procs(2, 7))
	if got := len(livePIDs(v)); got != 2 {
		t.Errorf("%d rows visible after a row was reused, want 2", got)
	}
}

// TestAppsSearchStillFilters checks recycling did not break the search box: the
// filter now has two reasons to hide a row and both must work.
func TestAppsSearchStillFilters(t *testing.T) {
	v := newTestAppsView()
	v.applyRows([]process.Proc{
		{PID: 1, Name: "chrome", GPU: -1},
		{PID: 2, Name: "bash", GPU: -1},
		{PID: 3, Name: "sshd", GPU: -1},
	})

	v.search = "sh"
	var shown []int
	for i := 0; i < v.model.Len(); i++ {
		if v.matchesRow(v.model.At(i)) {
			shown = append(shown, v.model.At(i).proc.PID)
		}
	}
	// bash and sshd match "sh"; chrome does not.
	if len(shown) != 2 {
		t.Errorf("search %q showed pids %v, want bash and sshd", v.search, shown)
	}

	// A retired row must stay hidden even when its name matches the search.
	v.applyRows([]process.Proc{{PID: 1, Name: "chrome", GPU: -1}})
	for i := 0; i < v.model.Len(); i++ {
		r := v.model.At(i)
		if !r.live && v.matchesRow(r) {
			t.Errorf("retired row %q matched the search; retirement must win", r.proc.Name)
		}
	}
}

// TestAppsClearModelRecycles covers the "Group by app" and kernel-thread toggles,
// which rebuild the table. Those must recycle too, or a user toggling them a few
// dozen times pays what an hour on the page used to cost.
func TestAppsClearModelRecycles(t *testing.T) {
	v := newTestAppsView()
	v.applyRows(procs(1, 2, 3, 4, 5))
	before := v.model.Len()

	for i := 0; i < 50; i++ {
		v.clearModel()
		if got := v.model.Len(); got != before {
			t.Fatalf("toggle %d: model holds %d rows, want %d kept for reuse", i, got, before)
		}
		if got := len(livePIDs(v)); got != 0 {
			t.Fatalf("toggle %d: %d rows still visible after clearModel", i, got)
		}
		v.applyRows(procs(1, 2, 3, 4, 5))
		if got := len(livePIDs(v)); got != 5 {
			t.Fatalf("toggle %d: %d rows visible after refilling, want 5", i, got)
		}
	}
	if got := v.model.Len(); got != before {
		t.Errorf("model holds %d rows after 50 rebuilds, want %d", got, before)
	}
}

// TestAppsRowIdentityIsStable checks a process keeps the same row object across
// ticks, which is what lets cells skip untouched values.
func TestAppsRowIdentityIsStable(t *testing.T) {
	v := newTestAppsView()
	v.applyRows(procs(1, 2, 3))
	first := v.byPID[2]
	for i := 0; i < 20; i++ {
		v.applyRows(procs(1, 2, 3))
	}
	if v.byPID[2] != first {
		t.Error("pid 2 was given a different row object while it was still running")
	}
	if first.proc.PID != 2 {
		t.Errorf("row for pid 2 now holds pid %d", first.proc.PID)
	}
}

// TestAppsChurnDoesNotGrowTheHeap is the end-to-end check on the fix. Ten
// thousand processes come and go through a table that never holds more than
// fifty, and the live heap has to stay flat.
func TestAppsChurnDoesNotGrowTheHeap(t *testing.T) {
	v := newTestAppsView()
	var all []int
	for i := 1; i <= 50; i++ {
		all = append(all, i)
	}
	v.applyRows(procs(all...))

	live := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapObjects
	}

	// Warm up first: the maps and buffers settle over the first few ticks.
	for round := 0; round < 50; round++ {
		v.applyRows(procs(churnSet(round, 50)...))
	}
	before := live()
	for round := 50; round < 250; round++ {
		v.applyRows(procs(churnSet(round, 50)...))
	}
	after := live()

	t.Logf("10000 processes through a 50-row table: live objects %d -> %d", before, after)
	if v.model.Len() != 50 {
		t.Errorf("model holds %d rows, want 50", v.model.Len())
	}
	// Each retired-and-reused row must not leave an object behind. A little
	// movement is normal; one object per process is not.
	if int64(after)-int64(before) > 2000 {
		t.Errorf("live objects grew by %d over 10000 processes; rows are still being retained",
			int64(after)-int64(before))
	}
}

// churnSet is a window of 50 distinct pids that slides with the round.
func churnSet(round, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 10000 + round*n + i
	}
	return out
}
