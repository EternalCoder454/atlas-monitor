package stats

import (
	"strconv"
	"strings"
	"testing"
)

var cpuStatSample = []byte("cpu  2255 34 2290 22625563 6290 127 456 0 0 0")

func TestParseCPUStatLine(t *testing.T) {
	core, idle, total, ok := parseCPUStatLine(cpuStatSample)
	if !ok {
		t.Fatal("ok = false for a valid line")
	}
	if core != -1 {
		t.Errorf("core = %d, want -1 for the aggregate line", core)
	}
	if want := uint64(22625563 + 6290); idle != want { // idle + iowait
		t.Errorf("idle = %d, want %d", idle, want)
	}
	if want := uint64(2255 + 34 + 2290 + 22625563 + 6290 + 127 + 456); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}

	// Per-core lines carry their number, which is what indexes the previous
	// sample — no string is built for it.
	for _, tt := range []struct {
		line string
		core int
	}{{"cpu0 1 2 3 4 5", 0}, {"cpu7 1 2 3 4 5", 7}, {"cpu31 1 2 3 4 5", 31}} {
		if c, _, _, ok := parseCPUStatLine([]byte(tt.line)); !ok || c != tt.core {
			t.Errorf("%q: core = %d, ok = %v; want %d", tt.line, c, ok, tt.core)
		}
	}

	// Lines that are not cpu lines, or are too short to hold an idle figure.
	for _, bad := range []string{"cpu 1 2 3", "intr 1 2 3 4 5 6", "cpuX 1 2 3 4 5"} {
		if _, _, _, ok := parseCPUStatLine([]byte(bad)); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// parseCPUStatLineFields is the original strings.Fields implementation, kept to
// cross-check the byte parser and to benchmark the difference.
func parseCPUStatLineFields(line string) (name string, idle, total uint64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return "", 0, 0, false
	}
	var nums []uint64
	for _, f := range fields[1:] {
		n, _ := strconv.ParseUint(f, 10, 64)
		nums = append(nums, n)
		total += n
	}
	idle = nums[3]
	if len(nums) > 4 {
		idle += nums[4]
	}
	return fields[0], idle, total, true
}

func TestParseCPUStatLineMatchesFields(t *testing.T) {
	for _, line := range []string{
		string(cpuStatSample),
		"cpu0 12345 0 6789 111213 141 5 16 0 0 0",
		"cpu15 1 2 3 4 5 6 7 8 9 10",
	} {
		_, idle, total, ok := parseCPUStatLine([]byte(line))
		fn, fi, ft, fok := parseCPUStatLineFields(line)
		if ok != fok || idle != fi || total != ft {
			t.Errorf("%q: byte parser (%d %d %v) disagrees with Fields (%s: %d %d %v)",
				line, idle, total, ok, fn, fi, ft, fok)
		}
	}
}

func BenchmarkParseCPUStatLine(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parseCPUStatLine(cpuStatSample)
	}
}

func BenchmarkParseCPUStatLineFields(b *testing.B) {
	s := string(cpuStatSample)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parseCPUStatLineFields(s)
	}
}

// BenchmarkCollectCPU measures one whole sample against this machine's live
// /proc and /sys — the figure that matters, since the collector runs once a
// second for as long as the app is open.
func BenchmarkCollectCPU(b *testing.B) {
	c := New(nil)
	c.initCPUStatic()
	defer c.closeCPU()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.collectCPU()
	}
}
