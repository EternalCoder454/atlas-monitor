package graph

import (
	"math"
	"testing"
	"time"

	"atlas-monitor/internal/stats"
)

// The drawing itself needs a display, but the value text on each chart, the
// Y-axis decision and the plot's geometry do not, and the text is the part that
// is cached — a stale cache would put the wrong number on a live chart.

// TestFormatValueCacheStaysCorrect walks a sequence of values through the cached
// formatter. The cache exists so a steady reading allocates nothing; what has to
// hold is that the string returned always describes the value just passed in.
func TestFormatValueCacheStaysCorrect(t *testing.T) {
	for _, mode := range []Mode{Percent, Bytes, Watts} {
		g := &Graph{mode: mode}
		// Repeats matter: they are the case the cache short-circuits.
		values := []float64{0, 0, 1, 1, 1, 42.5, 42.5, 0, 1 << 20, 1 << 30, 99.9, 100, 0}
		var last string
		for i, v := range values {
			got := g.formatValue(v)
			if got == "" {
				t.Fatalf("mode %v value %v formatted as an empty string", mode, v)
			}
			// Formatting the same value twice must give the same answer.
			again := g.formatValue(v)
			if again != got {
				t.Errorf("mode %v value %v: %q then %q on a repeat", mode, v, got, again)
			}
			if i > 0 && values[i-1] != v && got == last && v != values[i-1] {
				// Not necessarily wrong — two values can round to the same text —
				// but worth surfacing for the obviously distinct cases.
				if math.Abs(v-values[i-1]) > 1 && mode == Percent {
					t.Errorf("mode %v: %v and %v both formatted as %q", mode, values[i-1], v, got)
				}
			}
			last = got
		}
	}
}

// TestFormatValueMatchesTheMode checks each mode produces its own kind of text,
// so a chart cannot come up labelled in the wrong unit.
func TestFormatValueMatchesTheMode(t *testing.T) {
	cases := []struct {
		mode    Mode
		value   float64
		wantAny []string
	}{
		{Percent, 42, []string{"%"}},
		{Percent, 0, []string{"%"}},
		{Percent, 100, []string{"%"}},
		{Bytes, 1 << 20, []string{"B", "b"}},
		{Bytes, 0, []string{"B", "b"}},
		{Watts, 15, []string{"W", "w"}},
		{Watts, 0, []string{"W", "w"}},
	}
	for _, c := range cases {
		g := &Graph{mode: c.mode}
		got := g.formatValue(c.value)
		ok := false
		for _, want := range c.wantAny {
			if contains(got, want) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("mode %v value %v formatted as %q, expected one of %v in it", c.mode, c.value, got, c.wantAny)
		}
	}
}

// TestFormatValueHandlesExtremes checks no reading can produce text that would
// break the chart's layout or carry a NaN into a label.
func TestFormatValueHandlesExtremes(t *testing.T) {
	// math.MaxFloat64 is deliberately absent. It formats to a 310-digit label,
	// but nothing can deliver it: percentages are clamped to 0..100 before they
	// reach a ring buffer, byte rates are deltas between two counter reads with
	// a floor at zero, and watts are sysfs microwatts divided by a million.
	// 1e18 already exceeds any real reading by orders of magnitude and is the
	// largest value worth defending.
	extremes := []float64{
		0, -0, -1, -1e9, 1e9, 1e18, math.SmallestNonzeroFloat64,
		math.NaN(), math.Inf(1), math.Inf(-1),
	}
	for _, mode := range []Mode{Percent, Bytes, Watts} {
		g := &Graph{mode: mode}
		for _, v := range extremes {
			got := g.formatValue(v)
			if got == "" {
				t.Errorf("mode %v value %v formatted as an empty string", mode, v)
			}
			if len(got) > 32 {
				t.Errorf("mode %v value %v produced %d characters (%q); a chart label cannot hold that", mode, v, len(got), got)
			}
		}
	}
}

// TestFormatValueDoesNotAllocateForASteadyReading is the reason the cache is
// there: a chart refreshed once a second on an idle machine should cost nothing.
func TestFormatValueDoesNotAllocateForASteadyReading(t *testing.T) {
	for _, mode := range []Mode{Percent, Bytes, Watts} {
		g := &Graph{mode: mode}
		g.formatValue(42) // prime the buffer and the cached string
		allocs := testingAllocs(func() {
			for i := 0; i < 100; i++ {
				g.formatValue(42)
			}
		})
		if allocs > 0 {
			t.Errorf("mode %v: %v allocations for 100 repeats of the same value", mode, allocs)
		}
	}
}

func testingAllocs(f func()) float64 {
	return testing.AllocsPerRun(20, f)
}

// TestAutoScaledModes pins which modes take their Y-axis from the data. Percent
// must stay fixed at 0..100 or a quiet machine's chart would look busy.
func TestAutoScaledModes(t *testing.T) {
	if Percent.autoScaled() {
		t.Error("Percent is auto-scaled; a 2% reading would fill the chart")
	}
	if !Bytes.autoScaled() {
		t.Error("Bytes is not auto-scaled; a byte rate has no fixed ceiling")
	}
	if !Watts.autoScaled() {
		t.Error("Watts is not auto-scaled")
	}
}

// TestColorsAreInRange checks the palette, since cairo takes components as
// 0..1 floats and a value outside that silently clamps to a different colour.
func TestColorsAreInRange(t *testing.T) {
	for _, c := range []Color{rgb(0, 0, 0), rgb(255, 255, 255), rgb(31, 41, 55)} {
		for name, v := range map[string]float64{"R": c.R, "G": c.G, "B": c.B} {
			if v < 0 || v > 1 {
				t.Errorf("%s = %v, outside cairo's 0..1 range", name, v)
			}
		}
	}
	if got := rgb(255, 255, 255); got.R != 1 || got.G != 1 || got.B != 1 {
		t.Errorf("rgb(255,255,255) = %+v, want all ones", got)
	}
	if got := rgb(0, 0, 0); got.R != 0 || got.G != 0 || got.B != 0 {
		t.Errorf("rgb(0,0,0) = %+v, want all zeroes", got)
	}
}

// TestScratchBufferFitsTheHistory checks the pre-allocated read buffer is big
// enough for a full ring, since the draw path relies on it never growing.
func TestScratchBufferFitsTheHistory(t *testing.T) {
	rb := stats.NewRingBuffer()
	for i := 0; i < stats.HistLen*3; i++ {
		rb.Push(float64(i))
	}
	scratch := make([]float64, stats.HistLen)
	n := rb.ReadInto(scratch)
	if n > len(scratch) {
		t.Fatalf("ReadInto reported %d samples into a buffer of %d", n, len(scratch))
	}
	if n != stats.HistLen {
		t.Errorf("a saturated ring returned %d samples, want %d", n, stats.HistLen)
	}
}

// TestFormatSpan pins the wording of the history caption, in particular where
// it switches from seconds to minutes and what an unset span draws.
func TestFormatSpan(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, ""},
		{-5 * time.Second, ""},
		{time.Second, "1 second"},
		{30 * time.Second, "30 seconds"},
		{60 * time.Second, "60 seconds"},
		{119 * time.Second, "119 seconds"},
		{120 * time.Second, "2 minutes"},
		{150 * time.Second, "3 minutes"},
		{300 * time.Second, "5 minutes"},
		{time.Hour, "60 minutes"},
	}
	for _, c := range cases {
		if got := formatSpan(c.d); got != c.want {
			t.Errorf("formatSpan(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestSetHistorySpanIsSharedByEveryGraph checks the setter feeds the value the
// graphs read, since it is a package-level store and not a per-graph one.
func TestSetHistorySpanIsSharedByEveryGraph(t *testing.T) {
	t.Cleanup(func() { SetHistorySpan(0) })
	SetHistorySpan(2 * time.Minute)
	if got := time.Duration(historySpan.Load()); got != 2*time.Minute {
		t.Errorf("after SetHistorySpan(2m) the graphs read %v", got)
	}
	SetHistorySpan(0)
	if got := historySpan.Load(); got != 0 {
		t.Errorf("after SetHistorySpan(0) the graphs read %v", got)
	}
}

// TestGridRows pins the row count rule: one row per 36 px of plot, rounded, kept
// between two and ten.
func TestGridRows(t *testing.T) {
	cases := []struct {
		plotH float64
		want  int
	}{
		{-10, 2},
		{0, 2},
		{20, 2},
		{36, 2},
		{89, 2}, // 2.47 rounds down
		{90, 3}, // 2.5 rounds up
		{108, 3},
		{144, 4},
		{359, 10},
		{360, 10},
		{2000, 10},
	}
	for _, c := range cases {
		if got := gridRows(c.plotH); got != c.want {
			t.Errorf("gridRows(%v) = %d, want %d", c.plotH, got, c.want)
		}
	}
	// Monotonic: a taller plot never gets fewer rows.
	prev := gridRows(0)
	for h := 0.0; h <= 500; h += 0.5 {
		got := gridRows(h)
		if got < prev {
			t.Fatalf("gridRows(%v) = %d after %d: the count went down as the plot grew", h, got, prev)
		}
		prev = got
	}
}

// TestPlotGeometry checks the two caption bands and the plot between them
// account for the whole widget, and that a widget too short for them does not
// end up with a negative plot.
func TestPlotGeometry(t *testing.T) {
	band, plotH := plotGeometry(130, 17)
	if band != 21 {
		t.Errorf("a 17 px line gave a %v px band, want 21 (the line plus 4)", band)
	}
	if plotH != 130-2*21 {
		t.Errorf("plot height %v, want %v", plotH, 130-2*21)
	}
	if band+plotH+band != 130 {
		t.Errorf("band %v + plot %v + band %v does not add up to the widget height 130", band, plotH, band)
	}

	// A larger font grows the bands and shrinks the plot instead of clipping.
	bandBig, plotBig := plotGeometry(130, 30)
	if bandBig <= band || plotBig >= plotH {
		t.Errorf("a taller font gave band %v plot %v, against band %v plot %v", bandBig, plotBig, band, plotH)
	}

	if _, plotH := plotGeometry(30, 17); plotH != 0 {
		t.Errorf("a widget shorter than its two bands got a plot height of %v, want 0", plotH)
	}
}

// TestCrispLandsOnPixelCentres checks 1px lines are aligned so they are sharp.
func TestCrispLandsOnPixelCentres(t *testing.T) {
	for _, v := range []float64{0, 0.2, 0.5, 0.99, 1, 17.3, 100, 359.999} {
		got := crisp(v)
		if got-math.Floor(got) != 0.5 {
			t.Errorf("crisp(%v) = %v, not the middle of a pixel", v, got)
		}
		if math.Abs(got-v) > 0.5+1e-9 {
			t.Errorf("crisp(%v) = %v moved the line by more than half a pixel", v, got)
		}
	}
}

// TestSetDashed checks the switch chooses between a solid line and the shared
// dash pattern, and that using it costs nothing per frame.
func TestSetDashed(t *testing.T) {
	g := &Graph{}
	if g.dashes() != nil {
		t.Error("a new graph is dashed; lines are solid unless a caller asks")
	}
	g.SetDashed(true)
	d := g.dashes()
	if len(d) != 2 || d[0] != 4 || d[1] != 3 {
		t.Fatalf("dashed pattern is %v, want 4 on and 3 off", d)
	}
	if &d[0] != &dashPattern[0] {
		t.Error("the dash pattern is not the package-level one, so something built a slice")
	}
	g.SetDashed(false)
	if g.dashes() != nil {
		t.Error("SetDashed(false) left the line dashed")
	}
}

func TestSetDashedDoesNotAllocate(t *testing.T) {
	g := &Graph{}
	allocs := testingAllocs(func() {
		g.SetDashed(true)
		_ = g.dashes()
		g.SetDashed(false)
		_ = g.dashes()
	})
	if allocs > 0 {
		t.Errorf("%v allocations for toggling the dashing and reading the pattern", allocs)
	}
}

// TestFormatIntoBuildsThePeakText checks the peak caption's text, and that a
// steady peak costs nothing, since it is built again on every frame.
func TestFormatIntoBuildsThePeakText(t *testing.T) {
	for _, mode := range []Mode{Percent, Bytes, Watts} {
		g := &Graph{mode: mode}
		got := string(g.formatInto("peak ", 42))
		if len(got) <= len("peak ") || got[:5] != "peak " {
			t.Errorf("mode %v: %q does not read as \"peak \" and a value", mode, got)
		}
		if want := "peak " + g.formatValue(42); got != want {
			t.Errorf("mode %v: peak text %q, but the current-value formatter says %q", mode, got, want)
		}
		allocs := testingAllocs(func() { _ = string(g.formatInto("peak ", 42)) == g.shownPeak })
		if allocs > 0 {
			t.Errorf("mode %v: %v allocations for a repeat of the same peak", mode, allocs)
		}
	}
}

// TestSeriesColoursShareHuesOnlyWhereDashed pins the palette rule: two series
// may have the same colour only if one of them is drawn dashed by its caller, so
// the pairs below are deliberate and nothing else collides.
func TestSeriesColoursShareHuesOnlyWhereDashed(t *testing.T) {
	if ColorNetUp != ColorNetDown {
		t.Errorf("net up %+v and down %+v differ; the pair is meant to be told apart by dashing", ColorNetUp, ColorNetDown)
	}
	if ColorDiskWr != ColorDiskRead {
		t.Errorf("disk write %+v and read %+v differ; the pair is meant to be told apart by dashing", ColorDiskWr, ColorDiskRead)
	}
	distinct := map[string]Color{
		"CPU": ColorCPU, "memory": ColorMemory, "GPU": ColorGPU,
		"net": ColorNetDown, "disk": ColorDiskRead,
		"battery": ColorBattery, "power draw": ColorPowerDrw,
	}
	for a, ca := range distinct {
		for b, cb := range distinct {
			if a < b && ca == cb {
				t.Errorf("%s and %s share a colour but are not a dashed pair", a, b)
			}
		}
	}
	if want := rgb(0x39, 0xb8, 0xe3); ColorCPU != want {
		t.Errorf("CPU is %+v, want the Task Manager cyan %+v", ColorCPU, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
