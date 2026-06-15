package stats

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cpuTimes holds the idle and total jiffies of one /proc/stat cpu line.
type cpuTimes struct {
	idle  uint64
	total uint64
}

// cpuSample is one parsed /proc/stat cpu line, carried from the lock-free read
// phase to the locked update phase.
type cpuSample struct {
	name        string
	idle, total uint64
}

// cpuPrefix gates the /proc/stat scan to the leading cpu* lines.
var cpuPrefix = []byte("cpu")

// initCPUStatic fills the unchanging CPU fields and allocates ring buffers.
func (c *Collector) initCPUStatic() {
	logical := 0
	sockets := map[string]struct{}{}
	physCores := map[string]struct{}{}
	model := ""

	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		sc := bufio.NewScanner(f)
		var physID, coreID string
		for sc.Scan() {
			line := sc.Text()
			key, val, ok := splitKV(line)
			if !ok {
				if strings.TrimSpace(line) == "" {
					if physID != "" || coreID != "" {
						physCores[physID+":"+coreID] = struct{}{}
					}
					physID, coreID = "", ""
				}
				continue
			}
			switch key {
			case "processor":
				logical++
			case "model name":
				if model == "" {
					model = val
				}
			case "physical id":
				physID = val
				sockets[val] = struct{}{}
			case "core id":
				coreID = val
			}
		}
		f.Close()
	}
	if logical == 0 {
		logical = 1
	}
	if len(sockets) == 0 {
		sockets[""] = struct{}{}
	}

	// Precompute the per-second collectCPU scratch (paths + reusable buffers) so
	// the hot path allocates nothing per tick.
	c.cpuFreqPaths = make([]string, logical)
	for i := range c.cpuFreqPaths {
		c.cpuFreqPaths[i] = fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", i)
	}
	c.cpuFreqs = make([]float64, logical)
	c.cpuStatBuf = make([]byte, 0, 16*1024)
	c.cpuSamples = make([]cpuSample, 0, logical+1)

	temp := -1.0
	c.tempPath = findCPUTempPath()
	if c.tempPath != "" {
		if v, err := readUint(c.tempPath); err == nil {
			temp = float64(v) / 1000.0
		}
	}

	l1d, l1i, l2, l3 := readCaches()

	c.write(func(s *Stats) {
		s.CPU.UsageHist = NewRingBuffer()
		s.CPU.Cores = make([]CoreStat, logical)
		s.CPU.Logical = logical
		s.CPU.Sockets = len(sockets)
		s.CPU.PhysCores = len(physCores)
		if s.CPU.PhysCores == 0 {
			s.CPU.PhysCores = logical
		}
		s.CPU.Model = model
		s.CPU.Temp = temp
		s.CPU.BaseFreq = readBaseFreq()
		s.CPU.L1d, s.CPU.L1i, s.CPU.L2, s.CPU.L3 = l1d, l1i, l2, l3
	})
}

// collectCPU samples /proc/stat, per-core frequencies, and temperature. The
// /proc/stat parse reuses a buffer and parses bytes directly (no per-line string
// or field-slice allocation); the cpufreq paths are precomputed in initCPUStatic.
func (c *Collector) collectCPU() {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	c.cpuSamples = c.cpuSamples[:0]
	sc := bufio.NewScanner(f)
	sc.Buffer(c.cpuStatBuf, 64*1024) // reuse our buffer; do no per-tick allocation
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, cpuPrefix) {
			break // cpu* lines lead the file; stop before the large intr line
		}
		if name, idle, total, ok := parseCPUStatLine(line); ok {
			c.cpuSamples = append(c.cpuSamples, cpuSample{name: name, idle: idle, total: total})
		}
	}
	f.Close()

	maxFreq := 0.0
	for i := range c.cpuFreqs {
		c.cpuFreqs[i] = 0
		if v, err := readUint(c.cpuFreqPaths[i]); err == nil {
			c.cpuFreqs[i] = float64(v) / 1000.0
			if c.cpuFreqs[i] > maxFreq {
				maxFreq = c.cpuFreqs[i]
			}
		}
	}

	temp := -1.0
	if c.tempPath != "" {
		if v, err := readUint(c.tempPath); err == nil {
			temp = float64(v) / 1000.0
		}
	}

	c.write(func(s *Stats) {
		for _, sm := range c.cpuSamples {
			prev := c.cpuPrev[sm.name]
			dTotal := float64(sm.total - prev.total)
			dIdle := float64(sm.idle - prev.idle)
			usage := 0.0
			if prev.total != 0 && dTotal > 0 {
				usage = clamp((1-dIdle/dTotal)*100, 0, 100)
			}
			c.cpuPrev[sm.name] = cpuTimes{idle: sm.idle, total: sm.total}

			if sm.name == "cpu" {
				s.CPU.Usage = usage
				if s.CPU.UsageHist != nil {
					s.CPU.UsageHist.Push(usage)
				}
			} else if idx, ok := coreIndex(sm.name); ok && idx < len(s.CPU.Cores) {
				s.CPU.Cores[idx].Usage = usage
			}
		}
		for i := 0; i < len(s.CPU.Cores) && i < len(c.cpuFreqs); i++ {
			s.CPU.Cores[i].Freq = c.cpuFreqs[i]
		}
		s.CPU.CurFreq = maxFreq
		if temp >= 0 {
			s.CPU.Temp = temp
		}
	})
}

// parseCPUStatLine parses one "cpu..." line of /proc/stat. idle folds in iowait
// (column 4), matching the historical behaviour; total is the sum of all
// columns. ok is false if the line is too short to hold an idle figure. The
// returned name (e.g. "cpu", "cpu0") is the only allocation, used as a map key.
func parseCPUStatLine(line []byte) (name string, idle, total uint64, ok bool) {
	field := 0
	for i := 0; i < len(line); {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		start := i
		for i < len(line) && line[i] != ' ' {
			i++
		}
		if i == start {
			break
		}
		if field == 0 {
			name = string(line[start:i])
		} else {
			v := parseUintBytes(line[start:i])
			total += v
			if field == 4 || field == 5 { // columns idle and iowait
				idle += v
			}
		}
		field++
	}
	return name, idle, total, field >= 5
}

// Cores returns a copy of the per-core slice length (used internally).
func (s *Stats) Cores() []CoreStat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CPU.Cores
}

func coreIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "cpu") {
		return 0, false
	}
	n, err := strconv.Atoi(name[3:])
	if err != nil {
		return 0, false
	}
	return n, true
}

func splitKV(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

// findCPUTempPath locates a coretemp/k10temp hwmon temperature input.
func findCPUTempPath() string {
	names, _ := filepath.Glob("/sys/class/hwmon/hwmon*/name")
	for _, nf := range names {
		n, err := readString(nf)
		if err != nil {
			continue
		}
		if n == "coretemp" || n == "k10temp" {
			dir := filepath.Dir(nf)
			// Prefer the package/Tctl input; temp1_input is the usual first.
			if _, err := os.Stat(filepath.Join(dir, "temp1_input")); err == nil {
				return filepath.Join(dir, "temp1_input")
			}
		}
	}
	return ""
}

// readBaseFreq returns the rated base clock in MHz, or 0 if unknown.
func readBaseFreq() float64 {
	if v, err := readUint("/sys/devices/system/cpu/cpu0/cpufreq/base_frequency"); err == nil {
		return float64(v) / 1000.0
	}
	return 0
}

// readCaches reads L1d/L1i/L2/L3 sizes from cpu0's cache hierarchy.
func readCaches() (l1d, l1i, l2, l3 string) {
	idxs, _ := filepath.Glob("/sys/devices/system/cpu/cpu0/cache/index*")
	for _, idx := range idxs {
		level, _ := readString(filepath.Join(idx, "level"))
		ctype, _ := readString(filepath.Join(idx, "type"))
		size, _ := readString(filepath.Join(idx, "size"))
		switch {
		case level == "1" && ctype == "Data":
			l1d = size
		case level == "1" && ctype == "Instruction":
			l1i = size
		case level == "2":
			l2 = size
		case level == "3":
			l3 = size
		}
	}
	return
}
