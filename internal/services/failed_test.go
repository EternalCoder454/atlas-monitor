package services

import "testing"

func TestFailedIsCheapAndAgreesWithList(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("no systemd bus: %v", err)
	}
	defer c.Close()

	failed, err := c.Failed()
	if err != nil {
		t.Fatalf("Failed(): %v", err)
	}
	all, err := c.List(true)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	want := map[string]bool{}
	for _, s := range all {
		if s.Status == Failed {
			want[s.Name] = true
		}
	}
	if len(failed) != len(want) {
		t.Errorf("Failed() returned %d units, List() says %d are failed: %v vs %v",
			len(failed), len(want), failed, want)
	}
	for _, n := range failed {
		if !want[n] {
			t.Errorf("Failed() reported %q, which List() does not call failed", n)
		}
	}
	t.Logf("%d failed of %d services", len(failed), len(all))
}
