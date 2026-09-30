package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/process"
)

func byCPU(a, b *process.Proc) bool { return a.CPU < b.CPU }

func TestRankRowsGivesTiesOneRank(t *testing.T) {
	cpus := []float64{5, 1, 5, 0, 1, 5}
	rows := make([]*procRow, len(cpus))
	for i, c := range cpus {
		rows[i] = &procRow{key: process.Proc{CPU: c}}
	}
	ranks := make([]int32, len(rows))
	rankRows(rows, byCPU, nil, ranks)
	want := []int32{2, 1, 2, 0, 1, 2}
	for i := range want {
		if ranks[i] != want[i] {
			t.Fatalf("ranks %v, want %v", ranks, want)
		}
	}
}

func TestRankRowsUsesTheKeyNotTheReading(t *testing.T) {
	// A held table keeps sorting on the keys while the readings move on.
	rows := []*procRow{
		{proc: process.Proc{CPU: 9}, key: process.Proc{CPU: 1}},
		{proc: process.Proc{CPU: 0}, key: process.Proc{CPU: 2}},
	}
	ranks := make([]int32, 2)
	rankRows(rows, byCPU, nil, ranks)
	if ranks[0] != 0 || ranks[1] != 1 {
		t.Fatalf("ranks %v follow the readings, want the keys", ranks)
	}
}

func TestRankRowsIsOrderedAndRepeatable(t *testing.T) {
	rows := make([]*procRow, 500)
	for i := range rows {
		rows[i] = &procRow{key: process.Proc{CPU: float64((i * 7919) % 13)}}
	}
	ranks := make([]int32, len(rows))
	perm := rankRows(rows, byCPU, nil, ranks)
	for i := range rows {
		for j := range rows {
			a, b := &rows[i].key, &rows[j].key
			if (byCPU(a, b)) != (ranks[i] < ranks[j]) || (a.CPU == b.CPU) != (ranks[i] == ranks[j]) {
				t.Fatalf("rows %d and %d (%v, %v) ranked %d and %d", i, j, a.CPU, b.CPU, ranks[i], ranks[j])
			}
		}
	}
	// Scratch comes back for reuse, and a second run over it agrees.
	again := make([]int32, len(rows))
	rankRows(rows, byCPU, perm, again)
	for i := range ranks {
		if ranks[i] != again[i] {
			t.Fatalf("row %d ranked %d then %d", i, ranks[i], again[i])
		}
	}
}

// rankedView is a table whose sorter is the real C one, over a plain list model
// (no display needed), sorted by CPU.
func rankedView() (*appsView, *gtk.SortListModel) {
	v := newTestAppsView()
	v.ranks = newRankTable()
	sorter := v.ranks.newSorter()
	v.activeSorter, v.activeLess = sorter, byCPU
	return v, gtk.NewSortListModel(v.model, sorter)
}

func rowFromObject(o *coreglib.Object) *procRow { return gioutil.ObjectValue[*procRow](o) }

func sortedPIDs(v *appsView, m *gtk.SortListModel) []int {
	var out []int
	for i := uint(0); i < m.NItems(); i++ {
		out = append(out, rowFromObject(m.Item(i)).proc.PID)
	}
	return out
}

func TestCSorterOrdersRows(t *testing.T) {
	v, m := rankedView()
	snap := procs(1, 2, 3, 4)
	for i, cpu := range []float64{3, 1, 2, 1} {
		snap[i].CPU = cpu
	}
	v.applyRows(snap)
	if got, want := sortedPIDs(v, m), []int{2, 4, 3, 1}; !equalInts(got, want) {
		t.Fatalf("first tick sorted %v, want %v", got, want)
	}

	// Readings move in place: the next tick must re-sort without any row being
	// added, and rows that appear must land in their place.
	snap = procs(1, 2, 3, 4, 5)
	for i, cpu := range []float64{0, 9, 2, 1, 5} {
		snap[i].CPU = cpu
	}
	v.applyRows(snap)
	if got, want := sortedPIDs(v, m), []int{1, 4, 3, 5, 2}; !equalInts(got, want) {
		t.Fatalf("second tick sorted %v, want %v", got, want)
	}
}

func TestCSorterHoldsAHeldOrder(t *testing.T) {
	v, m := rankedView()
	snap := procs(1, 2, 3)
	for i, cpu := range []float64{1, 2, 3} {
		snap[i].CPU = cpu
	}
	v.applyRows(snap)
	v.menuOpen = true // holds the order

	snap = procs(1, 2, 3, 4)
	for i, cpu := range []float64{3, 2, 1, 2.5} {
		snap[i].CPU = cpu
	}
	v.applyRows(snap)
	// Rows 1..3 keep the order of their keys; the new row slots in by its own.
	if got, want := sortedPIDs(v, m), []int{1, 2, 4, 3}; !equalInts(got, want) {
		t.Fatalf("held table sorted %v, want %v", got, want)
	}
	v.menuOpen = false
	v.releaseHold()
	if got, want := sortedPIDs(v, m), []int{3, 2, 4, 1}; !equalInts(got, want) {
		t.Fatalf("released table sorted %v, want %v", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
