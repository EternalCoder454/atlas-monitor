// Package sensors reads every temperature, fan, voltage and power sensor the
// kernel knows about, and says what each one belongs to.
//
// The CPU and GPU pages already show the one temperature each that matters to
// them. The machine usually has a dozen more — drives, memory modules, the
// network cards, the motherboard's own chip, the fans — which Linux exposes
// through hwmon and nothing in Atlas looked at. The kernel names them by driver
// ("spd5118", "r8169_0_600:00"), so most of the work here is saying instead what
// the hardware is.
package sensors

import (
	"sort"
	"strconv"
	"strings"
)

// Kind is what a reading measures.
type Kind int

const (
	Temperature Kind = iota
	Fan
	Voltage
	Power
	Current
)

// Unit is the unit a kind is shown in.
func (k Kind) Unit() string {
	switch k {
	case Temperature:
		return "°C"
	case Fan:
		return "RPM"
	case Voltage:
		return "V"
	case Power:
		return "W"
	default:
		return "A"
	}
}

// Category is what sort of hardware a device is, which decides the order the
// page lists them in.
type Category int

const (
	Processor Category = iota
	Graphics
	Memory
	Storage
	Motherboard
	Network
	PowerSupply
	Other
)

// Reading is one sensor.
type Reading struct {
	Label string
	Kind  Kind
	// Value is in the kind's unit, and valid only when OK.
	Value float64
	OK    bool
	// High and Critical are the hardware's own thresholds, where it gives them;
	// zero where it does not.
	High, Critical float64
	// Core marks a per-core CPU temperature. There can be thirty of them, so
	// the page folds them away behind the package reading.
	Core bool

	index  int     // the N in tempN_input, for ordering
	scale  float64 // raw units per displayed unit
	source reader
}

// reader is what a reading re-reads its raw value from: a held-open sysfs file
// on Linux, and a fake in the tests.
type reader interface {
	Int() (int64, bool)
}

// Device is one piece of hardware and its sensors.
type Device struct {
	Name     string
	Category Category
	// Driver is the kernel's name for it, shown beside the friendly one because
	// it is what a search engine will find.
	Driver   string
	Readings []Reading
}

// Read refreshes every reading's value.
func (d *Device) Read() {
	for i := range d.Readings {
		r := &d.Readings[i]
		if r.source == nil {
			r.OK = false
			continue
		}
		raw, ok := r.source.Int()
		r.OK = ok
		if ok {
			r.Value = float64(raw) / r.scale
		}
	}
}

// Warmth grades a temperature against the hardware's own thresholds: 2 at or
// past critical, 1 at or past high, 0 otherwise or when there are none.
func (r *Reading) Warmth() int {
	if r.Kind != Temperature || !r.OK {
		return 0
	}
	switch {
	case r.Critical > 0 && r.Value >= r.Critical:
		return 2
	case r.High > 0 && r.Value >= r.High:
		return 1
	}
	return 0
}

// Format renders a value in its unit at a precision that means something for
// that kind: whole degrees and revolutions, millivolts, tenths of a watt.
func (r *Reading) Format() string {
	if !r.OK {
		return "—"
	}
	var s string
	switch r.Kind {
	case Temperature, Fan:
		s = strconv.FormatFloat(r.Value, 'f', 0, 64)
	case Voltage:
		s = strconv.FormatFloat(r.Value, 'f', 3, 64)
	case Power:
		s = strconv.FormatFloat(r.Value, 'f', 1, 64)
	default:
		s = strconv.FormatFloat(r.Value, 'f', 2, 64)
	}
	return s + " " + r.Kind.Unit()
}

// identify says what a hwmon device is, from its driver name and, where the
// driver does not say, the device behind it. model is the device's own model
// string where it has one (drives do), and address is the last component of its
// device path, which for a memory module is its bus address.
func identify(driver, model, address string) (string, Category) {
	d := strings.ToLower(driver)
	has := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(d, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has("coretemp", "k10temp", "zenpower", "cpu_thermal", "k8temp", "via_cputemp"):
		return "Processor", Processor
	case has("amdgpu", "radeon", "nouveau", "i915", "xe"):
		return "Graphics card", Graphics
	case has("spd5118", "jc42", "ee1004"):
		// Memory module thermal sensors sit on the SPD bus at 0x50 upwards, one
		// address per slot, so the address says which module it is.
		if _, a, ok := strings.Cut(address, "-"); ok {
			if n, err := strconv.ParseUint(a, 16, 16); err == nil && n >= 0x18 {
				slot := n & 7
				return "Memory module " + strconv.FormatUint(slot+1, 10), Memory
			}
		}
		return "Memory module", Memory
	case has("nvme", "drivetemp"):
		if model != "" {
			return model, Storage
		}
		return "Drive", Storage
	case has("iwlwifi", "ath9k", "ath10k", "ath11k", "ath12k", "mt76", "mt79", "rtw", "brcmfmac"):
		return "Wi-Fi adapter", Network
	case has("r8169", "r8125", "igb", "igc", "e1000e", "atlantic", "tg3", "bnxt", "ixgbe", "mlx", "enp", "eth"):
		return "Ethernet adapter", Network
	case has("acpitz", "nct", "it87", "it86", "asus", "gigabyte", "dell", "thinkpad", "applesmc", "hp_", "w83", "f71", "pch_"):
		return "Motherboard", Motherboard
	case has("bat", "ac", "adp", "ucsi"):
		return "Power supply", PowerSupply
	}
	return driver, Other
}

// label says what one sensor on a device measures, from the driver's own label
// where it has one. A few drivers use terse names that are rewritten; the rest
// are kept, because the driver knows its hardware better than a table here.
func label(driver string, kind Kind, index int, given string, unlabelled int) string {
	switch strings.ToLower(given) {
	case "edge":
		return "Edge"
	case "junction":
		return "Hotspot"
	case "mem":
		return "Memory"
	case "ppt":
		return "Power draw"
	case "vddgfx":
		return "Core voltage"
	case "vddnb":
		return "SoC voltage"
	case "composite":
		return "Drive"
	case "tctl":
		return "Control"
	case "tdie":
		return "Die"
	}
	if strings.HasPrefix(given, "Package id ") {
		return "Package"
	}
	if given != "" {
		return given
	}
	name := map[Kind]string{Temperature: "Temperature", Fan: "Fan", Voltage: "Voltage", Power: "Power", Current: "Current"}[kind]
	if unlabelled > 1 {
		name += " " + strconv.Itoa(index)
	}
	return name
}

// order puts devices in a stable, sensible order: by category, then name, and
// numbers devices that would otherwise share a name.
func order(devs []*Device) {
	sort.SliceStable(devs, func(i, j int) bool {
		if devs[i].Category != devs[j].Category {
			return devs[i].Category < devs[j].Category
		}
		return devs[i].Name < devs[j].Name
	})
	count := map[string]int{}
	for _, d := range devs {
		count[d.Name]++
	}
	seen := map[string]int{}
	for _, d := range devs {
		if count[d.Name] > 1 {
			seen[d.Name]++
			d.Name += " " + strconv.Itoa(seen[d.Name])
		}
	}
	for _, d := range devs {
		sort.SliceStable(d.Readings, func(i, j int) bool {
			a, b := d.Readings[i], d.Readings[j]
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			return a.index < b.index
		})
	}
}
