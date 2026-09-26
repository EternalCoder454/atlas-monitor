package smart

import (
	"os"
	"testing"

	"github.com/godbus/dbus/v5"
)

// TestReadThisMachinesDrives is an integration check: it asks udisks2 about
// whatever is actually in this machine and insists the answers are sane. It
// skips where udisks2 is not running, which is the case in a build container.
func TestReadThisMachinesDrives(t *testing.T) {
	c := New()
	if c == nil {
		t.Skip("udisks2 is not on this bus")
	}
	names, err := os.ReadDir("/sys/block")
	if err != nil {
		t.Skip("no /sys/block")
	}
	checked := 0
	for _, d := range names {
		h, ok := c.Read(d.Name())
		if !ok {
			continue
		}
		checked++
		if h.HasWear && (h.Wear < 0 || h.Wear > 100) {
			t.Errorf("%s: wear %d%% is outside 0..100", d.Name(), h.Wear)
		}
		if h.HasSpare && (h.Spare < 0 || h.Spare > 100) {
			t.Errorf("%s: spare %d%% is outside 0..100", d.Name(), h.Spare)
		}
		// A drive reporting 20 °C below freezing or hotter than boiling is a
		// unit conversion mistake, which is the easy thing to get wrong here.
		if h.HasTemperature && (h.TemperatureC < -20 || h.TemperatureC > 100) {
			t.Errorf("%s: %.1f °C is not a plausible drive temperature", d.Name(), h.TemperatureC)
		}
		t.Logf("%s: wear=%d%% spare=%d%% %.0f°C on=%dh written=%d errors=%d failing=%v",
			d.Name(), h.Wear, h.Spare, h.TemperatureC, h.PowerOnHours, h.WrittenBytes, h.MediaErrors, h.Failing)
	}
	if checked == 0 {
		t.Skip("no drive on this machine reports SMART through udisks2")
	}
}

// TestNilClientIsHarmless: a machine without udisks2 gets a nil client, and
// every caller should be able to use it without checking.
func TestNilClientIsHarmless(t *testing.T) {
	var c *Client
	if _, ok := c.Read("nvme0n1"); ok {
		t.Error("a nil client reported health")
	}
	if _, ok := (&Client{}).Read("nvme0n1"); ok {
		t.Error("a client with no connection reported health")
	}
}

// TestAsUintTakesWhateverWidthItIsGiven covers the reason that helper exists:
// the same SMART log mixes byte, uint16 and uint64 attributes.
func TestAsUintTakesWhateverWidthItIsGiven(t *testing.T) {
	for _, v := range []any{uint8(7), uint16(7), uint32(7), uint64(7), int64(7)} {
		if n, ok := asUint(dbus.MakeVariant(v)); !ok || n != 7 {
			t.Errorf("%T: got %d, %v", v, n, ok)
		}
	}
	if _, ok := asUint(dbus.MakeVariant("seven")); ok {
		t.Error("a string was accepted as a number")
	}
}
