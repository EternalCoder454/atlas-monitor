package graph

// Color is an RGB triple in the 0..1 range.
type Color struct{ R, G, B float64 }

func rgb(r, g, b uint8) Color {
	return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255}
}

// Semantic graph colors: the series hues are the Windows Task Manager's
// dark-mode palette, so the charts read the same as the ones people already know.
// Where two series share a hue (download and upload, read and write) the second
// is drawn dashed by its caller, so the pair stays distinguishable.
var (
	ColorCPU      = rgb(0x39, 0xb8, 0xe3) // cyan
	ColorMemory   = rgb(0x5c, 0x9e, 0xfa) // blue
	ColorGPU      = rgb(0xde, 0x68, 0xf2) // magenta
	ColorNetDown  = rgb(0xf5, 0x62, 0x8e) // pink
	ColorNetUp    = rgb(0xf5, 0x62, 0x8e) // pink, dashed
	ColorDiskRead = rgb(0x84, 0xc7, 0x18) // lime
	ColorDiskWr   = rgb(0x84, 0xc7, 0x18) // lime, dashed
	ColorBattery  = rgb(0x33, 0xd1, 0x7a) // battery green
	ColorPowerDrw = rgb(0xf6, 0xd3, 0x2d) // draw: amber

	// ColorFree is the neutral used for the unfilled part of a capacity bar. It
	// is a grey rather than a hue so that "space you still have" never competes
	// with the reading beside it.
	ColorFree = rgb(0x9a, 0x9d, 0xa3)
)
