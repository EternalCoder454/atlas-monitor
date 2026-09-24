package services

import (
	"testing"
)

func TestServicesSmoke(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("no system bus: %v", err)
	}
	defer c.Close()
	svcs, err := c.List(true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	t.Logf("got %d .service units; first 8:", len(svcs))
	for i := 0; i < 8 && i < len(svcs); i++ {
		s := svcs[i]
		t.Logf("  %-32s status=%d active=%-9s sub=%-8s enabled=%-9s  %s",
			s.Name, s.Status, s.Active, s.Sub, s.Enabled, s.Description)
	}
}

// TestListIsSortedByName pins a contract the Services page depends on from the
// other side of the codebase. The view merges each refresh into its existing
// rows with a sorted walk — that merge is what fixed the leak where replacing
// the whole model made GTK rebuild every realised row — and a merge fed
// unsorted input drops and duplicates rows instead of updating them. Nothing in
// the view can detect that, so the guarantee has to be pinned here.
func TestListIsSortedByName(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("no system bus: %v", err)
	}
	defer c.Close()

	for _, servicesOnly := range []bool{true, false} {
		svcs, err := c.List(servicesOnly)
		if err != nil {
			t.Fatalf("List(%v): %v", servicesOnly, err)
		}
		if len(svcs) < 2 {
			continue
		}
		for i := 1; i < len(svcs); i++ {
			if svcs[i-1].Name > svcs[i].Name {
				t.Fatalf("List(%v) is not sorted: %q precedes %q at index %d",
					servicesOnly, svcs[i-1].Name, svcs[i].Name, i)
			}
		}
		t.Logf("List(%v): %d units, sorted", servicesOnly, len(svcs))
	}
}

// TestListPopulatesEveryColumn checks each unit carries the values the four
// columns render, since an empty cell looks like a bug in the collector rather
// than a unit that genuinely has no description.
func TestListPopulatesEveryColumn(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("no system bus: %v", err)
	}
	defer c.Close()
	svcs, err := c.List(true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(svcs) == 0 {
		t.Skip("no .service units on this machine")
	}
	for _, s := range svcs {
		if s.Name == "" {
			t.Error("a unit came back with no name")
		}
		if s.Active == "" {
			t.Errorf("%s has an empty Active state", s.Name)
		}
		if s.Enabled == "" {
			t.Errorf("%s has an empty Enabled column; it should fall back to a dash", s.Name)
		}
		if s.Status < 0 {
			t.Errorf("%s has status %d", s.Name, s.Status)
		}
	}
	t.Logf("%d units, every column populated", len(svcs))
}
