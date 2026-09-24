//go:build !race

// This file decides what it decides by watching finalizers run, and the race
// detector changes when they do — under -race even the control case, a plain
// list model, reports nothing released. The behaviour being characterised
// belongs to gotk4 rather than to Atlas, so there is nothing here the race
// detector could usefully find.

package ui

import (
	"runtime"
	"testing"

	"unsafe"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/process"
)

// A row spliced into a gioutil.ListModel is stored in a process-global registry
// and handed to C as an id; the Go value is released only when the GObject
// wrapping that id is finalized. So the question these tests answer is whether
// splicing a row out of the model is enough to get the Go value back, because
// the Apps table adds a row for every process that appears and removes it again
// when the process exits — on a busy machine, dozens a second, forever.

// chain describes how much of the Apps view's model stack to build on top of the
// list model, so a leak can be attributed to the layer that introduces it.
type chain int

const (
	chainBare   chain = iota // the list model alone
	chainFilter              // + GtkFilterListModel with the view's own filter
	chainSort                // + GtkSortListModel with a comparison that unwraps rows
	chainFull                // + the selection model the column view is given
)

func (c chain) String() string {
	switch c {
	case chainBare:
		return "bare model"
	case chainFilter:
		return "+ filter"
	case chainSort:
		return "+ sort"
	default:
		return "+ selection"
	}
}

// collectedAfterRemoval splices n rows in, removes them all, and reports how
// many of the Go values became unreachable. Finalizers are the only way to
// observe this: the values live in a registry the Go heap profile attributes to
// gotk4, not to us.
func collectedAfterRemoval(t *testing.T, rounds, n int, c chain) (created, freed int) {
	t.Helper()
	model := gioutil.NewListModel[*procRow]()

	// Build the same stack the Apps view puts on top of its list model. Each
	// layer is retained for the whole test, as the real view retains it.
	var keep []any
	if c >= chainFilter {
		filter := gtk.NewCustomFilter(func(obj *coreglib.Object) bool {
			return gioutil.ObjectValue[*procRow](obj) != nil
		})
		fm := gtk.NewFilterListModel(model, &filter.Filter)
		keep = append(keep, filter, fm)
		if c >= chainSort {
			// The view's own sorter shape: unwrap both operands and compare.
			sorter := gtk.NewCustomSorter(func(a, b unsafe.Pointer) int {
				ra := gioutil.ObjectValue[*procRow](coreglib.Take(a))
				rb := gioutil.ObjectValue[*procRow](coreglib.Take(b))
				switch {
				case ra.proc.PID < rb.proc.PID:
					return -1
				case rb.proc.PID < ra.proc.PID:
					return 1
				}
				return 0
			})
			sm := gtk.NewSortListModel(fm, &sorter.Sorter)
			keep = append(keep, sorter, sm)
			if c >= chainFull {
				keep = append(keep, gtk.NewNoSelection(sm))
			}
		}
	}
	defer runtime.KeepAlive(keep)

	freedCh := make(chan struct{}, rounds*n)
	for r := 0; r < rounds; r++ {
		rows := make([]*procRow, n)
		for i := range rows {
			rows[i] = &procRow{proc: process.Proc{PID: r*n + i, Name: "proc" + itoa(r*n+i)}}
			runtime.SetFinalizer(rows[i], func(*procRow) { freedCh <- struct{}{} })
			created++
		}
		model.Splice(model.Len(), 0, rows...)
		// Drop every row again, the way an exited process is dropped.
		if l := model.Len(); l > 0 {
			model.Splice(0, l)
		}
		rows = nil
	}
	if model.Len() != 0 {
		t.Fatalf("model still holds %d rows", model.Len())
	}

	// Finalizers need two cycles: one to queue them, one after they have run.
	for i := 0; i < 4; i++ {
		runtime.GC()
		runtime.Gosched()
	}
	for {
		select {
		case <-freedCh:
			freed++
			continue
		default:
		}
		break
	}
	return created, freed
}

// TestSplicedOutRowsArePinnedByGoCallbacks pins the gotk4 behaviour the Apps
// table is built around, because the reason for that design is invisible in the
// code it produces.
//
// A row spliced out of a plain list model is released. Attach any Go callback to
// that model — the search GtkCustomFilter, the column GtkCustomSorters — and the
// same row is never released: the binding takes a reference on every item it
// hands the callback and never gives it back. On the Apps page, where a row is
// created per process, that measured about a megabyte a minute of ordinary
// process churn. Hence the free list in appsView: rows are recycled so the item
// set stays bounded, which bounds the pinning.
//
// If this test starts failing because the callback cases now release their rows,
// that is good news and the free list can go. Until then it is load-bearing.
func TestSplicedOutRowsArePinnedByGoCallbacks(t *testing.T) {
	released := map[chain]bool{}
	for _, c := range []chain{chainBare, chainFilter, chainSort, chainFull} {
		created, freed := collectedAfterRemoval(t, 20, 50, c)
		t.Logf("%-12s %d rows spliced in and out, %d released", c, created, freed)
		released[c] = freed*4 >= created*3
	}

	// Without a Go callback the model releases what it removes.
	if !released[chainBare] {
		t.Error("a plain list model no longer releases rows spliced out of it; " +
			"recycling rows would no longer be enough to bound the leak")
	}
	// With one, it does not. Any of these turning green means gotk4 fixed it.
	for _, c := range []chain{chainFilter, chainSort, chainFull} {
		if released[c] {
			t.Errorf("%s now releases rows spliced out of the model — gotk4 appears to have "+
				"fixed the reference it used to keep, so the free list in appsView can be removed", c)
		}
	}
}
