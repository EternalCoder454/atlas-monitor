//go:build !linux

package ease

import "errors"

// System is Linux-only. Windows has a real equivalent — EcoQoS, which is what
// Task Manager's efficiency mode uses — but telling which application is
// playing sound there means the audio session API, and none of it can be tried
// without a Windows machine to try it on.
func System(Identity) (*Controller, error) {
	return nil, errors.New("automatic easing is not available on this platform yet")
}
