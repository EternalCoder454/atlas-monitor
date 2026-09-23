package stats

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
)

// readString reads a sysfs/procfs file and trims surrounding whitespace.
func readString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// readUint reads a file containing a single unsigned integer.
func readUint(path string) (uint64, error) {
	s, err := readString(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(s, 10, 64)
}

// readInt reads a file containing a single signed integer.
func readInt(path string) (int, error) {
	s, err := readString(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// parseUintBytes parses the leading ASCII digits of b into a uint64 without
// allocating (no string conversion). Used on hot /proc parse paths.
func parseUintBytes(b []byte) uint64 {
	var v uint64
	for _, ch := range b {
		if ch < '0' || ch > '9' {
			break
		}
		v = v*10 + uint64(ch-'0')
	}
	return v
}

// readInto reads a whole /proc or /sys file into buf, growing it only when a
// file outgrows it. Returns the data (aliasing buf) and the buffer to keep.
// These files are small and are re-read every second, so reusing one buffer per
// call site removes the allocation entirely.
func readInto(path string, buf []byte) (data, keep []byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, buf, err
	}
	defer f.Close()
	if buf == nil {
		buf = make([]byte, 0, 8192)
	}
	buf = buf[:0]
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)] // grow via append's doubling
		}
		n, err := f.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			return buf, buf, nil
		}
		if err != nil {
			return nil, buf, err
		}
	}
}

// nextLine splits the first line off data, returning it without its newline.
func nextLine(data []byte) (line, rest []byte) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[:i], data[i+1:]
	}
	return data, nil
}

// field returns the idx-th space-separated field of b, or nil.
func field(b []byte, idx int) []byte {
	n := len(b)
	for i := 0; i < n; {
		for i < n && (b[i] == ' ' || b[i] == '\t') {
			i++
		}
		start := i
		for i < n && b[i] != ' ' && b[i] != '\t' {
			i++
		}
		if i == start {
			break
		}
		if idx == 0 {
			return b[start:i]
		}
		idx--
	}
	return nil
}

// clamp constrains v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
