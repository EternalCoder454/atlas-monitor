package stats

// initPower prepares battery stats if this machine has one.
func (c *Collector) initPower() {
	if c.pwr == nil || !c.pwr.Available() {
		return
	}
	c.write(func(s *Stats) {
		s.Power.Available = true
		s.Power.ChargeHist = NewRingBuffer()
		s.Power.DrawHist = NewRingBuffer()
	})
	c.collectPower()
}

// collectPower samples the battery and adapter.
func (c *Collector) collectPower() {
	st, ok := c.pwr.Read()
	if !ok {
		return
	}
	c.write(func(s *Stats) {
		s.Power.Battery = st.Battery
		s.Power.HasAC = st.HasAC
		s.Power.OnAC = st.OnAC
		if s.Power.ChargeHist != nil {
			s.Power.ChargeHist.Push(st.Battery.Percent)
		}
		if s.Power.DrawHist != nil {
			s.Power.DrawHist.Push(st.Battery.PowerW)
		}
	})
}

// PowerAvailable reports whether the machine has a battery, so the UI knows
// whether to offer the page.
func (c *Collector) PowerAvailable() bool { return c.pwr != nil && c.pwr.Available() }
