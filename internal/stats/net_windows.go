package stats

import (
	"net"
	"strings"
	"sync"
	"time"

	"atlas-monitor/internal/winapi"
)

// The network on Windows.
//
// One call to GetIfTable2 answers what /proc/net/dev and several sysfs
// attributes answer on Linux: every interface, its name, its link speed, its
// hardware address and its 64-bit byte counters.
//
// Windows names interfaces the way a person would — "Ethernet", "Wi-Fi",
// "vEthernet (Default Switch)" — so there is no equivalent of the eth0-to-Ethernet
// translation friendlyNetName does. The name is the label.

// discoverNets enumerates the interfaces and their static attributes.
func (c *Collector) discoverNets() {
	ifaces, err := winapi.ReadInterfaces()
	if err != nil {
		return
	}

	var nets []*NetStats
	for _, in := range ifaces {
		name := in.Name
		if name == "" {
			name = in.Description
		}
		if name == "" {
			continue // nothing to key it by, and nothing to label it with
		}
		n := &NetStats{
			Name:      name,
			Display:   windowsNetLabel(in),
			MAC:       in.MAC,
			SpeedMbit: -1,
			Wireless:  in.IsWireless(),
			loopback:  in.Loopback,
			DownHist:  NewRingBuffer(),
			UpHist:    NewRingBuffer(),
		}
		// A link that is down reports a speed of zero, which is not a speed —
		// leave it unknown, the same as Linux does with its -1.
		if in.Up && in.SpeedBits > 0 {
			n.SpeedMbit = int(in.SpeedBits / 1_000_000)
		}
		n.IPv4, n.IPv6 = interfaceAddrs(name)
		nets = append(nets, n)
	}

	sortNets(nets)
	active := c.defaultRouteIface()
	c.write(func(s *Stats) {
		s.Nets = nets
		s.ActiveNet = active
	})
}

// windowsNetLabel is what to call an interface.
//
// The alias is already a friendly name, so it is used as it stands. The one thing
// worth adding is the loopback, which Windows calls "Loopback Pseudo-Interface 1"
// — accurate and not what anybody wants to read on a chart.
func windowsNetLabel(in winapi.Interface) string {
	if in.Loopback {
		return "Loopback"
	}
	if in.Name != "" {
		return in.Name
	}
	return in.Description
}

// readNetBytes returns interface -> [rxBytes, txBytes].
//
// The map is the collector's own and is reused, so a tick allocates only for an
// interface name it has not seen before.
func (c *Collector) readNetBytes() map[string][2]uint64 {
	out := c.netCounters
	clear(out)
	ifaces, err := winapi.ReadInterfaces()
	if err != nil {
		return out
	}
	for _, in := range ifaces {
		name := in.Name
		if name == "" {
			name = in.Description
		}
		if name == "" {
			continue
		}
		out[name] = [2]uint64{in.RxBytes, in.TxBytes}
	}
	return out
}

// activeRoute caches which interface carries traffic off this machine.
//
// Finding out means asking the routing table, and the honest way to ask is to let
// the OS answer the actual question — if you were to send a packet to the
// internet, which interface would it leave by? A UDP socket answers that: connect
// on UDP sends nothing, it only makes the kernel pick a route and bind a local
// address. The alternative was transcribing MIB_IPFORWARD_ROW2 and re-implementing
// longest-prefix matching, which is a great deal more code to get subtly wrong.
//
// Cheap as it is, it is not something to do every second, so the answer is held
// for a while. Linux re-reads /proc/net/route on each tick because that is a
// single read of a file that is already open.
var activeRoute struct {
	sync.Mutex
	name string
	when time.Time
}

// activeRouteTTL is how long the cached answer stands. The default route changes
// when a cable goes in or Wi-Fi drops, which is worth noticing within seconds but
// not within one.
const activeRouteTTL = 10 * time.Second

// defaultRouteIface returns the interface carrying the default route.
func (c *Collector) defaultRouteIface() string {
	activeRoute.Lock()
	defer activeRoute.Unlock()
	if time.Since(activeRoute.when) < activeRouteTTL && activeRoute.name != "" {
		return activeRoute.name
	}

	name := routeLookup()
	if name != "" {
		activeRoute.name = name
	}
	activeRoute.when = time.Now()
	return activeRoute.name
}

// routeLookup finds the interface whose address the OS would send from.
func routeLookup() string {
	// A documented address outside this machine, never actually contacted: UDP
	// connect performs no I/O. The port is arbitrary.
	conn, err := net.DialTimeout("udp4", "192.0.2.1:9", time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()

	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP == nil || local.IP.IsUnspecified() {
		return ""
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, in := range ifaces {
		addrs, err := in.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if ok && ipnet.IP.Equal(local.IP) {
				return in.Name
			}
		}
	}
	return ""
}

// friendlyNetName exists so the shared code has one on both platforms. Windows
// aliases need no translating; the loopback is the exception.
func friendlyNetName(name string) string {
	if strings.Contains(strings.ToLower(name), "loopback") {
		return "Loopback"
	}
	return name
}
