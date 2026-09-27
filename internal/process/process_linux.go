package process

// The process table on Linux: one directory per process under /proc.
//
// Nearly all of the cost of this package is here. A scan opens and reads several
// files per process, and the machine this was written on has around 700 of them,
// so the work is arranged to allocate nothing per tick: every buffer and map is
// reused, the parsing works on bytes rather than strings, and the expensive parts
// — per-process sockets, GPU descriptors — are throttled and carried forward.

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
	"time"

	"atlas-monitor/internal/sysfs"
)

// clockTick is USER_HZ (jiffies per second); 100 on essentially all Linux/x86.
const clockTick = 100.0

// pageSize is used to convert statm resident pages to bytes.
const pageSize = 4096

func (c *Collector) collect() {
	now := time.Now()
	dt := now.Sub(c.lastTime).Seconds()
	first := c.lastTime.IsZero()
	if dt <= 0 {
		dt = 1
	}
	c.lastTime = now

	// Readdirnames rather than ReadDir: /proc has ~700 entries and we only need
	// the names, so this skips building a DirEntry per process and sorting them.
	procDir, err := os.Open("/proc")
	if err != nil {
		return
	}
	entries, err := procDir.Readdirnames(-1)
	procDir.Close()
	if err != nil {
		return
	}

	netRx, netTx := c.totalNet()
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
	doScan := c.wantNet.Load() && hasTraffic && c.scanCounter%3 == 0

	// GPU scan. Walking a process's open descriptors is the most expensive
	// thing here after the stat reads, so it is done for as few processes as
	// possible: the ones already known to hold a GPU handle, and any process
	// that was not here last tick — a game shows its per-process GPU load on
	// the very first tick after it launches. The full sweep is only a safety
	// net for anything those two miss, so it can be rare.
	gpuWanted := c.wantGPU.Load()
	gpuFullScan := gpuWanted && c.gpuScanCounter%gpuRescanTicks == 0
	c.gpuScanCounter++
	ioWanted := c.wantDiskIO.Load()
	// Every per-tick container is a reused one: emptying a map keeps its buckets,
	// so a steady process count settles into zero allocation per scan.
	newGPUEngine, newGPUPids := c.gpuPrevSpare, c.gpuPidsSpare
	newPrev, sockets := c.prevSpare, c.sockets // sockets: pid -> active socket count
	clear(newGPUEngine)
	clear(newGPUPids)
	clear(newPrev)
	clear(sockets)
	procs := c.scratch[:0]
	totalSockets := 0

	for _, name := range entries {
		// Only /proc/<pid> is a process. Checking the first byte first keeps
		// Atoi — and the error it would allocate — away from the two dozen
		// named entries in /proc.
		if len(name) == 0 || name[0] < '0' || name[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}

		if !c.readStat(pid, &c.stat) {
			continue
		}
		// Kernel threads are three quarters of /proc and are hidden by default.
		// Skipping them here is what makes that a performance win and not just
		// a shorter table: no /proc/[pid]/io open, no row, no work downstream.
		if c.stat.kernel && !c.includeKernel.Load() {
			continue
		}
		// string(...) copies the name out of the read buffer, which the next
		// read is about to overwrite.
		p := Proc{PID: pid, Name: string(c.stat.name), Kernel: c.stat.kernel, GPU: -1}
		p.RSS = c.readRSS(pid)

		var rb, wb uint64
		if ioWanted {
			rb, wb = c.readIO(pid)
		}
		prev, hadPrev := c.prev[pid]
		if hadPrev && !first {
			p.CPU = float64(c.stat.jiffies-prev.cpuJiffies) / clockTick / dt * 100
			if p.CPU < 0 {
				p.CPU = 0
			}
			p.DiskRead = deltaRate(rb, prev.readBytes, dt)
			p.DiskWrite = deltaRate(wb, prev.writeBytes, dt)
		}
		newPrev[pid] = procPrev{cpuJiffies: c.stat.jiffies, readBytes: rb, writeBytes: wb}

		// Both the socket count and the GPU counters come from the same place —
		// the process's open descriptors — so they share one walk. Done
		// separately, a tick where both were due read every link twice.
		wantGPU := gpuWanted && (gpuFullScan || !hadPrev || c.gpuPids[pid])
		if doScan || wantGPU {
			n, ns, hasDRM := c.scanFDs(pid, doScan, wantGPU)
			if doScan && n > 0 {
				sockets[pid] = n
				totalSockets += n
			}
			if hasDRM {
				newGPUPids[pid] = true
				newGPUEngine[pid] = ns
				p.GPU = 0 // holds a GPU handle: report 0 until a delta is measurable
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

	c.prev, c.prevSpare = newPrev, c.prev
	c.gpuPrev, c.gpuPrevSpare = newGPUEngine, c.gpuPrev
	c.gpuPids, c.gpuPidsSpare = newGPUPids, c.gpuPids

	c.mu.Lock()
	c.scratch, c.procs = c.procs, procs // swap: the UI keeps reading the old one
	c.mu.Unlock()
}

// openProc holds /proc open so every per-process file can be reached with
// openat and a relative path, instead of the kernel resolving the mount point
// afresh eight hundred times a second. It is opened on first use so a Collector
// works whether or not Start has been called.
func (c *Collector) openProc() bool {
	if c.procFD >= 0 {
		return true
	}
	fd, err := syscall.Open("/proc", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	c.procFD = fd
	return true
}

// slurpPID reads /proc/<pid>/<name> into the reusable buffer. The returned
// slice aliases that buffer and is only valid until the next call.
//
// Three things make this cheaper than the obvious os.ReadFile, and the scan
// does it eight hundred times a second:
//
//   - openat against a descriptor already held on /proc, so the kernel does not
//     resolve the mount point again for every file;
//   - the raw syscalls rather than os.Open, which allocates an *os.File and
//     attaches a finaliser to it — those were a quarter of everything the app
//     allocated;
//   - one read. procfs builds each of these files in one go and returns all of
//     it, so a read that does not fill the buffer is the end of the file;
//     looping until a second read reported EOF doubled the read syscalls.
func (c *Collector) slurpPID(pid int, name string) []byte {
	if !c.openProc() {
		return nil
	}
	c.path = strconv.AppendInt(c.path[:0], int64(pid), 10)
	c.path = append(c.path, name...)

	fd, err := syscall.Openat(c.procFD, string(c.path), syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	n := 0
	for n < len(c.buf) {
		room := len(c.buf) - n
		m, err := syscall.Read(fd, c.buf[n:])
		if m > 0 {
			n += m
		}
		if err != nil || m < room {
			break // error, or a short read: procfs has given us everything
		}
	}
	syscall.Close(fd)
	if n == 0 {
		return nil
	}
	return c.buf[:n]
}

// pfKthread is PF_KTHREAD in the kernel's task flags: the process is a kernel
// thread, not a program. Roughly three quarters of the entries in /proc are
// these (kworker, ksoftirqd, irq handlers), and they are hidden by default.
const pfKthread = 0x00200000

// readStat parses /proc/[pid]/stat for the name, the CPU time and whether this
// is a kernel thread. The flags word is in the line we already read, so the
// kernel-thread test costs nothing — reading /proc/[pid]/cmdline to tell them
// apart would be another open per process per tick.
//
// Resident memory deliberately comes from /proc/[pid]/statm instead of field 24
// here, even though that is a second file: the two do not agree, and statm is
// the one ps, top and htop report. See TestStatRSSDiffersFromStatm.
//
// Offsets are into rest, which starts at field 3, so index n is field n+3:
// flags 9, utime 14, stime 15.
func (c *Collector) readStat(pid int, out *statLine) bool {
	b := c.slurpPID(pid, "/stat")
	if b == nil {
		return false
	}
	return parseStatLine(b, out)
}

// parseStatLine is the parsing half of readStat, split out so it can be fed
// malformed input directly — see FuzzParseStatLine. out.name aliases b.
func parseStatLine(b []byte, out *statLine) bool {
	// comm is parenthesised and may contain spaces; split after the last ')'.
	lp := bytes.IndexByte(b, '(')
	rp := bytes.LastIndexByte(b, ')')
	if lp < 0 || rp < 0 || rp < lp || rp+2 > len(b) {
		return false
	}
	rest := b[rp+2:]

	out.name = b[lp+1 : rp]
	out.jiffies = fieldUint(rest, 11) + fieldUint(rest, 12)
	out.kernel = fieldUint(rest, 6)&pfKthread != 0
	return true
}

// readRSS returns resident set size in bytes from /proc/[pid]/statm. This is
// the figure ps and top report; /proc/[pid]/stat's own rss field counts
// differently and would make Atlas disagree with every other tool.
func (c *Collector) readRSS(pid int) uint64 {
	b := c.slurpPID(pid, "/statm")
	if b == nil {
		return 0
	}
	return fieldUint(b, 1) * pageSize // field 1 = resident pages
}

// readIO returns cumulative read_bytes/write_bytes (0 if not permitted).
func (c *Collector) readIO(pid int) (read, write uint64) {
	b := c.slurpPID(pid, "/io")
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

var (
	socketPrefix = []byte("socket:[")
	driPrefix    = []byte("/dev/dri/")
)

// gpuRescanTicks is how often every process is swept for new GPU handles. New
// processes and known GPU clients are checked every tick regardless, so this
// only has to catch a process that acquired its first GPU handle long after it
// started — rare enough that a sweep every half minute is generous.
const gpuRescanTicks = 30

// scanFDs walks a process's open descriptors once, counting sockets and summing
// DRM engine time as asked. Both callers want the same readlink of the same
// entries, so they share the walk.
//
// GPU clients are deduplicated by drm-client-id: one client can be reachable
// through several descriptors and would otherwise be counted repeatedly.
func (c *Collector) scanFDs(pid int, wantSockets, wantGPU bool) (sockets int, gpuNs uint64, hasDRM bool) {
	dir := "/proc/" + strconv.Itoa(pid) + "/fd"
	fds, err := readdirnames(dir)
	if err != nil {
		return 0, 0, false
	}
	var seen map[uint64]bool
	for _, fd := range fds {
		target, ok := c.readlink(dir + "/" + fd)
		if !ok {
			continue
		}
		if wantSockets && bytes.HasPrefix(target, socketPrefix) {
			sockets++
			continue
		}
		if !wantGPU || !bytes.HasPrefix(target, driPrefix) {
			continue
		}
		hasDRM = true
		clientID, ns, ok := c.readFdinfoGPU(pid, fd)
		if !ok {
			continue
		}
		if seen == nil {
			seen = make(map[uint64]bool)
		}
		if seen[clientID] {
			continue // same GPU client already counted via another descriptor
		}
		seen[clientID] = true
		gpuNs += ns
	}
	return sockets, gpuNs, hasDRM
}

// readlink resolves a symlink into the reusable buffer. The result aliases that
// buffer and is only valid until the next call. A target longer than the buffer
// is truncated, which is harmless here — callers only test its prefix.
func (c *Collector) readlink(path string) ([]byte, bool) {
	n, err := syscall.Readlink(path, c.link)
	if err != nil || n <= 0 {
		return nil, false
	}
	return c.link[:n], true
}

// readdirnames lists a directory's entry names.
func readdirnames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	return names, err
}

// readFdinfoGPU parses one /proc/[pid]/fdinfo/[fd], returning the GPU client id
// and the summed drm-engine-* nanoseconds (gfx + compute + decode + encode).
func (c *Collector) readFdinfoGPU(pid int, fd string) (clientID, engineNs uint64, ok bool) {
	b := c.slurpPID(pid, "/fdinfo/"+fd)
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

// totalNet sums rx/tx bytes across all interfaces except loopback. The file is
// held open and parsed as bytes: it is read every tick, and the obvious
// ReadFile-and-Split allocated the whole file plus a string per line each time.
func (c *Collector) totalNet() (rx, tx uint64) {
	if c.netDev == nil {
		c.netDev = sysfs.OpenSize("/proc/net/dev", 4096)
	}
	data, ok := c.netDev.Bytes()
	if !ok {
		return 0, 0
	}
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		i := bytes.IndexByte(line, ':')
		if i < 0 {
			continue // the two header rows
		}
		if string(bytes.TrimSpace(line[:i])) == "lo" {
			continue
		}
		// rx bytes is field 0 after the colon, tx bytes is field 8.
		r, rok := sysfs.ParseUint(netField(line[i+1:], 0))
		t, tok := sysfs.ParseUint(netField(line[i+1:], 8))
		if rok && tok {
			rx += r
			tx += t
		}
	}
	return rx, tx
}

// netField returns the idx-th space-separated field of b.
func netField(b []byte, idx int) []byte {
	n := len(b)
	for i := 0; i < n; {
		for i < n && (b[i] == ' ' || b[i] == '\t') {
			i++
		}
		start := i
		for i < n && b[i] != ' ' && b[i] != '\t' {
			i++
		}
		if i == start {
			break
		}
		if idx == 0 {
			return b[start:i]
		}
		idx--
	}
	return nil
}

// StartTime returns the process's start time in clock ticks since boot, from
// field 22 of /proc/[pid]/stat, and whether it could be read.
//
// This is the only reliable way to tell one use of a PID from another. PIDs are
// reused: the kernel wraps at /proc/sys/kernel/pid_max, which is 32768 on plenty
// of systems, and a busy machine can get back round to a given number in
// minutes. Anything that acts on a PID it was handed earlier — sending a signal,
// say — has to check that the pair (pid, start time) is still the same process,
// or it will eventually act on an innocent one.
func StartTime(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// comm is parenthesised and may contain spaces; the numbered fields resume
	// after the last ')'. rest begins at field 3, so field 22 is index 19.
	rp := bytes.LastIndexByte(b, ')')
	if rp < 0 || rp+2 > len(b) {
		return 0, false
	}
	return fieldUint(b[rp+2:], 19), true
}

// closePlatform releases the descriptors the /proc scan holds open.
func (c *Collector) closePlatform() {
	if c.procFD >= 0 {
		syscall.Close(c.procFD)
		c.procFD = -1
	}
	c.netDev.Close()
	c.netDev = nil
}
