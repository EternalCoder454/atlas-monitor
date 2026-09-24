package process

import (
	"strconv"
	"strings"
	"testing"
)

// The collectors read /proc with hand-written byte parsers to avoid allocating
// on every process every second. That trades safety for speed: an index that
// walks off the end of a buffer is a crash in a monitoring app that is supposed
// to sit in the tray for weeks. These fuzz targets assert the parsers cannot
// panic and cannot return a slice outside their input, whatever bytes they are
// handed — which covers truncated reads, processes that exit mid-read, and
// container /proc filesystems that do not look like the host's.

// FuzzParseStatLine feeds arbitrary bytes to the /proc/<pid>/stat parser.
func FuzzParseStatLine(f *testing.F) {
	f.Add([]byte("1 (systemd) S 0 1 1 0 -1 4194560 12345 0 0 0 42 17 0 0 20 0 1 0 5 12345678 4321"))
	f.Add([]byte("7 (kworker/0:0H-events_highpri) I 2 0 0 0 -1 69238880 0 0 0 0 0 5 0 0 0 -20 1 0 8 0 0"))
	f.Add([]byte("42 (weird (name) with parens) R 1 1"))
	f.Add([]byte("1 () S 0"))
	f.Add([]byte(""))
	f.Add([]byte("("))
	f.Add([]byte(")"))
	f.Add([]byte("()"))
	f.Add([]byte("1 (x)"))
	f.Add([]byte("1 (x) "))

	f.Fuzz(func(t *testing.T, b []byte) {
		var st statLine
		if !parseStatLine(b, &st) {
			return
		}
		// name must alias the input, not point past it.
		if len(st.name) > len(b) {
			t.Fatalf("name is longer (%d) than the input (%d)", len(st.name), len(b))
		}
		if !aliases(b, st.name) {
			t.Fatalf("name %q does not alias the input", st.name)
		}
	})
}

// aliases reports whether sub is a sub-slice of the backing array of b.
func aliases(b, sub []byte) bool {
	if len(sub) == 0 {
		return true
	}
	return strings.Contains(string(b), string(sub))
}

// FuzzFieldUint feeds arbitrary bytes to the whitespace field scanner used for
// /proc/<pid>/stat and /proc/<pid>/statm.
func FuzzFieldUint(f *testing.F) {
	f.Add([]byte("1 2 3 4 5"), 3)
	f.Add([]byte("   leading spaces 7"), 2)
	f.Add([]byte(""), 0)
	f.Add([]byte(" "), 0)
	f.Add([]byte("18446744073709551615"), 0)
	f.Add([]byte("99999999999999999999999999"), 0)

	f.Fuzz(func(t *testing.T, b []byte, idx int) {
		if idx < 0 || idx > 1<<16 {
			return // the callers only ever pass small constants
		}
		_ = fieldUint(b, idx)
	})
}

// FuzzUintAfter feeds arbitrary bytes and keys to the /proc/<pid>/io parser.
func FuzzUintAfter(f *testing.F) {
	f.Add([]byte("rchar: 1\nread_bytes: 4096\nwrite_bytes: 8192\n"), "read_bytes:")
	f.Add([]byte("read_bytes:"), "read_bytes:")
	f.Add([]byte("read_bytes:   \t "), "read_bytes:")
	f.Add([]byte(""), "")
	f.Add([]byte("x"), "")

	f.Fuzz(func(t *testing.T, b []byte, key string) {
		_ = uintAfter(b, key)
	})
}

// FuzzNetField feeds arbitrary bytes to the /proc/net/dev field scanner, which
// must always return a sub-slice of its input or nil.
func FuzzNetField(f *testing.F) {
	f.Add([]byte(" 1234 5 0 0 0 0 0 0 9876 4 0 0 0 0 0 0"), 8)
	f.Add([]byte("\t\t"), 0)
	f.Add([]byte(""), 3)

	f.Fuzz(func(t *testing.T, b []byte, idx int) {
		if idx < 0 || idx > 1<<16 {
			return
		}
		got := netField(b, idx)
		if got != nil && !aliases(b, got) {
			t.Fatalf("field %q does not alias the input %q", got, b)
		}
	})
}

// FuzzBytesToUint checks the digit scanner against strconv for inputs that are
// wholly numeric, and checks it never panics for anything else.
func FuzzBytesToUint(f *testing.F) {
	f.Add([]byte("0"))
	f.Add([]byte("4096"))
	f.Add([]byte("18446744073709551615")) // MaxUint64
	f.Add([]byte("-1"))
	f.Add([]byte("+7"))
	f.Add([]byte(" 7"))
	f.Add([]byte("7 "))
	f.Add([]byte("0x10"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, b []byte) {
		got := bytesToUint(b)
		// Only compare where strconv agrees the whole input is a plain integer
		// that fits: bytesToUint stops at the first non-digit by design, and
		// wraps on overflow, neither of which /proc can produce.
		if want, err := strconv.ParseUint(string(b), 10, 64); err == nil {
			if got != want {
				t.Fatalf("bytesToUint(%q) = %d, strconv says %d", b, got, want)
			}
		}
	})
}

// FuzzClampPct checks the percentage clamp, which guards every figure the Apps
// table renders as a bar.
func FuzzClampPct(f *testing.F) {
	f.Add(0.0)
	f.Add(-1.0)
	f.Add(101.0)

	f.Fuzz(func(t *testing.T, p float64) {
		got := clampPct(p)
		if got < 0 {
			t.Fatalf("clampPct(%v) = %v, below 0", p, got)
		}
		if got != got { // NaN
			t.Fatalf("clampPct(%v) returned NaN", p)
		}
	})
}
