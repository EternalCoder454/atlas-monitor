package stats

import (
	"runtime"
	"testing"
	"time"

	"atlas-monitor/internal/gpu"
)

// TestCollectorSmoke runs the real collectors against this machine's /proc and
// /sys for a few samples and sanity-checks the results. Run with -v to inspect.
func TestCollectorSmoke(t *testing.T) {
	c := New(gpu.NewReader())
	c.Start()
	defer c.Stop()

	// Wait for at least two samples so byte-rates are populated.
	time.Sleep(2300 * time.Millisecond)

	c.Read(func(s *Stats) {
		if s.CPU.Logical != runtime.NumCPU() {
			t.Errorf("CPU.Logical = %d, want %d", s.CPU.Logical, runtime.NumCPU())
		}
		if s.Mem.Total == 0 {
			t.Error("Mem.Total is 0")
		}
		if len(s.Disks) == 0 {
			t.Error("no disks discovered")
		}
		if len(s.Nets) == 0 {
			t.Error("no interfaces discovered")
		}

		t.Logf("CPU: %.1f%%  cur=%.0fMHz base=%.0fMHz temp=%.1f°C  %dx%d/%d  L1d=%s L2=%s L3=%s",
			s.CPU.Usage, s.CPU.CurFreq, s.CPU.BaseFreq, s.CPU.Temp,
			s.CPU.Sockets, s.CPU.PhysCores, s.CPU.Logical, s.CPU.L1d, s.CPU.L2, s.CPU.L3)
		t.Logf("MEM: used=%.2fGB / %.2fGB cached=%.2fGB avail=%.2fGB swap=%.2f/%.2fGB",
			gb(s.Mem.Used), gb(s.Mem.Total), gb(s.Mem.Cached), gb(s.Mem.Available),
			gb(s.Mem.SwapUsed), gb(s.Mem.SwapTotal))
		for _, d := range s.Disks {
			t.Logf("DISK %-10s size=%.1fGB used=%.1fGB free=%.1fGB  R=%.0fB/s W=%.0fB/s",
				d.Name, gb(d.SizeBytes), gb(d.Used), gb(d.Free), d.ReadRate, d.WriteRate)
		}
		for _, n := range s.Nets {
			t.Logf("NET  %-14s ipv4=%-15s speed=%dMbit  Rx=%.0fB/s Tx=%.0fB/s  total=%.1f/%.1fMB",
				n.Name, n.IPv4, n.SpeedMbit, n.RxRate, n.TxRate,
				float64(n.RxTotal)/1e6, float64(n.TxTotal)/1e6)
		}
		if s.GPU.Available {
			t.Logf("GPU  %s  usage=%.0f%% vram=%.2f/%.2fGB gtt=%.2fGB temp=%.0f°C fan=%d power=%.0fW sclk=%.0f mclk=%.0f",
				s.GPU.Name, s.GPU.Usage, gb(s.GPU.VramUsed), gb(s.GPU.VramTotal),
				gb(s.GPU.GttUsed), s.GPU.Temp, s.GPU.FanRPM, s.GPU.PowerW,
				s.GPU.GpuClockMHz, s.GPU.MemClockMHz)
		} else {
			t.Log("GPU  not detected")
		}
	})
}

func gb(b uint64) float64 { return float64(b) / (1 << 30) }
