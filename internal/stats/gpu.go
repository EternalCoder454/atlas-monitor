package stats

// initGPU prepares GPU stats if an AMD GPU was detected by the gpu.Reader.
func (c *Collector) initGPU() {
	if c.gpu == nil || !c.gpu.Available() {
		return
	}
	c.write(func(s *Stats) {
		s.GPU.Available = true
		s.GPU.Name = c.gpu.Name()
		s.GPU.UsageHist = NewRingBuffer()
		s.GPU.VramHist = NewRingBuffer()
	})
}

// collectGPU samples the AMD GPU via sysfs.
func (c *Collector) collectGPU() {
	sample := c.gpu.Read()

	vramPct := 0.0
	if sample.VramTotal > 0 {
		vramPct = float64(sample.VramUsed) / float64(sample.VramTotal) * 100
	}

	c.write(func(s *Stats) {
		s.GPU.Usage = sample.UsagePct
		s.GPU.VramUsed = sample.VramUsed
		s.GPU.VramTotal = sample.VramTotal
		s.GPU.GttUsed = sample.GttUsed
		s.GPU.Temp = sample.TempC
		s.GPU.FanRPM = sample.FanRPM
		s.GPU.PowerW = sample.PowerW
		s.GPU.GpuClockMHz = sample.SclkMHz
		s.GPU.MemClockMHz = sample.MclkMHz
		if s.GPU.UsageHist != nil {
			s.GPU.UsageHist.Push(sample.UsagePct)
		}
		if s.GPU.VramHist != nil {
			s.GPU.VramHist.Push(vramPct)
		}
	})
}
