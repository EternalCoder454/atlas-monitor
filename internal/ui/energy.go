package ui

import (
	"fmt"
	"sort"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/process"
)

// The Energy Saver page: what is costing you battery, and the one thing you can
// do about it without being root.
//
// The Apps table already rates every process for power impact — the same
// weighted score behind its Power column — but rating something is not the same
// as doing anything with it. This lists only the programs worth arguing with,
// says what each is actually doing, and offers to put it behind everything
// else.
type energyView struct {
	root  *gtk.ScrolledWindow
	box   *gtk.Box
	proc  *process.Collector
	group *adw.PreferencesGroup
	empty *gtk.Label

	// shown is what the list is currently displaying, so a page that has not
	// changed is not rebuilt underneath whoever is reaching for a button.
	shown string
	snap  []process.Proc
	// eased remembers what this session has already eased off, by pid and start
	// time: a pid on its own is reused, and easing off the wrong program later
	// because it inherited a number would be a nasty surprise.
	eased map[procIdent]bool
}

type procIdent struct {
	pid   int
	start uint64
}

// energyCandidates is how many programs the page will argue with at once.
const energyCandidates = 8

func newEnergyView(proc *process.Collector) *energyView {
	v := &energyView{proc: proc, eased: map[procIdent]bool{}}
	sw, box := newPage()
	v.root, v.box = sw, box

	title, caption, headBox := newHeader()
	title.text("Energy Saver")
	caption.text("Programs working hardest right now")
	box.Append(headBox)

	note := gtk.NewLabel("Easing a program off puts it behind everything else on the processor. " +
		"It keeps running and keeps its work; it just stops winning. This lasts until the " +
		"program is restarted, and cannot be undone without administrator rights.")
	note.SetWrap(true)
	note.SetXAlign(0)
	note.AddCSSClass("am-subtle")
	box.Append(note)

	v.group = adw.NewPreferencesGroup()
	box.Append(v.group)

	v.empty = gtk.NewLabel("Nothing is working hard enough to be worth easing off.")
	v.empty.SetWrap(true)
	v.empty.SetXAlign(0)
	v.empty.AddCSSClass("am-subtle")
	box.Append(v.empty)

	return v
}

func (v *energyView) Root() gtk.Widgetter { return v.root }

// Update rebuilds the list when what it would say has changed.
func (v *energyView) Update() {
	var candidates []process.Proc
	v.snap = v.proc.SnapshotInto(v.snap)
	for _, p := range v.snap {
		if p.Impact() >= process.ImpactModerate {
			candidates = append(candidates, p)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].PowerScore() > candidates[j].PowerScore()
	})
	if len(candidates) > energyCandidates {
		candidates = candidates[:energyCandidates]
	}

	key := ""
	for _, p := range candidates {
		key += fmt.Sprintf("%d:%s:%d;", p.PID, p.Name, p.Impact())
	}
	if key == v.shown {
		return
	}
	v.shown = key

	old := v.group
	v.group = adw.NewPreferencesGroup()
	for _, p := range candidates {
		v.group.Add(v.row(p))
	}
	v.box.Append(v.group)
	v.box.Remove(old)
	// The empty note is appended after the group, so it moves with it.
	v.box.Remove(v.empty)
	v.box.Append(v.empty)
	v.empty.SetVisible(len(candidates) == 0)
}

// row is one program: what it is doing, and a button to ease it off.
func (v *energyView) row(p process.Proc) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(p.Name)
	row.SetSubtitle(energyReason(p))
	row.SetSubtitleLines(2)

	ident := procIdent{pid: p.PID}
	if start, ok := process.StartTime(p.PID); ok {
		ident.start = start
	}

	btn := gtk.NewButtonWithLabel("Ease off")
	btn.SetVAlign(gtk.AlignCenter)
	nice, haveNice := process.Nice(p.PID)
	switch {
	case v.eased[ident] || (haveNice && nice >= process.EasedNice):
		btn.SetLabel("Eased off")
		btn.SetSensitive(false)
	default:
		btn.ConnectClicked(func() {
			if err := process.SetNice(p.PID, process.EasedNice); err != nil {
				row.SetSubtitle("Could not ease that off: " + err.Error())
				return
			}
			v.eased[ident] = true
			btn.SetLabel("Eased off")
			btn.SetSensitive(false)
		})
	}
	row.AddSuffix(btn)
	return row
}

// energyReason says what is actually costing the energy, because "High" on its
// own does not tell anyone whether to care.
func energyReason(p process.Proc) string {
	reason := fmt.Sprintf("%s · %s of a processor core", p.Impact(), string(format.AppendPercent1(nil, p.CPU)))
	if p.GPU > 0 {
		reason += fmt.Sprintf(" · %s of the graphics card", string(format.AppendPercent(nil, p.GPU)))
	}
	if r := p.DiskRead + p.DiskWrite; r > 0 {
		reason += " · " + format.Rate(r) + " to disk"
	}
	if n := p.NetIn + p.NetOut; n > 0 {
		reason += " · " + format.Rate(n) + " over the network"
	}
	return reason
}
