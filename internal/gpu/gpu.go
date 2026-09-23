// Package gpu reads GPU statistics without any vendor SDK at build time.
//
// Three backends, tried in order of how much they can tell us:
//
//   - amdgpu, straight from sysfs (utilisation, VRAM, clocks, power, fan);
//   - NVIDIA through NVML, dlopen'd at runtime so the driver is never a build
//     dependency;
//   - a generic DRM backend for everything else — Intel, nouveau, and any card
//     the other two do not claim — reading hwmon for temperature, fan and power,
//     sysfs for clocks, and the kernel's per-client fdinfo counters for
//     utilisation.
//
// The result is one GPU: the most capable card present.
package gpu

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"

	"atlas-monitor/internal/sysfs"
)

// PCI vendor IDs of the cards we recognise by name.
const (
	vendorAMD    = "0x1002"
	vendorNVIDIA = "0x10de"
	vendorIntel  = "0x8086"
)

// Sample is one instantaneous reading of the GPU. Fields a backend cannot fill
// are left at their zero value; TempC and the clocks use 0 for "unknown", and
// the UI renders that as a dash.
type Sample struct {
	UsagePct   float64
	VramUsed   uint64
	VramTotal  uint64
	GttUsed    uint64
	TempC      float64
	FanRPM     int     // 0 when the card reports a percentage instead
	FanPercent float64 // 0 when the card reports RPM instead
	PowerW     float64
	SclkMHz    float64 // engine/core clock
	MclkMHz    float64 // memory clock
}

// backend is one way of reading a particular card.
type backend interface {
	// label is the human-readable name shown on the GPU page.
	label() string
	// sample fills s with the current reading.
	sample(s *Sample)
	// close releases anything the backend holds open.
	close()
}

// Reader is the detected GPU, or an unavailable Reader when there is none.
type Reader struct {
	be   backend
	name string
}

// NewReader detects the best GPU on the machine. The returned Reader reports
// Available() == false when nothing usable was found.
func NewReader() *Reader {
	for _, detect := range []func() backend{newNVMLBackend, newAMDBackend, newDRMBackend} {
		if be := detect(); be != nil {
			return &Reader{be: be, name: be.label()}
		}
	}
	return &Reader{}
}

// Available reports whether a GPU was detected.
func (r *Reader) Available() bool { return r != nil && r.be != nil }

// Name returns a human-readable label for the GPU.
func (r *Reader) Name() string { return r.name }

// Read takes one sample. Missing values are left at their zero value.
func (r *Reader) Read() Sample {
	var s Sample
	if r.Available() {
		r.be.sample(&s)
	}
	return s
}

// Close releases whatever the backend holds. Safe on an unavailable Reader.
func (r *Reader) Close() {
	if r.Available() {
		r.be.close()
		r.be = nil
	}
}

// --- shared sysfs helpers ---------------------------------------------------

// drmCard is one /sys/class/drm/cardN entry.
type drmCard struct {
	card   string // "card1"
	dev    string // /sys/class/drm/card1/device
	vendor string // "0x1002"
}

// drmCards lists the real DRM cards, skipping connector nodes such as
// card1-DP-1 (which have no device/vendor of their own).
func drmCards() []drmCard {
	vendors, _ := filepath.Glob("/sys/class/drm/card*/device/vendor")
	out := make([]drmCard, 0, len(vendors))
	for _, vf := range vendors {
		v, err := os.ReadFile(vf)
		if err != nil {
			continue
		}
		dev := filepath.Dir(vf)
		out = append(out, drmCard{
			card:   filepath.Base(filepath.Dir(dev)),
			dev:    dev,
			vendor: strings.ToLower(strings.TrimSpace(string(v))),
		})
	}
	return out
}

// hwmonDir returns the card's hwmon directory, preferring one whose name
// matches want (e.g. "amdgpu"); any hwmon is accepted when want is empty.
func hwmonDir(devPath, want string) string {
	dirs, _ := filepath.Glob(filepath.Join(devPath, "hwmon", "hwmon*"))
	for _, d := range dirs {
		if want == "" {
			return d
		}
		name, err := os.ReadFile(filepath.Join(d, "name"))
		if err == nil && strings.TrimSpace(string(name)) == want {
			return d
		}
	}
	if want != "" && len(dirs) > 0 {
		return dirs[0] // right card, unexpected driver name — still usable
	}
	return ""
}

// hwmon holds a card's hwmon attributes open. They are sampled every second for
// as long as the GPU page is visible, and reopening each of them was four
// syscalls where re-reading the descriptor is one.
type hwmon struct {
	temp, fan, power, sclk, mclk *sysfs.File
}

// openHwmon holds open whichever of the standard attributes the driver exposes.
func openHwmon(dir string) hwmon {
	if dir == "" {
		return hwmon{}
	}
	return hwmon{
		temp: sysfs.Open(filepath.Join(dir, "temp1_input")),
		fan:  sysfs.Open(filepath.Join(dir, "fan1_input")),
		// Some drivers report average power, others instantaneous.
		power: sysfs.OpenFirst(
			filepath.Join(dir, "power1_average"),
			filepath.Join(dir, "power1_input")),
		sclk: sysfs.Open(filepath.Join(dir, "freq1_input")),
		mclk: sysfs.Open(filepath.Join(dir, "freq2_input")),
	}
}

// read fills the temperature, fan, power and clock fields.
func (h hwmon) read(s *Sample) {
	if v, ok := h.temp.Uint(); ok {
		s.TempC = float64(v) / 1000.0 // millidegrees
	}
	if v, ok := h.fan.Uint(); ok {
		s.FanRPM = int(v)
	}
	if v, ok := h.power.Uint(); ok {
		s.PowerW = float64(v) / 1e6 // microwatts
	}
	if v, ok := h.sclk.Uint(); ok {
		s.SclkMHz = float64(v) / 1e6 // Hz
	}
	if v, ok := h.mclk.Uint(); ok {
		s.MclkMHz = float64(v) / 1e6
	}
}

func (h hwmon) close() {
	h.temp.Close()
	h.fan.Close()
	h.power.Close()
	h.sclk.Close()
	h.mclk.Close()
}

// readU reads a one-shot unsigned integer, for detection and static values.
func readU(path string) (uint64, bool) { return sysfs.ReadUint(path) }

// readStr reads a one-shot string value, trimmed.
func readStr(path string) string { return sysfs.ReadString(path) }

// gpuName produces the friendliest label a card can give us: the marketing name
// from the kernel when the driver exposes one, then the PCI database, then the
// vendor and card node.
func gpuName(c drmCard) string {
	if n := readStr(filepath.Join(c.dev, "product_name")); n != "" {
		return n
	}
	if n := pciName(c.vendor, readStr(filepath.Join(c.dev, "device"))); n != "" {
		return n
	}
	return vendorLabel(c.vendor) + " (" + c.card + ")"
}

// vendorLabel names a PCI vendor ID.
func vendorLabel(vendor string) string {
	switch vendor {
	case vendorAMD:
		return "AMD GPU"
	case vendorNVIDIA:
		return "NVIDIA GPU"
	case vendorIntel:
		return "Intel GPU"
	default:
		return "GPU"
	}
}

// pciIDsPaths are where distributions keep the PCI device database.
var pciIDsPaths = []string{
	"/usr/share/hwdata/pci.ids",
	"/usr/share/misc/pci.ids",
	"/usr/share/pci.ids",
}

// pciName looks a device up in the system PCI database, e.g. 0x1002/0x744c ->
// "Navi 31 [Radeon RX 7900 XT/7900 XTX/7900M]". It is called once at startup;
// the file is a couple of megabytes and is not kept around afterwards.
//
// The format is a vendor line at column 0 followed by its devices, each indented
// by one tab:
//
//	1002  Advanced Micro Devices, Inc. [AMD/ATI]
//		744c  Navi 31 [Radeon RX 7900 XT/7900 XTX/7900M]
func pciName(vendor, device string) string {
	v, d := strings.TrimPrefix(vendor, "0x"), strings.TrimPrefix(device, "0x")
	if len(v) != 4 || len(d) != 4 {
		return ""
	}
	for _, path := range pciIDsPaths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		name := scanPCIIDs(f, v, d)
		f.Close()
		if name != "" {
			return name
		}
	}
	return ""
}

// scanPCIIDs walks the database for one vendor's block and returns the named
// device within it.
func scanPCIIDs(r io.Reader, vendor, device string) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8192), 64*1024)
	inVendor := false
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		switch {
		case line[0] != '\t': // a vendor line
			if inVendor {
				return "" // walked past our vendor without a match
			}
			inVendor = strings.HasPrefix(line, vendor+" ")
		case inVendor && len(line) > 1 && line[1] != '\t': // a device line
			rest := strings.TrimPrefix(line[1:], device)
			if len(rest) != len(line)-1 { // the prefix matched
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// clampPct constrains a percentage to 0..100.
func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
