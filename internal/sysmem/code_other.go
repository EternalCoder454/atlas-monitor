//go:build !linux

package sysmem

// ReleaseCode is Linux-only; elsewhere there is nothing to ask for.
func ReleaseCode() {}
