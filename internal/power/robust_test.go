package power

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Battery page reads firmware-provided sysfs attributes, and firmware lies:
// missing files, empty files, zeroes where a divisor is expected, values in the
// wrong unit. None of that may produce a NaN, a negative percentage or a panic,
// because every one of those figures is rendered into a label or a progress bar.

// checkState asserts the invariants every published State must satisfy,
// whatever the underlying files said.
func checkState(t *testing.T, st State, ok bool) {
	t.Helper()
	if !ok {
		return
	}
	b := st.Battery
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"Percent", b.Percent}, {"Health", b.Health}, {"EnergyWh", b.EnergyWh},
		{"FullWh", b.FullWh}, {"DesignWh", b.DesignWh}, {"PowerW", b.PowerW},
		{"VoltageV", b.VoltageV},
	} {
		if math.IsNaN(f.v) {
			t.Errorf("%s is NaN", f.name)
		}
		if math.IsInf(f.v, 0) {
			t.Errorf("%s is infinite", f.name)
		}
	}
	if b.Percent < 0 || b.Percent > 100 {
		t.Errorf("Percent = %v, outside [0,100]", b.Percent)
	}
	if b.Health < 0 || b.Health > 100 {
		t.Errorf("Health = %v, outside [0,100]", b.Health)
	}
	if b.TimeLeft < 0 {
		t.Errorf("TimeLeft = %v, negative", b.TimeLeft)
	}
	if b.Status == "" {
		t.Error("Status is empty; the page would show a blank label")
	}
}

// TestMalformedAttributes runs a battery tree whose every numeric attribute is
// garbage of a different kind.
func TestMalformedAttributes(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
	}{
		{"empty values", map[string]string{
			"type": "Battery", "status": "", "energy_now": "", "energy_full": "",
			"energy_full_design": "", "power_now": "", "voltage_now": "", "capacity": "",
		}},
		{"non-numeric values", map[string]string{
			"type": "Battery", "status": "Discharging", "energy_now": "nope",
			"energy_full": "also nope", "power_now": "-", "voltage_now": "NaN", "capacity": "abc",
		}},
		{"zero full charge", map[string]string{
			"type": "Battery", "status": "Discharging", "energy_now": "1000000",
			"energy_full": "0", "energy_full_design": "0", "power_now": "5000000", "voltage_now": "11000000",
		}},
		{"energy above full", map[string]string{
			"type": "Battery", "status": "Charging", "energy_now": "99000000",
			"energy_full": "50000000", "energy_full_design": "50000000", "power_now": "20000000", "voltage_now": "11000000",
		}},
		{"charge shape with zero voltage", map[string]string{
			"type": "Battery", "status": "Discharging", "charge_now": "3000000",
			"charge_full": "5000000", "current_now": "1000000", "voltage_now": "0",
		}},
		{"huge values", map[string]string{
			"type": "Battery", "status": "Discharging",
			"energy_now": "18446744073709551615", "energy_full": "18446744073709551615",
			"power_now": "18446744073709551615", "voltage_now": "18446744073709551615",
		}},
		{"negative-looking values", map[string]string{
			"type": "Battery", "status": "Discharging", "energy_now": "-5",
			"energy_full": "-10", "power_now": "-1", "voltage_now": "-1",
		}},
		{"whitespace only", map[string]string{
			"type": "Battery", "status": "   ", "energy_now": "   \n  ",
			"energy_full": "\t", "power_now": " ", "voltage_now": " ",
		}},
		{"capacity out of range", map[string]string{
			"type": "Battery", "status": "Discharging", "capacity": "1000",
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			supply(t, root, "BAT0", c.fields)
			r := NewAt(root)
			if !r.Available() {
				t.Skip("tree produced no battery")
			}
			st, ok := r.Read()
			checkState(t, st, ok)
			t.Logf("pct=%.1f health=%.1f energy=%.2fWh full=%.2fWh power=%.2fW left=%v status=%q",
				st.Battery.Percent, st.Battery.Health, st.Battery.EnergyWh,
				st.Battery.FullWh, st.Battery.PowerW, st.Battery.TimeLeft, st.Battery.Status)
		})
	}
}

// TestSupplyWithoutCapacityIgnored checks a supply that declares itself a
// battery but reports no capacity at all is not treated as one. This is the
// filter that keeps a wireless mouse off the Battery page.
func TestSupplyWithoutCapacityIgnored(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{"type": "Battery", "status": "Unknown"})
	r := NewAt(root)
	if r.Available() {
		t.Error("a battery with no energy_full, charge_full or capacity was accepted")
	}
	if _, ok := r.Read(); ok {
		t.Error("Read reported success with no usable battery")
	}
}

// TestUnreadableAttributes covers a battery directory whose files cannot be
// read. Some firmware revokes access to individual attributes, and sysfs reads
// can fail with EIO on a pack that has just been unplugged.
func TestUnreadableAttributes(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type": "Battery", "status": "Discharging",
		"energy_now": "25000000", "energy_full": "50000000", "power_now": "10000000",
	})
	// Make energy_now unreadable. Running as root defeats the mode bits, so the
	// test only asserts the fallback when the chmod actually takes effect.
	path := filepath.Join(root, "BAT0", "energy_now")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("running with privileges that ignore mode bits")
	}

	r := NewAt(root)
	if !r.Available() {
		t.Fatal("battery not discovered")
	}
	st, ok := r.Read()
	checkState(t, st, ok)
	// energy_now is gone, so the energy shape cannot be used and there is no
	// capacity file either: the percentage should be zero, not garbage.
	if st.Battery.Percent != 0 {
		t.Errorf("Percent = %v with an unreadable energy_now, want 0", st.Battery.Percent)
	}
}

// TestDirectoryInsteadOfFile covers a sysfs-like tree where an attribute is a
// directory. Reading one returns EISDIR rather than data.
func TestDirectoryInsteadOfFile(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type": "Battery", "status": "Discharging", "energy_full": "50000000",
	})
	if err := os.MkdirAll(filepath.Join(root, "BAT0", "energy_now"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewAt(root)
	if !r.Available() {
		t.Fatal("battery not discovered")
	}
	st, ok := r.Read()
	checkState(t, st, ok)
}

// TestSupplyVanishesBetweenDiscoveryAndRead covers the pack being removed after
// the Reader found it — a hot-swappable battery, or a USB device unplugged.
func TestSupplyVanishesBetweenDiscoveryAndRead(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type": "Battery", "status": "Discharging",
		"energy_now": "25000000", "energy_full": "50000000", "power_now": "10000000",
	})
	r := NewAt(root)
	if !r.Available() {
		t.Fatal("battery not discovered")
	}
	if _, ok := r.Read(); !ok {
		t.Fatal("first read failed")
	}
	if err := os.RemoveAll(filepath.Join(root, "BAT0")); err != nil {
		t.Fatal(err)
	}
	// Available still reports true — the Reader remembers what it found — so
	// the read has to cope with every attribute being gone.
	st, ok := r.Read()
	checkState(t, st, ok)
	if st.Battery.Percent != 0 {
		t.Errorf("Percent = %v after the pack vanished, want 0", st.Battery.Percent)
	}
}

// TestLongAndOddStrings covers text attributes: a vendor string long enough to
// break a label's layout, and one containing a newline or NUL.
func TestLongAndOddStrings(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type":         "Battery",
		"status":       "Discharging",
		"manufacturer": strings.Repeat("A", 4096),
		"model_name":   "Model\nWith\nNewlines",
		"technology":   "Li-\x00ion",
		"energy_now":   "25000000",
		"energy_full":  "50000000",
		"power_now":    "10000000",
	})
	r := NewAt(root)
	st, ok := r.Read()
	checkState(t, st, ok)
	// The values are passed through to labels; what matters is that reading
	// them does not fail and the numbers beside them survive.
	if st.Battery.Percent != 50 {
		t.Errorf("Percent = %v, want 50 — odd text should not disturb the figures", st.Battery.Percent)
	}
	t.Logf("vendor len=%d model=%q tech=%q", len(st.Battery.Vendor), st.Battery.Model, st.Battery.Technology)
}

// TestRepeatedReadsAreStable checks a Reader can be read many times without the
// figures drifting, since the Battery page calls Read once a second forever.
func TestRepeatedReadsAreStable(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type": "Battery", "status": "Discharging",
		"energy_now": "25000000", "energy_full": "50000000",
		"energy_full_design": "60000000", "power_now": "10000000", "voltage_now": "11000000",
	})
	r := NewAt(root)
	first, ok := r.Read()
	if !ok {
		t.Fatal("read failed")
	}
	for i := 0; i < 200; i++ {
		st, ok := r.Read()
		checkState(t, st, ok)
		if st.Battery.Percent != first.Battery.Percent || st.Battery.TimeLeft != first.Battery.TimeLeft {
			t.Fatalf("read %d drifted: pct %v->%v left %v->%v", i,
				first.Battery.Percent, st.Battery.Percent, first.Battery.TimeLeft, st.Battery.TimeLeft)
		}
	}
}
