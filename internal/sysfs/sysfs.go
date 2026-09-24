// Package sysfs re-reads small kernel files without reopening them.
//
// Atlas samples the same handful of /proc and /sys files every second — per-core
// frequencies, a temperature, /proc/stat, /proc/meminfo, the GPU's counters.
// Read with os.ReadFile that is four syscalls each (open, read, read, close)
// plus an allocation; held open and re-read with pread it is one. Profiling the
// app showed those opens were a sixth of its entire CPU time.
//
// Both procfs and sysfs regenerate a file's contents on each read, so pread
// from offset zero is how these are meant to be polled.
//
// A File is not safe for concurrent use; each collector goroutine owns its own.
package sysfs

import (
	"os"
	"syscall"
)

// smallValue is the buffer size for a single attribute — a number or a short
// string. Larger files say how much they need via OpenSize.
const smallValue = 256

// File is a kernel file held open for repeated reads.
type File struct {
	f   *os.File
	buf []byte
}

// Open holds path open for reading a single small value. It returns nil if the
// file cannot be opened, and every method is safe to call on a nil File —
// callers are collectors that must keep working when an attribute is absent.
func Open(path string) *File { return OpenSize(path, smallValue) }

// OpenSize holds path open with a buffer of at least size bytes.
func OpenSize(path string, size int) *File {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	if size < smallValue {
		size = smallValue
	}
	return &File{f: f, buf: make([]byte, size)}
}

// OpenFirst holds open the first of paths that exists, for attributes whose
// location differs between drivers. It returns nil when none do.
func OpenFirst(paths ...string) *File {
	for _, p := range paths {
		if f := Open(p); f != nil {
			return f
		}
	}
	return nil
}

// Close releases the descriptor.
func (f *File) Close() {
	if f != nil && f.f != nil {
		f.f.Close()
		f.f = nil
	}
}

// OK reports whether the file is open.
func (f *File) OK() bool { return f != nil && f.f != nil }

// Bytes re-reads the file and returns its contents. The result aliases the
// File's buffer and is only valid until the next read.
func (f *File) Bytes() ([]byte, bool) {
	if !f.OK() {
		return nil, false
	}
	for {
		n, err := syscall.Pread(int(f.f.Fd()), f.buf, 0)
		if err != nil || n < 0 {
			return nil, false
		}
		// A read that exactly fills the buffer may have been truncated; grow
		// and try again so a file that outgrows its buffer is never silently
		// cut short.
		if n == len(f.buf) {
			f.buf = make([]byte, len(f.buf)*2)
			continue
		}
		if n == 0 {
			return nil, false
		}
		return f.buf[:n], true
	}
}

// Uint re-reads the file as an unsigned integer.
func (f *File) Uint() (uint64, bool) {
	b, ok := f.Bytes()
	if !ok {
		return 0, false
	}
	return ParseUint(b)
}

// ParseUint reads the leading digits of b, ignoring any trailing newline.
func ParseUint(b []byte) (uint64, bool) {
	var v uint64
	digits := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			break
		}
		v = v*10 + uint64(c-'0')
		digits++
	}
	return v, digits > 0
}

// ReadUint reads a one-shot value from a file not worth holding open —
// discovery and static information, read once at startup.
func ReadUint(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return ParseUint(TrimSpace(b))
}

// ReadString reads a one-shot string value, trimmed.
func ReadString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(TrimSpace(b))
}

// TrimSpace drops surrounding ASCII whitespace.
func TrimSpace(b []byte) []byte {
	for len(b) > 0 && isSpace(b[0]) {
		b = b[1:]
	}
	for len(b) > 0 && isSpace(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	return b
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
