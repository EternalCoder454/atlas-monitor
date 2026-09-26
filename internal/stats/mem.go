package stats

// The part of memory reporting that is the same everywhere.
//
// What the two platforms disagree about is only where the numbers come from:
// /proc/meminfo in kilobytes, or GlobalMemoryStatusEx and the commit charge in
// bytes. Once they are bytes, deciding what counts as used and turning that into
// the percentages the charts plot is one piece of arithmetic, and it lives here
// so the two cannot drift apart.

// memSample is one reading, in bytes, as either platform reports it.
//
// Free and Available are both here because Linux distinguishes them — Free is
// memory nobody has touched, Available is what a new program could get without
// swapping, which is the larger and more useful figure. Windows reports one
// number for both.
type memSample struct {
	Total     uint64
	Available uint64
	Free      uint64
	Cached    uint64
	SwapTotal uint64
	SwapFree  uint64

	// SwapUsedDirect is set by a platform that measures swap use rather than
	// deriving it from what is free. Windows estimates the page file in use and
	// has no meaningful "free" figure to subtract from.
	SwapUsedDirect uint64
	HaveSwapUsed   bool
}

// initMemHist allocates the ring buffers the memory charts draw from.
func (c *Collector) initMemHist() {
	c.write(func(s *Stats) {
		s.Mem.UsageHist = NewRingBuffer()
		s.Mem.SwapHist = NewRingBuffer()
	})
}

// publishMem turns a sample into what the UI reads.
//
// Used is total minus *available*, not minus free. Page cache is memory the
// kernel will hand back the moment something wants it, so counting it as used
// would show a healthy machine as nearly full — which is the complaint every
// system monitor that gets this wrong receives.
func (c *Collector) publishMem(m memSample) {
	used := uint64(0)
	if m.Total > m.Available {
		used = m.Total - m.Available
	}

	swapUsed := m.SwapUsedDirect
	if !m.HaveSwapUsed {
		if m.SwapTotal > m.SwapFree {
			swapUsed = m.SwapTotal - m.SwapFree
		} else {
			swapUsed = 0
		}
	}

	usagePct := 0.0
	if m.Total > 0 {
		usagePct = float64(used) / float64(m.Total) * 100
	}
	swapPct := 0.0
	if m.SwapTotal > 0 {
		swapPct = float64(swapUsed) / float64(m.SwapTotal) * 100
	}

	c.write(func(s *Stats) {
		s.Mem.Total = m.Total
		s.Mem.Used = used
		s.Mem.Cached = m.Cached
		s.Mem.Available = m.Available
		s.Mem.Free = m.Free
		s.Mem.SwapTotal = m.SwapTotal
		s.Mem.SwapUsed = swapUsed
		if s.Mem.UsageHist != nil {
			s.Mem.UsageHist.Push(usagePct)
		}
		if s.Mem.SwapHist != nil {
			s.Mem.SwapHist.Push(swapPct)
		}
	})
}
