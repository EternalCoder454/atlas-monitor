// Package gfx decides how Atlas draws its window and trims the graphics stack
// GTK loads to do it.
//
// This is the single largest lever on the app's resident set. GTK's default
// GPU renderers pull in the whole Mesa stack: on an AMD box that is
// libgallium plus libLLVM, and the Vulkan loader additionally dlopens *every*
// installed ICD — including lavapipe, which drags in another copy of LLVM's
// code paths, and dzn, which is a Direct3D translation layer that can never be
// used on Linux. Together they account for roughly 60 MiB of resident memory in
// a process whose own working set is a few megabytes of numbers.
package gfx

import (
	"os"
	"path/filepath"
	"strings"
)

// Rendering modes, as stored in settings.
const (
	// ModeSoftware draws with Cairo on the CPU and tells GDK not to initialise
	// GL or Vulkan at all. No Mesa, no LLVM, no ICD scan. This is the default:
	// a system monitor redraws a few charts once a second, which software
	// rendering handles without breaking a sweat.
	ModeSoftware = "software"

	// ModeGPU uses the Vulkan renderer, restricted to the ICD of the GPU that is
	// actually installed. Smoother window resizing on high-refresh displays, at
	// the cost of the driver's memory.
	ModeGPU = "gpu"

	// ModeSystem changes nothing, leaving GTK's own defaults (and any GSK_/GDK_
	// variables in the environment) in charge.
	ModeSystem = "system"
)

// Modes lists the selectable rendering modes with their labels, in the order
// the Settings dialog shows them.
var Modes = []struct{ Value, Label, Detail string }{
	{ModeSoftware, "Software (lowest memory)", "Cairo. Skips the GPU driver stack entirely — around 60 MiB lighter."},
	{ModeGPU, "GPU", "Vulkan, limited to your GPU's driver. Smoother resizing, more memory."},
	{ModeSystem, "System default", "Whatever GTK picks. Use this if something looks wrong."},
}

// Normalize maps an unknown or empty mode onto the default.
func Normalize(mode string) string {
	switch mode {
	case ModeSoftware, ModeGPU, ModeSystem:
		return mode
	default:
		return ModeSoftware
	}
}

// Apply sets the GDK/GSK environment for mode. It must be called before GTK is
// initialised, and never overrides a variable that is already set — someone who
// exports GSK_RENDERER themselves keeps control.
func Apply(mode string) {
	switch Normalize(mode) {
	case ModeSoftware:
		setenv("GSK_RENDERER", "cairo")
		setenv("GDK_DISABLE", "gl,vulkan")
	case ModeGPU:
		icds := hardwareICDs()
		if len(icds) == 0 {
			// No identifiable hardware ICD: leave GTK's own defaults alone
			// rather than pinning it to a renderer that may not come up.
			return
		}
		list := strings.Join(icds, string(os.PathListSeparator))
		setenv("VK_DRIVER_FILES", list)  // current loader
		setenv("VK_ICD_FILENAMES", list) // pre-1.3.207 loader
		setenv("GSK_RENDERER", "vulkan")
		setenv("GDK_DISABLE", "gl")
	case ModeSystem:
	}
}

// setenv sets key unless the environment already carries a value for it.
func setenv(key, value string) {
	if _, ok := os.LookupEnv(key); ok {
		return
	}
	_ = os.Setenv(key, value)
}

// icdDirs are the standard Vulkan ICD manifest directories.
var icdDirs = []string{
	"/etc/vulkan/icd.d",
	"/usr/local/etc/vulkan/icd.d",
	"/usr/local/share/vulkan/icd.d",
	"/usr/share/vulkan/icd.d",
}

// vendorICDs maps a PCI vendor ID to the ICD manifest name fragments that can
// drive that vendor's hardware.
var vendorICDs = map[string][]string{
	"0x1002": {"radeon", "amd"},     // AMD / ATI
	"0x8086": {"intel"},             // Intel
	"0x10de": {"nouveau", "nvidia"}, // NVIDIA (open and proprietary)
	"0x1af4": {"virtio", "lvp"},     // virtio-gpu (VM)
	"0x1b36": {"virtio", "lvp"},     // QXL / QEMU
	"0x1ed5": {"moore"},             // Moore Threads
	"0x126f": {"powervr"},           // Imagination
}

// hardwareICDs returns the Vulkan manifests matching the GPUs this machine
// actually has. Restricting the loader to those stops it dlopening lavapipe
// (and its LLVM shader compiler) and the Direct3D translation layer, neither of
// which will ever render this window.
//
// It returns nil when the hardware can't be identified, in which case the
// caller leaves the loader alone rather than guessing.
func hardwareICDs() []string {
	vendors := drmVendors()
	if len(vendors) == 0 {
		return nil
	}
	var want []string
	for _, v := range vendors {
		want = append(want, vendorICDs[v]...)
	}
	if len(want) == 0 {
		return nil
	}

	var out []string
	seen := map[string]bool{}
	for _, dir := range icdDirs {
		manifests, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, m := range manifests {
			base := strings.ToLower(filepath.Base(m))
			for _, w := range want {
				if strings.Contains(base, w) && !seen[base] {
					seen[base] = true
					out = append(out, m)
					break
				}
			}
		}
	}
	return out
}

// drmVendorGlob locates the DRM cards' PCI vendor IDs. A variable so the tests
// can point it at a synthetic tree — this machine's real hardware would only
// ever exercise one branch of the ICD selection.
var drmVendorGlob = "/sys/class/drm/card*/device/vendor"

// drmVendors lists the PCI vendor IDs of the DRM cards present.
func drmVendors() []string {
	files, _ := filepath.Glob(drmVendorGlob)
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(string(b)))
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
