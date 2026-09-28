package stats

// The part of network reporting that is the same everywhere: turning two
// cumulative byte counters into a rate, keeping the interface list in a stable
// order, and reading each interface's addresses.
//
// Addresses come from Go's own net package, which works on both platforms, so
// only the counters and the default-route lookup are per-platform.

import (
	"net"
	"time"
)

// collectNets updates per-interface throughput and refreshes addresses.
func (c *Collector) collectNets() {
	now := time.Now()
	dt := now.Sub(c.netLast).Seconds()
	if c.netLast.IsZero() || dt <= 0 {
		dt = 1
	}
	c.netLast = now

	counters := c.readNetBytes()
	active := c.defaultRouteIface()

	// IP addresses change rarely but each refresh is a netlink round-trip per
	// interface, so only refresh them every 5th tick (and on the first).
	c.netTick++
	refreshAddrs := c.netTick%5 == 1

	c.write(func(s *Stats) {
		for _, n := range s.Nets {
			cv, ok := counters[n.Name]
			if ok {
				if n.havePrev {
					n.RxRate = rateOf(cv[0], n.prevRx, dt)
					n.TxRate = rateOf(cv[1], n.prevTx, dt)
				}
				n.prevRx, n.prevTx = cv[0], cv[1]
				n.havePrev = true
				n.RxTotal, n.TxTotal = cv[0], cv[1]
			}
			if n.DownHist != nil {
				n.DownHist.Push(n.RxRate)
			}
			if n.UpHist != nil {
				n.UpHist.Push(n.TxRate)
			}
			if refreshAddrs {
				n.IPv4, n.IPv6 = interfaceAddrs(n.Name)
			}
		}
		s.ActiveNet = active
	})
}

// interfaceAddrs returns the first IPv4 and IPv6 address of an interface.
func interfaceAddrs(name string) (ipv4, ipv6 string) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return "", ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP
		if v4 := ip.To4(); v4 != nil {
			if ipv4 == "" {
				ipv4 = v4.String()
			}
		} else if ipv6 == "" && !ip.IsLinkLocalUnicast() {
			ipv6 = ip.String()
		}
	}
	return ipv4, ipv6
}

// sortNets orders interfaces alphabetically with loopback pushed to the end.
func sortNets(nets []*NetStats) {
	for i := 1; i < len(nets); i++ {
		for j := i; j > 0 && netLess(nets[j], nets[j-1]); j-- {
			nets[j], nets[j-1] = nets[j-1], nets[j]
		}
	}
}

func netLess(a, b *NetStats) bool {
	if a.loopback != b.loopback {
		return !a.loopback // non-loopback first
	}
	return a.Name < b.Name
}
