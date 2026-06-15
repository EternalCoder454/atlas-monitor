package ui

import "testing"

func TestNetReorder(t *testing.T) {
	stable := []string{"wlp7s0", "enp6s0", "lo"}
	cases := []struct {
		active string
		want   []string
	}{
		{"wlp7s0", []string{"wlp7s0", "enp6s0", "lo"}}, // on Wi-Fi
		{"enp6s0", []string{"enp6s0", "wlp7s0", "lo"}}, // swapped to Ethernet
		{"", stable},                                   // no default route
	}
	for _, c := range cases {
		if got := orderNames(stable, c.active); !equalStrings(got, c.want) {
			t.Errorf("active=%q: got %v, want %v", c.active, got, c.want)
		}
	}
}
