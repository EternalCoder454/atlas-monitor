package stats

import (
	"bufio"
	"os"
	"strings"
)

// initMem allocates the memory ring buffers.
func (c *Collector) initMem() {
	c.write(func(s *Stats) {
		s.Mem.UsageHist = NewRingBuffer()
		s.Mem.SwapHist = NewRingBuffer()
	})
}

// collectMem parses /proc/meminfo. All values are converted to bytes.
func (c *Collector) collectMem() {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	m := make(map[string]uint64, 8)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		// Values in /proc/meminfo are in kB.
		m[key] = atou(fields[1]) * 1024
	}
	f.Close()

	total := m["MemTotal"]
	avail := m["MemAvailable"]
	free := m["MemFree"]
	cached := m["Cached"] + m["SReclaimable"] + m["Buffers"]
	used := uint64(0)
	if total > avail {
		used = total - avail
	}
	swapTotal := m["SwapTotal"]
	swapFree := m["SwapFree"]
	swapUsed := uint64(0)
	if swapTotal > swapFree {
		swapUsed = swapTotal - swapFree
	}

	usagePct := 0.0
	if total > 0 {
		usagePct = float64(used) / float64(total) * 100
	}
	swapPct := 0.0
	if swapTotal > 0 {
		swapPct = float64(swapUsed) / float64(swapTotal) * 100
	}

	c.write(func(s *Stats) {
		s.Mem.Total = total
		s.Mem.Used = used
		s.Mem.Cached = cached
		s.Mem.Available = avail
		s.Mem.Free = free
		s.Mem.SwapTotal = swapTotal
		s.Mem.SwapUsed = swapUsed
		if s.Mem.UsageHist != nil {
			s.Mem.UsageHist.Push(usagePct)
		}
		if s.Mem.SwapHist != nil {
			s.Mem.SwapHist.Push(swapPct)
		}
	})
}
