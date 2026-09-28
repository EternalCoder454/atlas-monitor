package sensors

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"atlas-monitor/internal/sysfs"
)

// root is where hwmon devices live; a variable so the tests can point it at a
// fake tree.
var root = "/sys/class/hwmon"

// Available reports whether the machine has any sensors at all, cheaply: the
// sidebar asks this at startup, before anyone has opened the page.
func Available() bool {
	inputs, _ := filepath.Glob(filepath.Join(root, "hwmon*", "*_input"))
	return len(inputs) > 0
}

// Discover finds every sensor and holds its file open for re-reading. The
// caller closes the devices with Close when it no longer needs them.
func Discover() []*Device {
	dirs, _ := filepath.Glob(filepath.Join(root, "hwmon*"))
	var devs []*Device
	for _, dir := range dirs {
		if d := discover(dir); d != nil {
			devs = append(devs, d)
		}
	}
	order(devs)
	for _, d := range devs {
		d.Read()
	}
	return devs
}

// Close releases every file the devices hold.
func Close(devs []*Device) {
	for _, d := range devs {
		for i := range d.Readings {
			if f, ok := d.Readings[i].source.(sysfsReader); ok {
				f.f.Close()
			}
			d.Readings[i].source = nil
		}
	}
}

// sysfsReader reads a signed integer from a held-open file. Temperatures can be
// below zero, which sysfs.File's own Uint cannot say.
type sysfsReader struct{ f *sysfs.File }

func (r sysfsReader) Int() (int64, bool) {
	b, ok := r.f.Bytes()
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(sysfs.TrimSpace(b)), 10, 64)
	return n, err == nil
}

var inputName = regexp.MustCompile(`^(temp|fan|in|power|curr)(\d+)_(input|average)$`)

func discover(dir string) *Device {
	driver := strings.TrimSpace(sysfs.ReadString(filepath.Join(dir, "name")))
	devPath, _ := filepath.EvalSymlinks(filepath.Join(dir, "device"))
	model := strings.TrimSpace(sysfs.ReadString(filepath.Join(dir, "device", "model")))
	name, cat := identify(driver, model, filepath.Base(devPath))

	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type found struct {
		kind  Kind
		index int
		file  string
	}
	var inputs []found
	unlabelled := map[Kind]int{}
	for _, e := range ents {
		m := inputName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		kind := map[string]Kind{"temp": Temperature, "fan": Fan, "in": Voltage, "power": Power, "curr": Current}[m[1]]
		idx, _ := strconv.Atoi(m[2])
		// A power sensor can offer both _input and _average; one is enough.
		if m[3] == "average" {
			if _, err := os.Stat(filepath.Join(dir, m[1]+m[2]+"_input")); err == nil {
				continue
			}
		}
		inputs = append(inputs, found{kind, idx, e.Name()})
		if sysfs.ReadString(filepath.Join(dir, m[1]+m[2]+"_label")) == "" {
			unlabelled[kind]++
		}
	}
	if len(inputs) == 0 {
		return nil
	}

	d := &Device{Name: name, Category: cat, Driver: driver}
	cores := 0
	for _, in := range inputs {
		prefix := strings.SplitN(in.file, "_", 2)[0] // "temp3"
		given := strings.TrimSpace(sysfs.ReadString(filepath.Join(dir, prefix+"_label")))
		f := sysfs.Open(filepath.Join(dir, in.file))
		if f == nil {
			continue
		}
		r := Reading{
			Label:  label(driver, in.kind, in.index, given, unlabelled[in.kind]),
			Kind:   in.kind,
			index:  in.index,
			scale:  scaleOf(in.kind),
			source: sysfsReader{f},
		}
		if in.kind == Temperature {
			r.High = threshold(filepath.Join(dir, prefix+"_max"))
			r.Critical = threshold(filepath.Join(dir, prefix+"_crit"))
			if strings.HasPrefix(given, "Core ") {
				r.Core = true
				cores++
			}
		}
		d.Readings = append(d.Readings, r)
	}
	// A few per-core readings are worth showing as they are; only a long list
	// is folded away.
	if cores <= 4 {
		for i := range d.Readings {
			d.Readings[i].Core = false
		}
	}
	return d
}

// scaleOf is the raw units per displayed unit: hwmon reports millidegrees,
// millivolts, milliamps and microwatts, and fans in plain RPM.
func scaleOf(k Kind) float64 {
	switch k {
	case Fan:
		return 1
	case Power:
		return 1e6
	default:
		return 1e3
	}
}

// threshold reads a temperature limit in degrees, or 0 when there is none or it
// is not believable — some drivers report 255 °C or 0 for "not set".
func threshold(path string) float64 {
	s := strings.TrimSpace(sysfs.ReadString(path))
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n >= 200000 {
		return 0
	}
	return float64(n) / 1000
}
