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

	// Finalizers need collections to queue and run them, and since gotk4 0.4.1
	// part of the release happens on GLib's main context, which has to be let
	// run — as it always is in the application.
	settle()
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

// TestSplicedOutRowsAreReleased checks that a row spliced out of the model is
// released, whatever sits on top of the model.
//
// Before gotk4 0.4.1 it was not: any layer with a Go callback — the search
// filter, the column sorters — took a reference on every row it was handed and
// never gave it back, so every process that ever came and went stayed in memory
// for the life of the application. The Apps view recycles rows (see the free
// list in appsView) because of that; this test is what says the recycling is now
// an economy rather than the only thing bounding a leak, and it fails if a
// future gotk4 brings the leak back.
func TestSplicedOutRowsAreReleased(t *testing.T) {
	for _, c := range []chain{chainBare, chainFilter, chainSort, chainFull} {
		created, freed := collectedAfterRemoval(t, 20, 50, c)
		t.Logf("%-12s %d rows spliced in and out, %d released", c, created, freed)
		if freed*4 < created*3 {
			t.Errorf("%s: only %d of %d rows released after being spliced out", c, freed, created)
		}
	}
}
