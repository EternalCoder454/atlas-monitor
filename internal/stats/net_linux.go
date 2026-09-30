package stats

// The network on Linux: /proc/net/dev for counters, /proc/net/route for which
// interface is actually carrying traffic, and sysfs for the rest.

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"atlas-monitor/internal/sysfs"
)

// discoverNets enumerates interfaces from /proc/net/dev and reads their static
// attributes (MAC, link speed, addresses).
func (c *Collector) discoverNets() {
	c.netDev = sysfs.OpenSize("/proc/net/dev", 4096)
	c.netRoute = sysfs.OpenSize("/proc/net/route", 4096)
	counters := c.readNetBytes()
	var nets []*NetStats
	for name := range counters {
		n := &NetStats{
			Name:      name,
			SpeedMbit: -1,
			DownHist:  NewRingBuffer(),
			UpHist:    NewRingBuffer(),
		}
		n.MAC = sysfs.ReadString(filepath.Join("/sys/class/net", name, "address"))
		// speed is absent on virtual interfaces and reads as -1 on a link that
		// is down, so a parse failure simply leaves it unknown.
		if sp, err := strconv.Atoi(sysfs.ReadString(filepath.Join("/sys/class/net", name, "speed"))); err == nil {
			n.SpeedMbit = sp
		}
		n.Display = friendlyNetName(name)
		n.Wireless = isWireless(name)
		n.loopback = name == "lo"
		n.IPv4, n.IPv6 = interfaceAddrs(name)
		n.index, _ = strconv.Atoi(sysfs.ReadString(filepath.Join("/sys/class/net", name, "ifindex")))
		nets = append(nets, n)
	}
	// Stable order: loopback last, otherwise alphabetical.
	sortNets(nets)
	active := c.defaultRouteIface()
	c.write(func(s *Stats) {
		s.Nets = nets
		s.ActiveNet = active
	})
}

// defaultRouteIface returns the interface carrying the default route. Called by
// the net collector goroutine so the UI never parses /proc/net/route itself.
func (c *Collector) defaultRouteIface() string {
	data, ok := c.netRoute.Bytes()
	if !ok {
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

// readNetBytes returns interface -> [rxBytes, txBytes], reusing the collector's
// buffer and map.
func (c *Collector) readNetBytes() map[string][2]uint64 {
	out := c.netCounters
	clear(out)
	data, ok := c.netDev.Bytes()
	if !ok {
		return out
	}
	for len(data) > 0 {
		var line []byte
		line, data = nextLine(data)
		i := bytes.IndexByte(line, ':')
		if i < 0 {
			continue // header lines
		}
		// rx bytes is field 0, tx bytes is field 8.
		rx, tx := field(line[i+1:], 0), field(line[i+1:], 8)
		if rx == nil || tx == nil {
			continue
		}
		name := string(bytes.TrimSpace(line[:i])) // reused as the map key after the first tick
		out[name] = [2]uint64{parseUintBytes(rx), parseUintBytes(tx)}
	}
	return out
}

// friendlyNetName maps a kernel interface name to a human label.
func friendlyNetName(name string) string {
	if name == "lo" {
		return "Loopback"
	}
	if isWireless(name) {
		return "Wi-Fi"
	}
	base := filepath.Join("/sys/class/net", name)
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

// isWireless reports whether an interface is Wi-Fi.
//
// Either directory is enough: "wireless" is the old ioctl-era attribute and
// "phy80211" the cfg80211 one, and which of them a driver presents depends on its
// age rather than on anything about the hardware.
func isWireless(name string) bool {
	base := filepath.Join("/sys/class/net", name)
	for _, marker := range []string{"wireless", "phy80211"} {
		if _, err := os.Stat(filepath.Join(base, marker)); err == nil {
			return true
		}
	}
	return false
}

// Netlink message layout, for reading the address table by hand.
const (
	nlHdrLen   = 16 // struct nlmsghdr
	ifAddrLen  = 8  // struct ifaddrmsg
	rtAttrLen  = 4  // struct rtattr
	nlmsgDone  = 3
	rtmNewAddr = 20
	ifaAddress = 1
	ifaLocal   = 2
)

// readAddrs fills c.addrs from one RTM_GETADDR dump, which answers for every
// interface. Go's net package would dump the whole link table and then the
// whole address table again for each interface it is asked about; with a
// handful of interfaces that was most of what a quiet Atlas allocated. It
// reports false if the dump failed, so the addresses shown stay as they were.
func (c *Collector) readAddrs() bool {
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETADDR, syscall.AF_UNSPEC)
	if err != nil {
		return false
	}
	clear(c.addrs)
	c.Read(func(s *Stats) { parseAddrDump(rib, s.Nets, c.addrs) })
	return true
}

// parseAddrDump walks a netlink address dump and records, for each interface in
// nets, its first IPv4 address and its first IPv6 address that is not
// link-local, which is what interfaceAddrs reports.
func parseAddrDump(rib []byte, nets []*NetStats, out map[string]ifAddr) {
	le := binary.NativeEndian
	for len(rib) >= nlHdrLen {
		msgLen := int(le.Uint32(rib))
		if msgLen < nlHdrLen || msgLen > len(rib) {
			return
		}
		typ := le.Uint16(rib[4:])
		body := rib[nlHdrLen:msgLen]
		if adv := (msgLen + 3) &^ 3; adv < len(rib) {
			rib = rib[adv:]
		} else {
			rib = nil
		}
		if typ == nlmsgDone {
			return
		}
		if typ != rtmNewAddr || len(body) < ifAddrLen {
			continue
		}
		family, index := body[0], int(le.Uint32(body[4:]))
		var name string
		for _, n := range nets {
			if n.index == index {
				name = n.Name
				break
			}
		}
		if name == "" {
			continue
		}
		var addr []byte
		attrs := body[ifAddrLen:]
		for len(attrs) >= rtAttrLen {
			l := int(le.Uint16(attrs))
			if l < rtAttrLen || l > len(attrs) {
				break
			}
			kind := le.Uint16(attrs[2:])
			// A point-to-point link puts its own address in LOCAL and the peer's
			// in ADDRESS; on anything else they are the same.
			if kind == ifaLocal || (kind == ifaAddress && addr == nil) {
				addr = attrs[rtAttrLen:l]
			}
			next := (l + 3) &^ 3
			if next >= len(attrs) {
				break
			}
			attrs = attrs[next:]
		}
		a := out[name]
		switch {
		case family == syscall.AF_INET && len(addr) == 4:
			if a.v4 == "" {
				a.v4 = netip.AddrFrom4([4]byte(addr)).String()
			}
		case family == syscall.AF_INET6 && len(addr) == 16:
			if ip := netip.AddrFrom16([16]byte(addr)); a.v6 == "" && !ip.IsLinkLocalUnicast() {
				a.v6 = ip.String()
			}
		}
		out[name] = a
	}
}
