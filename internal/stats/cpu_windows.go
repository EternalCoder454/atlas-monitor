package stats

import (
	"atlas-monitor/internal/format"
	"atlas-monitor/internal/winapi"
)

// The processor on Windows.
//
// One system call replaces /proc/stat: NtQuerySystemInformation returns an array
// of per-processor times, in order, which is exactly what the core grid wants.
// There is no aggregate line as /proc/stat has, so the whole-CPU figure is summed
// from the cores here — the arithmetic afterwards is the shared one.
//
// Two things Linux reports are simply not available. There is no general way to
// read a CPU temperature on Windows: the interfaces that exist are per-vendor,
// and the one that is not (WMI's thermal zone) is absent or administrator-only on
// most machines. Atlas already draws the temperature as unavailable when it reads
// below zero, which is what it is left as. Cache associativity and line size are
// read but unused, matching Linux.

// initCPUStatic fills the unchanging CPU fields and allocates the ring buffers.
func (c *Collector) initCPUStatic() {
	topo, err := winapi.ReadTopology()
	if err != nil {
		// Even without the topology there is a processor count to be had, and a
		// core grid with the right number of cells is most of what this is for.
		topo = winapi.Topology{Logical: len(mustCPUTimes())}
	}
	logical := topo.Logical
	if logical <= 0 {
		logical = 1
	}

	baseFreq := 0.0
	if _, max, err := winapi.ReadFrequencies(); err == nil && len(max) > 0 {
		baseFreq = float64(max[0])
	}

	c.cpuPrevCore = make([]cpuTimes, logical)

	cores := make([]CoreStat, logical)
	c.write(func(s *Stats) {
		s.CPU.Model = winapi.ProcessorName()
		s.CPU.Logical = logical
		s.CPU.PhysCores = topo.PhysCores
		s.CPU.Sockets = topo.Sockets
		s.CPU.BaseFreq = baseFreq
		s.CPU.L1d = cacheBytes(topo.L1D)
		s.CPU.L1i = cacheBytes(topo.L1I)
		s.CPU.L2 = cacheBytes(topo.L2)
		s.CPU.L3 = cacheBytes(topo.L3)
		s.CPU.Cores = cores
		s.CPU.UsageHist = NewRingBuffer()
		// Nothing reports this, and zero would read as a cold machine.
		s.CPU.Temp = -1
	})
}

// cacheBytes formats a cache size, or "" when it was not reported — which is what
// the UI hides the row for.
func cacheBytes(n uint64) string {
	if n == 0 {
		return ""
	}
	return format.Bytes(n)
}

// mustCPUTimes is ReadCPUTimes with the error dropped, for the startup path where
// the only thing wanted is how many entries come back.
func mustCPUTimes() []winapi.CPUTimes {
	t, err := winapi.ReadCPUTimes()
	if err != nil {
		return nil
	}
	return t
}

// closeCPU has nothing to release: Windows answers through system calls rather
// than files held open.
func (c *Collector) closeCPU() {}

// collectCPU samples the per-processor times and the current clock.
func (c *Collector) collectCPU() {
	times, err := winapi.ReadCPUTimes()
	if err != nil {
		return
	}

	c.cpuSamples = c.cpuSamples[:0]
	// The whole-CPU figure first, summed across the cores, so that it lands in
	// the same slice the shared code walks.
	var allIdle, allTotal uint64
	for _, t := range times {
		allIdle += t.Idle
		allTotal += t.Total
	}
	c.cpuSamples = append(c.cpuSamples, cpuSample{core: -1, idle: allIdle, total: allTotal})
	for i, t := range times {
		c.cpuSamples = append(c.cpuSamples, cpuSample{core: i, idle: t.Idle, total: t.Total})
	}

	// The fastest core, matching what Linux shows: on a machine that boosts, the
	// interesting number is the one a core actually reached.
	maxFreq := 0.0
	if current, _, err := winapi.ReadFrequencies(); err == nil {
		for _, mhz := range current {
			if f := float64(mhz); f > maxFreq {
				maxFreq = f
			}
		}
	}

	// -1 keeps the temperature marked unavailable rather than overwriting it.
	c.publishCPU(c.cpuSamples, maxFreq, -1)
}
