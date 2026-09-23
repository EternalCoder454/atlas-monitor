package gpu

import (
	"os"
	"path/filepath"
)

// amdBackend reads an AMD card straight from sysfs. amdgpu exposes everything
// we want as plain files, so this costs a handful of small reads per second and
// needs no scanning.
type amdBackend struct {
	name  string
	dev   string // /sys/class/drm/cardN/device
	hwmon string
}

func newAMDBackend() backend {
	for _, c := range drmCards() {
		if c.vendor != vendorAMD {
			continue
		}
		// gpu_busy_percent is the marker for a real amdgpu render node.
		if _, err := os.Stat(filepath.Join(c.dev, "gpu_busy_percent")); err != nil {
			continue
		}
		return &amdBackend{
			name:  gpuName(c),
			dev:   c.dev,
			hwmon: hwmonDir(c.dev, "amdgpu"),
		}
	}
	return nil
}

func (b *amdBackend) label() string { return b.name }
func (b *amdBackend) close()        {}

func (b *amdBackend) sample(s *Sample) {
	if v, ok := readU(filepath.Join(b.dev, "gpu_busy_percent")); ok {
		s.UsagePct = clampPct(float64(v))
	}
	s.VramUsed, _ = readU(filepath.Join(b.dev, "mem_info_vram_used"))
	s.VramTotal, _ = readU(filepath.Join(b.dev, "mem_info_vram_total"))
	s.GttUsed, _ = readU(filepath.Join(b.dev, "mem_info_gtt_used"))
	readHwmon(b.hwmon, s)
}
