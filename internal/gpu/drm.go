package gpu

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"atlas-monitor/internal/sysfs"
)

// drmBackend is the vendor-neutral fallback: Intel (i915 and xe), nouveau, and
// any card the AMD and NVIDIA backends did not claim.
//
// Temperature, fan and power come from the card's hwmon node, clocks from the
// driver's sysfs attributes. Utilisation has no common sysfs file, so it is
// derived from the kernel's per-client DRM counters in /proc/<pid>/fdinfo — the
// same numbers intel_gpu_top and nvtop read. Those require walking open file
// descriptors, which is expensive, so the set of GPU clients is only rediscovered
// every few seconds and the known ones are re-read each tick.
type drmBackend struct {
	name string

	hw        hwmon
	freq      *sysfs.File // "current engine clock", wherever this driver puts it
	vramUsed  *sysfs.File
	vramTotal *sysfs.File

	// Per-client engine accounting.
	clients     map[int]bool      // pids known to hold a /dev/dri handle
	prevEngine  map[string]uint64 // engine name -> cumulative nanoseconds
	curEngine   map[string]uint64
	lastSample  time.Time
	lastFullAt  time.Time
	buf         []byte
	linkBuf     []byte
	seenClients map[uint64]bool
}

// rescanInterval is how often the full process list is searched for new GPU
// clients. Between rescans only the known ones are re-read.
const rescanInterval = 5 * time.Second

func newDRMBackend() backend {
	cards := drmCards()
	if len(cards) == 0 {
		return nil
	}
	// Prefer a card that exposes a hwmon node: on a hybrid laptop that is the
	// discrete GPU, which is the one worth showing.
	best := cards[0]
	for _, c := range cards {
		if hwmonDir(c.dev, "") != "" {
			best = c
			break
		}
	}
	card := filepath.Join("/sys/class/drm", best.card)
	b := &drmBackend{
		name:        gpuName(best),
		hw:          openHwmon(hwmonDir(best.dev, "")),
		clients:     make(map[int]bool),
		prevEngine:  make(map[string]uint64),
		curEngine:   make(map[string]uint64),
		seenClients: make(map[uint64]bool),
		buf:         make([]byte, 4096),
		linkBuf:     make([]byte, 256),

		// Each driver puts the engine clock somewhere different; hold open
		// whichever one this card has.
		freq: sysfs.OpenFirst(
			filepath.Join(card, "gt_cur_freq_mhz"),                       // i915
			filepath.Join(best.dev, "tile0", "gt0", "freq0", "cur_freq"), // xe
			filepath.Join(best.dev, "gpu_clock"),                         // misc
		),
		vramUsed: sysfs.Open(filepath.Join(best.dev, "mem_info_vram_used")),
		vramTotal: sysfs.OpenFirst(
			filepath.Join(best.dev, "mem_info_vram_total"),
			filepath.Join(best.dev, "tile0", "physical_vram_size_bytes"), // xe discrete
		),
	}
	return b
}

func (b *drmBackend) label() string { return b.name }

func (b *drmBackend) close() {
	b.hw.close()
	b.freq.Close()
	b.vramUsed.Close()
	b.vramTotal.Close()
}

func (b *drmBackend) sample(s *Sample) {
	b.hw.read(s)
	if s.SclkMHz == 0 {
		if v, ok := b.freq.Uint(); ok {
			s.SclkMHz = float64(v) // these report MHz directly
		}
	}
	s.VramUsed, _ = b.vramUsed.Uint()
	s.VramTotal, _ = b.vramTotal.Uint()
	s.UsagePct = b.engineBusy()
}

// engineBusy returns the busiest engine's utilisation since the previous call.
// Engines are tracked separately and the maximum is reported: a card decoding
// video while the 3D engine idles is not "200% busy".
func (b *drmBackend) engineBusy() float64 {
	now := time.Now()
	full := now.Sub(b.lastFullAt) >= rescanInterval
	if full {
		b.lastFullAt = now
		b.discoverClients()
	}

	clear(b.curEngine)
	clear(b.seenClients)
	for pid := range b.clients {
		if !b.accumulate(pid) {
			delete(b.clients, pid) // process exited
		}
	}

	dt := now.Sub(b.lastSample).Seconds()
	b.lastSample = now
	if dt <= 0 || dt > 10 { // first call, or the view was hidden for a while
		b.prevEngine, b.curEngine = b.curEngine, b.prevEngine
		return 0
	}

	busiest := 0.0
	for name, ns := range b.curEngine {
		prev, ok := b.prevEngine[name]
		if !ok || ns < prev {
			continue
		}
		if pct := float64(ns-prev) / (dt * 1e9) * 100; pct > busiest {
			busiest = pct
		}
	}
	b.prevEngine, b.curEngine = b.curEngine, b.prevEngine
	return clampPct(busiest)
}

// discoverClients finds every process holding a /dev/dri handle.
func (b *drmBackend) discoverClients() {
	f, err := os.Open("/proc")
	if err != nil {
		return
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return
	}
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		if b.clients[pid] {
			continue
		}
		if b.holdsDRM(pid) {
			b.clients[pid] = true
		}
	}
}

// holdsDRM reports whether a process has any /dev/dri file descriptor open.
func (b *drmBackend) holdsDRM(pid int) bool {
	dir := "/proc/" + strconv.Itoa(pid) + "/fd"
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	fds, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return false
	}
	for _, fd := range fds {
		if target, ok := b.readlink(dir + "/" + fd); ok && bytes.HasPrefix(target, driPrefix) {
			return true
		}
	}
	return false
}

// accumulate adds one process's engine counters into curEngine. It returns
// false when the process is gone.
func (b *drmBackend) accumulate(pid int) bool {
	dir := "/proc/" + strconv.Itoa(pid) + "/fdinfo"
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	fds, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return false
	}
	for _, fd := range fds {
		b.readFdinfo(dir+"/"+fd, pid)
	}
	return true
}

// readFdinfo folds one fdinfo file's drm-engine-* counters into curEngine,
// skipping clients already counted through another descriptor.
func (b *drmBackend) readFdinfo(path string, pid int) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	n, _ := f.Read(b.buf)
	f.Close()
	data := b.buf[:n]
	if !bytes.Contains(data, drmPrefix) {
		return
	}

	// One GPU client can be shared by several descriptors; count it once.
	var clientID uint64
	if rest, ok := cutKey(data, clientKey); ok {
		clientID = parseUint(rest)
		key := uint64(pid)<<32 | clientID&0xffffffff
		if b.seenClients[key] {
			return
		}
		b.seenClients[key] = true
	}

	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		rest, ok := bytes.CutPrefix(line, enginePrefix)
		if !ok {
			continue
		}
		colon := bytes.IndexByte(rest, ':')
		if colon < 0 {
			continue
		}
		name := string(bytes.TrimSpace(rest[:colon]))
		b.curEngine[name] += parseUint(bytes.TrimSpace(rest[colon+1:]))
	}
}

var (
	driPrefix    = []byte("/dev/dri/")
	drmPrefix    = []byte("drm-")
	enginePrefix = []byte("drm-engine-")
	clientKey    = []byte("drm-client-id:")
)

// cutKey returns the text after key on the line that starts with it.
func cutKey(data, key []byte) ([]byte, bool) {
	i := bytes.Index(data, key)
	if i < 0 {
		return nil, false
	}
	rest := data[i+len(key):]
	if j := bytes.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return bytes.TrimSpace(rest), true
}

// parseUint reads the leading digits of b.
func parseUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			break
		}
		v = v*10 + uint64(c-'0')
	}
	return v
}

// readlink resolves a symlink into the reusable buffer. The result aliases that
// buffer and is only valid until the next call; a target longer than the buffer
// is truncated, which is harmless because callers only test its prefix.
func (b *drmBackend) readlink(path string) ([]byte, bool) {
	n, err := syscall.Readlink(path, b.linkBuf)
	if err != nil || n <= 0 {
		return nil, false
	}
	return b.linkBuf[:n], true
}
