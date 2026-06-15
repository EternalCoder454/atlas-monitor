package ui

import (
	"atlas-monitor/internal/stats"
)

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
