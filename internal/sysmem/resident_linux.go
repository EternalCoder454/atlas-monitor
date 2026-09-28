package sysmem

import (
	"os"
	"strconv"
	"strings"
)

// Resident is this process's resident set size in bytes.
//
// From /proc/self/statm, whose second field is the resident page count — the same
// figure a task manager shows for Atlas. Unlike the rest of this package it has
// nothing to do with cgo, so it is tagged by platform alone.
func Resident() (uint64, bool) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}
