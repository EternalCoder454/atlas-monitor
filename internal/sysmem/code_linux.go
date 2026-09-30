//go:build linux

package sysmem

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// madvPageout is MADV_PAGEOUT (Linux 5.4): reclaim these pages now.
const madvPageout = 21

// ReleaseCode drops the pages of Atlas's own executable that it is not using.
//
// The binary's code and read-only data are mapped from the file and read in as
// they are touched, and most of what is touched is touched once: starting up
// registers every GTK type gotk4 knows, and the code and tables for that are
// never needed again. Those pages stay counted in the resident set all the same
// — about 15 MiB of it — until the kernel happens to need the memory. Paging
// them out shows the working set for what it is: measured on the CPU page, the
// executable's share fell from 14.6 MiB to 5.2 MiB and settled at 7.8 MiB as the
// pages that are used came back, a fault each, from the page cache or the disk.
//
// Only the executable's own read-only mappings are touched. Shared libraries
// are left alone: the kernel would not reclaim pages other processes map
// anyway. A kernel without MADV_PAGEOUT refuses it, and nothing happens.
func ReleaseCode() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.Open("/proc/self/maps")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// address perms offset dev inode path — the path may contain spaces.
		fields := strings.SplitN(sc.Text(), " ", 6)
		if len(fields) < 6 || strings.ContainsRune(fields[1], 'w') {
			continue
		}
		if strings.TrimLeft(fields[5], " ") != exe {
			continue
		}
		lo, hi, ok := strings.Cut(fields[0], "-")
		if !ok {
			continue
		}
		start, err1 := strconv.ParseUint(lo, 16, 64)
		end, err2 := strconv.ParseUint(hi, 16, 64)
		if err1 != nil || err2 != nil || end <= start {
			continue
		}
		syscall.Syscall(syscall.SYS_MADVISE, uintptr(start), uintptr(end-start), madvPageout)
	}
}
