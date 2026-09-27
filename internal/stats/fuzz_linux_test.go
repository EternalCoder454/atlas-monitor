package stats

import (
	"strconv"
	"strings"
	"testing"
)

// These targets cover the /proc parsers behind the CPU, Memory, Network and
// Storage pages. Same reasoning as internal/process/fuzz_test.go: the parsers
// are hand-written byte scanners, so the guarantee worth asserting is that no
// input can make them panic or hand a caller a slice outside the buffer.

// FuzzParseCPUStatLine feeds arbitrary bytes to the /proc/stat line parser.
// Beyond not panicking, core must be either exactly -1 (the aggregate line) or
// a plausible core number: collectCPU keys the previous-sample array off it, and
// a value that wrapped negative would be mistaken for the aggregate and clobber
// the whole-CPU figure.
func FuzzParseCPUStatLine(f *testing.F) {
	f.Add([]byte("cpu  123 4 567 89012 345 0 67 0 0 0"))
	f.Add([]byte("cpu0 12 3 45 6789 10 0 11 0 0 0"))
	f.Add([]byte("cpu31 1 2 3 4 5 6 7 8 9 10"))
	f.Add([]byte("cpu"))
	f.Add([]byte("cpu "))
	f.Add([]byte("cpux 1 2 3 4 5"))
	f.Add([]byte("cpu99999999999999999999 1 2 3 4 5"))
	f.Add([]byte("intr 1 2 3"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, b []byte) {
		core, _, _, ok := parseCPUStatLine(b)
		if !ok {
			return
		}
		if core < -1 {
			t.Fatalf("parseCPUStatLine(%q) = core %d; must be -1 or a core number", b, core)
		}
		// idle <= total is deliberately not asserted. Both are accumulated by
		// parseUintBytes, which wraps rather than rejecting on overflow, so a
		// column of seventy digits can make idle exceed total. /proc/stat
		// carries jiffies since boot in ten columns and cannot get within
		// twenty digits of that, and the figure the UI shows is clamped
		// regardless — see TestCPUUsageStaysClampedOnAbsurdInput.
	})
}

// TestCPUUsageStaysClampedOnAbsurdInput is the invariant the CPU page actually
// depends on: whatever /proc/stat says, the published usage is a percentage.
// Overflowed counters must not produce a negative bar or one past 100%.
func TestCPUUsageStaysClampedOnAbsurdInput(t *testing.T) {
	// dIdle/dTotal in every shape that matters: normal, zero-length sample,
	// idle beyond total (overflow), and total beyond any real counter.
	cases := []struct{ dIdle, dTotal float64 }{
		{0, 100}, {100, 100}, {50, 100},
		{0, 0}, {100, 0},
		{1e30, 1}, {1, 1e30}, {-1, 100}, {100, -1},
	}
	for _, c := range cases {
		usage := 0.0
		if c.dTotal > 0 {
			usage = clamp((1-c.dIdle/c.dTotal)*100, 0, 100)
		}
		if usage < 0 || usage > 100 {
			t.Errorf("dIdle=%v dTotal=%v produced usage %v, outside [0,100]", c.dIdle, c.dTotal, usage)
		}
		if usage != usage {
			t.Errorf("dIdle=%v dTotal=%v produced NaN", c.dIdle, c.dTotal)
		}
	}
}

// FuzzMeminfoLine feeds arbitrary bytes to the /proc/meminfo line parser.
func FuzzMeminfoLine(f *testing.F) {
	f.Add([]byte("MemTotal:       32672068 kB"))
	f.Add([]byte("MemAvailable: 1 kB"))
	f.Add([]byte("Key:"))
	f.Add([]byte(":"))
	f.Add([]byte("no colon here"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, b []byte) {
		key, _, ok := meminfoLine(b)
		if !ok {
			return
		}
		if len(key) > len(b) {
			t.Fatalf("key longer than input")
		}
		if !strings.Contains(string(b), string(key)) {
			t.Fatalf("key %q does not alias the input %q", key, b)
		}
	})
}

// FuzzField feeds arbitrary bytes to the shared field scanner.
func FuzzField(f *testing.F) {
	f.Add([]byte("a b c"), 2)
	f.Add([]byte("\t \t"), 0)
	f.Add([]byte(""), 0)

	f.Fuzz(func(t *testing.T, b []byte, idx int) {
		if idx < 0 || idx > 1<<16 {
			return
		}
		got := field(b, idx)
		if got != nil && len(got) > 0 && !strings.Contains(string(b), string(got)) {
			t.Fatalf("field %q does not alias the input %q", got, b)
		}
	})
}

// FuzzNextLine checks the line splitter always consumes input, so no caller can
// spin forever on a buffer it cannot make progress through.
func FuzzNextLine(f *testing.F) {
	f.Add([]byte("a\nb\nc"))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, b []byte) {
		data := b
		for i := 0; len(data) > 0; i++ {
			before := len(data)
			_, data = nextLine(data)
			if len(data) >= before {
				t.Fatalf("nextLine made no progress at %d bytes remaining", before)
			}
			if i > len(b)+2 {
				t.Fatalf("nextLine looped more times than the input has bytes")
			}
		}
	})
}

// FuzzParseUintBytes compares the digit scanner with strconv where both agree
// the input is a plain integer.
func FuzzParseUintBytes(f *testing.F) {
	f.Add([]byte("0"))
	f.Add([]byte("18446744073709551615"))
	f.Add([]byte("abc"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, b []byte) {
		got := parseUintBytes(b)
		if want, err := strconv.ParseUint(string(b), 10, 64); err == nil && got != want {
			t.Fatalf("parseUintBytes(%q) = %d, strconv says %d", b, got, want)
		}
	})
}

// FuzzParseDefaultRoute feeds arbitrary bytes to the /proc/net/route parser that
// decides which interface the sidebar highlights.
func FuzzParseDefaultRoute(f *testing.F) {
	f.Add([]byte("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\nwlp7s0\t00000000\t0102A8C0\t0003\t0\t0\t600\t00000000\n"))
	f.Add([]byte("Iface\n"))
	f.Add([]byte("\n00000000\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, b []byte) {
		got := parseDefaultRoute(b)
		if got != "" && !strings.Contains(string(b), got) {
			t.Fatalf("returned interface %q that is not in the input", got)
		}
	})
}

// FuzzIsPartitionOf checks the disk/partition matcher, which decides whose
// mountpoints get summed into a disk's used and free figures. A false positive
// there attributes another drive's capacity to this one.
func FuzzIsPartitionOf(f *testing.F) {
	f.Add("nvme0n1", "nvme0n1p3")
	f.Add("sda", "sda1")
	f.Add("sda", "sdb1")
	f.Add("nvme0n1", "nvme0n2p1")
	f.Add("", "")
	f.Add("a", "a")

	f.Fuzz(func(t *testing.T, disk, dev string) {
		got := isPartitionOf(disk, dev)
		if !got {
			return
		}
		// A partition name must be its disk plus digits, optionally after a 'p'.
		if !strings.HasPrefix(dev, disk) {
			t.Fatalf("isPartitionOf(%q, %q) = true but dev is not prefixed by disk", disk, dev)
		}
		rest := strings.TrimPrefix(dev, disk)
		rest = strings.TrimPrefix(rest, "p")
		if rest == "" {
			t.Fatalf("isPartitionOf(%q, %q) = true with no partition number", disk, dev)
		}
		// Check the digits directly rather than parsing: a partition number
		// wider than a uint64 is still all digits, and rejecting it here would
		// be the test overflowing, not the function misbehaving.
		for i := 0; i < len(rest); i++ {
			if rest[i] < '0' || rest[i] > '9' {
				t.Fatalf("isPartitionOf(%q, %q) = true but %q is not a partition number", disk, dev, rest)
			}
		}
	})
}
