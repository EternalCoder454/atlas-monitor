package ui

import (
	"os"
	"testing"

	"atlas-monitor/internal/process"
)

// TestGroupByNameReusesBuffers checks the aggregation and, just as importantly,
// that running it twice over different input does not carry state forward — the
// slice and index map are reused between ticks.
func TestGroupByNameReusesBuffers(t *testing.T) {
	v := &appsView{groups: make(map[string]int)}

	first := []process.Proc{
		{PID: 1, Name: "chrome", CPU: 10, RSS: 100, GPU: -1, NetIn: 1, DiskRead: 5},
		{PID: 2, Name: "chrome", CPU: 5, RSS: 50, GPU: 3, NetIn: 2, DiskRead: 1},
		{PID: 3, Name: "bash", CPU: 1, RSS: 10, GPU: -1},
	}
	got := v.groupByName(first)
	if len(got) != 2 {
		t.Fatalf("grouped %d rows, want 2: %+v", len(got), got)
	}
	// Sorted by CPU descending, so chrome first.
	if got[0].Name != "chrome" || got[0].CPU != 15 || got[0].RSS != 150 {
		t.Errorf("chrome group = %+v, want CPU 15 / RSS 150", got[0])
	}
	if got[0].NetIn != 3 || got[0].DiskRead != 6 {
		t.Errorf("chrome group rates = %+v, want NetIn 3 / DiskRead 6", got[0])
	}
	if got[0].GPU != 3 { // -1 (no handle) must not drag the total down
		t.Errorf("chrome group GPU = %v, want 3", got[0].GPU)
	}
	if got[1].Name != "bash" || got[1].CPU != 1 {
		t.Errorf("bash group = %+v, want CPU 1", got[1])
	}

	// A second pass with completely different input must not see the first.
	second := []process.Proc{{PID: 9, Name: "vim", CPU: 2, RSS: 20, GPU: -1}}
	got = v.groupByName(second)
	if len(got) != 1 || got[0].Name != "vim" || got[0].CPU != 2 || got[0].RSS != 20 {
		t.Fatalf("second pass = %+v, want one vim row with CPU 2 / RSS 20", got)
	}
}

func TestGroupByNameEmpty(t *testing.T) {
	v := &appsView{groups: make(map[string]int)}
	if got := v.groupByName(nil); len(got) != 0 {
		t.Errorf("grouping nothing produced %d rows", len(got))
	}
}

func TestSearchHelpers(t *testing.T) {
	if got := lowerASCII("Chrome"); got != "chrome" {
		t.Errorf("lowerASCII = %q", got)
	}
	if got := lowerASCII("already"); got != "already" {
		t.Errorf("lowerASCII of a lower-case string = %q", got)
	}

	for _, c := range []struct {
		s, sub string
		want   bool
	}{
		{"systemd-logind", "logind", true},
		{"systemd-LOGIND", "logind", true}, // haystack folds, needle is pre-lowered
		{"bash", "", true},
		{"bash", "zsh", false},
		{"ab", "abc", false},
	} {
		if got := containsFold(c.s, c.sub); got != c.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", c.s, c.sub, got, c.want)
		}
	}

	if !containsBytes([]byte("12345"), "234") || containsBytes([]byte("12345"), "9") {
		t.Error("containsBytes mismatch")
	}

	if !lessFold("Apache", "bash") { // case-insensitive: a < b
		t.Error("lessFold should order Apache before bash")
	}
	if lessFold("bash", "Apache") {
		t.Error("lessFold is not antisymmetric")
	}
	if !lessFold("ab", "abc") {
		t.Error("lessFold should order a prefix first")
	}
}

func TestAppendGPUColumn(t *testing.T) {
	if got := string(appendGPU(nil, &process.Proc{GPU: -1})); got != "—" {
		t.Errorf("no GPU handle = %q, want an em dash", got)
	}
	if got := string(appendGPU(nil, &process.Proc{GPU: 0})); got != "0%" {
		t.Errorf("idle GPU client = %q, want 0%%", got)
	}
	if got := string(appendGPU(nil, &process.Proc{GPU: 37.6})); got != "38%" {
		t.Errorf("busy GPU client = %q, want 38%%", got)
	}
}

// TestProcIdentRefusesAReusedPid is the guard behind the context menu's signals
// and the Energy Saver page's memory of what it has eased off. Both hold a pid
// across time, and Linux hands pids out again.
func TestProcIdentRefusesAReusedPid(t *testing.T) {
	// Our own process is the one thing guaranteed to still be itself.
	self := identOf(os.Getpid())
	if self.start == 0 {
		t.Skip("cannot read our own start time")
	}
	if !self.same() {
		t.Error("our own process did not recognise itself")
	}

	// A pid that never existed.
	if (procIdent{pid: 1 << 30, start: 12345}).same() {
		t.Error("a pid that does not exist matched")
	}
	// The same pid, a different start time — which is exactly what a reused pid
	// looks like, and the case that would otherwise kill the wrong program.
	impostor := procIdent{pid: self.pid, start: self.start + 1}
	if impostor.same() {
		t.Error("a different start time on the same pid was accepted")
	}
	// An unreadable start time refuses rather than guessing.
	if (procIdent{pid: self.pid, start: 0}).same() {
		t.Error("a zero start time was treated as a match")
	}
}
