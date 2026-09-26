package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/autostart"
)

// The Startup page: what launches when you log in, and a switch for each.
//
// This is configuration rather than telemetry — it does not change from one
// second to the next — so it is read when the page is opened and when something
// is changed, and Update does nothing. Rebuilding a list of switches every tick
// would fight the user for the switch they were reaching for.
type startupView struct {
	root   *gtk.ScrolledWindow
	box    *gtk.Box
	group  *adw.PreferencesGroup
	reader *autostart.Reader

	showSystem bool
	banner     *gtk.Label
}

func newStartupView() *startupView {
	v := &startupView{reader: autostart.New()}
	sw, box := newPage()
	v.root, v.box = sw, box

	title, caption, headBox := newHeader()
	title.text("Startup")
	caption.text("Programs that start when you log in")
	box.Append(headBox)

	bar := adw.NewWrapBox()
	bar.SetChildSpacing(6)
	bar.SetLineSpacing(6)

	refresh := gtk.NewButtonWithLabel("Refresh")
	refresh.ConnectClicked(func() { v.rebuild() })
	bar.Append(refresh)

	// The same bargain the process table makes with kernel threads: most of
	// what is here is desktop plumbing that marks itself NoDisplay, and showing
	// it by default buries the three entries anybody came to find.
	sysToggle := gtk.NewToggleButton()
	sysToggle.SetLabel("System entries")
	sysToggle.SetTooltipText("Also show the desktop's own background pieces")
	sysToggle.ConnectToggled(func() {
		v.showSystem = sysToggle.Active()
		v.rebuild()
	})
	bar.Append(sysToggle)
	box.Append(bar)

	v.banner = gtk.NewLabel("")
	v.banner.SetXAlign(0)
	v.banner.SetWrap(true)
	v.banner.AddCSSClass("am-subtle")
	v.banner.SetVisible(false)
	box.Append(v.banner)

	v.group = adw.NewPreferencesGroup()
	box.Append(v.group)

	v.rebuild()
	return v
}

func (v *startupView) Root() gtk.Widgetter { return v.root }

// Update does nothing: see the note on the type.
func (v *startupView) Update() {}

// rebuild reads the entries again and lays the switches out afresh.
func (v *startupView) rebuild() {
	old := v.group
	v.group = adw.NewPreferencesGroup()

	entries := v.reader.List()
	shown := 0
	for _, e := range entries {
		if e.Plumbing && !v.showSystem {
			continue
		}
		shown++
		v.group.Add(v.row(e))
	}

	v.box.Append(v.group)
	if old != nil {
		v.box.Remove(old)
	}

	switch {
	case shown == 0 && len(entries) > 0:
		v.setBanner("Nothing of your own starts at login. " +
			"The desktop's own background pieces are there under System entries.")
	case shown == 0:
		v.setBanner("Nothing starts at login.")
	default:
		v.setBanner("")
	}
}

// row is one entry: a switch, the program's name, and what it actually runs.
func (v *startupView) row(e autostart.Entry) *adw.SwitchRow {
	row := adw.NewSwitchRow()
	row.SetTitle(e.Name)

	subtitle := e.Exec
	if e.Comment != "" {
		subtitle = e.Comment
	}
	switch {
	case !e.RunsHere:
		// Listing it as "on" would be a lie: its own rules exclude this desktop.
		subtitle = "Not used by this desktop · " + subtitle
	case e.Plumbing:
		subtitle = "Part of the desktop · " + subtitle
	}
	row.SetSubtitle(subtitle)
	row.SetSubtitleLines(2)
	row.SetActive(!e.Disabled)

	row.NotifyProperty("active", func() {
		want := row.Active()
		if want == !e.Disabled {
			return
		}
		if err := autostart.SetEnabled(e, want); err != nil {
			v.setBanner("Could not change that: " + err.Error())
			row.SetActive(!e.Disabled) // put the switch back where it was
			return
		}
		e.Disabled = !want
		v.setBanner("")
	})
	return row
}

func (v *startupView) setBanner(text string) {
	v.banner.SetText(text)
	v.banner.SetVisible(text != "")
}
