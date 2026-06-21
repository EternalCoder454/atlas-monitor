package ui

import (
	"os"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/process"
	"atlas-monitor/internal/stats"
)

// Window owns the content stack, the per-view map, and the refresh tick.
type Window struct {
	col          *stats.Collector
	proc         *process.Collector
	ai           *ai.Client
	settings     *config.Settings
	stack        *gtk.Stack
	views        map[string]View
	asst         *assistantView
	assistantRow *adw.ActionRow
	active       string
	visible      bool

	// Network rows are reordered live so the active interface stays first.
	netExp     *adw.ExpanderRow
	netRows    map[string]*adw.ActionRow
	netStable  []string // base order (collector order)
	netCurrent []string // currently displayed order
}

// NewWindow creates the content controller around a started collector.
func NewWindow(col *stats.Collector, client *ai.Client, settings *config.Settings) *Window {
	return &Window{
		col:      col,
		proc:     process.New(),
		ai:       client,
		settings: settings,
		views:    map[string]View{},
		visible:  true,
	}
}

// Build constructs the sidebar + content area and returns the root widget.
func (w *Window) Build() gtk.Widgetter {
	w.stack = gtk.NewStack()
	w.stack.SetHExpand(true)
	w.stack.SetVExpand(true)
	w.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	w.stack.SetTransitionDuration(120)

	w.addView("cpu", newCPUView(w.col))
	w.addView("memory", newMemView(w.col))

	var disks []*stats.DiskStats
	var nets []*stats.NetStats
	var gpuAvail bool
	var activeNet string
	w.col.Read(func(s *stats.Stats) {
		disks = append(disks, s.Disks...)
		nets = append(nets, s.Nets...)
		gpuAvail = s.GPU.Available
		activeNet = s.ActiveNet
	})
	for _, d := range disks {
		w.addView("disk:"+d.Name, newDiskView(w.col, d))
	}
	for _, n := range nets {
		w.addView("net:"+n.Name, newNetView(w.col, n))
	}
	if gpuAvail {
		w.addView("gpu", newGPUView(w.col))
	}

	w.asst = newAssistantView(w.col, w.proc, w.ai, w.settings)
	w.addView("assistant", w.asst)
	w.addView("apps", newAppsView(w.proc))
	w.addView("services", newServicesView())

	w.netStable = make([]string, len(nets))
	for i, n := range nets {
		w.netStable[i] = n.Name
	}
	orderedNets := orderByActive(nets, activeNet)

	sb := buildSidebar(disks, orderedNets, gpuAvail, w.selectView)
	w.assistantRow = sb.assistantRow
	w.netExp = sb.netExp
	w.netRows = sb.netRows
	w.netCurrent = make([]string, len(orderedNets))
	for i, n := range orderedNets {
		w.netCurrent[i] = n.Name
	}
	w.updateNetIcon(activeNet)
	w.SetAIEnabled(w.settings.AIEnabled)

	hbox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	hbox.Append(sb.root)
	hbox.Append(gtk.NewSeparator(gtk.OrientationVertical))
	hbox.Append(w.stack)

	// Default to CPU; ATLAS_VIEW=<name> can open another view at startup.
	initial := "cpu"
	if name := os.Getenv("ATLAS_VIEW"); name != "" {
		if _, ok := w.views[name]; ok {
			initial = name
		}
	}
	if initial == "assistant" && !w.settings.AIEnabled {
		initial = "cpu"
	}
	w.selectView(initial)
	return hbox
}

// SetAIEnabled shows/hides the Assistant entry and leaves it if it was active.
func (w *Window) SetAIEnabled(enabled bool) {
	if w.assistantRow != nil {
		w.assistantRow.SetVisible(enabled)
	}
	if !enabled && w.active == "assistant" {
		w.selectView("cpu")
	}
}

// RefreshQuickPrompts rebuilds the assistant's quick-prompt dropdown after the
// prompts are edited in Settings.
func (w *Window) RefreshQuickPrompts() {
	if w.asst != nil {
		w.asst.RefreshQuickPrompts()
	}
}

// StartRefresh installs the 1-second UI tick that updates the active view.
func (w *Window) StartRefresh() {
	glib.TimeoutAdd(1000, func() bool {
		if w.visible {
			w.reorderNets()
			w.tickActive()
		}
		return true
	})
}

// SetVisible pauses/resumes collection and refresh based on window visibility.
func (w *Window) SetVisible(visible bool) {
	w.visible = visible
	if visible {
		w.col.Resume()
		if w.active == "apps" || w.active == "assistant" {
			w.proc.Start()
		}
		w.tickActive()
	} else {
		w.col.Pause()
		w.proc.Stop()
	}
}

func (w *Window) addView(name string, v View) {
	w.views[name] = v
	w.stack.AddNamed(v.Root(), name)
}

func (w *Window) selectView(name string) {
	if _, ok := w.views[name]; !ok {
		return
	}
	w.active = name
	w.stack.SetVisibleChildName(name)
	// The per-process collector is expensive, so only run it where it is used:
	// the Apps table and the Assistant (which reports top processes).
	if name == "apps" || name == "assistant" {
		w.proc.Start()
	} else {
		w.proc.Stop()
	}
	w.tickActive()
}

func (w *Window) tickActive() {
	if v, ok := w.views[w.active]; ok {
		v.Update()
	}
}

// reorderNets keeps the active (default-route) interface at the top of the
// Network dropdown, re-sorting only when the active interface changes.
func (w *Window) reorderNets() {
	if w.netExp == nil || len(w.netRows) == 0 {
		return
	}
	var active string
	w.col.Read(func(s *stats.Stats) { active = s.ActiveNet })
	desired := orderNames(w.netStable, active)
	if equalStrings(desired, w.netCurrent) {
		return
	}
	for _, name := range w.netCurrent {
		if row := w.netRows[name]; row != nil {
			w.netExp.Remove(row)
		}
	}
	for _, name := range desired {
		if row := w.netRows[name]; row != nil {
			w.netExp.AddRow(row)
		}
	}
	w.netCurrent = desired
	w.updateNetIcon(active)
}

// updateNetIcon shows a wireless or wired glyph on the Network group depending
// on the active interface.
func (w *Window) updateNetIcon(active string) {
	if w.netExp == nil {
		return
	}
	icon := "network-wired-symbolic"
	if active != "" {
		if _, err := os.Stat("/sys/class/net/" + active + "/wireless"); err == nil {
			icon = "network-wireless-symbolic"
		}
	}
	w.netExp.SetIconName(icon)
}
