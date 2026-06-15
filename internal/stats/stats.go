// Package stats reads system statistics from /proc and /sys. One goroutine per
// subsystem samples on a 1-second ticker and writes into a shared, mutex-
// protected Stats struct. Collection pauses (0% CPU) when the window is hidden.
package stats

import (
	"sync"
	"time"

	"atlas-monitor/internal/gpu"
)

// CoreStat holds one logical CPU core's usage.
type CoreStat struct {
	Usage float64 // percent 0..100
}

// CPUStats is the aggregate + per-core CPU state.
type CPUStats struct {
	Usage     float64
	UsageHist *RingBuffer
	Cores     []CoreStat
	CurFreq   float64 // MHz, max across cores (turbo)
	Temp      float64 // °C, -1 if unavailable

	// Static information, filled once at startup.
	Model     string
	Sockets   int
	PhysCores int
	Logical   int
	BaseFreq  float64 // MHz, 0 if unknown
	L1d, L1i  string
	L2, L3    string
}

// MemStats mirrors the relevant /proc/meminfo fields (bytes).
type MemStats struct {
	Total, Used, Cached, Available, Free uint64
	SwapTotal, SwapUsed                  uint64
	UsageHist                            *RingBuffer // RAM used %
	SwapHist                             *RingBuffer // swap used %
}

// DiskStats is one block device's space + throughput.
type DiskStats struct {
	Name                  string
	Model                 string // e.g. "Samsung SSD 970 EVO Plus 1TB"
	IsSwap                bool   // zram / compressed-RAM swap device
	IsRoot                bool   // hosts the root (/) filesystem — the primary disk
	SizeBytes, Used, Free uint64
	ReadTotal, WriteTotal uint64  // cumulative bytes
	ReadRate, WriteRate   float64 // bytes/sec
	ReadHist, WriteHist   *RingBuffer

	mounts              []string
	prevRead, prevWrite uint64
	havePrev            bool
}

// Label is the human-friendly device name: "Swap" for zram, the hardware model
// when known, otherwise the kernel device node.
func (d *DiskStats) Label() string {
	if d.IsSwap {
		return "Swap"
	}
	if d.Model != "" {
		return d.Model
	}
	return d.Name
}

// NetStats is one interface's addresses + throughput.
type NetStats struct {
	Name, Display         string // Display is a friendly label, e.g. "Wi-Fi"
	MAC, IPv4, IPv6       string
	SpeedMbit             int // -1 if unknown
	RxTotal, TxTotal      uint64
	RxRate, TxRate        float64 // bytes/sec
	DownHist, UpHist      *RingBuffer

	prevRx, prevTx uint64
	havePrev       bool
}

// Label is the friendly interface name, falling back to the kernel name.
func (n *NetStats) Label() string {
	if n.Display != "" {
		return n.Display
	}
	return n.Name
}

// GPUStats is the AMD GPU state (populated only when a GPU is detected).
type GPUStats struct {
	Available                bool
	Name                     string
	Usage                    float64
	UsageHist                *RingBuffer
	VramUsed, VramTotal      uint64
	GttUsed                  uint64
	VramHist                 *RingBuffer // VRAM used %
	Temp                     float64
	FanRPM                   int
	PowerW                   float64
	GpuClockMHz, MemClockMHz float64
}

// Stats is the shared snapshot. The embedded RWMutex guards all scalar fields.
// Ring buffers carry their own locks and may be read without holding mu.
type Stats struct {
	mu        sync.RWMutex
	CPU       CPUStats
	Mem       MemStats
	Disks     []*DiskStats
	Nets      []*NetStats
	GPU       GPUStats
	ActiveNet string // kernel name of the default-route interface (computed off the UI thread)
}

// gate implements the pause/resume mechanism. Collectors block in wait() while
// paused, consuming no CPU.
type gate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	paused bool
}

func newGate() *gate {
	g := &gate{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// Collector owns the Stats struct and the sampling goroutines.
type Collector struct {
	stats  *Stats
	gpu    *gpu.Reader
	gate   *gate
	stopCh chan struct{}
	wg     sync.WaitGroup

	started bool

	// Per-collector previous samples / timestamps.
	cpuPrev  map[string]cpuTimes
	diskLast time.Time
	netLast  time.Time
	tempPath string

	netTick int // collectNets tick counter; throttles the per-interface address refresh

	// collectCPU scratch — only the CPU goroutine touches these, so no locking.
	cpuFreqPaths []string    // precomputed /sys cpufreq paths, one per logical core
	cpuStatBuf   []byte      // reused /proc/stat scan buffer (avoids per-tick line allocs)
	cpuSamples   []cpuSample // reused /proc/stat parse results
}

// New creates a Collector. gpuReader may report Available()==false.
func New(gpuReader *gpu.Reader) *Collector {
	return &Collector{
		stats:   &Stats{},
		gpu:     gpuReader,
		gate:    newGate(),
		stopCh:  make(chan struct{}),
		cpuPrev: make(map[string]cpuTimes),
	}
}

// Read runs f while holding the read lock. f must not block.
func (c *Collector) Read(f func(*Stats)) {
	c.stats.mu.RLock()
	defer c.stats.mu.RUnlock()
	f(c.stats)
}

func (c *Collector) write(f func(*Stats)) {
	c.stats.mu.Lock()
	defer c.stats.mu.Unlock()
	f(c.stats)
}

// Start discovers devices, captures static info, and launches one goroutine
// per subsystem. Safe to call once.
func (c *Collector) Start() {
	if c.started {
		return
	}
	c.started = true

	c.initCPUStatic()
	c.discoverDisks()
	c.discoverNets()
	c.initMem()
	c.initGPU()

	c.launch(c.collectCPU)
	c.launch(c.collectMem)
	c.launch(c.collectDisks)
	c.launch(c.collectNets)
	if c.stats.GPU.Available {
		c.launch(c.collectGPU)
	}
}

// Stop halts all goroutines and waits for them to exit.
func (c *Collector) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	c.gate.mu.Lock()
	c.gate.cond.Broadcast()
	c.gate.mu.Unlock()
	c.wg.Wait()
}

// Pause stops sampling. Goroutines block until Resume.
func (c *Collector) Pause() {
	c.gate.mu.Lock()
	c.gate.paused = true
	c.gate.mu.Unlock()
}

// Resume restarts sampling.
func (c *Collector) Resume() {
	c.gate.mu.Lock()
	c.gate.paused = false
	c.gate.cond.Broadcast()
	c.gate.mu.Unlock()
}

// launch runs collect on a 1s ticker, blocking while paused or returning on
// Stop. collect is invoked once immediately so the UI has data at startup.
func (c *Collector) launch(collect func()) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		t := time.NewTicker(time.Second)
		defer t.Stop()
		if !c.waitResumed() {
			return
		}
		collect()
		for {
			if !c.waitResumed() {
				return
			}
			select {
			case <-c.stopCh:
				return
			case <-t.C:
				collect()
			}
		}
	}()
}

// waitResumed blocks while paused and returns false if the collector is
// stopping.
func (c *Collector) waitResumed() bool {
	c.gate.mu.Lock()
	for c.gate.paused && !c.isStopped() {
		c.gate.cond.Wait()
	}
	stopped := c.isStopped()
	c.gate.mu.Unlock()
	return !stopped
}

func (c *Collector) isStopped() bool {
	select {
	case <-c.stopCh:
		return true
	default:
		return false
	}
}
