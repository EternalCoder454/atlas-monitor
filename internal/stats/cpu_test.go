package stats

import (
	"strconv"
	"strings"
	"testing"
)

var cpuStatSample = []byte("cpu  2255 34 2290 22625563 6290 127 456 0 0 0")

func TestParseCPUStatLine(t *testing.T) {
	name, idle, total, ok := parseCPUStatLine(cpuStatSample)
	if !ok {
		t.Fatal("ok = false for a valid line")
	}
	if name != "cpu" {
		t.Errorf("name = %q, want cpu", name)
	}
	if want := uint64(22625563 + 6290); idle != want { // idle + iowait
		t.Errorf("idle = %d, want %d", idle, want)
	}
	if want := uint64(2255 + 34 + 2290 + 22625563 + 6290 + 127 + 456); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}

	// Per-core line (single space) parses; a line with no idle column is rejected.
	if n, _, _, ok := parseCPUStatLine([]byte("cpu0 1 2 3 4 5")); !ok || n != "cpu0" {
		t.Errorf("cpu0 line: name=%q ok=%v", n, ok)
	}
	if _, _, _, ok := parseCPUStatLine([]byte("cpu 1 2 3")); ok {
		t.Error("line without an idle column should be ok=false")
	}

	// The byte parser must agree with the old strings.Fields implementation.
	fn, fi, ft, _ := parseCPUStatLineFields(string(cpuStatSample))
	if fn != name || fi != idle || ft != total {
		t.Errorf("byte parser disagrees with Fields parser: (%q %d %d) vs (%q %d %d)",
			name, idle, total, fn, fi, ft)
	}
}

// parseCPUStatLineFields is the previous strings.Fields-based implementation,
// kept here only to (a) cross-check correctness and (b) benchmark the allocation
// difference against the byte parser now used in collectCPU.
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
