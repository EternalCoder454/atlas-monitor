package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/config"
	"atlas-monitor/internal/desktop"
	"atlas-monitor/internal/ease"
	"atlas-monitor/internal/format"
	"atlas-monitor/internal/process"
)

// The Energy Saver page: what is costing you battery, and what can be done about
// it without being root.
//
// Applications are handled by internal/ease: automatically, when the switch is
// on, and by hand from here either way — reversibly, through the weight their
// unit gets. Programs that are not an application's (a service, something
// started from a shell script) keep the older remedy below them: a lower nice
// value, which is all there is for a lone process, and which Linux will not let
// an ordinary user undo.
type energyView struct {
	root     *gtk.ScrolledWindow
	box      *gtk.Box
	proc     *process.Collector
	easer    *ease.Controller
	settings *config.Settings
	apps     *appResolver

	// The applications section, when there is an easer: the group, its rows by
	// application ID, and the key the rows were built for. Rows are rebuilt
	// only when an application comes, goes or changes status; between those,
	// their figures are updated in place.
	appGroup *adw.PreferencesGroup
	appRows  map[string]*energyAppRow
	appKey   string
	appKeyB  []byte
	appsNone *gtk.Label

	// The other programs, by process.
	group *adw.PreferencesGroup
	empty *gtk.Label
	shown string
	cand  []process.Proc // candidates, reused every tick
	keyB  []byte         // the key under construction, reused every tick
	// eased remembers what this session has already eased off, by pid and start
	// time: a pid on its own is reused, and easing off the wrong program later
	// because it inherited a number would be a nasty surprise.
	eased map[procIdent]bool
}

type energyAppRow struct {
	row *adw.ActionRow
	app ease.App
	sub string // the subtitle now set, so an unchanged one is not set again
}

// energyCandidates is how many programs the page will argue with at once.
const energyCandidates = 8

func newEnergyView(proc *process.Collector, easer *ease.Controller, easeErr error, settings *config.Settings) *energyView {
	v := &energyView{
		proc: proc, easer: easer, settings: settings,
		apps:    newAppResolver(desktop.Default()),
		eased:   map[procIdent]bool{},
		appRows: map[string]*energyAppRow{},
	}
	sw, box := newPage()
	v.root, v.box = sw, box

	title, caption, headBox := newHeader()
	title.text("Energy Saver")
	caption.text("Programs working hardest right now")
	box.Append(headBox)

	if easer != nil {
		v.buildAppSection()
	} else if easeErr != nil {
		note := gtk.NewLabel("Applications cannot be eased off automatically here: " + easeErr.Error() + ".")
		note.SetWrap(true)
		note.SetXAlign(0)
		note.AddCSSClass("am-subtle")
		box.Append(note)
	}

	undo := "This lasts until the program is restarted, and cannot be undone " +
		"without administrator rights."
	if process.EaseOffReversible {
		undo = "This lasts until the program is restarted, and can be undone."
	}
	v.group = adw.NewPreferencesGroup()
	if easer != nil {
		v.group.SetTitle("Other programs")
	}
	v.group.SetDescription("Easing a program off puts it behind everything else on the processor. " +
		"It keeps running and keeps its work; it just stops winning. " + undo)
	box.Append(v.group)

	v.empty = gtk.NewLabel("Nothing else is working hard enough to be worth easing off.")
	v.empty.SetWrap(true)
	v.empty.SetXAlign(0)
	v.empty.AddCSSClass("am-subtle")
	box.Append(v.empty)

	return v
}

// buildAppSection is the automatic switch and the applications list.
func (v *energyView) buildAppSection() {
	g := adw.NewPreferencesGroup()
	sw := adw.NewSwitchRow()
	sw.SetTitle("Ease off busy apps automatically")
	sw.SetSubtitle("An app that keeps a core busy for half a minute is put behind the rest, " +
		"and put back when it calms down. Apps playing or recording sound, the app you are using, " +
		"and terminals are left alone.")
	sw.SetActive(v.settings.EnergyAuto)
	sw.NotifyProperty("active", func() {
		v.settings.EnergyAuto = sw.Active()
		_ = config.Save(*v.settings)
		v.easer.SetAutomatic(sw.Active())
		v.Update()
	})
	g.Add(sw)
	v.box.Append(g)

	v.appGroup = adw.NewPreferencesGroup()
	v.appGroup.SetTitle("Apps")
	v.box.Append(v.appGroup)
	v.appsNone = gtk.NewLabel("No app is busy right now.")
	v.appsNone.SetXAlign(0)
	v.appsNone.AddCSSClass("am-subtle")
	v.box.Append(v.appsNone)
}

func (v *energyView) Root() gtk.Widgetter { return v.root }

// applyWants asks for everything the impact score is made of. Ranking programs
// by what they cost is the whole page, and a term missing because a column is
// hidden somewhere else would change the order without saying so.
func (v *energyView) applyWants() {
	v.proc.SetWantDiskIO(true)
	v.proc.SetWantGPU(true)
	v.proc.SetWantNet(true)
}

// Update refreshes both lists.
func (v *energyView) Update() {
	if v.easer != nil {
		v.updateApps()
	}
	v.updateOthers()
}

// updateApps shows the applications the easer knows to be busy or eased.
func (v *energyView) updateApps() {
	all := v.easer.Snapshot()
	if len(all) > energyCandidates {
		// Always keep the eased ones on the list, whatever they are doing now:
		// the button to put one back must not scroll away.
		keep := all[:0:0]
		for i, a := range all {
			if i < energyCandidates || a.Status == ease.EasedAuto || a.Status == ease.EasedManual {
				keep = append(keep, a)
			}
		}
		all = keep
	}
	key := v.appKeyB[:0]
	for _, a := range all {
		key = append(key, a.ID...)
		key = append(key, ':')
		key = strconv.AppendInt(key, int64(a.Status), 10)
		key = append(key, ';')
	}
	v.appKeyB = key
	if string(key) != v.appKey {
		v.appKey = string(key)
		old := v.appGroup
		v.appGroup = adw.NewPreferencesGroup()
		v.appGroup.SetTitle("Apps")
		clear(v.appRows)
		for _, a := range all {
			r := v.appRow(a)
			v.appRows[a.ID] = r
			v.appGroup.Add(r.row)
		}
		v.box.InsertChildAfter(v.appGroup, old)
		v.box.Remove(old)
	}
	for _, a := range all {
		if r := v.appRows[a.ID]; r != nil {
			if sub := appStatus(a, v.settings.EnergyAuto); sub != r.sub {
				r.sub = sub
				r.row.SetSubtitle(sub)
			}
		}
	}
	v.appsNone.SetVisible(len(all) == 0)
}

// appRow is one application: its icon and name, what Energy Saver is doing about
// it, a button to ease it off or put it back, and the choice to never ease it.
func (v *energyView) appRow(a ease.App) *energyAppRow {
	row := adw.NewActionRow()
	row.SetTitle(a.Name)
	sub := appStatus(a, v.settings.EnergyAuto)
	row.SetSubtitle(sub)
	row.SetSubtitleLines(2)
	if ai := v.apps.of(a.Unit); ai != nil && ai.icon != "" {
		img := gtk.NewImage()
		img.SetPixelSize(24)
		setIcon(img, ai.icon)
		row.AddPrefix(img)
	}

	id := a.ID
	eased := a.Status == ease.EasedAuto || a.Status == ease.EasedManual
	btn := gtk.NewButtonWithLabel("Ease off")
	if eased {
		btn.SetLabel("Put back")
	}
	btn.SetVAlign(gtk.AlignCenter)
	btn.ConnectClicked(func() {
		var err error
		if eased {
			err = v.easer.Restore(id)
		} else {
			err = v.easer.Ease(id)
		}
		if err != nil {
			row.SetSubtitle("Could not do that: " + err.Error())
			return
		}
		v.appKey = "" // rebuild with the new status
		v.Update()
	})
	row.AddSuffix(btn)

	never := gtk.NewCheckButtonWithLabel("Never ease off automatically")
	never.SetActive(contains(v.settings.EnergyNever, id))
	never.ConnectToggled(func() {
		v.settings.EnergyNever = without(v.settings.EnergyNever, id)
		if never.Active() {
			v.settings.EnergyNever = append(v.settings.EnergyNever, id)
		}
		_ = config.Save(*v.settings)
		v.easer.SetNever(v.settings.EnergyNever)
		v.appKey = ""
		v.Update()
	})
	pop := gtk.NewPopover()
	pop.SetChild(never)
	more := gtk.NewMenuButton()
	more.SetIconName("view-more-symbolic")
	more.SetTooltipText("More options")
	more.SetPopover(pop)
	more.SetVAlign(gtk.AlignCenter)
	more.AddCSSClass("flat")
	row.AddSuffix(more)
	return &energyAppRow{row: row, app: a, sub: sub}
}

// appStatus says what Energy Saver is doing about an application, and why.
func appStatus(a ease.App, automatic bool) string {
	cpu := string(format.AppendPercent1(nil, a.CPU)) + " of a core"
	switch a.Status {
	case ease.EasedAuto:
		return "Eased off automatically · " + cpu
	case ease.EasedManual:
		return "Eased off by you · " + cpu
	case ease.KeptSound:
		return "Playing or recording sound, so left alone · " + cpu
	case ease.KeptTerminal:
		return "A terminal, so left alone · " + cpu
	case ease.KeptNever:
		return "Set never to ease off · " + cpu
	case ease.KeptInUse:
		return "In use, so left alone · " + cpu
	case ease.KeptOther:
		return "Its priority was set by something else, so left alone · " + cpu
	case ease.Busy:
		if automatic {
			return "Busy · " + cpu + " · eased off if it keeps this up"
		}
		return "Busy · " + cpu
	}
	return "Using " + cpu
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// updateOthers rebuilds the per-process list when what it would say has changed.
// With an easer, applications are its business and this lists the rest.
func (v *energyView) updateOthers() {
	candidates := v.cand[:0]
	v.proc.View(func(procs []process.Proc) {
		for i := range procs {
			p := &procs[i]
			if v.easer != nil && v.apps.of(p.Unit) != nil {
				continue
			}
			if p.Impact() >= process.ImpactModerate {
				candidates = append(candidates, *p)
			}
		}
	})
	slices.SortFunc(candidates, func(a, b process.Proc) int {
		return cmp.Compare(b.PowerScore(), a.PowerScore())
	})
	if len(candidates) > energyCandidates {
		candidates = candidates[:energyCandidates]
	}
	v.cand = candidates

	key := v.keyB[:0]
	for _, p := range candidates {
		key = strconv.AppendInt(key, int64(p.PID), 10)
		key = append(key, ':')
		key = append(key, p.Name...)
		key = append(key, ':')
		key = strconv.AppendInt(key, int64(p.Impact()), 10)
		key = append(key, ';')
	}
	v.keyB = key
	if string(key) == v.shown {
		return
	}
	v.shown = string(key)

	old := v.group
	v.group = adw.NewPreferencesGroup()
	v.group.SetTitle(old.Title())
	v.group.SetDescription(old.Description())
	for _, p := range candidates {
		v.group.Add(v.row(p))
	}
	v.box.InsertChildAfter(v.group, old)
	v.box.Remove(old)
	v.empty.SetVisible(len(candidates) == 0)
}

// row is one program: what it is doing, and a button to ease it off.
func (v *energyView) row(p process.Proc) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(p.Name)
	row.SetSubtitle(energyReason(p))
	row.SetSubtitleLines(2)

	ident := identOf(p.PID)

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
