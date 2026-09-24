package process

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestStatRSSDiffersFromStatm records why the scan still opens
// /proc/[pid]/statm for every process it reports, instead of taking RSS from
// field 24 of the stat line it has already read.
//
// The two do not agree — on this machine they differ for roughly a quarter of
// processes, by around 8% — and statm is the figure ps, top and htop report.
// Saving one open per process is not worth every memory number in the app
// disagreeing with every other tool. If this test ever starts passing, the
// kernel has changed and the cheaper read becomes available.
func TestStatRSSDiffersFromStatm(t *testing.T) {
	c := New()
	pids, err := readProcPIDs()
	if err != nil {
		t.Skip("cannot read /proc")
	}

	checked, mismatches := 0, 0
	for _, pid := range pids {
		var st statLine
		if !c.readStat(pid, &st) {
			continue
		}
		fromStat, ok := statFieldRSS(pid)
		if !ok {
			continue
		}
		want, ok := statmRSS(pid)
		if !ok {
			continue // exited between the two reads
		}
		checked++
		if fromStat != want {
			mismatches++
		}
	}
	if checked < 20 {
		t.Skipf("only %d processes could be compared", checked)
	}
	t.Logf("compared %d processes: %d disagree between stat field 24 and statm", checked, mismatches)
	if mismatches == 0 {
		t.Log("they agree on this kernel — RSS could now come from the stat line " +
			"already being read, saving one open per process per tick")
	}
}

// statFieldRSS reads RSS the cheaper way, from /proc/[pid]/stat field 24.
func statFieldRSS(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	rp := strings.LastIndex(string(b), ")")
	if rp < 0 || rp+2 > len(b) {
		return 0, false
	}
	f := strings.Fields(string(b)[rp+2:])
	if len(f) < 22 {
		return 0, false
	}
	v, err := strconv.ParseUint(f[21], 10, 64) // field 24
	if err != nil {
		return 0, false
	}
	return v * pageSize, true
}

// TestReadStatFields checks the rest of what one stat line has to yield.
func TestReadStatFields(t *testing.T) {
	c := New()
	var st statLine
	if !c.readStat(os.Getpid(), &st) {
		t.Fatal("could not read our own stat line")
	}
	t.Logf("self: name=%q jiffies=%d kernel=%v", string(st.name), st.jiffies, st.kernel)

	if st.kernel {
		t.Error("the test process is not a kernel thread")
	}
	if len(st.name) == 0 {
		t.Error("our own name did not parse")
	}
	if rss := c.readRSS(os.Getpid()); rss < 1<<20 || rss > 1<<40 {
		t.Errorf("our own RSS reads as %d, which is implausible", rss)
	}
	if !c.readStat(1, &st) || len(st.name) == 0 {
		t.Error("pid 1 did not parse")
	}
}

// TestReadStatNameAliasesBuffer documents the contract the scan relies on: the
// name points into the read buffer and is only valid until the next read, so a
// caller that keeps a process must copy it first.
func TestReadStatNameAliasesBuffer(t *testing.T) {
	c := New()
	var st statLine
	self := os.Getpid()
	if !c.readStat(self, &st) {
		t.Fatal("could not read our own stat line")
	}
	name := string(st.name) // the copy a caller has to make
	if name == "" {
		t.Fatal("no name parsed")
	}
	// Reading another process reuses the buffer; the earlier slice may now show
	// anything, but the copy must be unaffected.
	c.readStat(1, &st)
	if name == "" || name != string([]byte(name)) {
		t.Error("the copied name did not survive a second read")
	}
	for i := 0; i < 3; i++ {
		if !c.readStat(self, &st) || string(st.name) != name {
			t.Fatalf("re-reading gave %q, want %q", string(st.name), name)
		}
	}
}

// statmRSS reads resident bytes the old way, from /proc/[pid]/statm.
func statmRSS(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0, false
	}
	v, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return v * pageSize, true
}

// readProcPIDs lists the process IDs currently in /proc.
func readProcPIDs() ([]int, error) {
	f, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(names))
	for _, n := range names {
		if pid, err := strconv.Atoi(n); err == nil {
			out = append(out, pid)
		}
	}
	return out, nil
}
