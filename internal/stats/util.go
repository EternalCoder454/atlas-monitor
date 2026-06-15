package stats

import (
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

// atou parses an unsigned integer, returning 0 on error.
func atou(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
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
