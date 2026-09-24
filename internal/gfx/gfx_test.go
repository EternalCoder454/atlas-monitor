package gfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This package is the largest single lever on Atlas's resident set — the
// difference between loading Mesa and LLVM or not — and the logic that picks
// which Vulkan ICDs the loader may open is the part that has to be right. Get it
// wrong in one direction and lavapipe drags LLVM in anyway; wrong in the other
// and the window comes up on a renderer that cannot work.

func TestNormalize(t *testing.T) {
	for _, mode := range []string{ModeSoftware, ModeGPU, ModeSystem} {
		if got := Normalize(mode); got != mode {
			t.Errorf("Normalize(%q) = %q, want it unchanged", mode, got)
		}
	}
	for _, bad := range []string{"", "opengl", "GPU", "vulkan", " software", "SOFTWARE"} {
		if got := Normalize(bad); got != ModeSoftware {
			t.Errorf("Normalize(%q) = %q, want the %q default", bad, got, ModeSoftware)
		}
	}
}

// TestModesListedForSettings checks every selectable mode survives Normalize and
// carries the text the Settings dialog renders.
func TestModesListedForSettings(t *testing.T) {
	if len(Modes) == 0 {
		t.Fatal("no rendering modes listed")
	}
	seen := map[string]bool{}
	for _, m := range Modes {
		if Normalize(m.Value) != m.Value {
			t.Errorf("mode %q in the Settings list is not a value Normalize accepts", m.Value)
		}
		if m.Label == "" || m.Detail == "" {
			t.Errorf("mode %q has an empty label or detail", m.Value)
		}
		if seen[m.Value] {
			t.Errorf("mode %q listed twice", m.Value)
		}
		seen[m.Value] = true
	}
	for _, want := range []string{ModeSoftware, ModeGPU, ModeSystem} {
		if !seen[want] {
			t.Errorf("mode %q is selectable but not listed in Settings", want)
		}
	}
	if Modes[0].Value != ModeSoftware {
		t.Errorf("Settings lists %q first; the memory-saving default should lead", Modes[0].Value)
	}
}

// TestApplySoftwareDisablesTheGPUStack is the memory guarantee: software mode has
// to tell GDK not to bring up GL or Vulkan at all, because merely choosing the
// Cairo renderer does not stop the ICD scan.
func TestApplySoftwareDisablesTheGPUStack(t *testing.T) {
	clearGFXEnv(t)
	Apply(ModeSoftware)
	if got := os.Getenv("GSK_RENDERER"); got != "cairo" {
		t.Errorf("GSK_RENDERER = %q, want cairo", got)
	}
	if got := os.Getenv("GDK_DISABLE"); !strings.Contains(got, "gl") || !strings.Contains(got, "vulkan") {
		t.Errorf("GDK_DISABLE = %q, want both gl and vulkan disabled", got)
	}
}

// TestApplyNeverOverridesTheEnvironment checks the documented promise that
// someone who exports GSK_RENDERER themselves keeps control.
func TestApplyNeverOverridesTheEnvironment(t *testing.T) {
	for _, mode := range []string{ModeSoftware, ModeGPU, ModeSystem} {
		clearGFXEnv(t)
		t.Setenv("GSK_RENDERER", "ngl")
		t.Setenv("GDK_DISABLE", "vulkan")
		Apply(mode)
		if got := os.Getenv("GSK_RENDERER"); got != "ngl" {
			t.Errorf("mode %q overrode GSK_RENDERER: %q", mode, got)
		}
		if got := os.Getenv("GDK_DISABLE"); got != "vulkan" {
			t.Errorf("mode %q overrode GDK_DISABLE: %q", mode, got)
		}
	}
}

// TestApplySystemChangesNothing checks the escape hatch really is one.
func TestApplySystemChangesNothing(t *testing.T) {
	clearGFXEnv(t)
	Apply(ModeSystem)
	for _, key := range []string{"GSK_RENDERER", "GDK_DISABLE", "VK_DRIVER_FILES", "VK_ICD_FILENAMES"} {
		if v, ok := os.LookupEnv(key); ok {
			t.Errorf("system mode set %s=%q; it should change nothing", key, v)
		}
	}
}

// TestApplyGPUPinsTheHardwareICD builds a synthetic machine with an AMD card and
// a full set of installed manifests, and checks the loader is pointed at the
// AMD one and nothing else. lavapipe and dzn are the two that matter: the first
// pulls in LLVM, the second is a Direct3D translation layer that can never run
// here.
func TestApplyGPUPinsTheHardwareICD(t *testing.T) {
	root := t.TempDir()
	icdDir := filepath.Join(root, "icd.d")
	writeManifests(t, icdDir,
		"radeon_icd.x86_64.json",
		"lvp_icd.x86_64.json", // lavapipe
		"dzn_icd.x86_64.json", // Direct3D translation
		"intel_icd.x86_64.json",
		"nouveau_icd.x86_64.json",
	)
	withFakeMachine(t, root, icdDir, map[string]string{"card0": "0x1002"})

	clearGFXEnv(t)
	Apply(ModeGPU)

	got := os.Getenv("VK_DRIVER_FILES")
	if got == "" {
		t.Fatal("VK_DRIVER_FILES not set; the loader would scan every ICD")
	}
	// The old variable has to be set too, for loaders before 1.3.207.
	if os.Getenv("VK_ICD_FILENAMES") != got {
		t.Errorf("VK_ICD_FILENAMES = %q, want the same list as VK_DRIVER_FILES", os.Getenv("VK_ICD_FILENAMES"))
	}
	if !strings.Contains(got, "radeon") {
		t.Errorf("VK_DRIVER_FILES = %q, missing the AMD manifest", got)
	}
	for _, unwanted := range []string{"lvp", "dzn", "intel", "nouveau"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("VK_DRIVER_FILES = %q, includes %q which cannot drive this card", got, unwanted)
		}
	}
	if got := os.Getenv("GSK_RENDERER"); got != "vulkan" {
		t.Errorf("GSK_RENDERER = %q, want vulkan", got)
	}
	// GL is disabled in GPU mode, Vulkan is not.
	if d := os.Getenv("GDK_DISABLE"); !strings.Contains(d, "gl") || strings.Contains(d, "vulkan") {
		t.Errorf("GDK_DISABLE = %q, want gl disabled and vulkan left on", d)
	}
}

// TestApplyGPUWithUnknownHardwareLeavesTheLoaderAlone covers the documented
// fallback: pinning the loader to a guess would risk the window not coming up at
// all, so an unrecognised vendor means changing nothing.
func TestApplyGPUWithUnknownHardwareLeavesTheLoaderAlone(t *testing.T) {
	root := t.TempDir()
	icdDir := filepath.Join(root, "icd.d")
	writeManifests(t, icdDir, "radeon_icd.x86_64.json", "lvp_icd.x86_64.json")

	for _, machine := range []map[string]string{
		{"card0": "0xbeef"}, // a vendor not in the table
		{},                  // no cards at all
	} {
		withFakeMachine(t, root, icdDir, machine)
		clearGFXEnv(t)
		Apply(ModeGPU)
		for _, key := range []string{"VK_DRIVER_FILES", "VK_ICD_FILENAMES", "GSK_RENDERER", "GDK_DISABLE"} {
			if v, ok := os.LookupEnv(key); ok {
				t.Errorf("machine %v: set %s=%q; unidentifiable hardware should change nothing", machine, key, v)
			}
		}
	}
}

// TestApplyGPUWithNoManifestsInstalled covers a machine whose vendor is known
// but which has no Vulkan drivers installed at all.
func TestApplyGPUWithNoManifestsInstalled(t *testing.T) {
	root := t.TempDir()
	icdDir := filepath.Join(root, "icd.d")
	if err := os.MkdirAll(icdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	withFakeMachine(t, root, icdDir, map[string]string{"card0": "0x1002"})
	clearGFXEnv(t)
	Apply(ModeGPU)
	if v, ok := os.LookupEnv("VK_DRIVER_FILES"); ok {
		t.Errorf("VK_DRIVER_FILES = %q with no manifests installed", v)
	}
	if v, ok := os.LookupEnv("GSK_RENDERER"); ok {
		t.Errorf("GSK_RENDERER = %q; with no Vulkan driver GTK's default should stand", v)
	}
}

// TestMultipleGPUsSelectBothVendors covers a hybrid laptop: an Intel iGPU and a
// discrete NVIDIA card both need their manifests kept.
func TestMultipleGPUsSelectBothVendors(t *testing.T) {
	root := t.TempDir()
	icdDir := filepath.Join(root, "icd.d")
	writeManifests(t, icdDir,
		"intel_icd.x86_64.json",
		"nvidia_icd.json",
		"nouveau_icd.x86_64.json",
		"lvp_icd.x86_64.json",
		"radeon_icd.x86_64.json",
	)
	withFakeMachine(t, root, icdDir, map[string]string{"card0": "0x8086", "card1": "0x10de"})

	clearGFXEnv(t)
	Apply(ModeGPU)
	got := os.Getenv("VK_DRIVER_FILES")
	for _, want := range []string{"intel", "nvidia", "nouveau"} {
		if !strings.Contains(got, want) {
			t.Errorf("VK_DRIVER_FILES = %q, missing %q", got, want)
		}
	}
	for _, unwanted := range []string{"lvp", "radeon"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("VK_DRIVER_FILES = %q, includes %q for hardware that is not present", got, unwanted)
		}
	}
	// The list is separated the way the loader expects.
	if n := len(strings.Split(got, string(os.PathListSeparator))); n != 3 {
		t.Errorf("VK_DRIVER_FILES has %d entries, want 3: %q", n, got)
	}
}

// TestDuplicateCardsListEachManifestOnce covers two identical cards, which is
// what a real dual-GPU machine reports, plus the render nodes beside them.
func TestDuplicateCardsListEachManifestOnce(t *testing.T) {
	root := t.TempDir()
	icdDir := filepath.Join(root, "icd.d")
	writeManifests(t, icdDir, "radeon_icd.x86_64.json", "amd_icd.json")
	withFakeMachine(t, root, icdDir, map[string]string{
		"card0": "0x1002", "card1": "0x1002", "card2": "0x1002",
	})

	clearGFXEnv(t)
	Apply(ModeGPU)
	got := os.Getenv("VK_DRIVER_FILES")
	for _, m := range []string{"radeon", "amd"} {
		if n := strings.Count(got, m+"_icd"); n != 1 {
			t.Errorf("manifest %q appears %d times in %q, want once", m, n, got)
		}
	}
}

// TestVendorTableIsWellFormed checks the vendor IDs are in the lower-case 0x
// form sysfs writes, since the lookup is a plain map hit on the trimmed value.
func TestVendorTableIsWellFormed(t *testing.T) {
	if len(vendorICDs) == 0 {
		t.Fatal("vendor table is empty")
	}
	for id, frags := range vendorICDs {
		if !strings.HasPrefix(id, "0x") {
			t.Errorf("vendor %q is not in 0x form", id)
		}
		if id != strings.ToLower(id) {
			t.Errorf("vendor %q is not lower-case; sysfs values are compared after ToLower", id)
		}
		if len(frags) == 0 {
			t.Errorf("vendor %q maps to no manifest fragments", id)
		}
		for _, f := range frags {
			if f != strings.ToLower(f) {
				t.Errorf("vendor %q fragment %q is not lower-case; manifest names are lower-cased before matching", id, f)
			}
		}
	}
}

// TestVendorIDWithTrailingNewline covers the real shape of a sysfs read: the
// file ends in a newline, and the value must still match the table.
func TestVendorIDWithTrailingNewline(t *testing.T) {
	root := t.TempDir()
	icdDir := filepath.Join(root, "icd.d")
	writeManifests(t, icdDir, "radeon_icd.x86_64.json")
	dir := filepath.Join(root, "drm", "card0", "device")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Upper case with surrounding whitespace: the read has to cope with both.
	if err := os.WriteFile(filepath.Join(dir, "vendor"), []byte("  0X1002  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	swapGlob(t, filepath.Join(root, "drm", "card*", "device", "vendor"))
	swapICDDirs(t, icdDir)

	clearGFXEnv(t)
	Apply(ModeGPU)
	if got := os.Getenv("VK_DRIVER_FILES"); !strings.Contains(got, "radeon") {
		t.Errorf("VK_DRIVER_FILES = %q; a vendor ID with whitespace and upper case should still match", got)
	}
}

// ---------------------------------------------------------------- helpers

// clearGFXEnv removes every variable Apply touches for the duration of the test.
func clearGFXEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"GSK_RENDERER", "GDK_DISABLE", "VK_DRIVER_FILES", "VK_ICD_FILENAMES"} {
		t.Setenv(key, "") // register the restore
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

// writeManifests creates empty ICD manifest files with the given names.
func writeManifests(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// withFakeMachine points the package at a synthetic /sys/class/drm and ICD
// directory describing the given cards (name -> PCI vendor ID).
func withFakeMachine(t *testing.T, root, icdDir string, cards map[string]string) {
	t.Helper()
	for name, vendor := range cards {
		dir := filepath.Join(root, "drm", name, "device")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "vendor"), []byte(vendor+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	swapGlob(t, filepath.Join(root, "drm", "card*", "device", "vendor"))
	swapICDDirs(t, icdDir)
}

func swapGlob(t *testing.T, pattern string) {
	t.Helper()
	old := drmVendorGlob
	drmVendorGlob = pattern
	t.Cleanup(func() { drmVendorGlob = old })
}

func swapICDDirs(t *testing.T, dirs ...string) {
	t.Helper()
	old := icdDirs
	icdDirs = dirs
	t.Cleanup(func() { icdDirs = old })
}
