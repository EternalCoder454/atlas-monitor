package stats

import "bytes"

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
