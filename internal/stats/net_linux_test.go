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
