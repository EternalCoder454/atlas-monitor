package ui

import (
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/graph"
	"atlas-monitor/internal/power"
	"atlas-monitor/internal/stats"
)

type powerView struct {
	root *gtk.ScrolledWindow
	col  *stats.Collector
	// pack is the kernel name of the one battery this page shows, or "" for the
	// summed view. A machine with a single pack only ever uses "".
	pack      string
	number    *liveLabel
	caption   *liveLabel
	capBuf    []byte
	chargeGr  *graph.Graph
	drawGr    *graph.Graph
	drawTitle *gtk.Label
	identity  *liveLabel

	vStatus, vDraw, vRemaining  *liveLabel
	vCharge, vHealth, vDesign   *liveLabel
	vCycles, vVoltage, vAdapter *liveLabel
}

func newPowerView(col *stats.Collector, pack string) *powerView {
	v := &powerView{col: col, pack: pack}
	sw, box := newPage()
	v.root = sw

	var headBox *gtk.Box
	v.number, v.caption, headBox = newHeadline()
	box.Append(headBox)

	var chargeHist, drawHist *stats.RingBuffer
	var b power.Battery
	col.Read(func(s *stats.Stats) {
		if ps, ok := packOf(s, pack); ok {
			chargeHist, drawHist, b = ps.ChargeHist, ps.DrawHist, ps.Battery
			return
		}
		chargeHist, drawHist = s.Power.ChargeHist, s.Power.DrawHist
		b = s.Power.Battery
	})

	v.chargeGr = graph.New("Charge", graph.ColorBattery, chargeHist, graph.Percent, 150)
	box.Append(v.chargeGr)

	v.drawTitle = sectionTitle("POWER DRAW")
	box.Append(v.drawTitle)
	v.drawGr = graph.New("Draw", graph.ColorPowerDrw, drawHist, graph.Watts, 120)
	box.Append(v.drawGr)

	box.Append(sectionTitle("DETAILS"))
	g := newStatGrid()
	v.vStatus = g.add("Status")
	v.vDraw = g.add("Power draw")
	v.vRemaining = g.add("Time remaining")
	v.vCharge = g.add("Charge")
	v.vHealth = g.add("Battery health")
	v.vDesign = g.add("Design capacity")
	v.vCycles = g.add("Charge cycles")
	v.vVoltage = g.add("Voltage")
	v.vAdapter = g.add("AC adapter")
	box.Append(g)

	box.Append(sectionTitle("BATTERY"))
	ident := gtk.NewLabel("")
	ident.AddCSSClass("am-subtle")
	ident.SetXAlign(0)
	ident.SetWrap(true)
	v.identity = newLiveLabel(ident)
	box.Append(ident)
	v.identity.text(batteryIdentity(b))

	return v
}

func (v *powerView) Root() gtk.Widgetter { return v.root }

func (v *powerView) Update() {
	var b power.Battery
	var hasAC, onAC bool
	v.col.Read(func(s *stats.Stats) {
		hasAC, onAC = s.Power.HasAC, s.Power.OnAC
		if ps, ok := packOf(s, v.pack); ok {
			b = ps.Battery
			return
		}
		b = s.Power.Battery
	})

	v.number.percent(b.Percent)
	// On a per-pack page, say which pack. Without it the two pages are the same
	// shape and the same words, and the only way to tell BAT0 from BAT1 is to
	// scroll to the model number at the bottom.
	line := v.caption.scratch()
	if v.pack != "" {
		line = append(line, v.pack...)
		line = append(line, " · "...)
	}
	v.caption.commit(appendBatteryCaption(line, b, hasAC, onAC))

	v.vStatus.text(b.Status)
	if b.PowerW > 0 {
		v.vDraw.commit(format.AppendWatts(v.vDraw.scratch(), b.PowerW))
	} else {
		v.vDraw.text("—")
	}
	v.vRemaining.commit(format.AppendDuration(v.vRemaining.scratch(), b.TimeLeft))
	v.vCharge.commit(appendWhOf(v.vCharge.scratch(), b.EnergyWh, b.FullWh))
	v.vHealth.commit(appendHealth(v.vHealth.scratch(), b.Health))
	v.vDesign.commit(appendWh(v.vDesign.scratch(), b.DesignWh))
	v.vCycles.commit(appendCycles(v.vCycles.scratch(), b.CycleCount))
	v.vVoltage.commit(appendVolts(v.vVoltage.scratch(), b.VoltageV))
	v.vAdapter.text(adapterText(hasAC, onAC))

	// The draw graph is meaningless on a machine that never reports a rate.
	show := b.PowerW > 0 || b.TimeLeft > 0
	v.drawTitle.SetVisible(show)
	v.drawGr.SetVisible(show)

	v.chargeGr.Refresh()
	if show {
		v.drawGr.Refresh()
	}
}

// packOf finds one pack's readings by kernel name. It reports false for the
// summed view, and for a name that is no longer there — a pack pulled out of a
// hot-swap bay, whose page is still open.
func packOf(s *stats.Stats, name string) (stats.PackStats, bool) {
	if name == "" {
		return stats.PackStats{}, false
	}
	for _, p := range s.Power.Packs {
		if p.Battery.Name == name {
			return p, true
		}
	}
	return stats.PackStats{}, false
}

// appendBatteryCaption is the line under the big percentage: what the battery
// is doing and, when it can be estimated, for how long.
func appendBatteryCaption(dst []byte, b power.Battery, hasAC, onAC bool) []byte {
	switch {
	case b.Status == "Full" || (b.Percent >= 99 && onAC):
		return append(dst, "Fully charged"...)
	case b.Charging():
		dst = append(dst, "Charging"...)
		if b.TimeLeft > 0 {
			dst = append(dst, " · "...)
			dst = format.AppendDuration(dst, b.TimeLeft)
			dst = append(dst, " until full"...)
		}
		return dst
	case b.Discharging():
		dst = append(dst, "On battery"...)
		if b.TimeLeft > 0 {
			dst = append(dst, " · "...)
			dst = format.AppendDuration(dst, b.TimeLeft)
			dst = append(dst, " remaining"...)
		}
		return dst
	case hasAC && onAC:
		return append(dst, "Plugged in, not charging"...)
	default:
		return append(dst, b.Status...)
	}
}

func appendWh(dst []byte, wh float64) []byte {
	if wh <= 0 {
		return append(dst, "—"...)
	}
	return append(strconv.AppendFloat(dst, wh, 'f', 1, 64), " Wh"...)
}

func appendWhOf(dst []byte, now, full float64) []byte {
	if full <= 0 {
		return append(dst, "—"...)
	}
	dst = appendWh(dst, now)
	dst = append(dst, " of "...)
	return appendWh(dst, full)
}

func appendHealth(dst []byte, pct float64) []byte {
	if pct <= 0 {
		return append(dst, "—"...)
	}
	dst = format.AppendPercent(dst, pct)
	switch {
	case pct >= 90:
		return append(dst, " of original"...)
	case pct >= 70:
		return append(dst, " of original · worn"...)
	default:
		return append(dst, " of original · heavily worn"...)
	}
}

func appendCycles(dst []byte, n int) []byte {
	if n <= 0 {
		return append(dst, "—"...)
	}
	return strconv.AppendInt(dst, int64(n), 10)
}

func appendVolts(dst []byte, v float64) []byte {
	if v <= 0 {
		return append(dst, "—"...)
	}
	return append(strconv.AppendFloat(dst, v, 'f', 2, 64), " V"...)
}

func adapterText(hasAC, onAC bool) string {
	switch {
	case !hasAC:
		return "—"
	case onAC:
		return "Connected"
	default:
		return "Disconnected"
	}
}

// batteryIdentity is the static description of the pack, set once.
func batteryIdentity(b power.Battery) string {
	parts := make([]string, 0, 4)
	for _, s := range []string{b.Vendor, b.Model, b.Technology} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if b.Packs > 1 {
		parts = append(parts, strconv.Itoa(b.Packs)+" packs")
	}
	if len(parts) == 0 {
		return b.Name
	}
	return b.Name + " — " + joinWords(parts, " · ")
}

func joinWords(parts []string, sep string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}
