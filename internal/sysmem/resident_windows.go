package sysmem

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS. CB must be set before the
// call; the kernel uses it to tell which version it was handed.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// PROCESS_MEMORY_COUNTERS is 72 bytes on 64-bit Windows: two 32-bit fields and
// eight pointer-sized ones. Checked, because a wrong layout would report some other
// field as the working set and the number would look believable.
const (
	_ = unsafe.Sizeof(processMemoryCounters{}) - 72
	_ = 72 - unsafe.Sizeof(processMemoryCounters{})

	_ = unsafe.Offsetof(processMemoryCounters{}.WorkingSetSize) - 16
	_ = 16 - unsafe.Offsetof(processMemoryCounters{}.WorkingSetSize)
)

var (
	psapi                    = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

// Resident is this process's resident set size in bytes.
//
// Windows calls it the working set, which is what Task Manager shows in its Memory
// column — so the Settings page reads the same number a curious user would check
// it against.
func Resident() (uint64, bool) {
	c := processMemoryCounters{CB: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	r, _, _ := procGetProcessMemoryInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&c)),
		uintptr(c.CB))
	if r == 0 {
		return 0, false
	}
	return uint64(c.WorkingSetSize), true
}
