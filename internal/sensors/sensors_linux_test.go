package sensors

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeHwmon builds a hwmon tree: each device is a directory of files, and a
// device link to a directory named devName.
func fakeHwmon(t *testing.T, devices map[string]map[string]string, devNames map[string]string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "class", "hwmon")
	t.Cleanup(func() { root = "/sys/class/hwmon" })
	for hw, files := range devices {
		dir := filepath.Join(root, hw)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if dn, ok := devNames[hw]; ok {
			target := filepath.Join(base, "devices", dn)
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if model, ok := files["model"]; ok {
				os.WriteFile(filepath.Join(target, "model"), []byte(model+"\n"), 0o644)
			}
			if err := os.Symlink(target, filepath.Join(dir, "device")); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDiscoverFakeTree(t *testing.T) {
	fakeHwmon(t, map[string]map[string]string{
		"hwmon0": {"name": "nvme", "model": "Samsung SSD 970 EVO Plus 1TB",
			"temp1_input": "43850", "temp1_label": "Composite", "temp1_crit": "84850",
			"temp2_input": "39850", "temp2_label": "Sensor 1"},
		"hwmon1": {"name": "spd5118", "temp1_input": "38000", "temp1_max": "55000", "temp1_crit": "85000"},
		"hwmon2": {"name": "spd5118", "temp1_input": "37750"},
		"hwmon3": {"name": "coretemp",
			"temp1_input": "38000", "temp1_label": "Package id 0", "temp1_max": "80000", "temp1_crit": "100000",
			"temp2_input": "35000", "temp2_label": "Core 0",
			"temp3_input": "36000", "temp3_label": "Core 1",
			"temp10_input": "37000", "temp10_label": "Core 8",
			"temp11_input": "39000", "temp11_label": "Core 9",
			"temp12_input": "-5000", "temp12_label": "Core 10"},
		"hwmon4": {"name": "amdgpu", "temp1_input": "58000", "temp1_label": "junction",
			"fan1_input": "546", "in0_input": "692", "in0_label": "vddgfx",
			"power1_average": "80000000", "power1_label": "PPT"},
		"hwmon5": {"name": "mystery_chip", "temp1_input": "30000", "temp2_input": "31000"},
		"hwmon6": {"name": "empty"},
	}, map[string]string{
		"hwmon0": "pci/nvme/nvme1",
		"hwmon1": "i2c-10/10-0051",
		"hwmon2": "i2c-10/10-0053",
	})

	devs := Discover()
	defer Close(devs)
	var names []string
	for _, d := range devs {
		names = append(names, d.Name)
	}
	want := []string{"Processor", "Graphics card", "Memory module 2", "Memory module 4",
		"Samsung SSD 970 EVO Plus 1TB", "mystery_chip"}
	if len(names) != len(want) {
		t.Fatalf("devices = %q, want %q", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("devices = %q, want %q", names, want)
		}
	}

	cpu := devs[0]
	var labels []string
	for _, r := range cpu.Readings {
		labels = append(labels, r.Label)
	}
	// Numeric order: temp10 after temp3, not after temp1.
	if got := labels; len(got) != 6 || got[0] != "Package" || got[1] != "Core 0" || got[3] != "Core 8" || got[5] != "Core 10" {
		t.Errorf("processor readings = %q", got)
	}
	if !cpu.Readings[1].Core || cpu.Readings[0].Core {
		t.Error("five per-core readings should fold away, and the package should not")
	}
	if r := cpu.Readings[5]; !r.OK || r.Value != -5 {
		t.Errorf("a temperature below zero read as %v, ok %v", r.Value, r.OK)
	}

	gpu := devs[1]
	got := map[string]string{}
	for _, r := range gpu.Readings {
		got[r.Label] = r.Format()
	}
	for label, value := range map[string]string{"Hotspot": "58 °C", "Fan": "546 RPM", "Core voltage": "0.692 V", "Power draw": "80.0 W"} {
		if got[label] != value {
			t.Errorf("graphics %s = %q, want %q (all: %v)", label, got[label], value, got)
		}
	}

	if r := devs[2].Readings[0]; r.High != 55 || r.Critical != 85 {
		t.Errorf("memory module thresholds = %v / %v", r.High, r.Critical)
	}
	if rs := devs[5].Readings; len(rs) != 2 || rs[0].Label != "Temperature 1" || rs[1].Label != "Temperature 2" {
		t.Errorf("unlabelled sensors = %+v; want them numbered", rs)
	}
}

func TestWarmth(t *testing.T) {
	r := Reading{Kind: Temperature, OK: true, High: 80, Critical: 100}
	for v, want := range map[float64]int{50: 0, 80: 1, 99: 1, 100: 2} {
		r.Value = v
		if got := r.Warmth(); got != want {
			t.Errorf("Warmth at %v = %d, want %d", v, got, want)
		}
	}
	none := Reading{Kind: Temperature, OK: true, Value: 120}
	if none.Warmth() != 0 {
		t.Error("a sensor with no thresholds was graded")
	}
	fan := Reading{Kind: Fan, OK: true, Value: 5000, High: 1}
	if fan.Warmth() != 0 {
		t.Error("a fan was graded as a temperature")
	}
}
