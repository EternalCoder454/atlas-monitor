package stats

import (
	"bufio"
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

// collectCPU samples /proc/stat, per-core frequencies, and temperature.
func (c *Collector) collectCPU() {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	type sample struct {
		name        string
		idle, total uint64
	}
	var samples []sample
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		var nums []uint64
		var total uint64
		for _, f := range fields[1:] {
			n := atou(f)
			nums = append(nums, n)
			total += n
		}
		idle := nums[3]
		if len(nums) > 4 {
			idle += nums[4] // + iowait
		}
		samples = append(samples, sample{name: fields[0], idle: idle, total: total})
	}
	f.Close()

	logical := len(c.stats.Cores())
	freqs := make([]float64, logical)
	maxFreq := 0.0
	for i := 0; i < logical; i++ {
		p := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", i)
		if v, err := readUint(p); err == nil {
			mhz := float64(v) / 1000.0
			freqs[i] = mhz
			if mhz > maxFreq {
				maxFreq = mhz
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
		for _, sm := range samples {
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
		for i := 0; i < len(s.CPU.Cores) && i < len(freqs); i++ {
			s.CPU.Cores[i].Freq = freqs[i]
		}
		s.CPU.CurFreq = maxFreq
		if temp >= 0 {
			s.CPU.Temp = temp
		}
	})
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
