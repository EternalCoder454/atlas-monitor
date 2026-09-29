//go:build !linux

package sensors

// Windows has no general interface for hardware sensors. The ones that exist are
// per vendor, and WMI's thermal zone is absent or administrator-only on most
// machines — the same reason the CPU page reports no temperature there. So the
// Sensors page is not offered at all rather than shown empty.

// Available is false: see above.
func Available() bool { return false }

// Discover finds nothing: see above.
func Discover() []*Device { return nil }

// Close has nothing to release.
func Close([]*Device) {}
