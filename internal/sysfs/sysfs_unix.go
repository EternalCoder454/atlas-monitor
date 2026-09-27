//go:build unix

package sysfs

import "syscall"

// readAt re-reads the file from offset zero in a single syscall.
//
// This is the reason the package exists. Profiling showed os.ReadFile's
// open/read/read/close was a sixth of the app's CPU time; pread on a
// already-open descriptor is one syscall and no allocation. os.File.ReadAt would
// reach the same system call, but goes through Go's file locking and partial-read
// loop to do it, and this is the hottest path in the collector.
func (f *File) readAt(b []byte) (int, error) {
	return syscall.Pread(int(f.f.Fd()), b, 0)
}
