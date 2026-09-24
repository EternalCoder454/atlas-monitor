package process

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// psRow is one line of `ps` output: the reference figures we check ourselves
// against.
type psRow struct {
	rss  uint64 // bytes
	comm string
}

// readPS shells out to ps for every process it can see. ps reads
// /proc/<pid>/statm for RSS, which is the same file readRSS uses — see
// TestStatRSSDiffersFromStatm for why the stat field is not interchangeable.
func readPS(t *testing.T) map[int]psRow {
	t.Helper()
	ps, err := exec.LookPath("ps")
	if err != nil {
		t.Skip("ps not installed")
	}
	out, err := exec.Command(ps, "-eo", "pid=,rss=,comm=").Output()
	if err != nil {
		t.Skipf("ps failed: %v", err)
	}
	rows := make(map[int]psRow, 512)
	for _, line := range strings.Split(string(out), "\n") {
		// comm can contain spaces ("npm exec @wonde"), so only the first two
		// columns may be split on whitespace — the rest of the line is the name.
		rest := strings.TrimLeft(line, " ")
		pidTok, rest := cutToken(rest)
		rssTok, rest := cutToken(rest)
		comm := strings.TrimSpace(rest)
		pid, err1 := strconv.Atoi(pidTok)
		kb, err2 := strconv.ParseUint(rssTok, 10, 64)
		if err1 != nil || err2 != nil || comm == "" {
			continue
		}
		rows[pid] = psRow{rss: kb * 1024, comm: comm}
	}
	return rows
}

// cutToken splits off the first whitespace-delimited token, returning it and
// the remainder with its leading whitespace removed.
func cutToken(s string) (tok, rest string) {
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " ")
}

// TestRSSAgreesWithPS is the headline correctness check: the RSS column in the
// Apps table has to be the number ps and every other tool on the machine
// reports, or the app is lying. Processes are allowed to grow or shrink between
// our sample and ps's, so a per-process mismatch is not a failure on its own —
// a systematic one is.
func TestRSSAgreesWithPS(t *testing.T) {
	c := New()
	c.collect()
	got := c.Snapshot()
	ref := readPS(t)

	var compared, agree, off int
	for _, p := range got {
		r, ok := ref[p.PID]
		if !ok {
			continue // started or exited between the two scans
		}
		compared++
		diff := int64(p.RSS) - int64(r.rss)
		if diff < 0 {
			diff = -diff
		}
		// 4 MiB or 10%, whichever is larger: a busy process really can move
		// that much in the milliseconds between the two reads.
		tol := int64(r.rss / 10)
		if tol < 4<<20 {
			tol = 4 << 20
		}
		if diff <= tol {
			agree++
		} else {
			off++
			if off <= 5 {
				t.Logf("RSS mismatch pid=%d %s: atlas=%d ps=%d (diff %d)", p.PID, p.Name, p.RSS, r.rss, diff)
			}
		}
	}
	if compared < 5 {
		t.Skipf("only %d processes visible in both views, nothing to compare", compared)
	}
	if ratio := float64(agree) / float64(compared); ratio < 0.95 {
		t.Errorf("RSS agrees with ps for only %d/%d processes (%.0f%%); want >=95%%", agree, compared, ratio*100)
	} else {
		t.Logf("RSS agrees with ps for %d/%d processes (%.1f%%)", agree, compared, ratio*100)
	}
}

// TestNamesAgreeWithPS checks the Name column against ps's comm. Names come
// out of the parenthesised field of /proc/<pid>/stat, which is truncated to 15
// bytes by the kernel — exactly what ps prints — so these should match
// character for character.
func TestNamesAgreeWithPS(t *testing.T) {
	c := New()
	c.collect()
	got := c.Snapshot()
	ref := readPS(t)

	var compared, match, logged int
	for _, p := range got {
		r, ok := ref[p.PID]
		if !ok {
			continue
		}
		compared++
		if p.Name == r.comm {
			match++
			continue
		}
		if logged < 5 {
			logged++
			t.Logf("name mismatch pid=%d: atlas=%q ps=%q", p.PID, p.Name, r.comm)
		}
	}
	if compared < 5 {
		t.Skipf("only %d processes visible in both views", compared)
	}
	// A process that execs between the two scans legitimately changes name.
	if ratio := float64(match) / float64(compared); ratio < 0.98 {
		t.Errorf("names agree with ps for only %d/%d (%.0f%%); want >=98%%", match, compared, ratio*100)
	} else {
		t.Logf("names agree with ps for %d/%d (%.1f%%)", match, compared, ratio*100)
	}
}

// TestPIDCoverage checks we are not silently dropping processes. ps sees kernel
// threads, so the comparison runs with the kernel toggle on; the remaining
// difference should only be processes that started or exited between scans.
func TestPIDCoverage(t *testing.T) {
	c := New()
	c.SetIncludeKernel(true)
	c.collect()
	got := c.Snapshot()
	ref := readPS(t)

	ours := make(map[int]bool, len(got))
	for _, p := range got {
		ours[p.PID] = true
	}
	var missing int
	for pid := range ref {
		if !ours[pid] {
			missing++
			if missing <= 5 {
				t.Logf("ps sees pid=%d %s, we do not", pid, ref[pid].comm)
			}
		}
	}
	if len(ref) == 0 {
		t.Skip("ps returned nothing")
	}
	// ps itself, and the shell that ran it, are gone by the time we look.
	if ratio := float64(missing) / float64(len(ref)); ratio > 0.05 {
		t.Errorf("missing %d of %d PIDs ps reported (%.0f%%)", missing, len(ref), ratio*100)
	} else {
		t.Logf("saw %d of %d PIDs ps reported (%d missing)", len(ref)-missing, len(ref), missing)
	}
}

// TestCPUWithinPhysicalLimits checks the CPU column cannot report more work
// than the machine is capable of doing. A single threaded process may exceed
// 100% of one core, but the whole system cannot exceed ncpu*100.
func TestCPUWithinPhysicalLimits(t *testing.T) {
	c := New()
	c.Start()
	defer c.Stop()
	time.Sleep(2200 * time.Millisecond)
	got := c.Snapshot()

	ncpu := runtime.NumCPU()
	ceiling := float64(ncpu) * 100
	var total float64
	for _, p := range got {
		if p.CPU < 0 {
			t.Errorf("pid=%d %s reports negative CPU %.2f", p.PID, p.Name, p.CPU)
		}
		if p.CPU > ceiling+1 {
			t.Errorf("pid=%d %s reports %.1f%% CPU, above the %d-core ceiling of %.0f%%",
				p.PID, p.Name, p.CPU, ncpu, ceiling)
		}
		total += p.CPU
	}
	// Allow headroom: our sample window and the kernel's jiffy accounting do
	// not line up exactly, and rounding is per-process.
	if total > ceiling*1.1+10 {
		t.Errorf("process CPU sums to %.1f%%, above the %d-core ceiling of %.0f%%", total, ncpu, ceiling)
	}
	t.Logf("%d processes, CPU sums to %.1f%% of a %.0f%% ceiling", len(got), total, ceiling)
}

// TestRatesNonNegative checks every derived rate. These are all deltas divided
// by elapsed time, and a counter that wraps or resets (a vanished interface, a
// process whose io file becomes unreadable) must not produce a negative rate in
// the table.
func TestRatesNonNegative(t *testing.T) {
	c := New()
	c.Start()
	defer c.Stop()
	time.Sleep(2200 * time.Millisecond)

	for _, p := range c.Snapshot() {
		if p.NetIn < 0 || p.NetOut < 0 {
			t.Errorf("pid=%d %s negative net rate: in=%.1f out=%.1f", p.PID, p.Name, p.NetIn, p.NetOut)
		}
		if p.DiskRead < 0 || p.DiskWrite < 0 {
			t.Errorf("pid=%d %s negative disk rate: r=%.1f w=%.1f", p.PID, p.Name, p.DiskRead, p.DiskWrite)
		}
		// GPU is -1 by design for processes holding no GPU handle.
		if p.GPU < 0 && p.GPU != -1 {
			t.Errorf("pid=%d %s bad GPU value %.2f (want >=0 or exactly -1)", p.PID, p.Name, p.GPU)
		}
		if p.GPU > 100.1 {
			t.Errorf("pid=%d %s GPU %.1f%% exceeds 100%%", p.PID, p.Name, p.GPU)
		}
	}
}
