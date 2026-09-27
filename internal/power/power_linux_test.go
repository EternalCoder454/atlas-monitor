package power

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// supply writes a synthetic /sys/class/power_supply entry. This machine is a
// desktop with no battery, so every behaviour here is exercised against trees
// built to match what real firmware reports.
func supply(t *testing.T, root, name string, fields map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestEnergyShape covers the common laptop: microwatt-hours and microwatts.
func TestEnergyShape(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type":               "Battery",
		"status":             "Discharging",
		"manufacturer":       "LGC",
		"model_name":         "45N1127",
		"technology":         "Li-ion",
		"cycle_count":        "212",
		"voltage_now":        "11400000", // 11.4 V
		"energy_now":         "24000000", // 24 Wh
		"energy_full":        "48000000", // 48 Wh
		"energy_full_design": "56000000", // 56 Wh
		"power_now":          "12000000", // 12 W
	})
	supply(t, root, "AC", map[string]string{"type": "Mains", "online": "0"})

	st, ok := NewAt(root).Read()
	if !ok {
		t.Fatal("no battery found")
	}
	b := st.Battery
	if b.Percent != 50 {
		t.Errorf("Percent = %v, want 50", b.Percent)
	}
	if b.EnergyWh != 24 || b.FullWh != 48 || b.DesignWh != 56 {
		t.Errorf("energies = %v/%v/%v Wh, want 24/48/56", b.EnergyWh, b.FullWh, b.DesignWh)
	}
	if b.PowerW != 12 {
		t.Errorf("PowerW = %v, want 12", b.PowerW)
	}
	// 48 of 56 Wh still usable.
	if got := b.Health; got < 85.6 || got > 85.8 {
		t.Errorf("Health = %.2f%%, want ~85.7%%", got)
	}
	// 24 Wh at 12 W = 2 hours.
	if b.TimeLeft != 2*time.Hour {
		t.Errorf("TimeLeft = %v, want 2h", b.TimeLeft)
	}
	if !b.Discharging() || b.Charging() {
		t.Errorf("status handling wrong for %q", b.Status)
	}
	if b.Model != "45N1127" || b.Vendor != "LGC" || b.Technology != "Li-ion" || b.CycleCount != 212 {
		t.Errorf("identity fields wrong: %+v", b)
	}
	if !st.HasAC || st.OnAC {
		t.Errorf("AC state = has:%v on:%v, want has:true on:false", st.HasAC, st.OnAC)
	}
}

// TestChargeShape covers firmware that reports amp-hours instead, which has to
// be multiplied by the pack voltage to reach the same watt-hours.
func TestChargeShape(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type":               "Battery",
		"status":             "Charging",
		"voltage_now":        "12000000", // 12 V
		"charge_now":         "2000000",  // 2 Ah  -> 24 Wh
		"charge_full":        "4000000",  // 4 Ah  -> 48 Wh
		"charge_full_design": "5000000",  // 5 Ah  -> 60 Wh
		"current_now":        "1000000",  // 1 A   -> 12 W
	})
	supply(t, root, "ADP1", map[string]string{"type": "Mains", "online": "1"})

	st, ok := NewAt(root).Read()
	if !ok {
		t.Fatal("no battery found")
	}
	b := st.Battery
	if b.EnergyWh != 24 || b.FullWh != 48 || b.DesignWh != 60 {
		t.Errorf("energies = %v/%v/%v Wh, want 24/48/60", b.EnergyWh, b.FullWh, b.DesignWh)
	}
	if b.PowerW != 12 {
		t.Errorf("PowerW = %v, want 12", b.PowerW)
	}
	if b.Percent != 50 {
		t.Errorf("Percent = %v, want 50", b.Percent)
	}
	// Charging: 24 Wh still to take on at 12 W = 2 hours.
	if b.TimeLeft != 2*time.Hour {
		t.Errorf("TimeLeft = %v, want 2h", b.TimeLeft)
	}
	if !b.Charging() {
		t.Error("Charging() should be true")
	}
	if !st.OnAC {
		t.Error("OnAC should be true")
	}
}

// TestCapacityFallback covers a pack that reports only a percentage.
func TestCapacityFallback(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type": "Battery", "status": "Full", "capacity": "97",
	})
	st, ok := NewAt(root).Read()
	if !ok {
		t.Fatal("no battery found")
	}
	if st.Battery.Percent != 97 {
		t.Errorf("Percent = %v, want 97", st.Battery.Percent)
	}
	if st.Battery.TimeLeft != 0 {
		t.Errorf("TimeLeft = %v, want 0 when the rate is unknown", st.Battery.TimeLeft)
	}
	if st.HasAC {
		t.Error("HasAC should be false when no adapter is present")
	}
}

// TestTwoPacks checks that a laptop with two batteries is summed.
func TestTwoPacks(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"BAT0", "BAT1"} {
		supply(t, root, n, map[string]string{
			"type": "Battery", "status": "Discharging",
			"energy_now": "12000000", "energy_full": "24000000",
			"energy_full_design": "24000000", "power_now": "6000000",
		})
	}
	st, ok := NewAt(root).Read()
	if !ok {
		t.Fatal("no battery found")
	}
	b := st.Battery
	if b.Packs != 2 {
		t.Errorf("Packs = %d, want 2", b.Packs)
	}
	if b.EnergyWh != 24 || b.FullWh != 48 || b.PowerW != 12 {
		t.Errorf("summed = %v Wh of %v at %v W, want 24/48/12", b.EnergyWh, b.FullWh, b.PowerW)
	}
	if b.Percent != 50 {
		t.Errorf("Percent = %v, want 50", b.Percent)
	}
}

// TestPeripheralBatteryIgnored: a wireless mouse shows up as a Battery supply
// but reports no capacity, and must not be mistaken for the machine's pack.
func TestPeripheralBatteryIgnored(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "hidpp_battery_0", map[string]string{
		"type": "Battery", "status": "Discharging", "scope": "Device",
	})
	r := NewAt(root)
	if r.Available() {
		t.Error("a battery with no capacity should not count as the machine's")
	}
}

func TestNoSupplies(t *testing.T) {
	r := NewAt(t.TempDir())
	if r.Available() {
		t.Error("Available() should be false with no supplies")
	}
	if _, ok := r.Read(); ok {
		t.Error("Read() should report no battery")
	}
	// A missing root must not panic either — that is a desktop.
	r = NewAt(filepath.Join(t.TempDir(), "definitely-not-here"))
	if _, ok := r.Read(); ok {
		t.Error("Read() on a missing root should report no battery")
	}
}

// TestImplausibleRate: a near-zero draw must not produce a "3 weeks remaining".
func TestImplausibleRate(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type": "Battery", "status": "Discharging",
		"energy_now": "48000000", "energy_full": "48000000", "power_now": "100000", // 0.1 W
	})
	st, _ := NewAt(root).Read()
	if st.Battery.TimeLeft != 0 {
		t.Errorf("TimeLeft = %v, want 0 for an implausible 480-hour estimate", st.Battery.TimeLeft)
	}
}

// TestLiveMachine is informational: it says what this machine actually has.
func TestLiveMachine(t *testing.T) {
	r := New()
	st, ok := r.Read()
	if !ok {
		t.Logf("no battery on this machine (desktop); AC present: %v", st.HasAC)
		return
	}
	t.Logf("battery %s: %.0f%% %s, %.1f/%.1f Wh, %.1f W, health %.0f%%, %v left",
		st.Battery.Name, st.Battery.Percent, st.Battery.Status,
		st.Battery.EnergyWh, st.Battery.FullWh, st.Battery.PowerW,
		st.Battery.Health, st.Battery.TimeLeft)
}

// TestTwoPacksAreReportedSeparately covers the ThinkPad arrangement: an
// internal pack and a hot-swap one, discharged in sequence rather than
// together.
//
// Summed, the machine reads one percentage and the interesting part is gone —
// that one pack is nearly empty while the other is full, and that they have
// aged differently. This is a T480 with a tired internal pack and a healthy
// external one.
func TestTwoPacksAreReportedSeparately(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{ // internal, worn, nearly flat
		"type":               "Battery",
		"status":             "Discharging",
		"manufacturer":       "SMP",
		"model_name":         "01AV489",
		"voltage_now":        "11400000",
		"energy_now":         "2000000",  // 2 Wh
		"energy_full":        "12000000", // 12 Wh today
		"energy_full_design": "24000000", // 24 Wh when new -> 50% health
		"power_now":          "4000000",  // 4 W
	})
	supply(t, root, "BAT1", map[string]string{ // hot-swap, healthy, full
		"type":               "Battery",
		"status":             "Full",
		"manufacturer":       "SMP",
		"model_name":         "01AV423",
		"voltage_now":        "11400000",
		"energy_now":         "24000000", // 24 Wh
		"energy_full":        "24000000", // 24 Wh today
		"energy_full_design": "24000000", // 24 Wh when new -> 100% health
		"power_now":          "0",
	})
	supply(t, root, "AC", map[string]string{"type": "Mains", "online": "0"})

	st, ok := NewAt(root).Read()
	if !ok {
		t.Fatal("Read reported no battery")
	}

	if got := len(st.Packs); got != 2 {
		t.Fatalf("got %d packs, want 2", got)
	}
	if st.Battery.Packs != 2 {
		t.Errorf("aggregate says %d packs, want 2", st.Battery.Packs)
	}

	// The aggregate is still the firmware-shaped answer: 26 of 36 Wh.
	if want := 26.0 / 36.0 * 100; !closeTo(st.Battery.Percent, want) {
		t.Errorf("aggregate = %.1f%%, want %.1f%%", st.Battery.Percent, want)
	}

	// ...and each pack keeps its own story, which the sum destroys.
	bat0, bat1 := st.Packs[0], st.Packs[1]
	if bat0.Name != "BAT0" || bat1.Name != "BAT1" {
		t.Fatalf("packs came back as %q, %q; want BAT0, BAT1 in order", bat0.Name, bat1.Name)
	}
	if !closeTo(bat0.Percent, 2.0/12.0*100) {
		t.Errorf("BAT0 = %.1f%%, want %.1f%%", bat0.Percent, 2.0/12.0*100)
	}
	if !closeTo(bat1.Percent, 100) {
		t.Errorf("BAT1 = %.1f%%, want 100%%", bat1.Percent)
	}
	if !closeTo(bat0.Health, 50) || !closeTo(bat1.Health, 100) {
		t.Errorf("health: BAT0 %.0f%%, BAT1 %.0f%%; want 50%% and 100%%", bat0.Health, bat1.Health)
	}
	if !closeTo(bat0.PowerW, 4) || bat1.PowerW != 0 {
		t.Errorf("draw: BAT0 %.1f W, BAT1 %.1f W; want 4 W and 0 W", bat0.PowerW, bat1.PowerW)
	}
	if bat0.Status != "Discharging" || bat1.Status != "Full" {
		t.Errorf("status: BAT0 %q, BAT1 %q", bat0.Status, bat1.Status)
	}
	// 2 Wh at 4 W is half an hour.
	if want := 30 * time.Minute; bat0.TimeLeft < want-time.Minute || bat0.TimeLeft > want+time.Minute {
		t.Errorf("BAT0 time left = %v, want about %v", bat0.TimeLeft, want)
	}
}

// TestOnePackStillFillsPacks keeps the ordinary laptop honest: one entry, and
// it agrees with the aggregate.
func TestOnePackStillFillsPacks(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "BAT0", map[string]string{
		"type":        "Battery",
		"status":      "Discharging",
		"voltage_now": "11400000",
		"energy_now":  "24000000",
		"energy_full": "48000000",
		"power_now":   "12000000",
	})
	st, ok := NewAt(root).Read()
	if !ok {
		t.Fatal("Read reported no battery")
	}
	if len(st.Packs) != 1 {
		t.Fatalf("got %d packs, want 1", len(st.Packs))
	}
	if !closeTo(st.Packs[0].Percent, st.Battery.Percent) {
		t.Errorf("single pack %.1f%% disagrees with the aggregate %.1f%%",
			st.Packs[0].Percent, st.Battery.Percent)
	}
}

func closeTo(got, want float64) bool { return got-want < 0.05 && want-got < 0.05 }
