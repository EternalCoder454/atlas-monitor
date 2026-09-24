package process

import (
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A system monitor is unusual in that its input changes while it is being read:
// /proc entries appear and vanish mid-scan, and the UI thread reconfigures the
// collector from under it. These tests drive both at once.

// selfFDs counts this process's open descriptors.
func selfFDs(t *testing.T) int {
	t.Helper()
	f, err := os.Open("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd: %v", err)
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		t.Skipf("cannot list /proc/self/fd: %v", err)
	}
	return len(names) - 1 // the handle used to read the directory
}

// churn spawns short-lived processes until stop is closed, so the scan sees
// entries in /proc that are gone by the time it tries to open their files.
func churn(t *testing.T, stop <-chan struct{}) *sync.WaitGroup {
	t.Helper()
	sh, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not installed")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// A process that exists for a few milliseconds is the interesting
			// case: long enough to be listed, short enough to be gone before
			// its stat, statm and io files are read.
			cmd := exec.Command(sh, "0.01")
			if cmd.Start() != nil {
				return
			}
			go func() { _ = cmd.Wait() }()
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return &wg
}

// TestChurnDuringScan runs the collector against a /proc that is changing
// underneath it. Every process the scan lists may be gone before its files are
// opened, which is the case the whole slurpPID path has to tolerate.
func TestChurnDuringScan(t *testing.T) {
	stop := make(chan struct{})
	wg := churn(t, stop)
	defer func() { close(stop); wg.Wait() }()

	c := New()
	c.collect() // prime
	deadline := time.Now().Add(4 * time.Second)
	scans := 0
	for time.Now().Before(deadline) {
		c.collect()
		scans++
		for _, p := range c.Snapshot() {
			if p.PID <= 0 {
				t.Fatalf("scan %d produced pid %d", scans, p.PID)
			}
			if p.Name == "" {
				t.Fatalf("scan %d produced pid %d with an empty name", scans, p.PID)
			}
			if p.CPU < 0 || p.DiskRead < 0 || p.DiskWrite < 0 || p.NetIn < 0 || p.NetOut < 0 {
				t.Fatalf("scan %d pid %d has a negative rate: %+v", scans, p.PID, p)
			}
		}
	}
	t.Logf("%d scans against a churning /proc", scans)
	if scans < 10 {
		t.Errorf("only %d scans completed in 4s", scans)
	}
}

// TestPerPIDStateBoundedUnderChurn is the leak test for the collector's own
// bookkeeping. Every per-process map is double-buffered and refilled from the
// live process set each tick, so once churn stops the maps must fall back to the
// size of that set rather than remembering every PID that ever existed.
func TestPerPIDStateBoundedUnderChurn(t *testing.T) {
	c := New()
	c.SetIncludeKernel(true)
	c.collect()
	base := len(c.prev)

	stop := make(chan struct{})
	wg := churn(t, stop)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		c.collect()
	}
	close(stop)
	wg.Wait()

	// Let the spawned processes finish, then scan twice so the double buffer
	// turns over completely.
	time.Sleep(300 * time.Millisecond)
	c.collect()
	c.collect()

	live := len(c.Snapshot())
	for name, n := range map[string]int{
		"prev":      len(c.prev),
		"prevSpare": len(c.prevSpare),
		"gpuPrev":   len(c.gpuPrev),
		"gpuPids":   len(c.gpuPids),
		"lastNet":   len(c.lastNet),
		"sockets":   len(c.sockets),
	} {
		// Generous headroom for processes that come and go on their own; what
		// is being caught is unbounded growth, not a handful of entries.
		if n > live+base/2+64 {
			t.Errorf("%s holds %d entries after churn; %d processes are live", name, n, live)
		}
	}
	t.Logf("after churn: %d live processes, prev=%d gpuPrev=%d gpuPids=%d lastNet=%d sockets=%d",
		live, len(c.prev), len(c.gpuPrev), len(c.gpuPids), len(c.lastNet), len(c.sockets))
}

// TestConcurrentReconfiguration hammers every method the UI thread can call
// while the sampling goroutine is running. Run under -race, this is the test
// that would catch an unsynchronised field.
func TestConcurrentReconfiguration(t *testing.T) {
	c := New()
	c.Start()
	defer c.Stop()

	var stop atomic.Bool
	var wg sync.WaitGroup
	worker := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				f(i)
				time.Sleep(time.Millisecond)
			}
		}()
	}

	worker(func(i int) { c.SetIncludeKernel(i%2 == 0) })
	worker(func(i int) { c.SetInterval(time.Duration(1+i%3) * time.Second) })
	var buf []Proc
	var bufMu sync.Mutex
	worker(func(int) {
		bufMu.Lock()
		buf = c.SnapshotInto(buf)
		bufMu.Unlock()
	})
	worker(func(int) { _ = c.Snapshot() })
	worker(func(int) { c.Start() }) // must be a no-op while running

	time.Sleep(3 * time.Second)
	stop.Store(true)
	wg.Wait()

	if n := len(c.Snapshot()); n == 0 {
		t.Error("collector produced no processes after being reconfigured throughout")
	}
}

// TestStartStopCyclesReleaseEverything checks the descriptors and the goroutine
// are given back each time. The collector is started and stopped whenever the
// Apps or Assistant page is shown or hidden, so a few descriptors kept per cycle
// would exhaust the process over a long session.
func TestStartStopCyclesReleaseEverything(t *testing.T) {
	c := New()
	// One warm-up cycle so lazily-created state is not counted as a leak.
	c.Start()
	time.Sleep(50 * time.Millisecond)
	c.Stop()

	fdsBefore := selfFDs(t)
	goBefore := runtime.NumGoroutine()

	const cycles = 40
	for i := 0; i < cycles; i++ {
		c.Start()
		time.Sleep(10 * time.Millisecond)
		c.Stop()
	}

	// Goroutines are joined by Stop, so no settling time is needed for them.
	fdsAfter := selfFDs(t)
	goAfter := runtime.NumGoroutine()
	if fdsAfter > fdsBefore {
		t.Errorf("%d start/stop cycles leaked %d descriptors (%d -> %d)", cycles, fdsAfter-fdsBefore, fdsBefore, fdsAfter)
	}
	if goAfter > goBefore {
		t.Errorf("%d start/stop cycles leaked %d goroutines (%d -> %d)", cycles, goAfter-goBefore, goBefore, goAfter)
	}
	if n := len(c.Snapshot()); n == 0 {
		t.Error("collector produced nothing after the cycles")
	}
}

// TestStopIsIdempotent checks a double Stop cannot close a descriptor twice —
// which, once the number has been handed to something else, would close an
// unrelated file.
func TestStopIsIdempotent(t *testing.T) {
	c := New()
	c.Start()
	time.Sleep(50 * time.Millisecond)
	c.Stop()
	c.Stop()
	c.Stop()

	// A sentinel descriptor: if a redundant Stop closed a recycled number, this
	// is what it would hit.
	f, err := os.Open("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c.Stop()
	if _, err := f.Stat(); err != nil {
		t.Errorf("a repeated Stop closed an unrelated descriptor: %v", err)
	}
}

// TestSnapshotIntoAliasing checks the caller's buffer is never left aliasing the
// collector's own slice. The Apps view keeps one buffer forever and reads from it
// while the next scan runs, so a shared backing array would be a data race and a
// torn table.
func TestSnapshotIntoAliasing(t *testing.T) {
	c := New()
	c.collect()
	buf := c.SnapshotInto(nil)
	if len(buf) == 0 {
		t.Skip("no processes")
	}
	// Scribble on the caller's copy, then take another scan and check the
	// collector's data was not modified through the shared array.
	for i := range buf {
		buf[i].PID = -1
		buf[i].Name = "scribbled"
	}
	c.collect()
	for _, p := range c.Snapshot() {
		if p.PID == -1 || p.Name == "scribbled" {
			t.Fatal("writing to a SnapshotInto buffer modified the collector's process list")
		}
	}
}
