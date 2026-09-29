// Package graph is a reusable GtkDrawingArea + Cairo widget that renders a live
// 60-sample ring buffer as a filled area chart with a line on top, in the style
// of the Windows Task Manager performance graphs: a bordered plot with a square
// grid, and the captions above and below it rather than over the data.
package graph

import (
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/stats"
)

// Mode selects the Y-axis scaling and value formatting.
type Mode int

const (
	// Percent fixes the Y-axis to 0..100 and formats values as "NN%".
	Percent Mode = iota
	// Bytes auto-scales the Y-axis and formats values as byte rates.
	Bytes
	// Watts auto-scales the Y-axis and formats values as a power draw.
	Watts
)

// autoScaled reports whether the mode picks its Y-axis from the data rather
// than pinning it to 0..100.
func (m Mode) autoScaled() bool { return m == Bytes || m == Watts }

// Graph is a single live chart bound to a ring buffer.
type Graph struct {
	*gtk.DrawingArea
	label   string
	color   Color
	rb      *stats.RingBuffer
	mode    Mode
	scratch []float64 // pre-allocated read buffer, never grows
	dashed  bool      // draw the line dashed; see SetDashed

	// Current-value text, cached so a steady reading costs no allocation.
	valBuf  []byte
	valText string
	peakBuf []byte

	// Text is drawn through Pango rather than Cairo's "toy" API, so the chart
	// labels use the desktop font at the desktop's hinting settings instead of
	// whatever cairo_select_font_face picks. The layouts are built once from the
	// widget and reused: creating one per frame would both re-shape the text and
	// churn a gotk4 wrapper each time. A font change at runtime needs a restart
	// to take effect here, which is the same deal the rendering mode has.
	labelLayout *pango.Layout
	valueLayout *pango.Layout
	peakLayout  *pango.Layout
	spanLayout  *pango.Layout
	zeroLayout  *pango.Layout
	shownValue  string
	shownPeak   string

	// The history caption is the same text on every graph and changes only when
	// the refresh interval does, so each graph remembers which span its layout
	// was last set for and touches Pango again only when the shared one moves.
	spanNanos int64
	spanText  string
}

// New builds a graph for the given ring buffer. height is the requested
// content height in pixels.
func New(label string, color Color, rb *stats.RingBuffer, mode Mode, height int) *Graph {
	g := &Graph{
		DrawingArea: gtk.NewDrawingArea(),
		label:       label,
		color:       color,
		rb:          rb,
		mode:        mode,
		scratch:     make([]float64, stats.HistLen),
	}
	// Deliberately a fixed height rather than one that grows with the window.
	// Letting the charts expand was tried and is worse: a percentage chart draws
	// its line at the reading, so on an idle machine a taller chart is simply a
	// larger empty area, and on the Storage page the growth pushed the details
	// below the bottom edge. What the space needed was meaning, not more of it —
	// hence the peak marker on the auto-scaled charts.
	g.SetContentHeight(height)
	g.SetHExpand(true)
	g.AddCSSClass("am-graph")
	g.SetDrawFunc(g.draw)
	return g
}

// Layout of the widget, top to bottom: a caption band, the plot, a caption band.
const (
	// captionPad is added to one line of the graph's own text to make a caption
	// band. The line height comes from Pango, so a larger system font grows the
	// bands instead of clipping the text.
	captionPad = 4
	// captionInset keeps caption text off the very edge of the widget, in line
	// with the plot's border.
	captionInset = 1
	// ceilingGap is the least space kept between the caption's two ends.
	ceilingGap = 12.0
	// captionTextY is where a line of text starts inside its band, which centres
	// it because the band is the text height plus captionPad.
	captionTextY = captionPad / 2

	// cellTarget is the row height the grid aims for, in pixels. Rows are counted
	// out of the plot height, so the actual height drifts a little either side.
	cellTarget = 36
	minRows    = 2
	maxRows    = 10

	// A flat wash under the line. It is a little fainter on a light theme, where
	// the same alpha of a saturated colour reads much heavier against white.
	fillAlphaDark  = 0.22
	fillAlphaLight = 0.16

	lineWidth = 1.5
)

// dashPattern is the on/off lengths of a dashed line, in pixels: 4 on, 3 off. It
// is a package-level slice so that turning dashing on hands Cairo the same
// backing array every frame instead of allocating a fresh one.
var dashPattern = []float64{4, 3}

// historySpan is how much time the graphs' 60 samples cover, in nanoseconds. It
// is the same for every graph, because they are all fed by the one refresh
// interval, so it lives here and is stored atomically: the UI sets it whenever
// the interval changes and the graphs read it on their next draw.
var historySpan atomic.Int64

// SetHistorySpan tells every graph how much time its history covers, which is the
// refresh interval times the number of samples. It labels the left end of the
// time axis. A zero or negative span hides the label.
func SetHistorySpan(d time.Duration) { historySpan.Store(int64(d)) }

// formatSpan words a history span for the bottom-left caption: whole seconds
// under two minutes, whole minutes from there on. Two minutes is where counting
// seconds stops being readable at a glance. It returns "" for a span that is
// unset, which draws nothing.
func formatSpan(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return ""
	}
	if d < 2*time.Minute {
		s := int(d / time.Second)
		if s == 1 {
			return "1 second"
		}
		return strconv.Itoa(s) + " seconds"
	}
	// Rounded rather than truncated: 150 s is closer to the "3 minutes" it reads
	// as than to "2".
	return strconv.Itoa(int((d+30*time.Second)/time.Minute)) + " minutes"
}

// plotGeometry splits a widget of height h into two caption bands, each a line of
// text (lineH) plus captionPad tall, and the plot between them. It returns the
// band height, which is also where the plot starts, and the plot height. A
// widget too short for both bands gets a plot of zero height, not a negative one.
func plotGeometry(h float64, lineH int) (band, plotH float64) {
	band = float64(lineH + captionPad)
	plotH = h - 2*band
	if plotH < 0 {
		plotH = 0
	}
	return band, plotH
}

// gridRows is how many rows the grid divides a plot of the given height into:
// one per cellTarget pixels, but never fewer than two, which would leave a chart
// with a single empty box, nor more than ten, which would be a mesh.
func gridRows(plotH float64) int {
	rows := int(math.Round(plotH / cellTarget))
	if rows < minRows {
		return minRows
	}
	if rows > maxRows {
		return maxRows
	}
	return rows
}

// crisp moves a coordinate to the middle of the pixel it falls in. A one-pixel
// line centred on a pixel boundary is smeared over two pixels at half strength;
// centred on the middle of a pixel it is one sharp pixel.
func crisp(v float64) float64 { return math.Floor(v) + 0.5 }

// SetDashed draws the line dashed instead of solid, with the fill unchanged. It
// is for a chart that shares its colour with another, like write beside read or
// upload beside download, so that the two can be told apart. It takes effect on
// the next draw, which the caller's regular Refresh provides.
func (g *Graph) SetDashed(on bool) { g.dashed = on }

// dashes is the dash pattern for the line, or nil for a solid one.
func (g *Graph) dashes() []float64 {
	if g.dashed {
		return dashPattern
	}
	return nil
}

// Refresh requests a redraw. Call from the GTK main thread.
func (g *Graph) Refresh() { g.QueueDraw() }

func (g *Graph) draw(area *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	if g.rb == nil || w <= 0 || h <= 0 {
		return
	}
	width, height := float64(w), float64(h)

	fr, fg, fb := Foreground(area)

	n := g.rb.ReadInto(g.scratch)

	if g.labelLayout == nil {
		// The label layout carries its own two trailing spaces, which Pango counts
		// in the width, so the value can be placed straight after it. With no label
		// there is nothing to separate the value from.
		labelText := ""
		if g.label != "" {
			labelText = g.label + "  "
		}
		g.labelLayout = area.CreatePangoLayout(labelText)
		g.valueLayout = area.CreatePangoLayout("")
		g.peakLayout = area.CreatePangoLayout("")
		g.spanLayout = area.CreatePangoLayout("")
		g.zeroLayout = area.CreatePangoLayout("0")
	}

	// Current value. Pango measures the text, so it lands where it should instead
	// of where a character-count estimate guessed.
	cur := 0.0
	if n > 0 {
		cur = g.scratch[n-1]
	}
	text := g.formatValue(cur)
	if text != g.shownValue {
		g.valueLayout.SetText(text)
		g.shownValue = text
	}
	_, th := g.valueLayout.PixelSize()

	// The caption bands are one line of text tall (plus the padding), measured
	// rather than assumed so they follow the system font. The value's layout is
	// never empty, so it always has a line height; the label is measured as well
	// because it also gives the value its x position, and because an unusual label
	// can pull in a fallback font with a taller line.
	lw, lineH := 0, th
	if g.label != "" {
		var lh int
		lw, lh = g.labelLayout.PixelSize()
		if lh > lineH {
			lineH = lh
		}
	}
	band, plotH := plotGeometry(height, lineH)

	if plotH >= 2 {
		// Determine vertical scale.
		scale := 100.0
		if g.mode.autoScaled() {
			scale = g.rb.Max() * 1.25
			if scale < 1 {
				scale = 1
			}
		}

		g.drawGrid(cr, fr, fg, fb, width, band, plotH)
		if n >= 2 {
			g.drawSeries(cr, fr, fg, fb, width, band, plotH, n, scale)
		}

		// The border goes over the series, so a line at the floor or the ceiling of
		// the chart does not blur it.
		cr.SetLineWidth(1)
		cr.SetSourceRGBA(fr, fg, fb, 0.30)
		cr.Rectangle(0.5, band+0.5, width-1, plotH-1)
		cr.Stroke()
	}

	// Top band, left: the label, then the current value beside it.
	if g.label != "" {
		cr.SetSourceRGBA(fr, fg, fb, 0.66)
		cr.MoveTo(captionInset, captionTextY)
		pangocairo.ShowLayout(cr, g.labelLayout)
	}
	cr.SetSourceRGBA(fr, fg, fb, 0.92)
	cr.MoveTo(captionInset+float64(lw), captionTextY)
	pangocairo.ShowLayout(cr, g.valueLayout)

	// Top band, right: what the top of the chart is worth.
	//
	// On an auto-scaled chart the vertical axis means nothing on its own: the
	// same picture describes kilobytes and gigabytes, so the highest reading
	// still on screen says what the height stands for. A percentage chart is
	// pinned to a hundred — but nothing on screen said so, and sitting beside
	// charts that do rescale, a line along the bottom could as easily have been
	// read as a machine at full tilt on a chart scaled to itself.
	ceiling := ""
	switch {
	case g.mode.autoScaled() && n > 0:
		if peak := g.rb.Max(); peak > 0 {
			// Built in the scratch buffer and compared as bytes, so a steady peak
			// allocates no string.
			buf := g.formatInto("peak ", peak)
			if string(buf) != g.shownPeak {
				g.shownPeak = string(buf)
				g.peakLayout.SetText(g.shownPeak)
			}
			ceiling = g.shownPeak
		}
	case g.mode == Percent:
		ceiling = "100%"
		if ceiling != g.shownPeak {
			g.peakLayout.SetText(ceiling)
			g.shownPeak = ceiling
		}
	}
	// Only where it fits: in a narrow window "Download  1.2 MB/s" and "peak
	// 5.4 MB/s" meet, and the reading beside the label is the one that matters.
	if ceiling != "" {
		pw, _ := g.peakLayout.PixelSize()
		vw, _ := g.valueLayout.PixelSize()
		leftEnd := captionInset + float64(lw+vw)
		if x := width - float64(pw) - captionInset; x >= leftEnd+ceilingGap {
			cr.SetSourceRGBA(fr, fg, fb, 0.5)
			cr.MoveTo(x, captionTextY)
			pangocairo.ShowLayout(cr, g.peakLayout)
		}
	}

	// Bottom band: the time axis, from how far back the history goes to now.
	if ns := historySpan.Load(); ns != g.spanNanos {
		g.spanNanos = ns
		g.spanText = formatSpan(time.Duration(ns))
		g.spanLayout.SetText(g.spanText)
	}
	bottomY := height - band + captionTextY
	cr.SetSourceRGBA(fr, fg, fb, 0.5)
	if g.spanText != "" {
		cr.MoveTo(captionInset, bottomY)
		pangocairo.ShowLayout(cr, g.spanLayout)
	}
	zw, _ := g.zeroLayout.PixelSize()
	cr.MoveTo(width-float64(zw)-captionInset, bottomY)
	pangocairo.ShowLayout(cr, g.zeroLayout)
}

// drawGrid strokes the grid inside the plot whose top edge is at top. Its rows
// divide the plot evenly, and the columns repeat the row height leftwards from
// the right edge, where the newest sample is, so every cell is a square.
func (g *Graph) drawGrid(cr *cairo.Context, fr, fg, fb, width, top, plotH float64) {
	rows := gridRows(plotH)
	cell := plotH / float64(rows)

	cr.SetLineWidth(1)
	cr.SetSourceRGBA(fr, fg, fb, 0.08)
	// The outermost lines are the border's, so only the ones between are drawn.
	for i := 1; i < rows; i++ {
		y := crisp(top + cell*float64(i))
		cr.MoveTo(0, y)
		cr.LineTo(width, y)
	}
	for k := 1; ; k++ {
		x := width - cell*float64(k)
		if x <= 1 { // the left border is that far in
			break
		}
		x = crisp(x)
		cr.MoveTo(x, top)
		cr.LineTo(x, top+plotH)
	}
	cr.Stroke()
}

// drawSeries fills under the sample line and strokes it, all clipped to the plot.
func (g *Graph) drawSeries(cr *cairo.Context, fr, fg, fb, width, top, plotH float64, n int, scale float64) {
	bottom := top + plotH
	dx := width / float64(stats.HistLen-1)
	px := func(i int) float64 { return width - dx*float64(n-1-i) }
	py := func(v float64) float64 {
		r := v / scale
		if r > 1 {
			r = 1
		}
		if r < 0 {
			r = 0
		}
		return bottom - r*plotH
	}

	// Clipped so the stroke's width and a dash's ends cannot spill into the
	// caption bands. Save and Restore take the clip away again, and the dash with
	// it.
	cr.Save()
	cr.Rectangle(0, top, width, plotH)
	cr.Clip()

	// Filled area under the curve, a flat wash of the series colour. The palette
	// was chosen against a dark background. On a light one the paler colours leave
	// a line that all but disappears against white, so the line is taken down a
	// shade when the theme is light; the fill only gets fainter, since it is
	// already a tint. Dark mode keeps the colour it was drawn for.
	light := (fr+fg+fb)/3 < 0.5 // dark text means a light background
	lr, lg, lb := g.color.R, g.color.G, g.color.B
	fillAlpha := fillAlphaDark
	if light {
		lr, lg, lb = lr*0.72, lg*0.72, lb*0.72
		fillAlpha = fillAlphaLight
	}
	cr.MoveTo(px(0), bottom)
	for i := 0; i < n; i++ {
		cr.LineTo(px(i), py(g.scratch[i]))
	}
	cr.LineTo(px(n-1), bottom)
	cr.ClosePath()
	cr.SetSourceRGBA(g.color.R, g.color.G, g.color.B, fillAlpha)
	cr.Fill()

	// The line on top, dashed if this chart asked for it.
	if d := g.dashes(); d != nil {
		cr.SetDash(d, 0)
	}
	cr.SetSourceRGBA(lr, lg, lb, 1)
	cr.SetLineWidth(lineWidth)
	cr.MoveTo(px(0), py(g.scratch[0]))
	for i := 1; i < n; i++ {
		cr.LineTo(px(i), py(g.scratch[i]))
	}
	cr.Stroke()
	cr.Restore()
}

// formatInto renders v the way this chart formats its readings, after prefix,
// into a scratch buffer kept apart from the current-value one so the two cannot
// clobber each other mid-draw.
func (g *Graph) formatInto(prefix string, v float64) []byte {
	g.peakBuf = append(g.peakBuf[:0], prefix...)
	switch g.mode {
	case Bytes:
		g.peakBuf = format.AppendRate(g.peakBuf, v)
	case Watts:
		g.peakBuf = format.AppendWatts(g.peakBuf, v)
	default:
		g.peakBuf = format.AppendPercent(g.peakBuf, v)
	}
	return g.peakBuf
}

// formatValue renders v into the graph's scratch buffer and returns the text,
// reusing the previous string whenever the reading is unchanged (the common
// case for an idle interface or a pinned percentage).
func (g *Graph) formatValue(v float64) string {
	switch g.mode {
	case Bytes:
		g.valBuf = format.AppendRate(g.valBuf[:0], v)
	case Watts:
		g.valBuf = format.AppendWatts(g.valBuf[:0], v)
	default:
		g.valBuf = format.AppendPercent(g.valBuf[:0], v)
	}
	if g.valText != string(g.valBuf) { // compares without allocating
		g.valText = string(g.valBuf)
	}
	return g.valText
}

// Foreground returns the widget's themed text colour, used for grid lines and
// labels. gtk_widget_get_color is a plain struct read; the GtkStyleContext it
// replaces is deprecated and allocated a wrapper object on every frame.
func Foreground(w *gtk.DrawingArea) (r, g, b float64) {
	c := w.Color()
	if c == nil {
		return 0.5, 0.5, 0.5
	}
	return float64(c.Red()), float64(c.Green()), float64(c.Blue())
}
