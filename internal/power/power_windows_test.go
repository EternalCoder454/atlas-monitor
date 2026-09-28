package power

import (
	"testing"

	"atlas-monitor/internal/winapi"
)

// TestWindowsStatusUsesTheWordsTheRestOfAtlasReads.
//
// Charging() and Discharging() test the Status string by name, and so does the
// health check that warns about a battery running down. A word that is nearly
// right — "charging", "AC" — silently disables both.
func TestWindowsStatusUsesTheWordsTheRestOfAtlasReads(t *testing.T) {
	for _, c := range []struct {
		name string
		in   winapi.Power
		want string
	}{
		{"on battery", winapi.Power{HasBattery: true, Percent: 62}, "Discharging"},
		{"charging", winapi.Power{HasBattery: true, OnAC: true, Charging: true, Percent: 62}, "Charging"},
		{"full on mains", winapi.Power{HasBattery: true, OnAC: true, Percent: 100}, "Full"},
		// A laptop with a charge threshold sits here all day: plugged in, not
		// charging, and not full.
		{"held below full", winapi.Power{HasBattery: true, OnAC: true, Percent: 80}, "Not charging"},
	} {
		if got := windowsStatus(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// ...and the two helpers that read them still work on those words.
	if !(Battery{Status: windowsStatus(winapi.Power{HasBattery: true})}).Discharging() {
		t.Error("Discharging() did not recognise the status for being on battery")
	}
	if !(Battery{Status: windowsStatus(winapi.Power{HasBattery: true, OnAC: true, Charging: true})}).Charging() {
		t.Error("Charging() did not recognise the charging status")
	}
}

// TestNoBatteryIsNotAnError: a desktop has none, and Read must report that rather
// than failing — while still saying whether the machine is on mains, which is how
// a UPS shows up.
func TestNoBatteryIsNotAnError(t *testing.T) {
	r := &Reader{hasBattery: false}
	if r.Available() {
		t.Error("a machine with no battery reported one")
	}
	if _, ok := r.Read(); ok {
		t.Error("Read reported a battery on a machine with none")
	}
}
