package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"

	"atlas-monitor/internal/services"
)

// newTestServicesView builds just enough of the view to exercise apply: the
// list model is a plain GObject, so this needs no display.
func newTestServicesView() *servicesView {
	return &servicesView{
		model: gioutil.NewListModel[*svcRow](),
		cells: make(map[uintptr]svcCell),
	}
}

func units(names ...string) []services.Service {
	out := make([]services.Service, len(names))
	for i, n := range names {
		out[i] = services.Service{Name: n, Active: "active", Status: services.Running}
	}
	return out
}

func modelNames(v *servicesView) []string {
	out := make([]string, 0, v.model.Len())
	for i := 0; i < v.model.Len(); i++ {
		out = append(out, v.model.At(i).svc.Name)
	}
	return out
}

func equal(a, b []string) bool {
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

// TestServicesApplyMerge is the heart of the Services leak fix: a refresh that
// changes nothing must not touch the model, and one that does must splice only
// the difference while keeping the list sorted.
func TestServicesApplyMerge(t *testing.T) {
	v := newTestServicesView()

	v.apply(units("a.service", "c.service", "e.service"))
	if got := modelNames(v); !equal(got, []string{"a.service", "c.service", "e.service"}) {
		t.Fatalf("initial load = %v", got)
	}
	rowA, rowC := v.order[0], v.order[1]

	// Same list again: every row object must survive.
	v.apply(units("a.service", "c.service", "e.service"))
	if v.order[0] != rowA || v.order[1] != rowC {
		t.Error("an unchanged refresh replaced row objects")
	}

	// A unit changes state: the existing row is updated in place.
	changed := units("a.service", "c.service", "e.service")
	changed[1].Active, changed[1].Status = "failed", services.Failed
	v.apply(changed)
	if v.order[1] != rowC {
		t.Error("a state change replaced the row object instead of updating it")
	}
	if v.order[1].svc.Status != services.Failed {
		t.Errorf("row state = %v, want Failed", v.order[1].svc.Status)
	}

	// Arrivals and departures, in sorted positions.
	v.apply(units("a.service", "b.service", "e.service", "f.service"))
	if got := modelNames(v); !equal(got, []string{"a.service", "b.service", "e.service", "f.service"}) {
		t.Fatalf("after churn = %v", got)
	}
	if v.order[0] != rowA {
		t.Error("a surviving unit lost its row through an insert/remove")
	}

	// Everything goes away.
	v.apply(nil)
	if got := modelNames(v); len(got) != 0 {
		t.Fatalf("after clearing = %v, want empty", got)
	}
	if len(v.order) != 0 {
		t.Fatalf("order left %d entries behind", len(v.order))
	}

	// …and comes back.
	v.apply(units("z.service"))
	if got := modelNames(v); !equal(got, []string{"z.service"}) {
		t.Fatalf("after reload = %v", got)
	}
}

// TestServicesApplyKeepsModelInSyncWithOrder guards the invariant the merge
// depends on: v.order and the list model are the same sequence.
func TestServicesApplyKeepsModelInSyncWithOrder(t *testing.T) {
	v := newTestServicesView()
	for _, list := range [][]services.Service{
		units("b", "d", "f"),
		units("a", "b", "c", "d", "e", "f", "g"),
		units("d"),
		units("a", "d", "z"),
		nil,
		units("m", "n"),
	} {
		v.apply(list)
		if len(v.order) != v.model.Len() {
			t.Fatalf("order has %d entries, model has %d", len(v.order), v.model.Len())
		}
		for i := range v.order {
			if v.order[i] != v.model.At(i) {
				t.Fatalf("row %d differs between order and model", i)
			}
		}
	}
}

// TestStartupLabel covers the states this machine actually reports, and the
// promise that an unknown one is shown rather than hidden.
func TestStartupLabel(t *testing.T) {
	for state, want := range map[string]string{
		"enabled":         "On",
		"disabled":        "Off",
		"static":          "As needed",
		"enabled-runtime": "On until reboot",
		"masked":          "Blocked",
		"indirect":        "Indirect",
		"alias":           "Alias",
		"transient":       "Temporary",
		"":                "—",
		"some-new-state":  "some-new-state",
	} {
		if got := startupLabel(state); got != want {
			t.Errorf("startupLabel(%q) = %q, want %q", state, got, want)
		}
	}
	// The two states people actually read as on-or-off get shorter.
	for _, state := range []string{"enabled", "disabled"} {
		if len(startupLabel(state)) >= len(state) {
			t.Errorf("%q became %q, which is no shorter", state, startupLabel(state))
		}
	}
	// "static" is the exception and deliberately so: "As needed" is longer than
	// the word it replaces, and it is the only one of the three whose systemd
	// name tells the reader nothing. Clarity is what it is buying, not width.
	if got := startupLabel("static"); len(got) <= len("static") {
		t.Errorf("startupLabel(\"static\") = %q; if it has become shorter, "+
			"the comment above no longer describes the trade being made", got)
	}
}
