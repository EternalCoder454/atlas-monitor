// Package process collects per-process statistics from /proc/[pid]/*. The
// collector only runs while the Apps view is visible, since scanning every
// process each second is the most expensive sampling we do.
package process

// The process table: what is running, how hard, and with what.
//
// This file is the part that is the same on both platforms — the collector's
// lifecycle, the snapshot the UI reads, and the arithmetic that turns two
// cumulative counters into a rate. Where the list itself comes from is not
// shared at all: see process_linux.go, which walks /proc, and
// process_windows.go, which gets the whole table from one system call.

import (
	"sync"
	"sync/atomic"
	"time"

	"atlas-monitor/internal/sysfs"
)

// netScanThreshold is the combined rx+tx rate (bytes/sec) below which the
// per-process socket scan is skipped. Enumerating every process's /proc/fd
// links is expensive, and with no traffic there is nothing to attribute.
const netScanThreshold = 8192

// Proc is a single process snapshot for the Apps table.
type Proc struct {
	PID    int
	Name   string
	Kernel bool // a kernel thread (kworker, ksoftirqd, …) rather than a program
	// Unit is the systemd unit the process runs in — "app-org.kde.dolphin@….service",
	// "pipewire.service" — or "" where there is none (Windows, a container, a
	// kernel thread). Desktops start each application in a unit of its own, so
	// this is what tells one application's processes from another's; see
	// internal/desktop for turning it into a name.
	Unit string
	// Count is how many processes this entry stands for, set by callers that
	// fold several into one. Zero for a single process.
	Count     int
	CPU       float64 // percent of one core (may exceed 100 for threaded procs)
	RSS       uint64  // resident bytes
	GPU       float64 // percent of GPU engine time; -1 if the process holds no GPU handle
	NetIn     float64 // estimated bytes/sec
	NetOut    float64 // estimated bytes/sec
	DiskRead  float64 // bytes/sec
	DiskWrite float64 // bytes/sec
}

type procPrev struct {
	name                  string
	unit                  string
	cpuJiffies            uint64
	readBytes, writeBytes uint64
}

// Collector samples all processes on a 1s ticker between Start and Stop.
// statLine is what one /proc/[pid]/stat line tells us. It is declared here
// rather than beside its parser because the Collector holds one as scratch, and
// the Collector is shared between platforms.
//
// name aliases the read buffer rather than being a string: three quarters of the
// processes in /proc are kernel threads that are then discarded, and building a
// string for each of those was the largest remaining allocation in the scan. The
// caller copies it only for a process it is going to report, and must do so before
// the next read reuses the buffer.
type statLine struct {
	name    []byte
	jiffies uint64 // utime + stime
	kernel  bool
}

type Collector struct {
	mu    sync.RWMutex
	procs []Proc

	prev      map[int]procPrev
	prevSpare map[int]procPrev // double-buffered with prev; avoids a per-tick map alloc
	lastTime  time.Time

	prevNetRx, prevNetTx uint64
	lastNetTime          time.Time

	// Per-process network attribution is throttled and its result carried
	// forward between scans (it is only a rough estimate).
	scanCounter  int
	lastNet      map[int][2]float64
	lastNetSpare map[int][2]float64 // double-buffered with lastNet

	// Per-process GPU load via DRM fdinfo. Known GPU-client pids are scanned
	// every tick; the full process set is rescanned periodically to find new ones.
	gpuPrev        map[int]uint64
	gpuPrevSpare   map[int]uint64
	gpuPids        map[int]bool
	gpuPidsSpare   map[int]bool
	gpuScanCounter int

	// unitScanCounter paces the re-reading of every process's unit; see
	// unitRescanTicks.
	unitScanCounter int

	// What the UI is actually showing. Both default to true so a caller that
	// never says otherwise gets every figure, as before.
	wantDiskIO atomic.Bool
	wantGPU    atomic.Bool
	wantNet    atomic.Bool

	// Scratch reused by collect: the process list under construction and the
	// per-pid socket counts. Only the sampling goroutine touches them.
	scratch []Proc
	sockets map[int]int

	// procFD is a descriptor held on /proc so per-process files can be opened
	// with openat and a relative path. buf is reused for every read, path for
	// building those relative paths, link for readlink(2) on fd entries, dents
	// for directory listings and pids for the list of processes they produce.
	//
	// These are the Linux scan's working state. Windows needs none of it — one
	// system call returns the whole table — and leaves them unused; see
	// closePlatform, which is where the difference is handled.
	procFD int
	netDev *sysfs.File // /proc/net/dev, held open for the per-tick total
	buf    []byte
	path   []byte
	link   []byte
	dents  []byte
	pids   []int

	// gpuClients is the set of drm-client-ids one process's descriptor walk has
	// already counted, reused from one process to the next.
	gpuClients map[uint64]bool

	// interval is the sampling period in nanoseconds, read atomically.
	interval atomic.Int64

	// includeKernel mirrors the Apps table's "Kernel threads" toggle. When it
	// is off — the default — kernel threads are dropped as soon as the stat
	// line identifies them, which is most of the scan.
	includeKernel atomic.Bool

	// stat is the scratch the Linux scan parses one process into, reused across
	// processes so a scan allocates only when a process name changes. Unused on
	// Windows, along with the descriptors above it.
	stat statLine

	runMu   sync.Mutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// New returns an idle collector.
func New() *Collector {
	c := &Collector{
		prev:         make(map[int]procPrev),
		prevSpare:    make(map[int]procPrev),
		lastNet:      make(map[int][2]float64),
		lastNetSpare: make(map[int][2]float64),
		gpuPrev:      make(map[int]uint64),
		gpuPrevSpare: make(map[int]uint64),
		gpuPids:      make(map[int]bool),
		gpuPidsSpare: make(map[int]bool),
		sockets:      make(map[int]int),
		buf:          make([]byte, 8192),
		path:         make([]byte, 0, 32),
		link:         make([]byte, 256),
		dents:        make([]byte, 16384),
		gpuClients:   make(map[uint64]bool),
		procFD:       -1,
	}
	c.interval.Store(int64(time.Second))
	c.wantDiskIO.Store(true)
	c.wantGPU.Store(true)
	c.wantNet.Store(true)
	return c
}

// SetIncludeKernel controls whether kernel threads are collected at all. It is
// safe to call from the UI thread; the next tick picks it up.
func (c *Collector) SetIncludeKernel(include bool) { c.includeKernel.Store(include) }

// SetWantDiskIO says whether anything is displaying per-process disk figures.
//
// They cost an open, a read and a parse of /proc/[pid]/io for every process on
// every tick — about a tenth of the whole scan — and the columns that show them
// are hidden by default, because on any machine with room for a page cache they
// read zero for nearly everything. Collecting them for nobody to look at is the
// one piece of this scan that buys nothing at all.
//
// Safe from the UI thread; the next tick picks it up.
func (c *Collector) SetWantDiskIO(want bool) { c.wantDiskIO.Store(want) }

// SetWantGPU says whether anything is displaying per-process GPU load. Walking
// a process's open descriptors to find DRM handles is the most expensive part
// of the scan after the stat reads; on a machine with no GPU column on screen
// there is nothing to spend it on.
func (c *Collector) SetWantGPU(want bool) { c.wantGPU.Store(want) }

// SetWantNet says whether anything is displaying per-process network figures.
// Attributing traffic to a process means enumerating its open sockets, which is
// the most expensive thing this scan does; with the columns put away there is
// nothing to spend it on.
func (c *Collector) SetWantNet(want bool) { c.wantNet.Store(want) }

// SetInterval changes how often the process list is sampled. It takes effect
// within one tick of the current period.
func (c *Collector) SetInterval(d time.Duration) {
	if d < time.Second {
		d = time.Second
	}
	c.interval.Store(int64(d))
}

func (c *Collector) period() time.Duration { return time.Duration(c.interval.Load()) }

// Start launches the sampling goroutine if not already running.
func (c *Collector) Start() {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if c.running {
		return
	}
	c.running = true
	c.stopCh = make(chan struct{})
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		t := time.NewTicker(c.period())
		defer t.Stop()
		c.collect()
		for {
			select {
			case <-c.stopCh:
				return
			case <-t.C:
				c.collect()
				t.Reset(c.period())
			}
		}
	}()
}

// Stop halts sampling and waits for the goroutine to exit.
func (c *Collector) Stop() {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	close(c.stopCh)
	c.wg.Wait()
	c.closePlatform()
}

// Snapshot returns a copy of the latest process list.
func (c *Collector) Snapshot() []Proc { return c.SnapshotInto(nil) }

// SnapshotInto copies the latest process list into dst, growing it only when
// the process count rises. The UI holds one buffer and reuses it every second,
// so a 700-process machine stops churning ~55 KiB of garbage per tick.
func (c *Collector) SnapshotInto(dst []Proc) []Proc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cap(dst) < len(c.procs) {
		// Headroom, so a process count that wobbles from tick to tick does not
		// reallocate the caller's buffer every time it ticks up by one.
		dst = make([]Proc, len(c.procs), len(c.procs)+len(c.procs)/4+16)
	}
	dst = dst[:len(c.procs)]
	copy(dst, c.procs)
	return dst
}

// View calls fn with the latest process list while holding the collector's read
// lock, so a caller that only reads a few fields of each process does not pay to
// copy them all. fn must not keep the slice or write to it, and must be quick:
// the next scan waits for it.
func (c *Collector) View(fn func([]Proc)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fn(c.procs)
}

func clampPct(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func deltaRate(cur, prev uint64, dt float64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}
