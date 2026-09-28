// Package power reads battery and AC-adapter state from
// /sys/class/power_supply.
//
// The kernel exposes a battery in one of two shapes depending on the firmware:
// energy_* in microwatt-hours with power_now in microwatts, or charge_* in
// microamp-hours with current_now in microamps and a separate voltage. Both are
// normalised here to watt-hours and watts so the rest of the app never has to
// care which one a machine happens to use.
package power

import "time"

// Battery is the aggregate state of the machine's batteries. Laptops with two
// packs are summed, which is what the firmware-reported percentage does too.
type Battery struct {
	Name       string // kernel name of the first pack, e.g. "BAT0"
	Vendor     string
	Model      string
	Technology string // e.g. "Li-ion"
	Status     string // Charging, Discharging, Full, Not charging, Unknown
	Packs      int    // how many batteries were summed

	Percent    float64 // 0..100
	EnergyWh   float64 // current charge
	FullWh     float64 // capacity when full today
	DesignWh   float64 // capacity when new; 0 if the firmware does not say
	PowerW     float64 // magnitude of the current flow, charging or discharging
	VoltageV   float64
	CycleCount int

	// Health is FullWh/DesignWh as a percentage, or 0 when design capacity is
	// unknown. It is how much of the original capacity the pack still holds.
	Health float64

	// TimeLeft is the estimate to empty when discharging, or to full when
	// charging. Zero when the rate is unknown or the battery is idle.
	TimeLeft time.Duration
}

// Charging reports whether the pack is taking charge.
func (b Battery) Charging() bool { return b.Status == "Charging" }

// Discharging reports whether the machine is running off the battery.
func (b Battery) Discharging() bool { return b.Status == "Discharging" }

// State is one reading of the machine's power supplies.
type State struct {
	// Battery is every pack summed, which is the figure a laptop's firmware
	// reports and the one people mean by "battery percentage".
	Battery Battery

	// Packs is each pack on its own, in kernel name order, with its own
	// percentage, health and time estimate filled in.
	//
	// A ThinkPad with an internal pack and a hot-swap one discharges them in
	// sequence, not together: summed, the machine simply reads 80%, and the
	// fact that one pack is empty and the other full — or that one has aged
	// twice as far as the other — is not visible anywhere. This is that detail.
	// It has one entry on an ordinary laptop and none on a desktop.
	Packs []Battery

	HasAC bool // an AC adapter was found
	OnAC  bool // …and it is plugged in
}

// timeLeft estimates the run time at the current rate: to empty when
// discharging, to full when charging. Zero when nothing useful can be said.
func timeLeft(b Battery) time.Duration {
	if b.PowerW <= 0 {
		return 0
	}
	var wh float64
	switch b.Status {
	case "Discharging":
		wh = b.EnergyWh
	case "Charging":
		wh = b.FullWh - b.EnergyWh
	default:
		return 0
	}
	if wh <= 0 {
		return 0
	}
	hours := wh / b.PowerW
	if hours > 48 { // a nonsense rate; better to say nothing
		return 0
	}
	return time.Duration(hours * float64(time.Hour))
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
