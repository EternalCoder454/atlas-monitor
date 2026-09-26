package stats

// initPower prepares battery stats if this machine has one.
func (c *Collector) initPower() {
	if c.pwr == nil || !c.pwr.Available() {
		return
	}
	st, _ := c.pwr.Read()
	c.write(func(s *Stats) {
		s.Power.Available = true
		s.Power.ChargeHist = NewRingBuffer()
		s.Power.DrawHist = NewRingBuffer()
		// One set of buffers per pack, allocated once. Only machines with more
		// than one pack ever have a page that reads them, but keeping the slice
		// the same shape for one pack keeps the lookup honest.
		s.Power.Packs = make([]PackStats, len(st.Packs))
		for i, p := range st.Packs {
			s.Power.Packs[i] = PackStats{
				Battery:    p,
				ChargeHist: NewRingBuffer(),
				DrawHist:   NewRingBuffer(),
			}
		}
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
		// Packs are matched by name rather than by position: a pack that goes
		// missing must not shift another one's history onto the wrong page.
		for _, p := range st.Packs {
			for i := range s.Power.Packs {
				if s.Power.Packs[i].Battery.Name != p.Name {
					continue
				}
				s.Power.Packs[i].Battery = p
				s.Power.Packs[i].ChargeHist.Push(p.Percent)
				s.Power.Packs[i].DrawHist.Push(p.PowerW)
				break
			}
		}
	})
}

// PowerAvailable reports whether the machine has a battery, so the UI knows
// whether to offer the page.
func (c *Collector) PowerAvailable() bool { return c.pwr != nil && c.pwr.Available() }
