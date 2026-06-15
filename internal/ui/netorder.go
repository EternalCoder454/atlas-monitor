package ui

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"atlas-monitor/internal/stats"
)

// activeNetName returns the interface carrying the default route (the one
// you're "actually using" for the internet), or "" if there is none.
func activeNetName() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()

	best := ""
	bestMetric := int(^uint(0) >> 1)
	sc := bufio.NewScanner(f)
	header := true
	for sc.Scan() {
		if header {
			header = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 || fields[1] != "00000000" { // destination 0.0.0.0 = default route
			continue
		}
		if metric, _ := strconv.Atoi(fields[6]); metric < bestMetric {
			bestMetric, best = metric, fields[0]
		}
	}
	return best
}

// orderByActive returns nets with the active interface moved to the front.
func orderByActive(nets []*stats.NetStats, active string) []*stats.NetStats {
	out := make([]*stats.NetStats, 0, len(nets))
	for _, n := range nets {
		if n.Name == active {
			out = append(out, n)
		}
	}
	for _, n := range nets {
		if n.Name != active {
			out = append(out, n)
		}
	}
	return out
}

// orderNames returns the stable name order with active moved to the front.
func orderNames(stable []string, active string) []string {
	out := make([]string, 0, len(stable))
	for _, n := range stable {
		if n == active {
			out = append(out, n)
		}
	}
	for _, n := range stable {
		if n != active {
			out = append(out, n)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
