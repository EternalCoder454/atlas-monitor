package ui

import (
	"bytes"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
)

// liveLabel wraps a GtkLabel that is refreshed on every tick. It remembers the
// text it last pushed, so an unchanged value costs nothing: no formatting
// allocation, no Go→C string copy, no cgo call and no Pango relayout. Most
// values on a page are identical from one second to the next (cache sizes, an
// idle interface's "0 B/s", a steady temperature), so this removes the bulk of
// the per-tick work a view would otherwise do.
type liveLabel struct {
	label *gtk.Label
	buf   []byte // formatting scratch, reused across ticks
	cur   []byte // what the label currently displays
	set   bool   // cur is valid (distinguishes "" from "never set")
}

func newLiveLabel(l *gtk.Label) *liveLabel { return &liveLabel{label: l} }

// commit pushes b to the label when it differs from the displayed text. b must
// have been built on the buffer returned by scratch.
func (v *liveLabel) commit(b []byte) {
	v.buf = b // keep the (possibly grown) buffer for next time
	if v.set && bytes.Equal(b, v.cur) {
		return
	}
	v.cur = append(v.cur[:0], b...)
	v.set = true
	v.label.SetText(string(b))
}

// scratch returns the reusable formatting buffer, emptied.
func (v *liveLabel) scratch() []byte { return v.buf[:0] }

// text sets a plain string, skipping the update when it is unchanged.
func (v *liveLabel) text(s string) {
	if v.set && string(v.cur) == s {
		return
	}
	v.cur = append(v.cur[:0], s...)
	v.set = true
	v.label.SetText(s)
}

func (v *liveLabel) bytesVal(b uint64) { v.commit(format.AppendBytes(v.scratch(), b)) }
func (v *liveLabel) gib(b uint64)      { v.commit(format.AppendGiB(v.scratch(), b)) }
func (v *liveLabel) rate(bps float64)  { v.commit(format.AppendRate(v.scratch(), bps)) }
func (v *liveLabel) mhz(m float64)     { v.commit(format.AppendMHz(v.scratch(), m)) }
func (v *liveLabel) percent(p float64) { v.commit(format.AppendPercent(v.scratch(), p)) }
func (v *liveLabel) temp(c float64)    { v.commit(format.AppendTemp(v.scratch(), c)) }
func (v *liveLabel) intVal(n int)      { v.commit(format.AppendInt(v.scratch(), n)) }

// gibOf renders "<used> / <total>" in one pass (GPU VRAM).
func (v *liveLabel) gibOf(used, total uint64) {
	b := format.AppendGiB(v.scratch(), used)
	b = append(b, " / "...)
	v.commit(format.AppendGiB(b, total))
}
