//go:build !linux && !windows

package sysmem

// Resident has no implementation here, so the Settings page shows the figure as
// unavailable rather than as a zero that would read as "no memory in use".
func Resident() (uint64, bool) { return 0, false }
