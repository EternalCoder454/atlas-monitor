package stats

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"atlas-monitor/internal/sysfs"
)

// cpuTimes holds the idle and total jiffies of one /proc/stat cpu line.
type cpuTimes struct {
	idle  uint64
	total uint64
}

// cpuSample is one parsed /proc/stat cpu line, carried from the lock-free read
// phase to the locked update phase. core is -1 for the aggregate "cpu" line.
type cpuSample struct {
	core        int
	idle, total uint64
}

// cpuPrefix gates the /proc/stat scan to the leading cpu* lines.
var cpuPrefix = []byte("cpu")

// initCPUStatic fills the unchanging CPU fields, allocates ring buffers, and
// opens the files the per-second sample re-reads.
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

	// Hold open everything the per-second sample touches. Reopening these was a
	// sixth of the app's CPU time; held descriptors turn four syscalls each into
	// one. /proc/stat needs room for a line per core.
	c.procStat = sysfs.OpenSize("/proc/stat", 1024+logical*128)
	c.cpuFreq = make([]*sysfs.File, 0, logical)
	for i := 0; i < logical; i++ {
		c.cpuFreq = append(c.cpuFreq, sysfs.Open(
			"/sys/devices/system/cpu/cpu"+strconv.Itoa(i)+"/cpufreq/scaling_cur_freq"))
	}
	c.cpuTemp = sysfs.Open(findCPUTempPath())
	c.cpuPrevCore = make([]cpuTimes, logical)
	c.cpuSamples = make([]cpuSample, 0, logical+1)

	temp := -1.0
	if v, ok := c.cpuTemp.Uint(); ok {
		temp = float64(v) / 1000.0
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

// closeCPU releases the held descriptors.
func (c *Collector) closeCPU() {
	c.procStat.Close()
	c.cpuTemp.Close()
	for _, f := range c.cpuFreq {
		f.Close()
	}
	c.cpuFreq = nil
}

// collectCPU samples /proc/stat, per-core frequencies, and temperature. Every
// file it touches is already open, the parse works on bytes, and cores are
// indexed by number rather than keyed by a string — so a tick allocates nothing.
func (c *Collector) collectCPU() {
	data, ok := c.procStat.Bytes()
	if !ok {
		return
	}
	c.cpuSamples = c.cpuSamples[:0]
	for len(data) > 0 {
		var line []byte
		line, data = nextLine(data)
		if !bytes.HasPrefix(line, cpuPrefix) {
			break // cpu* lines lead the file; stop before the large intr line
		}
		if core, idle, total, ok := parseCPUStatLine(line); ok {
			c.cpuSamples = append(c.cpuSamples, cpuSample{core: core, idle: idle, total: total})
		}
	}

	maxFreq := 0.0
	for _, f := range c.cpuFreq {
		if v, ok := f.Uint(); ok {
			if mhz := float64(v) / 1000.0; mhz > maxFreq {
				maxFreq = mhz
			}
		}
	}

	temp := -1.0
	if v, ok := c.cpuTemp.Uint(); ok {
		temp = float64(v) / 1000.0
	}

	c.write(func(s *Stats) {
		for _, sm := range c.cpuSamples {
			prev := &c.cpuPrevAll
			if sm.core >= 0 {
				if sm.core >= len(c.cpuPrevCore) {
					continue
				}
				prev = &c.cpuPrevCore[sm.core]
			}
			dTotal := float64(sm.total - prev.total)
			dIdle := float64(sm.idle - prev.idle)
			usage := 0.0
			if prev.total != 0 && dTotal > 0 {
				usage = clamp((1-dIdle/dTotal)*100, 0, 100)
			}
			*prev = cpuTimes{idle: sm.idle, total: sm.total}

			if sm.core < 0 {
				s.CPU.Usage = usage
				if s.CPU.UsageHist != nil {
					s.CPU.UsageHist.Push(usage)
				}
			} else if sm.core < len(s.CPU.Cores) {
				s.CPU.Cores[sm.core].Usage = usage
			}
		}
		s.CPU.CurFreq = maxFreq
		if temp >= 0 {
			s.CPU.Temp = temp
		}
	})
}

// maxCore bounds the core number a "cpuN" line may carry. The parser adds
// digits without an overflow check — that is the point of it, no allocation and
// no strconv — so a line with an absurd number of digits would wrap the value
// and, cast to int, could come out negative, which collectCPU would read as the
// aggregate line and use to overwrite the whole-CPU figure. No kernel builds
// with anything near this many CPUs (CONFIG_NR_CPUS tops out in the thousands),
// so rejecting the line is the right answer.
const maxCore = 1 << 20

// parseCPUStatLine parses one "cpu..." line of /proc/stat. idle folds in iowait
// (column 4), matching the historical behaviour; total is the sum of all
// columns. core is -1 for the aggregate line and the core number otherwise, so
// nothing has to be turned into a string. ok is false if the line is too short
// to hold an idle figure.
func parseCPUStatLine(line []byte) (core int, idle, total uint64, ok bool) {
	field := 0
	core = -1
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
			name := line[start:i]
			if !bytes.HasPrefix(name, cpuPrefix) {
				return -1, 0, 0, false
			}
			if rest := name[len(cpuPrefix):]; len(rest) > 0 {
				n, valid := sysfs.ParseUint(rest)
				if !valid || n > maxCore {
					return -1, 0, 0, false
				}
				core = int(n)
			}
		} else {
			v := parseUintBytes(line[start:i])
			total += v
			if field == 4 || field == 5 { // columns idle and iowait
				idle += v
			}
		}
		field++
	}
	return core, idle, total, field >= 5
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
		n := sysfs.ReadString(nf)
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
	if v, ok := sysfs.ReadUint("/sys/devices/system/cpu/cpu0/cpufreq/base_frequency"); ok {
		return float64(v) / 1000.0
	}
	return 0
}

// readCaches reads L1d/L1i/L2/L3 sizes from cpu0's cache hierarchy.
func readCaches() (l1d, l1i, l2, l3 string) {
	idxs, _ := filepath.Glob("/sys/devices/system/cpu/cpu0/cache/index*")
	for _, idx := range idxs {
		level := sysfs.ReadString(filepath.Join(idx, "level"))
		ctype := sysfs.ReadString(filepath.Join(idx, "type"))
		size := sysfs.ReadString(filepath.Join(idx, "size"))
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
