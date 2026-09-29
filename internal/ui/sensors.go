package ui

import (
	"strconv"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/sensors"
)

// The Sensors page: every temperature, fan and power reading the hardware
// offers, grouped by what it belongs to. See internal/sensors.
//
// It reads only while it is the page on screen, like every other page; the
// files are held open so that each reading is one re-read, not an open and a
// close.
type sensorsView struct {
	root *gtk.ScrolledWindow
	devs []*sensors.Device
	// rows parallels devs and each device's readings.
	rows [][]sensorRow
	// cores is each device's folded per-core list, or nil, with the label that
	// reports the hottest of them while it is folded.
	cores []*adw.ExpanderRow
}

type sensorRow struct {
	value  *liveLabel
	label  *gtk.Label
	warmth int
}

func newSensorsView() *sensorsView {
	v := &sensorsView{devs: sensors.Discover()}
	sw, box := newPage()
	v.root = sw

	title, caption, head := newHeader()
	title.text("Sensors")
	caption.text("Temperatures, fans and power, as the hardware reports them")
	box.Append(head)

	for _, d := range v.devs {
		g := adw.NewPreferencesGroup()
		g.SetTitle(d.Name)
		// The kernel's name for it: what a search for the chip will find.
		g.SetDescription(d.Driver)
		var exp *adw.ExpanderRow
		rows := make([]sensorRow, len(d.Readings))
		for i := range d.Readings {
			r := &d.Readings[i]
			row := adw.NewActionRow()
			row.SetTitle(r.Label)
			l := gtk.NewLabel(r.Format())
			l.AddCSSClass("am-num")
			row.AddSuffix(l)
			rows[i] = sensorRow{value: newLiveLabel(l), label: l}
			if r.Core {
				if exp == nil {
					exp = adw.NewExpanderRow()
					exp.SetTitle("Cores")
					g.Add(exp)
				}
				exp.AddRow(row)
				continue
			}
			g.Add(row)
		}
		v.rows = append(v.rows, rows)
		v.cores = append(v.cores, exp)
		box.Append(g)
	}
	v.Update()
	return v
}

func (v *sensorsView) Root() gtk.Widgetter { return v.root }

// Update re-reads every sensor and moves only the labels whose text changed.
func (v *sensorsView) Update() {
	for i, d := range v.devs {
		d.Read()
		hottest, have := 0.0, false
		for j := range d.Readings {
			r := &d.Readings[j]
			row := &v.rows[i][j]
			row.value.text(r.Format())
			if w := r.Warmth(); w != row.warmth {
				row.label.RemoveCSSClass("am-warm")
				row.label.RemoveCSSClass("am-hot")
				if w > 0 {
					row.label.AddCSSClass(heatClasses[w-1])
				}
				row.warmth = w
			}
			if r.Core && r.OK && (!have || r.Value > hottest) {
				hottest, have = r.Value, true
			}
		}
		if exp := v.cores[i]; exp != nil && have {
			exp.SetSubtitle("Hottest " + strconv.FormatFloat(hottest, 'f', 0, 64) + " °C")
		}
	}
}
