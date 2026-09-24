package graph

// Color is an RGB triple in the 0..1 range.
type Color struct{ R, G, B float64 }

func rgb(r, g, b uint8) Color {
	return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255}
}

// Semantic graph colors from the Atlas Monitor spec (GNOME palette).
var (
	ColorCPU      = rgb(0x35, 0x84, 0xe4) // GNOME blue
	ColorMemory   = rgb(0xe0, 0x1b, 0x24) // red
	ColorGPU      = rgb(0xff, 0x78, 0x00) // orange
	ColorNetDown  = rgb(0x2e, 0xc2, 0x7e) // green
	ColorNetUp    = rgb(0xc0, 0x61, 0xcb) // purple
	ColorDiskRead = rgb(0xf5, 0xc2, 0x11) // yellow
	ColorDiskWr   = rgb(0xed, 0x33, 0x3b) // red-orange
	ColorBattery  = rgb(0x33, 0xd1, 0x7a) // battery green
	ColorPowerDrw = rgb(0xf6, 0xd3, 0x2d) // draw: amber

	// ColorFree is the neutral used for the unfilled part of a capacity bar. It
	// is a grey rather than a hue so that "space you still have" never competes
	// with the reading beside it.
	ColorFree = rgb(0x9a, 0x9d, 0xa3)
)
