package power

// The battery on Linux: /sys/class/power_supply.
//
// Each pack is a directory, and so is each AC adapter. The figures come in
// microwatt-hours or microampere-hours depending on the firmware, which is what
// most of readPack is about.

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sysfsRoot is where the kernel exposes power supplies.
const sysfsRoot = "/sys/class/power_supply"

// Reader holds the resolved sysfs paths.
type Reader struct {
	batteries []string // directories of type Battery
	mains     []string // directories of type Mains
}

// New scans the standard sysfs root.
func New() *Reader { return NewAt(sysfsRoot) }

// NewAt scans an arbitrary root. Used by the tests, which build synthetic
// supplies — a desktop has no battery to read.
func NewAt(root string) *Reader {
	entries, err := os.ReadDir(root)
	if err != nil {
		return &Reader{}
	}
	r := &Reader{}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		switch readStr(filepath.Join(dir, "type")) {
		case "Battery":
			// Some laptops expose peripheral batteries (a wireless mouse) here;
			// only count packs that report a capacity.
			if has(dir, "energy_full") || has(dir, "charge_full") || has(dir, "capacity") {
				r.batteries = append(r.batteries, dir)
			}
		case "Mains", "USB":
			r.mains = append(r.mains, dir)
		}
	}
	sort.Strings(r.batteries)
	sort.Strings(r.mains)
	return r
}

// Available reports whether this machine has a battery.
func (r *Reader) Available() bool { return r != nil && len(r.batteries) > 0 }

// Read takes one sample. ok is false when there is no battery.
func (r *Reader) Read() (State, bool) {
	var st State
	if r == nil {
		return st, false
	}
	for _, dir := range r.mains {
		st.HasAC = true
		if v, ok := readUint(filepath.Join(dir, "online")); ok && v == 1 {
			st.OnAC = true
		}
	}
	if len(r.batteries) == 0 {
		return st, false
	}

	b := Battery{Packs: len(r.batteries)}
	st.Packs = make([]Battery, 0, len(r.batteries))
	for i, dir := range r.batteries {
		one := readPack(dir)
		finishPack(&one, dir)
		st.Packs = append(st.Packs, one)
		if i == 0 {
			b.Name, b.Vendor, b.Model = one.Name, one.Vendor, one.Model
			b.Technology, b.Status = one.Technology, one.Status
			b.VoltageV = one.VoltageV
		}
		b.EnergyWh += one.EnergyWh
		b.FullWh += one.FullWh
		b.DesignWh += one.DesignWh
		b.PowerW += one.PowerW
		b.CycleCount += one.CycleCount
		// A machine is charging if any pack is.
		if one.Status == "Charging" {
			b.Status = "Charging"
		}
	}

	switch {
	case b.FullWh > 0:
		b.Percent = clamp(b.EnergyWh / b.FullWh * 100)
	default:
		// No usable energy figures: fall back to the firmware's own percentage.
		if v, ok := readUint(filepath.Join(r.batteries[0], "capacity")); ok {
			b.Percent = clamp(float64(v))
		}
	}
	if b.DesignWh > 0 && b.FullWh > 0 {
		b.Health = clamp(b.FullWh / b.DesignWh * 100)
	}
	b.TimeLeft = timeLeft(b)

	st.Battery = b
	return st, true
}

// finishPack fills in the figures that are derived rather than read: the
// percentage, the health and the time estimate. The aggregate computes its own
// from the summed energies, so this is only for the per-pack view.
func finishPack(b *Battery, dir string) {
	b.Packs = 1
	switch {
	case b.FullWh > 0:
		b.Percent = clamp(b.EnergyWh / b.FullWh * 100)
	default:
		if v, ok := readUint(filepath.Join(dir, "capacity")); ok {
			b.Percent = clamp(float64(v))
		}
	}
	if b.DesignWh > 0 && b.FullWh > 0 {
		b.Health = clamp(b.FullWh / b.DesignWh * 100)
	}
	b.TimeLeft = timeLeft(*b)
}

// readPack reads a single battery directory.
func readPack(dir string) Battery {
	b := Battery{
		Name:       filepath.Base(dir),
		Vendor:     readStr(filepath.Join(dir, "manufacturer")),
		Model:      readStr(filepath.Join(dir, "model_name")),
		Technology: readStr(filepath.Join(dir, "technology")),
		Status:     readStr(filepath.Join(dir, "status")),
	}
	if b.Status == "" {
		b.Status = "Unknown"
	}
	if v, ok := readUint(filepath.Join(dir, "cycle_count")); ok {
		b.CycleCount = int(v)
	}
	if v, ok := readUint(filepath.Join(dir, "voltage_now")); ok {
		b.VoltageV = float64(v) / 1e6 // microvolts
	}

	// Preferred shape: energies in microwatt-hours, rate in microwatts.
	energy, haveEnergy := readUint(filepath.Join(dir, "energy_now"))
	full, haveFull := readUint(filepath.Join(dir, "energy_full"))
	if haveEnergy && haveFull {
		b.EnergyWh = float64(energy) / 1e6
		b.FullWh = float64(full) / 1e6
		if v, ok := readUint(filepath.Join(dir, "energy_full_design")); ok {
			b.DesignWh = float64(v) / 1e6
		}
		if v, ok := readUint(filepath.Join(dir, "power_now")); ok {
			b.PowerW = float64(v) / 1e6
		}
		return b
	}

	// Other shape: charges in microamp-hours, rate in microamps. Multiply by the
	// pack voltage to get the same watt-hours and watts as above.
	charge, haveCharge := readUint(filepath.Join(dir, "charge_now"))
	cfull, haveCFull := readUint(filepath.Join(dir, "charge_full"))
	if haveCharge && haveCFull && b.VoltageV > 0 {
		b.EnergyWh = float64(charge) / 1e6 * b.VoltageV
		b.FullWh = float64(cfull) / 1e6 * b.VoltageV
		if v, ok := readUint(filepath.Join(dir, "charge_full_design")); ok {
			b.DesignWh = float64(v) / 1e6 * b.VoltageV
		}
		if v, ok := readUint(filepath.Join(dir, "current_now")); ok {
			b.PowerW = float64(v) / 1e6 * b.VoltageV
		}
	}
	return b
}

func has(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func readStr(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readUint(path string) (uint64, bool) {
	s := readStr(path)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil
}
