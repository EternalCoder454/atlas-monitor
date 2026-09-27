package stats

import (
	"bytes"

	"atlas-monitor/internal/sysfs"
)

// initMem allocates the memory ring buffers and holds /proc/meminfo open.
func (c *Collector) initMem() {
	c.memInfo = sysfs.OpenSize("/proc/meminfo", 4096)
	c.initMemHist()
}

// collectMem parses /proc/meminfo. All values are converted to bytes.
// The file is read into a reused buffer and scanned as bytes: at one sample a
// second, a map and fifty per-line field slices are not worth allocating.
func (c *Collector) collectMem() {
	data, ok := c.memInfo.Bytes()
	if !ok {
		return
	}

	var total, avail, free, cached, sreclaim, buffers, swapTotal, swapFree uint64
	for len(data) > 0 {
		var line []byte
		line, data = nextLine(data)
		key, kb, valid := meminfoLine(line)
		if !valid {
			continue
		}
		// Values in /proc/meminfo are in kB.
		switch string(key) { // no allocation: the compiler compares in place
		case "MemTotal":
			total = kb
		case "MemAvailable":
			avail = kb
		case "MemFree":
			free = kb
		case "Cached":
			cached = kb
		case "SReclaimable":
			sreclaim = kb
		case "Buffers":
			buffers = kb
		case "SwapTotal":
			swapTotal = kb
		case "SwapFree":
			swapFree = kb
		}
	}
	const kB = 1024
	c.publishMem(memSample{
		Total:     total * kB,
		Available: avail * kB,
		Free:      free * kB,
		Cached:    (cached + sreclaim + buffers) * kB,
		SwapTotal: swapTotal * kB,
		SwapFree:  swapFree * kB,
	})
}

// meminfoLine splits one "Key:   1234 kB" line into its key and value.
func meminfoLine(line []byte) (key []byte, value uint64, ok bool) {
	i := bytes.IndexByte(line, ':')
	if i < 0 {
		return nil, 0, false
	}
	v := field(line[i+1:], 0)
	if v == nil {
		return nil, 0, false
	}
	return line[:i], parseUintBytes(v), true
}
