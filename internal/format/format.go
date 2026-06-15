// Package format provides human-readable formatting of bytes, rates, and
// frequencies, shared by the graph widget and the content views.
package format

import "fmt"

const (
	kib = 1024.0
	mib = kib * 1024
	gib = mib * 1024
	tib = gib * 1024
)

// Bytes formats a byte count with binary (IEC) units, e.g. "10.7 GiB".
func Bytes(b uint64) string {
	f := float64(b)
	switch {
	case f >= tib:
		return fmt.Sprintf("%.2f TiB", f/tib)
	case f >= gib:
		return fmt.Sprintf("%.2f GiB", f/gib)
	case f >= mib:
		return fmt.Sprintf("%.1f MiB", f/mib)
	case f >= kib:
		return fmt.Sprintf("%.0f KiB", f/kib)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// GiB formats a byte count specifically in gibibytes, e.g. "10.71 GiB".
func GiB(b uint64) string {
	return fmt.Sprintf("%.2f GiB", float64(b)/gib)
}

// Rate formats a byte-per-second value, e.g. "1.5 MiB/s".
func Rate(bps float64) string {
	switch {
	case bps >= gib:
		return fmt.Sprintf("%.2f GiB/s", bps/gib)
	case bps >= mib:
		return fmt.Sprintf("%.1f MiB/s", bps/mib)
	case bps >= kib:
		return fmt.Sprintf("%.0f KiB/s", bps/kib)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

// MHz formats a clock speed in MHz, switching to GHz above 1000.
func MHz(mhz float64) string {
	if mhz >= 1000 {
		return fmt.Sprintf("%.2f GHz", mhz/1000)
	}
	if mhz <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f MHz", mhz)
}

// Percent formats a 0..100 value with no decimals.
func Percent(p float64) string {
	return fmt.Sprintf("%.0f%%", p)
}

// Temp formats a temperature in Celsius, or "—" if unavailable (<0).
func Temp(c float64) string {
	if c < 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f °C", c)
}
