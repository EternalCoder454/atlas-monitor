// Package format provides human-readable formatting of bytes, rates, and
// frequencies, shared by the graph widget and the content views.
//
// Every formatter comes in two shapes: an Append* function that writes into a
// caller-owned buffer and allocates nothing, and a string wrapper for one-shot
// callers. The live views refresh once a second across hundreds of labels, so
// they use the Append* forms and only materialise a string when the text
// actually changed (see ui.liveLabel).
package format

import "strconv"

const (
	kib = 1024.0
	mib = kib * 1024
	gib = mib * 1024
	tib = gib * 1024
)

// dash is the placeholder shown for values that are unavailable.
const dash = "—"

// appendFixed appends v with prec decimals followed by unit.
func appendFixed(dst []byte, v float64, prec int, unit string) []byte {
	dst = strconv.AppendFloat(dst, v, 'f', prec, 64)
	return append(dst, unit...)
}

// AppendBytes appends a byte count with binary (IEC) units, e.g. "10.7 GiB".
func AppendBytes(dst []byte, b uint64) []byte {
	f := float64(b)
	switch {
	case f >= tib:
		return appendFixed(dst, f/tib, 2, " TiB")
	case f >= gib:
		return appendFixed(dst, f/gib, 2, " GiB")
	case f >= mib:
		return appendFixed(dst, f/mib, 1, " MiB")
	case f >= kib:
		return appendFixed(dst, f/kib, 0, " KiB")
	default:
		return append(strconv.AppendUint(dst, b, 10), " B"...)
	}
}

// AppendGiB appends a byte count specifically in gibibytes, e.g. "10.71 GiB".
func AppendGiB(dst []byte, b uint64) []byte {
	return appendFixed(dst, float64(b)/gib, 2, " GiB")
}

// AppendRate appends a byte-per-second value, e.g. "1.5 MiB/s".
func AppendRate(dst []byte, bps float64) []byte {
	switch {
	case bps >= gib:
		return appendFixed(dst, bps/gib, 2, " GiB/s")
	case bps >= mib:
		return appendFixed(dst, bps/mib, 1, " MiB/s")
	case bps >= kib:
		return appendFixed(dst, bps/kib, 0, " KiB/s")
	default:
		return appendFixed(dst, bps, 0, " B/s")
	}
}

// AppendMHz appends a clock speed in MHz, switching to GHz above 1000.
func AppendMHz(dst []byte, mhz float64) []byte {
	switch {
	case mhz >= 1000:
		return appendFixed(dst, mhz/1000, 2, " GHz")
	case mhz <= 0:
		return append(dst, dash...)
	default:
		return appendFixed(dst, mhz, 0, " MHz")
	}
}

// AppendPercent appends a 0..100 value with no decimals.
func AppendPercent(dst []byte, p float64) []byte {
	return appendFixed(dst, p, 0, "%")
}

// AppendPercent1 appends a 0..100 value with one decimal (process columns).
func AppendPercent1(dst []byte, p float64) []byte {
	return appendFixed(dst, p, 1, "%")
}

// AppendTemp appends a temperature in Celsius, or the dash if unavailable (<0).
func AppendTemp(dst []byte, c float64) []byte {
	if c < 0 {
		return append(dst, dash...)
	}
	return appendFixed(dst, c, 0, " °C")
}

// AppendInt appends a plain integer.
func AppendInt(dst []byte, v int) []byte { return strconv.AppendInt(dst, int64(v), 10) }

// Bytes formats a byte count with binary (IEC) units, e.g. "10.7 GiB".
func Bytes(b uint64) string { return string(AppendBytes(nil, b)) }

// GiB formats a byte count specifically in gibibytes, e.g. "10.71 GiB".
func GiB(b uint64) string { return string(AppendGiB(nil, b)) }

// Rate formats a byte-per-second value, e.g. "1.5 MiB/s".
func Rate(bps float64) string { return string(AppendRate(nil, bps)) }

// MHz formats a clock speed in MHz, switching to GHz above 1000.
func MHz(mhz float64) string { return string(AppendMHz(nil, mhz)) }
