package gpu

import (
	"os"
	"path/filepath"

	"atlas-monitor/internal/sysfs"
)

// amdBackend reads an AMD card straight from sysfs. amdgpu exposes everything
// we want as plain files, so this costs a handful of small reads per second and
// needs no scanning.
type amdBackend struct {
	name string

	busy      *sysfs.File
	vramUsed  *sysfs.File
	vramTotal *sysfs.File
	gttUsed   *sysfs.File
	hw        hwmon
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
			name:      gpuName(c),
			busy:      sysfs.Open(filepath.Join(c.dev, "gpu_busy_percent")),
			vramUsed:  sysfs.Open(filepath.Join(c.dev, "mem_info_vram_used")),
			vramTotal: sysfs.Open(filepath.Join(c.dev, "mem_info_vram_total")),
			gttUsed:   sysfs.Open(filepath.Join(c.dev, "mem_info_gtt_used")),
			hw:        openHwmon(hwmonDir(c.dev, "amdgpu")),
		}
	}
	return nil
}

func (b *amdBackend) label() string { return b.name }

func (b *amdBackend) close() {
	b.busy.Close()
	b.vramUsed.Close()
	b.vramTotal.Close()
	b.gttUsed.Close()
	b.hw.close()
}

func (b *amdBackend) sample(s *Sample) {
	if v, ok := b.busy.Uint(); ok {
		s.UsagePct = clampPct(float64(v))
	}
	s.VramUsed, _ = b.vramUsed.Uint()
	s.VramTotal, _ = b.vramTotal.Uint()
	s.GttUsed, _ = b.gttUsed.Uint()
	b.hw.read(s)
}
