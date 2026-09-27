package power

import (
	"time"

	"atlas-monitor/internal/winapi"
)

// The battery on Windows: GetSystemPowerStatus.
//
// This is a good deal less than Linux gives. sysfs reports each pack's energy in
// watt-hours, its voltage, its design capacity and its cycle count, which is what
// lets Atlas say a pack holds 75% of its original capacity. Windows' power status
// reports a percentage, whether the machine is plugged in, and an estimate of the
// time left.
//
// The richer figures do exist — behind the battery device IOCTLs, reached through
// SetupAPI device enumeration — and design capacity and cycle count are the two
// worth going after, because battery health is the most useful line on that page.
// They are not read yet, and the fields are left at zero, which the UI already
// treats as "the firmware does not say" and hides. Reporting a health figure
// derived from a percentage would be making one up.
//
// There is also only ever one pack here. Windows sums multiple batteries into a
// single composite status, so the per-pack breakdown a ThinkPad shows on Linux has
// nothing behind it. Packs therefore holds the one aggregate entry rather than
// being empty, so the page has something to show.

// Reader reads the machine's power status. It has no state: the answer comes from
// a system call, so there are no paths to resolve up front.
type Reader struct {
	hasBattery bool
}

// New checks once whether this machine has a battery at all.
func New() *Reader {
	p, err := winapi.ReadPower()
	if err != nil {
		return nil
	}
	return &Reader{hasBattery: p.HasBattery}
}

// Available reports whether there is a battery to read.
func (r *Reader) Available() bool { return r != nil && r.hasBattery }

// Read takes one sample. ok is false when there is no battery.
func (r *Reader) Read() (State, bool) {
	var st State
	if r == nil {
		return st, false
	}

	p, err := winapi.ReadPower()
	if err != nil {
		return st, false
	}

	// Windows says whether it is on AC even on a machine with no battery, which is
	// worth reporting: a desktop that has lost mains power is on a UPS.
	st.HasAC = !p.Unknown
	st.OnAC = p.OnAC

	if !p.HasBattery {
		return st, false
	}

	b := Battery{
		Name:    "Battery",
		Packs:   1,
		Percent: clamp(float64(p.Percent)),
		Status:  windowsStatus(p),
	}
	// Only meaningful while discharging: Windows reports the time to empty, and
	// has no equivalent estimate for charging.
	if p.SecondsLeft > 0 && b.Status == "Discharging" {
		b.TimeLeft = time.Duration(p.SecondsLeft) * time.Second
	}

	st.Battery = b
	st.Packs = []Battery{b}
	return st, true
}

// windowsStatus maps the power status onto the words Linux's sysfs uses, because
// that is the vocabulary the rest of Atlas reads — Charging and Discharging are
// tested for by name.
func windowsStatus(p winapi.Power) string {
	switch {
	case p.Charging:
		return "Charging"
	case p.OnAC && p.Percent >= 100:
		return "Full"
	case p.OnAC:
		// Plugged in and not charging: a pack at its charge limit, which is what
		// a laptop with a conservation threshold does all day.
		return "Not charging"
	default:
		return "Discharging"
	}
}
