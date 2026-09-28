package stats

// The part of processor reporting that is the same everywhere.
//
// Both platforms count time the processor spent idle and time it spent in total,
// as numbers that only go up. Neither reports a percentage, because a percentage
// needs two readings — so the arithmetic, and the decision about what to do when
// a counter misbehaves, belongs in one place rather than twice.

// cpuTimes holds one processor's idle and total counters as last seen. The unit
// is whatever the platform counts in — jiffies on Linux, 100-nanosecond ticks on
// Windows — which does not matter, because only ratios of differences are taken.
type cpuTimes struct {
	idle  uint64
	total uint64
}

// cpuSample is one processor's counters from the current tick, carried from the
// lock-free read phase to the locked update phase. core is -1 for the whole-CPU
// figure and the core number otherwise, so nothing has to be turned into a string.
type cpuSample struct {
	core        int
	idle, total uint64
}

// publishCPU turns this tick's counters into usage percentages and writes them.
//
// A core's first sample produces no reading at all rather than a wrong one: with
// nothing to compare against, the only honest answer is to wait for the next
// tick. The same guard covers a counter that has gone backwards, which happens
// across a suspend and on a processor that has been offline.
func (c *Collector) publishCPU(samples []cpuSample, maxFreq, temp float64) {
	c.write(func(s *Stats) {
		for _, sm := range samples {
			prev := &c.cpuPrevAll
			if sm.core >= 0 {
				if sm.core >= len(c.cpuPrevCore) {
					continue
				}
				prev = &c.cpuPrevCore[sm.core]
			}
			usage := 0.0
			if prev.total != 0 && sm.total > prev.total && sm.idle >= prev.idle {
				dTotal := float64(sm.total - prev.total)
				dIdle := float64(sm.idle - prev.idle)
				usage = clamp((1-dIdle/dTotal)*100, 0, 100)
			}
			*prev = cpuTimes{idle: sm.idle, total: sm.total}

			if sm.core < 0 {
				s.CPU.Usage = usage
				if s.CPU.UsageHist != nil {
					s.CPU.UsageHist.Push(usage)
				}
			} else if sm.core < len(s.CPU.Cores) {
				s.CPU.Cores[sm.core].Usage = usage
			}
		}
		s.CPU.CurFreq = maxFreq
		if temp >= 0 {
			s.CPU.Temp = temp
		}
	})
}
