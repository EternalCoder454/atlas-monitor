// Package process collects per-process statistics from /proc/[pid]/*. The
// collector only runs while the Apps view is visible, since scanning every
// process each second is the most expensive sampling we do.
package process

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// clockTick is USER_HZ (jiffies per second); 100 on essentially all Linux/x86.
const clockTick = 100.0

// pageSize is used to convert statm resident pages to bytes.
const pageSize = 4096

// netScanThreshold is the combined rx+tx rate (bytes/sec) below which the
// per-process socket scan is skipped. Enumerating every process's /proc/fd
// links is expensive, and with no traffic there is nothing to attribute.
const netScanThreshold = 8192

// Proc is a single process snapshot for the Apps table.
type Proc struct {
	PID       int
	PPID      int
	Name      string
	CPU       float64 // percent of one core (may exceed 100 for threaded procs)
	RSS       uint64  // resident bytes
	GPU       float64 // -1 when unavailable
	NetIn     float64 // estimated bytes/sec
	NetOut    float64 // estimated bytes/sec
	DiskRead  float64 // bytes/sec
	DiskWrite float64 // bytes/sec
}

type procPrev struct {
	cpuJiffies            uint64
	readBytes, writeBytes uint64
}

// Collector samples all processes on a 1s ticker between Start and Stop.
type Collector struct {
	mu    sync.RWMutex
	procs []Proc

	prev     map[int]procPrev
	lastTime time.Time

	prevNetRx, prevNetTx uint64
	lastNetTime          time.Time

	// Per-process network attribution is throttled and its result carried
	// forward between scans (it is only a rough estimate).
	scanCounter int
	lastNet     map[int][2]float64

	// Per-process GPU load via DRM fdinfo. Known GPU-client pids are scanned
	// every tick; the full process set is rescanned periodically to find new ones.
	gpuPrev        map[int]uint64
	gpuPids        map[int]bool
	gpuScanCounter int

	// buf is reused for every /proc file read to avoid per-read allocation.
	buf []byte

	runMu   sync.Mutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// New returns an idle collector.
func New() *Collector {
	return &Collector{
		prev:    make(map[int]procPrev),
		lastNet: make(map[int][2]float64),
		gpuPrev: make(map[int]uint64),
		gpuPids: make(map[int]bool),
		buf:     make([]byte, 8192),
	}
}

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
		t := time.NewTicker(time.Second)
		defer t.Stop()
		c.collect()
		for {
			select {
			case <-c.stopCh:
				return
			case <-t.C:
				c.collect()
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
}

// Snapshot returns a copy of the latest process list.
func (c *Collector) Snapshot() []Proc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Proc, len(c.procs))
	copy(out, c.procs)
	return out
}

func (c *Collector) collect() {
	now := time.Now()
	dt := now.Sub(c.lastTime).Seconds()
	first := c.lastTime.IsZero()
	if dt <= 0 {
		dt = 1
	}
	c.lastTime = now

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}

	netRx, netTx := totalNet()
	netDt := now.Sub(c.lastNetTime).Seconds()
	if c.lastNetTime.IsZero() || netDt <= 0 {
		netDt = 1
	}
	c.lastNetTime = now
	var totalRxRate, totalTxRate float64
	if !first {
		totalRxRate = deltaRate(netRx, c.prevNetRx, netDt)
		totalTxRate = deltaRate(netTx, c.prevNetTx, netDt)
	}
	c.prevNetRx, c.prevNetTx = netRx, netTx

	// Per-process socket enumeration is the most expensive part of the scan, so
	// only do it when there is real traffic to attribute and only every 3rd
	// tick; values are carried forward in between.
	c.scanCounter++
	hasTraffic := totalRxRate+totalTxRate > netScanThreshold
	doScan := hasTraffic && c.scanCounter%3 == 0

	// GPU scan: every tick for known GPU clients, with a full rescan every 5th
	// tick to discover new ones (keeps the per-tick fd walk small).
	gpuFullScan := c.gpuScanCounter%5 == 0
	c.gpuScanCounter++
	newGPUEngine := make(map[int]uint64)
	newGPUPids := make(map[int]bool)

	procs := make([]Proc, 0, len(entries))
	newPrev := make(map[int]procPrev, len(entries))
	sockets := make(map[int]int) // pid -> active socket count
	totalSockets := 0

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}

		name, ppid, cpuJiffies, ok := c.readStat(pid)
		if !ok {
			continue
		}
		p := Proc{PID: pid, PPID: ppid, Name: name}
		p.RSS = c.readRSS(pid)

		rb, wb := c.readIO(pid)
		prev, hadPrev := c.prev[pid]
		if hadPrev && !first {
			p.CPU = float64(cpuJiffies-prev.cpuJiffies) / clockTick / dt * 100
			if p.CPU < 0 {
				p.CPU = 0
			}
			p.DiskRead = deltaRate(rb, prev.readBytes, dt)
			p.DiskWrite = deltaRate(wb, prev.writeBytes, dt)
		}
		newPrev[pid] = procPrev{cpuJiffies: cpuJiffies, readBytes: rb, writeBytes: wb}

		if doScan {
			if n := countSockets(pid); n > 0 {
				sockets[pid] = n
				totalSockets += n
			}
		}

		if gpuFullScan || c.gpuPids[pid] {
			if ns, hasDRM := c.readGPUEngineNs(pid); hasDRM {
				newGPUPids[pid] = true
				newGPUEngine[pid] = ns
				if prev, ok := c.gpuPrev[pid]; ok && !first && ns >= prev {
					p.GPU = clampPct(float64(ns-prev) / (dt * 1e9) * 100)
				}
			}
		}

		procs = append(procs, p)
	}

	// Estimated per-process network: distribute interface throughput across
	// processes proportionally to their open socket count (labeled "Net ≈").
	switch {
	case doScan && totalSockets > 0:
		newNet := make(map[int][2]float64, len(sockets))
		for i := range procs {
			if n, ok := sockets[procs[i].PID]; ok {
				share := float64(n) / float64(totalSockets)
				in, out := totalRxRate*share, totalTxRate*share
				procs[i].NetIn, procs[i].NetOut = in, out
				newNet[procs[i].PID] = [2]float64{in, out}
			}
		}
		c.lastNet = newNet
	case hasTraffic:
		// Between scans: reuse the previous attribution.
		for i := range procs {
			if v, ok := c.lastNet[procs[i].PID]; ok {
				procs[i].NetIn, procs[i].NetOut = v[0], v[1]
			}
		}
	default:
		// Network idle: nothing to attribute.
		if len(c.lastNet) > 0 {
			c.lastNet = make(map[int][2]float64)
		}
	}

	c.prev = newPrev
	c.gpuPrev = newGPUEngine
	c.gpuPids = newGPUPids

	c.mu.Lock()
	c.procs = procs
	c.mu.Unlock()
}

// slurp reads a small /proc file into the reusable buffer. The returned slice
// aliases the buffer and is only valid until the next slurp call.
func (c *Collector) slurp(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	n := 0
	for n < len(c.buf) {
		m, err := f.Read(c.buf[n:])
		n += m
		if err != nil {
			break
		}
	}
	f.Close()
	return c.buf[:n]
}

// readStat parses /proc/[pid]/stat for name, ppid, and utime+stime jiffies.
func (c *Collector) readStat(pid int) (name string, ppid int, cpuJiffies uint64, ok bool) {
	b := c.slurp("/proc/" + strconv.Itoa(pid) + "/stat")
	if b == nil {
		return "", 0, 0, false
	}
	// comm is parenthesised and may contain spaces; split after the last ')'.
	lp := bytes.IndexByte(b, '(')
	rp := bytes.LastIndexByte(b, ')')
	if lp < 0 || rp < 0 || rp < lp {
		return "", 0, 0, false
	}
	name = string(b[lp+1 : rp]) // small copy out of the shared buffer
	rest := b[rp+2:]
	// rest field 0 is field 3 (state); ppid=field 4, utime=field 14, stime=field 15.
	ppid = int(fieldUint(rest, 1))
	utime := fieldUint(rest, 11)
	stime := fieldUint(rest, 12)
	return name, ppid, utime + stime, true
}

// readRSS returns resident set size in bytes from /proc/[pid]/statm.
func (c *Collector) readRSS(pid int) uint64 {
	b := c.slurp("/proc/" + strconv.Itoa(pid) + "/statm")
	if b == nil {
		return 0
	}
	return fieldUint(b, 1) * pageSize // field 1 = resident pages
}

// readIO returns cumulative read_bytes/write_bytes (0 if not permitted).
func (c *Collector) readIO(pid int) (read, write uint64) {
	b := c.slurp("/proc/" + strconv.Itoa(pid) + "/io")
	if b == nil {
		return 0, 0
	}
	return uintAfter(b, "read_bytes:"), uintAfter(b, "write_bytes:")
}

// fieldUint returns the idx-th whitespace-separated field of b as a uint64.
func fieldUint(b []byte, idx int) uint64 {
	field, i, n := 0, 0, len(b)
	for i < n {
		for i < n && b[i] == ' ' {
			i++
		}
		start := i
		for i < n && b[i] != ' ' {
			i++
		}
		if i > start {
			if field == idx {
				return bytesToUint(b[start:i])
			}
			field++
		}
	}
	return 0
}

// uintAfter finds key in b and parses the unsigned integer that follows it.
func uintAfter(b []byte, key string) uint64 {
	i := bytes.Index(b, []byte(key))
	if i < 0 {
		return 0
	}
	i += len(key)
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return bytesToUint(b[i:])
}

// bytesToUint parses leading ASCII digits of b into a uint64.
func bytesToUint(b []byte) uint64 {
	var v uint64
	for _, ch := range b {
		if ch < '0' || ch > '9' {
			break
		}
		v = v*10 + uint64(ch-'0')
	}
	return v
}

// countSockets counts socket file descriptors of a process (own procs only).
func countSockets(pid int) int {
	dir := "/proc/" + strconv.Itoa(pid) + "/fd"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		target, err := os.Readlink(dir + "/" + e.Name())
		if err != nil {
			continue
		}
		if strings.HasPrefix(target, "socket:[") {
			count++
		}
	}
	return count
}

// readGPUEngineNs sums the DRM engine nanoseconds across a process's GPU
// clients (deduplicated by drm-client-id, since one client can be shared by
// several fds). hasDRM reports whether the process holds any /dev/dri handle.
func (c *Collector) readGPUEngineNs(pid int) (total uint64, hasDRM bool) {
	dir := "/proc/" + strconv.Itoa(pid) + "/fd"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, false
	}
	var seen map[uint64]bool
	for _, e := range entries {
		target, err := os.Readlink(dir + "/" + e.Name())
		if err != nil || !strings.HasPrefix(target, "/dev/dri/") {
			continue
		}
		hasDRM = true
		clientID, ns, ok := c.readFdinfoGPU(pid, e.Name())
		if !ok {
			continue
		}
		if seen == nil {
			seen = make(map[uint64]bool)
		}
		if seen[clientID] {
			continue // same GPU client already counted via another fd
		}
		seen[clientID] = true
		total += ns
	}
	return total, hasDRM
}

// readFdinfoGPU parses one /proc/[pid]/fdinfo/[fd], returning the GPU client id
// and the summed drm-engine-* nanoseconds (gfx + compute + decode + encode).
func (c *Collector) readFdinfoGPU(pid int, fd string) (clientID, engineNs uint64, ok bool) {
	b := c.slurp("/proc/" + strconv.Itoa(pid) + "/fdinfo/" + fd)
	if b == nil {
		return 0, 0, false
	}
	for len(b) > 0 {
		var line []byte
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, b = b[:i], b[i+1:]
		} else {
			line, b = b, nil
		}
		if rest, found := bytes.CutPrefix(line, []byte("drm-client-id:")); found {
			clientID = bytesToUint(bytes.TrimSpace(rest))
			ok = true
		} else if rest, found := bytes.CutPrefix(line, []byte("drm-engine-")); found {
			if i := bytes.IndexByte(rest, ':'); i >= 0 {
				engineNs += bytesToUint(bytes.TrimSpace(rest[i+1:]))
				ok = true
			}
		}
	}
	return clientID, engineNs, ok
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

// totalNet sums rx/tx bytes across all interfaces except loopback.
func totalNet() (rx, tx uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		if name == "lo" {
			continue
		}
		fields := strings.Fields(line[i+1:])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

func deltaRate(cur, prev uint64, dt float64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}
