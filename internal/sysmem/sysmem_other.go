//go:build !linux || !cgo

package sysmem

import "runtime/debug"

// Tune is a no-op where glibc's mallopt is unavailable.
func Tune() {}

// Trim is a no-op where glibc's malloc_trim is unavailable.
func Trim() {}

// Release returns unused Go spans to the OS.
func Release() { debug.FreeOSMemory() }
