package stats

import (
	"os"
	"path/filepath"
	"testing"
)

// meminfoSample is a trimmed but realistically shaped /proc/meminfo.
var meminfoSample = []byte(
	"MemTotal:       32673528 kB\n" +
		"MemFree:         1234560 kB\n" +
		"MemAvailable:   25325568 kB\n" +
		"Buffers:           40960 kB\n" +
		"Cached:          8765432 kB\n" +
		"SwapCached:            0 kB\n" +
		"Active:         12345678 kB\n" +
		"SReclaimable:     409600 kB\n" +
		"SwapTotal:       8388608 kB\n" +
		"SwapFree:        8388608 kB\n")

func TestMeminfoLine(t *testing.T) {
	tests := []struct {
		line  string
		key   string
		value uint64
		ok    bool
	}{
		{"MemTotal:       32673528 kB", "MemTotal", 32673528, true},
		{"MemFree: 1 kB", "MemFree", 1, true},
		{"HugePages_Total:       0", "HugePages_Total", 0, true},
		{"not a meminfo line", "", 0, false},
		{"Empty:", "", 0, false},
	}
	for _, tt := range tests {
		key, value, ok := meminfoLine([]byte(tt.line))
		if ok != tt.ok || string(key) != tt.key || value != tt.value {
			t.Errorf("meminfoLine(%q) = (%q, %d, %v), want (%q, %d, %v)",
				tt.line, key, value, ok, tt.key, tt.value, tt.ok)
		}
	}
}

func TestField(t *testing.T) {
	// A /proc/diskstats row: leading spaces, then major minor name counters…
	line := []byte("   259       0 nvme0n1 123 45 67890 1234 56 7 89012 345")
	for _, tt := range []struct {
		idx  int
		want string
	}{{0, "259"}, {1, "0"}, {2, "nvme0n1"}, {5, "67890"}, {9, "89012"}, {99, ""}} {
		if got := string(field(line, tt.idx)); got != tt.want {
			t.Errorf("field(%d) = %q, want %q", tt.idx, got, tt.want)
		}
	}
}

func TestNextLine(t *testing.T) {
	data := []byte("one\ntwo\nthree")
	var got []string
	for len(data) > 0 {
		var line []byte
		line, data = nextLine(data)
		got = append(got, string(line))
	}
	if len(got) != 3 || got[0] != "one" || got[2] != "three" {
		t.Errorf("nextLine walk = %q, want [one two three]", got)
	}
}

// TestReadInto checks the reusable read buffer against files larger and smaller
// than its current capacity, since /proc files are read into it every second.
func TestReadInto(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 40_000)
	for i := range big {
		big[i] = byte('a' + i%26)
	}
	small := []byte("hello\n")

	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	bigPath, smallPath := write("big", big), write("small", small)

	var buf []byte
	for i := 0; i < 3; i++ {
		data, keep, err := readInto(smallPath, buf)
		buf = keep
		if err != nil || string(data) != string(small) {
			t.Fatalf("small read %d: %q, %v", i, data, err)
		}
		data, keep, err = readInto(bigPath, buf)
		buf = keep
		if err != nil || len(data) != len(big) || string(data) != string(big) {
			t.Fatalf("big read %d: len=%d err=%v", i, len(data), err)
		}
	}
	if _, _, err := readInto(filepath.Join(dir, "missing"), buf); err == nil {
		t.Error("readInto on a missing file should return an error")
	}
}

// TestCollectMemLiveSanity parses this machine's real /proc/meminfo through the
// collector and checks the figures hang together.
func TestCollectMemLiveSanity(t *testing.T) {
	c := New(nil)
	c.initMem()
	c.collectMem()
	c.Read(func(s *Stats) {
		if s.Mem.Total == 0 {
			t.Fatal("MemTotal is 0")
		}
		if s.Mem.Used > s.Mem.Total {
			t.Errorf("used %d > total %d", s.Mem.Used, s.Mem.Total)
		}
		if s.Mem.Available > s.Mem.Total {
			t.Errorf("available %d > total %d", s.Mem.Available, s.Mem.Total)
		}
		if s.Mem.SwapUsed > s.Mem.SwapTotal {
			t.Errorf("swap used %d > swap total %d", s.Mem.SwapUsed, s.Mem.SwapTotal)
		}
		if s.Mem.Total%1024 != 0 {
			t.Errorf("total %d is not a whole number of kB — unit conversion is off", s.Mem.Total)
		}
	})
}

func BenchmarkCollectMemParse(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		data := meminfoSample
		for len(data) > 0 {
			var line []byte
			line, data = nextLine(data)
			meminfoLine(line)
		}
	}
}
