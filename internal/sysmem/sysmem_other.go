//go:build !linux || !cgo

package sysmem

import "runtime/debug"

// Tune is a no-op where glibc's mallopt is unavailable.
func Tune() {}

// Trim is a no-op where glibc's malloc_trim is unavailable.
func Trim() {}

// Release returns unused Go spans to the OS, and on Linux pages out the
// executable's idle code.
func Release() {
	debug.FreeOSMemory()
	ReleaseCode()
}
