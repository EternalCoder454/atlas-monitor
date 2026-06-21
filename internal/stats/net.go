package stats

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// discoverNets enumerates interfaces from /proc/net/dev and reads their static
// attributes (MAC, link speed, addresses).
func (c *Collector) discoverNets() {
	counters := readNetDev()
	var nets []*NetStats
	for name := range counters {
		n := &NetStats{
			Name:      name,
			SpeedMbit: -1,
			DownHist:  NewRingBuffer(),
			UpHist:    NewRingBuffer(),
		}
		if mac, err := readString(filepath.Join("/sys/class/net", name, "address")); err == nil {
			n.MAC = mac
		}
		if sp, err := readInt(filepath.Join("/sys/class/net", name, "speed")); err == nil {
			n.SpeedMbit = sp
		}
		n.Display = friendlyNetName(name)
		n.IPv4, n.IPv6 = interfaceAddrs(name)
		nets = append(nets, n)
	}
	// Stable order: loopback last, otherwise alphabetical.
	sortNets(nets)
	active := defaultRouteIface()
	c.write(func(s *Stats) {
		s.Nets = nets
		s.ActiveNet = active
	})
}

// collectNets updates per-interface throughput and refreshes addresses.
func (c *Collector) collectNets() {
	now := time.Now()
	dt := now.Sub(c.netLast).Seconds()
	if c.netLast.IsZero() || dt <= 0 {
		dt = 1
	}
	c.netLast = now

	counters := readNetDev()
	active := defaultRouteIface()

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

// defaultRouteIface returns the interface carrying the default route. Called by
// the net collector goroutine so the UI never parses /proc/net/route itself.
func defaultRouteIface() string {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	return parseDefaultRoute(data)
}

// parseDefaultRoute returns the interface with destination 0.0.0.0 and the
// lowest metric in /proc/net/route content, or "". Split out for testing.
func parseDefaultRoute(data []byte) string {
	best := ""
	bestMetric := int(^uint(0) >> 1)
	first := true
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		if first { // skip the header row
			first = false
			continue
		}
		fields := bytes.Fields(line)
		if len(fields) < 8 || string(fields[1]) != "00000000" { // dest 0.0.0.0 = default route
			continue
		}
		if metric, _ := strconv.Atoi(string(fields[6])); metric < bestMetric {
			bestMetric, best = metric, string(fields[0])
		}
	}
	return best
}

// readNetDev returns interface -> [rxBytes, txBytes].
func readNetDev() map[string][2]uint64 {
	out := make(map[string][2]uint64)
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue // header lines
		}
		name := strings.TrimSpace(line[:i])
		fields := strings.Fields(line[i+1:])
		if len(fields) < 9 {
			continue
		}
		// rx bytes is field 0, tx bytes is field 8.
		out[name] = [2]uint64{atou(fields[0]), atou(fields[8])}
	}
	return out
}

// friendlyNetName maps a kernel interface name to a human label.
func friendlyNetName(name string) string {
	if name == "lo" {
		return "Loopback"
	}
	base := filepath.Join("/sys/class/net", name)
	if _, err := os.Stat(filepath.Join(base, "wireless")); err == nil {
		return "Wi-Fi"
	}
	if _, err := os.Stat(filepath.Join(base, "phy80211")); err == nil {
		return "Wi-Fi"
	}
	switch {
	case strings.HasPrefix(name, "docker"):
		return "Docker"
	case strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "virbr"):
		return "Bridge"
	case strings.HasPrefix(name, "veth"):
		return "Virtual Ethernet"
	case strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "wg"):
		return "Tunnel / VPN"
	}
	// A physical NIC exposes a device symlink; virtual ones do not.
	if _, err := os.Stat(filepath.Join(base, "device")); err == nil {
		return "Ethernet"
	}
	return name
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
	aLo := a.Name == "lo"
	bLo := b.Name == "lo"
	if aLo != bLo {
		return !aLo // non-loopback first
	}
	return a.Name < b.Name
}
