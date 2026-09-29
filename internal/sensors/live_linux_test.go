package sensors

import "testing"

// TestDiscoverThisMachine lists what this machine has. Nothing is asserted about
// which devices exist; that every reading reads and names itself is.
func TestDiscoverThisMachine(t *testing.T) {
	if !Available() {
		t.Skip("no hwmon sensors here")
	}
	devs := Discover()
	defer Close(devs)
	if len(devs) == 0 {
		t.Fatal("Available said yes and Discover found nothing")
	}
	for _, d := range devs {
		t.Logf("%s  [%s]", d.Name, d.Driver)
		for _, r := range d.Readings {
			t.Logf("    %-16s %-12s core=%v high=%v crit=%v", r.Label, r.Format(), r.Core, r.High, r.Critical)
			if r.Label == "" {
				t.Errorf("%s: a reading with no label", d.Name)
			}
			if !r.OK {
				t.Errorf("%s / %s did not read", d.Name, r.Label)
			}
			if r.Kind == Temperature && r.OK && (r.Value < -40 || r.Value > 150) {
				t.Errorf("%s / %s = %v °C, not a believable temperature", d.Name, r.Label, r.Value)
			}
		}
	}
}
