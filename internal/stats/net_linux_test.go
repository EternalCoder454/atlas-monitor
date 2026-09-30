package stats

import "testing"

// routeSample is a realistic /proc/net/route: a header, two default routes
// (dest 00000000) with different metrics, and a more-specific route that must
// be ignored.
var routeSample = []byte(
	"Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"enp6s0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"wlp7s0\t00000000\t0102A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"wlp7s0\t0002A8C0\t00000000\t0001\t0\t0\t600\t00FFFFFF\t0\t0\t0\n")

func TestParseDefaultRoute(t *testing.T) {
	if got := parseDefaultRoute(routeSample); got != "enp6s0" {
		t.Errorf("got %q, want enp6s0 (the default route with the lowest metric)", got)
	}
	if got := parseDefaultRoute([]byte("Iface\tDestination\tGateway\n")); got != "" {
		t.Errorf("no default route: got %q, want empty", got)
	}
}

func BenchmarkParseDefaultRoute(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parseDefaultRoute(routeSample)
	}
}

// liveNets is every interface on this machine, as discoverNets would build it.
func liveNets(t testing.TB) []*NetStats {
	t.Helper()
	c := New(nil)
	c.discoverNets()
	var nets []*NetStats
	c.Read(func(s *Stats) { nets = append(nets, s.Nets...) })
	return nets
}

// One netlink dump must give the same addresses as asking about each interface.
func TestReadAddrsMatchesNet(t *testing.T) {
	c := New(nil)
	c.discoverNets()
	c.Read(func(s *Stats) {
		if len(s.Nets) == 0 {
			t.Skip("no interfaces")
		}
	})
	if !c.readAddrs() {
		t.Skip("netlink address dump unavailable")
	}
	c.Read(func(s *Stats) {
		for _, n := range s.Nets {
			v4, v6 := interfaceAddrs(n.Name)
			if got := c.addrs[n.Name]; got.v4 != v4 || got.v6 != v6 {
				t.Errorf("%s: dump gave %+v, net package gave %q %q", n.Name, got, v4, v6)
			}
		}
	})
}

func TestParseAddrDumpRejectsTruncated(t *testing.T) {
	out := map[string]ifAddr{}
	for _, b := range [][]byte{nil, {1, 2, 3}, make([]byte, 16), {255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}} {
		parseAddrDump(b, nil, out) // must not panic
	}
}

func BenchmarkReadAddrs(b *testing.B) {
	c := New(nil)
	c.discoverNets()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.readAddrs()
	}
}

func BenchmarkInterfaceAddrsPerName(b *testing.B) {
	nets := liveNets(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, n := range nets {
			interfaceAddrs(n.Name)
		}
	}
}
