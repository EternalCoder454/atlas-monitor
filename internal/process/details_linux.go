package process

import (
	"bytes"
	"errors"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

// Details reads what /proc knows about one process. It fails only when the
// process is gone; anything else it cannot read is left out.
func Details(pid int) (Info, error) {
	dir := "/proc/" + strconv.Itoa(pid) + "/"
	status, err := os.ReadFile(dir + "status")
	if err != nil {
		return Info{}, errors.New("the process has exited")
	}
	in := Info{PID: pid}
	parseStatus(status, &in)

	if b, err := os.ReadFile(dir + "stat"); err == nil {
		parseDetailStat(b, &in)
	}
	if b, err := os.ReadFile(dir + "cmdline"); err == nil {
		in.Cmdline = splitCmdline(b)
	}
	in.Exe, _ = os.Readlink(dir + "exe")
	if in.PPID > 0 {
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(in.PPID) + "/comm"); err == nil {
			in.ParentName = strings.TrimSpace(string(b))
		}
	}
	if b, err := os.ReadFile(dir + "smaps_rollup"); err == nil {
		// RSS from the same walk of the page tables as PSS and Private, so the
		// three agree. status's VmRSS is the kernel's running counter, batched
		// per CPU, and can sit below a PSS read an instant later.
		if rss := kBField(b, "Rss:"); rss > 0 {
			in.RSS = rss
		}
		in.PSS = kBField(b, "Pss:")
		in.Private = kBField(b, "Private_Clean:") + kBField(b, "Private_Dirty:")
		in.HaveSmaps = true
	}
	if names, err := readdirnames(dir + "fd"); err == nil {
		in.OpenFiles, in.HaveFiles = len(names), true
	}
	if b, err := os.ReadFile(dir + "cgroup"); err == nil {
		in.Unit = string(UnitFromCgroup(b))
	}
	return in, nil
}

// readdirnames lists a directory's entry names.
func readdirnames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

// parseStatus reads the fields of /proc/[pid]/status that the panel shows.
func parseStatus(b []byte, in *Info) {
	for _, line := range strings.Split(string(b), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "Name":
			in.Name = val
		case "State":
			// "S (sleeping)": the word in brackets is the readable half.
			if i, j := strings.IndexByte(val, '('), strings.IndexByte(val, ')'); i >= 0 && j > i {
				in.State = val[i+1 : j]
			}
		case "PPid":
			in.PPID, _ = strconv.Atoi(val)
		case "Uid":
			if f := strings.Fields(val); len(f) > 0 {
				in.User = f[0]
				if u, err := user.LookupId(f[0]); err == nil {
					in.User = u.Username
				}
			}
		case "Threads":
			in.Threads, _ = strconv.Atoi(val)
		case "VmRSS":
			in.RSS = kB(val)
		case "VmSwap":
			in.Swap = kB(val)
		}
	}
}

// parseDetailStat reads the CPU time, nice value and start time from
// /proc/[pid]/stat. Fields are counted from after the last ')', where field 3
// (state) is index 0: utime 14 and stime 15 are indices 11 and 12, nice 19 is 16,
// and starttime 22 is 19.
func parseDetailStat(b []byte, in *Info) {
	rp := bytes.LastIndexByte(b, ')')
	if rp < 0 || rp+2 > len(b) {
		return
	}
	f := strings.Fields(string(b[rp+2:]))
	if len(f) < 20 {
		return
	}
	utime, _ := strconv.ParseUint(f[11], 10, 64)
	stime, _ := strconv.ParseUint(f[12], 10, 64)
	in.CPUTime = time.Duration(float64(utime+stime) / clockTick * float64(time.Second))
	if n, err := strconv.Atoi(f[16]); err == nil {
		in.Nice, in.HaveNice = n, true
	}
	if start, err := strconv.ParseUint(f[19], 10, 64); err == nil {
		if boot, ok := bootTime(); ok {
			in.Started = boot.Add(time.Duration(float64(start) / clockTick * float64(time.Second)))
		}
	}
}

// bootTime is when the machine started, from the btime line of /proc/stat.
func bootTime() (time.Time, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			if s, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(s, 0), true
			}
		}
	}
	return time.Time{}, false
}

// splitCmdline turns the NUL-separated /proc/[pid]/cmdline into arguments. A
// program that rewrites its own arguments sometimes leaves one string with
// spaces and no NULs; that is kept as it is rather than guessed at.
func splitCmdline(b []byte) []string {
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}

// kBField finds the line "key   1234 kB" in b and returns it in bytes. The key
// must start the line: "Pss:" is also the tail of "SwapPss:".
func kBField(b []byte, key string) uint64 {
	var rest []byte
	if bytes.HasPrefix(b, []byte(key)) {
		rest = b[len(key):]
	} else if i := bytes.Index(b, []byte("\n"+key)); i >= 0 {
		rest = b[i+1+len(key):]
	} else {
		return 0
	}
	if j := bytes.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return kB(string(rest))
}

// kB parses "1234 kB" into bytes.
func kB(s string) uint64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.ParseUint(f[0], 10, 64)
	return n * 1024
}
