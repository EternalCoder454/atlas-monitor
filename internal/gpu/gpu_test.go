package gpu

import (
	"strings"
	"testing"
	"time"
)

// pciIDsSample mirrors the real database's shape: vendor lines at column 0,
// devices indented one tab, subsystems two.
const pciIDsSample = `# comment line
1002  Advanced Micro Devices, Inc. [AMD/ATI]
	744c  Navi 31 [Radeon RX 7900 XT/7900 XTX/7900M]
		1002 0e3b  Radeon RX 7900 XTX
	73ff  Navi 23 [Radeon RX 6600/6600 XT/6600M]
10de  NVIDIA Corporation
	2684  AD102 [GeForce RTX 4090]
8086  Intel Corporation
	a780  Raptor Lake-S GT1 [UHD Graphics 770]
`

func TestScanPCIIDs(t *testing.T) {
	tests := []struct {
		vendor, device, want string
	}{
		{"1002", "744c", "Navi 31 [Radeon RX 7900 XT/7900 XTX/7900M]"},
		{"1002", "73ff", "Navi 23 [Radeon RX 6600/6600 XT/6600M]"},
		{"10de", "2684", "AD102 [GeForce RTX 4090]"},
		{"8086", "a780", "Raptor Lake-S GT1 [UHD Graphics 770]"},
		{"1002", "ffff", ""}, // unknown device in a known vendor
		{"dead", "744c", ""}, // unknown vendor
	}
	for _, tt := range tests {
		got := scanPCIIDs(strings.NewReader(pciIDsSample), tt.vendor, tt.device)
		if got != tt.want {
			t.Errorf("scanPCIIDs(%s, %s) = %q, want %q", tt.vendor, tt.device, got, tt.want)
		}
	}
}

// TestScanPCIIDsIgnoresSubsystems makes sure a two-tab subsystem line is never
// mistaken for the device name.
func TestScanPCIIDsIgnoresSubsystems(t *testing.T) {
	if got := scanPCIIDs(strings.NewReader(pciIDsSample), "1002", "0e3b"); got != "" {
		t.Errorf("matched a subsystem line: %q", got)
	}
}

func TestVendorLabel(t *testing.T) {
	for id, want := range map[string]string{
		vendorAMD:    "AMD GPU",
		vendorNVIDIA: "NVIDIA GPU",
		vendorIntel:  "Intel GPU",
		"0xbeef":     "GPU",
	} {
		if got := vendorLabel(id); got != want {
			t.Errorf("vendorLabel(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestClampPct(t *testing.T) {
	for in, want := range map[float64]float64{-5: 0, 0: 0, 42: 42, 100: 100, 180: 100} {
		if got := clampPct(in); got != want {
			t.Errorf("clampPct(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestReaderSmoke exercises whichever backend this machine actually has. It is
// informational — a machine with no GPU is a valid outcome — but it does check
// that a detected card produces figures that make sense.
func TestReaderSmoke(t *testing.T) {
	r := NewReader()
	defer r.Close()

	if !r.Available() {
		t.Skip("no GPU detected on this machine")
	}
	t.Logf("backend %T, name %q", r.be, r.Name())

	r.Read() // prime the delta-based counters
	time.Sleep(1100 * time.Millisecond)
	s := r.Read()

	t.Logf("usage=%.0f%% vram=%.2f/%.2f GiB gtt=%.2f GiB temp=%.0f°C fan=%d RPM/%.0f%% power=%.0fW sclk=%.0f mclk=%.0f",
		s.UsagePct, gib(s.VramUsed), gib(s.VramTotal), gib(s.GttUsed),
		s.TempC, s.FanRPM, s.FanPercent, s.PowerW, s.SclkMHz, s.MclkMHz)

	if s.UsagePct < 0 || s.UsagePct > 100 {
		t.Errorf("usage %.1f%% is out of range", s.UsagePct)
	}
	if s.VramTotal > 0 && s.VramUsed > s.VramTotal {
		t.Errorf("VRAM used %d exceeds total %d", s.VramUsed, s.VramTotal)
	}
	if r.Name() == "" {
		t.Error("a detected GPU has no name")
	}
}

func gib(b uint64) float64 { return float64(b) / (1 << 30) }
