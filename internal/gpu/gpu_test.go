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

// TestDRMBackendDirect exercises the generic backend even on a machine where
// the AMD or NVIDIA backend would win. It is the path Intel and nouveau users
// get, and it is the only one that has to walk /proc to find GPU clients, so it
// is worth running wherever there is a DRM card at all.
func TestDRMBackendDirect(t *testing.T) {
	be := newDRMBackend()
	if be == nil {
		t.Skip("no DRM card on this machine")
	}
	defer be.close()

	if be.label() == "" {
		t.Error("the backend produced no name")
	}
	t.Logf("generic DRM backend picked %q", be.label())

	// Two samples: the first primes the engine counters, the second measures.
	var first, second Sample
	be.sample(&first)
	time.Sleep(1100 * time.Millisecond)
	be.sample(&second)

	t.Logf("usage=%.1f%% vram=%.2f/%.2f GiB temp=%.0f°C fan=%d power=%.1fW sclk=%.0f",
		second.UsagePct, gib(second.VramUsed), gib(second.VramTotal),
		second.TempC, second.FanRPM, second.PowerW, second.SclkMHz)

	if second.UsagePct < 0 || second.UsagePct > 100 {
		t.Errorf("usage %.1f%% is out of range", second.UsagePct)
	}
	if second.VramTotal > 0 && second.VramUsed > second.VramTotal {
		t.Errorf("VRAM used %d exceeds total %d", second.VramUsed, second.VramTotal)
	}
	// The first sample has no previous counters to difference against, so it
	// must report zero rather than a spike from the boot-time totals.
	if first.UsagePct != 0 {
		t.Errorf("the priming sample reported %.1f%% usage, want 0", first.UsagePct)
	}

	drm, ok := be.(*drmBackend)
	if !ok {
		t.Fatalf("unexpected backend type %T", be)
	}
	t.Logf("found %d GPU clients, %d engines", len(drm.clients), len(drm.prevEngine))
	if len(drm.clients) == 0 {
		t.Log("no processes hold a /dev/dri handle — nothing was rendering")
	}
}
