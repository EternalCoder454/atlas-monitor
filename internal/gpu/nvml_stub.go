//go:build !linux || !cgo

package gpu

// newNVMLBackend is unavailable without cgo: NVML is dlopen'd at runtime.
func newNVMLBackend() backend { return nil }
