package process

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// countSocketsTheSlowWay is the descriptor walk as it was written before it was
// made cheap: list the directory, read every link by its full path. It is the
// reference the fast walk has to agree with.
func countSocketsTheSlowWay(t *testing.T, pid int) int {
	t.Helper()
	dir := "/proc/" + strconv.Itoa(pid) + "/fd"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if target, err := os.Readlink(dir + "/" + e.Name()); err == nil && strings.HasPrefix(target, "socket:[") {
			n++
		}
	}
	return n
}

// TestScanFDsCountsSockets checks the walk against the plain one, both for a
// process with a handful of descriptors and for one with more than a single
// getdents buffer holds — which is where a listing loop that stopped after the
// first batch, or mis-stepped between records, would undercount.
func TestScanFDsCountsSockets(t *testing.T) {
	c := New()
	defer c.closePlatform()
	self := os.Getpid()

	open := func(n int) {
		for i := 0; i < n; i++ {
			fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { syscall.Close(fd) })
		}
	}

	open(3)
	got, _, _ := c.scanFDs(self, true, false)
	if want := countSocketsTheSlowWay(t, self); got != want {
		t.Fatalf("a few descriptors: scanFDs counted %d sockets, readlink counts %d", got, want)
	}

	// A record is at least 24 bytes, so this many entries cannot fit in one
	// buffer of dents.
	many := len(c.dents)/24 + 100
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil || lim.Cur < uint64(many+100) {
		t.Skipf("descriptor limit %d is too low to open %d sockets", lim.Cur, many)
	}
	open(many)
	got, _, _ = c.scanFDs(self, true, false)
	if want := countSocketsTheSlowWay(t, self); got != want {
		t.Fatalf("%d descriptors: scanFDs counted %d sockets, readlink counts %d", many, got, want)
	}
}

// TestScanFDsOfAProcessThatIsGone checks a vanished process reads as having no
// descriptors rather than failing: processes exit between the listing and the
// walk all the time.
func TestScanFDsOfAProcessThatIsGone(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("cannot run true:", err)
	}
	c := New()
	defer c.closePlatform()
	if n, ns, drm := c.scanFDs(cmd.Process.Pid, true, true); n != 0 || ns != 0 || drm {
		t.Fatalf("exited process: got %d sockets, %d ns, drm %v", n, ns, drm)
	}
}

// TestListPIDsFindsProcesses checks the /proc listing against os.ReadDir, twice
// over, since the second pass has to rewind the descriptor the first one read
// to the end of.
func TestListPIDsFindsProcesses(t *testing.T) {
	c := New()
	defer c.closePlatform()
	for pass := 1; pass <= 2; pass++ {
		pids, ok := c.listPIDs(nil)
		if !ok {
			t.Fatalf("pass %d: listing failed", pass)
		}
		seen := make(map[int]bool, len(pids))
		for _, pid := range pids {
			if pid <= 0 {
				t.Fatalf("pass %d: listed pid %d", pass, pid)
			}
			seen[pid] = true
		}
		if !seen[os.Getpid()] || !seen[1] {
			t.Fatalf("pass %d: listing of %d processes is missing this one or init", pass, len(pids))
		}

		entries, err := os.ReadDir("/proc")
		if err != nil {
			t.Fatal(err)
		}
		missing := 0
		for _, e := range entries {
			if pid, err := strconv.Atoi(e.Name()); err == nil && !seen[pid] {
				missing++ // can happen legitimately: it started after our listing
			}
		}
		// Churn between the two listings accounts for a few; a parser that
		// skipped records would lose a large share.
		if missing > 20 {
			t.Fatalf("pass %d: %d processes in /proc were not listed (listed %d)", pass, missing, len(pids))
		}
	}
}

// TestRenamedProcessIsSeen covers the name cache: a process's name is kept from
// the last scan unless the stat line says otherwise, so a process that execs
// something else has to show its new name on the next tick.
func TestRenamedProcessIsSeen(t *testing.T) {
	cmd := exec.Command("sh", "-c", "read x; exec sleep 30")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skip("cannot run sh:", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	pid := cmd.Process.Pid

	c := New()
	defer c.closePlatform()
	nameOf := func() string {
		c.collect()
		for _, p := range c.Snapshot() {
			if p.PID == pid {
				return p.Name
			}
		}
		return ""
	}

	if got := nameOf(); got != "sh" && got != "bash" && got != "dash" {
		t.Fatalf("before exec: name %q", got)
	}
	io.WriteString(stdin, "\n")
	deadline := time.Now().Add(5 * time.Second)
	for {
		comm, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
		if strings.TrimSpace(string(comm)) == "sleep" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never exec'd sleep")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := nameOf(); got != "sleep" {
		t.Fatalf("after exec: name %q, want sleep", got)
	}
}
