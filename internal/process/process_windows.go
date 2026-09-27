package process

import (
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"atlas-monitor/internal/winapi"
)

// The process table on Windows: one system call for all of it.
//
// The Linux scan is the expensive part of this package — an open and a read per
// process, several hundred times a tick, carefully arranged to allocate nothing.
// None of that is needed here. NtQuerySystemInformation returns every process
// with its CPU times, working set and I/O counters in a single buffer, so the
// work is a walk over what came back.
//
// Two columns cannot be filled and are left as the UI's "no reading" values
// rather than as zero, which would be a claim:
//
//   - Per-process GPU time. Linux reads it from each process's DRM fdinfo.
//     Windows keeps the equivalent behind ETW, which means an event-tracing
//     session for something the Apps table shows in a column, so GPU is -1.
//   - Per-process network bytes. Same story: attributing traffic to a process
//     needs ETW or the per-connection tables sampled far faster than a tick.
//     Linux only estimates this anyway, by counting sockets and dividing the
//     machine's total between them.

// collect samples every process and works out the rates.
func (c *Collector) collect() {
	now := time.Now()
	dt := now.Sub(c.lastTime).Seconds()
	first := c.lastTime.IsZero()
	if dt <= 0 {
		dt = 1
	}
	c.lastTime = now

	list, err := winapi.ReadProcesses()
	if err != nil {
		return
	}

	includeKernel := c.includeKernel.Load()
	ioWanted := c.wantDiskIO.Load()

	newPrev := c.prevSpare
	clear(newPrev)
	procs := c.scratch[:0]

	for _, p := range list {
		// The two pseudo-processes are Windows' equivalent of kernel threads: the
		// idle process is the machine doing nothing, and System is where the
		// kernel's own threads live. Neither is a program, and the idle one would
		// otherwise sit at the top of a list sorted by CPU, permanently, showing
		// whatever fraction of the machine is unused.
		kernel := p.PID == 0 || p.PID == 4
		if kernel && !includeKernel {
			continue
		}

		prev, hadPrev := c.prev[p.PID]
		cur := procPrev{
			cpuJiffies: p.CPUTicks,
			readBytes:  p.ReadBytes,
			writeBytes: p.WriteBytes,
		}
		newPrev[p.PID] = cur

		proc := Proc{
			PID:    p.PID,
			Name:   processName(p),
			Kernel: kernel,
			RSS:    p.WorkingSet,
			// Nothing reports these; -1 is what the UI draws as no reading.
			GPU: -1,
		}

		if hadPrev && !first {
			if p.CPUTicks > prev.cpuJiffies {
				used := float64(p.CPUTicks-prev.cpuJiffies) / winapi.TicksPerSecond
				proc.CPU = clampPct(used / dt * 100)
			}
			if ioWanted {
				proc.DiskRead = deltaRate(p.ReadBytes, prev.readBytes, dt)
				proc.DiskWrite = deltaRate(p.WriteBytes, prev.writeBytes, dt)
			}
		}
		procs = append(procs, proc)
	}

	// The previous map and its spare swap rather than being reallocated, the same
	// arrangement the Linux scan uses: a steady process count settles into no
	// allocation per tick.
	c.prev, c.prevSpare = newPrev, c.prev
	c.scratch = procs

	c.mu.Lock()
	c.procs = append(c.procs[:0], procs...)
	c.mu.Unlock()
}

// processName is what to call a process in the list.
//
// Windows reports the executable's file name, extension and all. The extension is
// dropped because every row would otherwise end in ".exe", which is four
// characters of nothing in a column that has to be narrow.
func processName(p winapi.Process) string {
	name := p.Name
	if name == "" {
		// A process whose name the kernel will not give up — one that is
		// protected, or exiting — still has a pid, and that is better than a
		// blank row.
		return "(pid " + itoa(p.PID) + ")"
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 && strings.EqualFold(name[i:], ".exe") {
		name = name[:i]
	}
	return name
}

// itoa is strconv.Itoa for small positive numbers, kept local so this file does
// not pull in strconv for one call on a path that almost never runs.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// closePlatform has nothing to release: there are no descriptors held open.
func (c *Collector) closePlatform() {}

// StartTime identifies a process beyond its pid.
//
// Pids are reused, so the Energy Saver page pairs one with the moment its process
// started before remembering that it eased something off. Linux uses field 22 of
// /proc/[pid]/stat; here it is the creation time, which serves the same purpose —
// a different process with the same number will not share it.
func StartTime(pid int) (uint64, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	return uint64(creation.Nanoseconds()), true
}
