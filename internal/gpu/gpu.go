// Package gpu reads AMD GPU statistics directly from sysfs (no ROCm / nvtop
// dependency). It auto-detects the AMD card by scanning DRM vendor IDs.
package gpu

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// amdVendorID is the PCI vendor ID for AMD/ATI.
const amdVendorID = "0x1002"

// Reader holds the resolved sysfs paths for the detected AMD GPU.
type Reader struct {
	available bool
	name      string
	devPath   string // /sys/class/drm/cardN/device
	hwmon     string // .../device/hwmon/hwmonM
}

// Sample is one instantaneous reading of the GPU.
type Sample struct {
	UsagePct  float64
	VramUsed  uint64
	VramTotal uint64
	GttUsed   uint64
	TempC     float64
	FanRPM    int
	PowerW    float64
	SclkMHz   float64 // engine/core clock
	MclkMHz   float64 // memory clock
}

// NewReader scans /sys/class/drm/card* for the first AMD card (vendor 0x1002)
// and locates its amdgpu hwmon node. The returned Reader reports Available()
// == false when no AMD GPU is present.
func NewReader() *Reader {
	r := &Reader{}
	cards, _ := filepath.Glob("/sys/class/drm/card*/device/vendor")
	for _, vfile := range cards {
		v, err := os.ReadFile(vfile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(v)) != amdVendorID {
			continue
		}
		// Skip render/connector nodes such as card1-DP-1.
		dev := filepath.Dir(vfile) // .../cardN/device
		if _, err := os.Stat(filepath.Join(dev, "gpu_busy_percent")); err != nil {
			continue
		}
		r.devPath = dev
		r.available = true
		r.name = friendlyName(dev)
		r.hwmon = findAmdHwmon(dev)
		break
	}
	return r
}

// Available reports whether an AMD GPU was detected.
func (r *Reader) Available() bool { return r != nil && r.available }

// Name returns a human-readable label for the GPU.
func (r *Reader) Name() string { return r.name }

// Read takes one sample. Missing values are left at their zero value.
func (r *Reader) Read() Sample {
	var s Sample
	if !r.available {
		return s
	}
	if v, err := readU(filepath.Join(r.devPath, "gpu_busy_percent")); err == nil {
		s.UsagePct = float64(v)
	}
	s.VramUsed, _ = readU(filepath.Join(r.devPath, "mem_info_vram_used"))
	s.VramTotal, _ = readU(filepath.Join(r.devPath, "mem_info_vram_total"))
	s.GttUsed, _ = readU(filepath.Join(r.devPath, "mem_info_gtt_used"))

	if r.hwmon != "" {
		if v, err := readU(filepath.Join(r.hwmon, "temp1_input")); err == nil {
			s.TempC = float64(v) / 1000.0
		}
		if v, err := readU(filepath.Join(r.hwmon, "fan1_input")); err == nil {
			s.FanRPM = int(v)
		}
		if v, err := readU(filepath.Join(r.hwmon, "power1_average")); err == nil {
			s.PowerW = float64(v) / 1e6 // microwatts -> watts
		}
		if v, err := readU(filepath.Join(r.hwmon, "freq1_input")); err == nil {
			s.SclkMHz = float64(v) / 1e6 // Hz -> MHz
		}
		if v, err := readU(filepath.Join(r.hwmon, "freq2_input")); err == nil {
			s.MclkMHz = float64(v) / 1e6
		}
	}
	return s
}

// findAmdHwmon returns the hwmon directory under the card whose name is
// "amdgpu", or "" if none is found.
func findAmdHwmon(devPath string) string {
	dirs, _ := filepath.Glob(filepath.Join(devPath, "hwmon", "hwmon*"))
	for _, d := range dirs {
		name, err := os.ReadFile(filepath.Join(d, "name"))
		if err == nil && strings.TrimSpace(string(name)) == "amdgpu" {
			return d
		}
	}
	return ""
}

// friendlyName builds a label from the DRM card name, e.g. "AMD GPU (card1)".
func friendlyName(devPath string) string {
	// devPath is .../cardN/device; the card dir is its parent.
	card := filepath.Base(filepath.Dir(devPath))
	return "AMD GPU (" + card + ")"
}

func readU(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}
