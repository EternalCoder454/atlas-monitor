package stats

import (
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"atlas-monitor/internal/gpu"
)

// selfFDs counts this process's open descriptors. The collectors hold a file
// open per sampled attribute, so this is the figure that would climb if the
// lifecycle were wrong.
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
	return len(names) - 1
}

// histHead is the CPU history ring's write index, which advances once per
// sample. Stats carries no timestamp, so this is how the tests tell whether the
// collectors are still running: a moving head means fresh samples. It wraps
// every HistLen samples, which no test here runs long enough to reach.
func histHead(c *Collector) int {
	h := -1
	c.Read(func(s *Stats) {
		if s.CPU.UsageHist == nil {
			return
		}
		s.CPU.UsageHist.mu.RLock()
		h = s.CPU.UsageHist.head
		s.CPU.UsageHist.mu.RUnlock()
	})
	return h
}

// TestPauseResumeCycles drives the gate the way the window does when it is
// minimised and restored. Sampling must actually stop, actually restart, and
// leave no goroutine parked on the condition variable.
func TestPauseResumeCycles(t *testing.T) {
	c := New(gpu.NewReader())
	c.Start()
	defer c.Stop()
	time.Sleep(1200 * time.Millisecond)

	goBefore := runtime.NumGoroutine()
	fdsBefore := selfFDs(t)

	const cycles = 60
	for i := 0; i < cycles; i++ {
		c.Pause()
		time.Sleep(5 * time.Millisecond)
		c.Resume()
		time.Sleep(5 * time.Millisecond)
	}

	// Sampling must still be live: take the current reading, wait for a tick,
	// and require the collector to have produced a fresh one.
	before := histHead(c)
	time.Sleep(1800 * time.Millisecond)
	if after := histHead(c); after == before {
		t.Errorf("no sample after %d pause/resume cycles (history head stuck at %d)", cycles, before)
	}

	goAfter := runtime.NumGoroutine()
	fdsAfter := selfFDs(t)
	if goAfter > goBefore {
		t.Errorf("%d pause/resume cycles leaked %d goroutines (%d -> %d)", cycles, goAfter-goBefore, goBefore, goAfter)
	}
	if fdsAfter > fdsBefore {
		t.Errorf("%d pause/resume cycles leaked %d descriptors (%d -> %d)", cycles, fdsAfter-fdsBefore, fdsBefore, fdsAfter)
	}
}

// TestPauseActuallyStopsSampling checks Pause is not merely advisory: with the
// window hidden the collectors must stop costing syscalls entirely, not just
// stop being read.
//
// One sample is allowed to land after Pause. Each collector checks the gate and
// then blocks in a select on its ticker, so a tick that was already pending
// completes before the goroutine parks. That straggler is bounded at one per
// collector per pause, and closing the window on it would mean making the gate
// selectable for no practical gain — so the bound is what this test asserts,
// followed by silence.
func TestPauseActuallyStopsSampling(t *testing.T) {
	c := New(gpu.NewReader())
	c.SetInterval(time.Second)
	c.Start()
	defer c.Stop()
	time.Sleep(1200 * time.Millisecond)

	atPause := histHead(c)
	c.Pause()

	// Long enough for a pending tick to fire and the goroutine to park.
	time.Sleep(1600 * time.Millisecond)
	settled := histHead(c)
	if n := (settled - atPause + HistLen) % HistLen; n > 1 {
		t.Errorf("%d samples landed after Pause; at most one straggler is expected", n)
	}

	// From here nothing at all may happen.
	time.Sleep(2500 * time.Millisecond)
	if after := histHead(c); after != settled {
		t.Errorf("collector sampled while paused: history head moved %d -> %d", settled, after)
	}

	c.Resume()
	time.Sleep(1500 * time.Millisecond)
	if after := histHead(c); after == settled {
		t.Error("collector did not resume sampling")
	}
}

// TestConcurrentReadsWhileCollecting has many readers in the lock while the
// samplers write. Under -race this is what would expose a figure published
// outside the lock.
func TestConcurrentReadsWhileCollecting(t *testing.T) {
	c := New(gpu.NewReader())
	c.Start()
	defer c.Stop()

	var stop atomic.Bool
	var reads atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				c.Read(func(s *Stats) {
					// Touch everything a page would touch.
					_ = s.CPU.Usage
					_ = s.Mem.Total
					for _, d := range s.Disks {
						_ = d.Label()
						_ = d.Used
					}
					for _, n := range s.Nets {
						_ = n.Label()
						_ = n.RxRate
					}
					_ = s.GPU.Usage
					_ = s.Power.Battery.Percent
					_ = s.ActiveNet
				})
				reads.Add(1)
			}
		}()
	}
	// Reconfigure underneath them.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			c.SetInterval(time.Duration(1+i%3) * time.Second)
			if i%4 == 0 {
				c.Pause()
				c.Resume()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	time.Sleep(3 * time.Second)
	stop.Store(true)
	wg.Wait()
	t.Logf("%d concurrent reads while sampling and reconfiguring", reads.Load())
	if reads.Load() == 0 {
		t.Error("no reads completed")
	}
}

// TestStopIsIdempotent checks repeated Stops cannot double-close. Every held
// descriptor is closed there, and closing a number that has since been reused
// would take out an unrelated file.
func TestStopIsIdempotent(t *testing.T) {
	c := New(gpu.NewReader())
	c.Start()
	time.Sleep(1200 * time.Millisecond)
	c.Stop()
	c.Stop()

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

// TestStopReleasesDescriptors checks the held files are actually given back.
// 0.7.0 replaced per-tick opens with descriptors kept for the process's life, so
// this is the regression that change could have introduced.
func TestStopReleasesDescriptors(t *testing.T) {
	before := selfFDs(t)

	c := New(gpu.NewReader())
	c.Start()
	time.Sleep(1300 * time.Millisecond)
	during := selfFDs(t)
	c.Stop()
	// Nothing in Stop is asynchronous, but the GPU reader may have handed work
	// to the runtime, so allow a moment.
	time.Sleep(100 * time.Millisecond)
	after := selfFDs(t)

	t.Logf("descriptors: %d before, %d while collecting, %d after Stop", before, during, after)
	if during <= before {
		t.Errorf("collector held no extra descriptors while running (%d -> %d); the test is not measuring anything", before, during)
	}
	if after > before {
		t.Errorf("Stop left %d descriptors open (%d -> %d)", after-before, before, after)
	}
}

// TestIntervalIsHonoured checks the configurable refresh rate actually changes
// the sampling rate, in both directions.
func TestIntervalIsHonoured(t *testing.T) {
	if testing.Short() {
		t.Skip("takes ten seconds")
	}
	c := New(gpu.NewReader())
	c.SetInterval(time.Second)
	c.Start()
	defer c.Stop()

	count := func(window time.Duration) int {
		last := histHead(c)
		n := 0
		deadline := time.Now().Add(window)
		for time.Now().Before(deadline) {
			if h := histHead(c); h != last {
				last = h
				n++
			}
			time.Sleep(20 * time.Millisecond)
		}
		return n
	}

	fast := count(4 * time.Second)
	c.SetInterval(3 * time.Second)
	time.Sleep(3 * time.Second) // let the new period take effect
	slow := count(6 * time.Second)

	t.Logf("%d samples in 4s at 1s, %d samples in 6s at 3s", fast, slow)
	if fast < 3 {
		t.Errorf("only %d samples in 4s at a 1s interval", fast)
	}
	if slow > fast {
		t.Errorf("a 3s interval produced %d samples in 6s, more than %d in 4s at 1s", slow, fast)
	}
}

// TestIntervalFloor checks a caller cannot drive the collectors faster than once
// a second. Every page's cost is per-sample, so this is the guard that keeps a
// bad config from pinning a core.
func TestIntervalFloor(t *testing.T) {
	c := New(gpu.NewReader())
	for _, d := range []time.Duration{0, -time.Hour, time.Millisecond, 999 * time.Millisecond} {
		c.SetInterval(d)
		if got := c.period(); got < time.Second {
			t.Errorf("SetInterval(%v) gave a period of %v, below the one-second floor", d, got)
		}
	}
	c.SetInterval(5 * time.Second)
	if got := c.period(); got != 5*time.Second {
		t.Errorf("SetInterval(5s) gave %v", got)
	}
}
