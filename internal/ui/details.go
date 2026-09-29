package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/process"
)

// The Details panel: what a process is, before somebody decides to end it.
//
// The Apps table answers "what is busy"; it cannot answer "what is this" — a row
// called "python3" or "Isolated Web Co" does not say what started it or what it
// is running. Everything here is read when the panel opens, not by the scan,
// because most of it costs a file per process.

// showProcessDetails opens the panel for one process.
func showProcessDetails(parent gtk.Widgetter, pid int, apps *appResolver) {
	in, err := process.Details(pid)
	page := adw.NewPreferencesPage()
	title := "Process " + strconv.Itoa(pid)
	if err != nil {
		g := adw.NewPreferencesGroup()
		g.SetDescription("Process " + strconv.Itoa(pid) + " has exited.")
		page.Add(g)
		presentDetails(parent, title, page)
		return
	}
	if in.Name != "" {
		title = in.Name
	}

	g := adw.NewPreferencesGroup()
	g.SetTitle("Process")
	addProperty(g, "Name", in.Name)
	addProperty(g, "Process ID", strconv.Itoa(in.PID))
	if in.PPID > 0 {
		parentText := strconv.Itoa(in.PPID)
		if in.ParentName != "" {
			parentText = in.ParentName + " (" + parentText + ")"
		}
		addProperty(g, "Started by", parentText)
	}
	addProperty(g, "User", in.User)
	if !in.Started.IsZero() {
		addProperty(g, "Running since", startedText(in.Started, time.Now()))
	}
	addProperty(g, "State", capitalise(in.State))
	if in.Threads > 0 {
		addProperty(g, "Threads", strconv.Itoa(in.Threads))
	}
	if in.HaveNice {
		addProperty(g, "Priority", niceText(in.Nice))
	}
	page.Add(g)

	g = adw.NewPreferencesGroup()
	g.SetTitle("Program")
	addProperty(g, "Executable", in.Exe)
	if len(in.Cmdline) > 0 {
		addProperty(g, "Command line", quoteArgs(in.Cmdline))
	}
	if a := apps.of(in.Unit); a != nil {
		addProperty(g, "Application", a.name)
	}
	addProperty(g, "Unit", in.Unit)
	page.Add(g)

	g = adw.NewPreferencesGroup()
	g.SetTitle("Resources")
	g.SetDescription("Resident memory counts pages shared with other programs in full. " +
		"Proportional divides each shared page among the programs sharing it, and private " +
		"is what would be freed if this one closed.")
	addProperty(g, "Processor time", cpuTimeText(in.CPUTime))
	addProperty(g, "Resident memory", format.Bytes(in.RSS))
	if in.HaveSmaps {
		addProperty(g, "Proportional memory", format.Bytes(in.PSS))
	}
	if in.HaveSmaps || in.Private > 0 {
		addProperty(g, "Private memory", format.Bytes(in.Private))
	}
	if in.Swap > 0 {
		addProperty(g, "Swapped out", format.Bytes(in.Swap))
	}
	if in.HaveFiles {
		addProperty(g, "Open files", strconv.Itoa(in.OpenFiles))
	}
	if !in.HaveSmaps && !in.HaveFiles {
		g.SetDescription(g.Description() + " Some of this process's details belong to another user and cannot be read.")
	}
	page.Add(g)

	presentDetails(parent, title, page)
}

// showGroupDetails opens the panel for a grouped row: the application, and every
// process it is made of, each of which opens a panel of its own.
func showGroupDetails(parent gtk.Widgetter, name string, app *appIdent, members []process.Proc, apps *appResolver) {
	page := adw.NewPreferencesPage()

	var cpu float64
	var rss uint64
	units := map[string]bool{}
	for _, p := range members {
		cpu += p.CPU
		rss += p.RSS
		if p.Unit != "" {
			units[p.Unit] = true
		}
	}

	g := adw.NewPreferencesGroup()
	g.SetTitle("Application")
	addProperty(g, "Name", name)
	if app != nil {
		addProperty(g, "Application ID", app.id)
	}
	addProperty(g, "Processes", strconv.Itoa(len(members)))
	addProperty(g, "Processor", string(format.AppendPercent1(nil, cpu))+" of a core")
	addProperty(g, "Resident memory", format.Bytes(rss))
	if len(units) > 0 {
		list := make([]string, 0, len(units))
		for u := range units {
			list = append(list, u)
		}
		sort.Strings(list)
		label := "Unit"
		if len(list) > 1 {
			label = "Units"
		}
		addProperty(g, label, strings.Join(list, "\n"))
	}
	page.Add(g)

	sorted := append([]process.Proc(nil), members...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CPU > sorted[j].CPU })
	const most = 100
	g = adw.NewPreferencesGroup()
	g.SetTitle("Processes")
	if len(sorted) > most {
		g.SetDescription(fmt.Sprintf("The %d busiest of %d.", most, len(sorted)))
		sorted = sorted[:most]
	}
	var dlg *adw.Dialog
	for _, p := range sorted {
		row := adw.NewActionRow()
		row.SetTitle(p.Name)
		row.SetSubtitle(fmt.Sprintf("PID %d · %s · %s", p.PID,
			string(format.AppendPercent1(nil, p.CPU)), format.Bytes(p.RSS)))
		row.SetActivatable(true)
		row.AddSuffix(gtk.NewImageFromIconName("go-next-symbolic"))
		pid := p.PID
		row.ConnectActivated(func() {
			if dlg != nil {
				showProcessDetails(dlg, pid, apps)
			}
		})
		g.Add(row)
	}
	page.Add(g)

	dlg = presentDetails(parent, name, page)
}

// presentDetails puts a page in a dialog with a header and shows it.
func presentDetails(parent gtk.Widgetter, title string, page *adw.PreferencesPage) *adw.Dialog {
	dlg := adw.NewDialog()
	dlg.SetTitle(title)
	dlg.SetContentWidth(560)
	dlg.SetContentHeight(640)
	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(page)
	dlg.SetChild(tv)
	dlg.Present(parent)
	return dlg
}

// addProperty adds a label-and-value row in libadwaita's property style: the
// label small above, the value — which is what anyone is here for — prominent,
// and selectable so a path or a command line can be copied. An empty value is
// left out rather than shown as a blank.
func addProperty(g *adw.PreferencesGroup, label, value string) {
	if value == "" {
		return
	}
	row := adw.NewActionRow()
	row.SetTitle(label)
	row.SetSubtitle(value)
	row.SetSubtitleSelectable(true)
	row.SetUseMarkup(false)
	row.AddCSSClass("property")
	g.Add(row)
}

// startedText is a start time as a date and how long ago it was.
func startedText(t, now time.Time) string {
	stamp := t.Format("15:04")
	if y, m, d := t.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		stamp = t.Format("2 Jan 15:04")
	}
	return stamp + " · " + agoText(now.Sub(t))
}

// agoText is a duration in the largest unit that fits, rounded down.
func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments ago"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	default:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// cpuTimeText is processor time as hours, minutes and seconds.
func cpuTimeText(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d/time.Hour), int(d/time.Minute)%60, int(d/time.Second)%60
	switch {
	case h > 0:
		return fmt.Sprintf("%d h %02d min %02d s", h, m, s)
	case m > 0:
		return fmt.Sprintf("%d min %02d s", m, s)
	default:
		return fmt.Sprintf("%d s", s)
	}
}

// niceText says what a nice value means rather than just giving the number.
func niceText(n int) string {
	switch {
	case n < 0:
		return fmt.Sprintf("Raised (%d)", n)
	case n == 0:
		return "Normal"
	default:
		return fmt.Sprintf("Lowered (+%d)", n)
	}
}

func capitalise(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// quoteArgs joins a command line back into something that could be pasted into
// a shell: arguments with spaces or quotes in them are quoted.
func quoteArgs(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`") {
			b.WriteString("'" + strings.ReplaceAll(a, "'", `'\''`) + "'")
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}
