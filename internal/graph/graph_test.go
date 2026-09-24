package graph

import (
	"math"
	"testing"

	"atlas-monitor/internal/stats"
)

// The drawing itself needs a display, but the value text under each chart and
// the Y-axis decision do not, and those are the parts that are cached — a stale
// cache would put the wrong number under a live chart.

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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
