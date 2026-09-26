package stats

import "atlas-monitor/internal/winapi"

// initMem allocates the memory ring buffers. There is no file to hold open:
// Windows answers this through a system call rather than a pseudo-file.
func (c *Collector) initMem() { c.initMemHist() }

// collectMem samples physical memory and the page file.
//
// "Swap" here is the page file, and unlike Linux it is an estimate rather than a
// reading — see winapi.ReadMemory, which explains what it is derived from. It is
// reported as a used figure directly, because Windows has no equivalent of
// SwapFree to subtract.
func (c *Collector) collectMem() {
	m, err := winapi.ReadMemory()
	if err != nil {
		return
	}
	c.publishMem(memSample{
		Total:     m.TotalPhys,
		Available: m.AvailPhys,
		// Windows draws no distinction between free and available, so the same
		// figure serves for both rather than inventing one.
		Free:           m.AvailPhys,
		Cached:         m.Cached,
		SwapTotal:      m.PageFileTotal,
		SwapUsedDirect: m.PageFileUsed,
		HaveSwapUsed:   true,
	})
}
