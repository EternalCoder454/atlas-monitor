package process

import (
	"sort"
	"testing"
	"time"
)

// TestProcessSmoke runs the collector against this machine's /proc and prints
// the top processes by CPU. Run with -v to inspect.
func TestProcessSmoke(t *testing.T) {
	c := New()
	c.Start()
	time.Sleep(2200 * time.Millisecond)
	ps := c.Snapshot()
	c.Stop()

	if len(ps) == 0 {
		t.Fatal("no processes collected")
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].CPU > ps[j].CPU })

	t.Logf("collected %d processes; top by CPU:", len(ps))
	for i := 0; i < 8 && i < len(ps); i++ {
		p := ps[i]
		t.Logf("  %-20s pid=%-7d cpu=%5.1f%% rss=%6.1fMB gpu=%4.1f%% net≈=%5.0f/%-5.0f disk=%.0f/%.0f",
			p.Name, p.PID, p.CPU, float64(p.RSS)/1e6, p.GPU, p.NetIn, p.NetOut, p.DiskRead, p.DiskWrite)
	}

	sort.Slice(ps, func(i, j int) bool { return ps[i].GPU > ps[j].GPU })
	t.Log("top by GPU:")
	for i := 0; i < 6 && i < len(ps) && ps[i].GPU > 0; i++ {
		t.Logf("  %-20s pid=%-7d gpu=%5.1f%%", ps[i].Name, ps[i].PID, ps[i].GPU)
	}
}
