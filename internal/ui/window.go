package ui

import (
	"os"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/process"
	"atlas-monitor/internal/stats"
	"atlas-monitor/internal/sysmem"
)

// lazyView is a page that is only built the first time it is opened. A machine
// with several disks and interfaces has a dozen pages, each a scroller full of
// widgets and two Cairo charts; building them all up front costs megabytes of
// GTK objects for pages the user may never look at. The sidebar row is cheap,
// so navigation is unaffected.
//
// The reverse — tearing a page down again when the user leaves it — does not
// work and is deliberately not attempted. Destroying a GTK page reclaims
// nothing: gotk4 keeps each signal handler's Go closure alive for the widget's
// lifetime and those closures reference the page, so the two hold each other
// up. Rebuilding one simply costs its memory a second time. Not building it
// until it is asked for is the saving that actually lands.
type lazyView struct {
	build func() View
	view  View
}

// trimInterval is how often idle memory is handed back to the OS while the
// window is visible. GTK and the GLib allocator hold on to freed blocks; a
// periodic trim keeps the resident set flat over a long session. It is wall
// time rather than a tick count so it does not stretch with the refresh
// interval.
const trimInterval = time.Minute

// Window owns the content stack, the per-view map, and the refresh tick.
type Window struct {
	col          *stats.Collector
	proc         *process.Collector
	ai           *ai.Client
	settings     *config.Settings
	stack        *gtk.Stack
	views        map[string]*lazyView
	asst         assistant
	sidebar      *sidebar
	assistantRow *adw.ActionRow
	split        *adw.OverlaySplitView
	menuBtn      *gtk.ToggleButton
	active       string
	visible      bool
	tick         glib.SourceHandle
	lastTrim     time.Time

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
		views:    map[string]*lazyView{},
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

	col := w.col
	w.addView("cpu", func() View { return newCPUView(col, w.settings) })
	w.addView("memory", func() View { return newMemView(col) })

	var disks []*stats.DiskStats
	var nets []*stats.NetStats
	var gpuAvail bool
	var packs []string
	var activeNet string
	batteryAvail := w.col.PowerAvailable()
	w.col.Read(func(s *stats.Stats) {
		disks = append(disks, s.Disks...)
		nets = append(nets, s.Nets...)
		gpuAvail = s.GPU.Available
		for _, p := range s.Power.Packs {
			packs = append(packs, p.Battery.Name)
		}
		activeNet = s.ActiveNet
	})
	for _, d := range disks {
		w.addView("disk:"+d.Name, func() View { return newDiskView(col, d) })
	}
	for _, n := range nets {
		w.addView("net:"+n.Name, func() View { return newNetView(col, n) })
	}
	if gpuAvail {
		w.addView("gpu", func() View { return newGPUView(col) })
	}
	if batteryAvail {
		w.addView("power", func() View { return newPowerView(col, "") })
		// A machine with two packs discharges them in sequence, so the summed
		// figure hides which one is doing the work and how they have aged. One
		// page each, but only when there is more than one to tell apart.
		if len(packs) > 1 {
			for _, name := range packs {
				w.addView("power:"+name, func() View { return newPowerView(col, name) })
			}
		}
	}

	if aiCompiledIn {
		w.addView("assistant", func() View {
			a := newAssistant(col, w.proc, w.ai, w.settings)
			w.asst = a
			return a
		})
	}
	w.addView("apps", func() View { return newAppsView(w.proc, gpuAvail, w.settings) })
	w.addView("services", func() View { return newServicesView() })

	w.netStable = make([]string, len(nets))
	for i, n := range nets {
		w.netStable[i] = n.Name
	}
	orderedNets := orderByActive(nets, activeNet)

	sb := buildSidebar(disks, orderedNets, packs, gpuAvail, batteryAvail, aiCompiledIn, w.selectView)
	w.sidebar = sb
	w.assistantRow = sb.assistantRow
	w.netExp = sb.netExp
	w.netRows = sb.netRows
	w.netCurrent = make([]string, len(orderedNets))
	for i, n := range orderedNets {
		w.netCurrent[i] = n.Name
	}
	w.updateNetIcon(activeNet)
	w.SetAIEnabled(w.settings.AIEnabled)
	w.col.SetInterval(w.refreshInterval())
	w.proc.SetInterval(w.refreshInterval())

	// The two panes, and the rule that collapses them.
	//
	// Wide enough, this is the fixed two-pane layout it has always been: the
	// sidebar sits beside the content and never covers it. Below the breakpoint
	// there is not enough width for both — a 200px sidebar out of 500 is nearly
	// half the window — so the sidebar becomes an overlay that slides over the
	// content and closes again once a page is chosen.
	split := adw.NewOverlaySplitView()
	split.SetSidebar(sb.root)
	split.SetContent(w.stack)
	split.SetMinSidebarWidth(200)
	split.SetMaxSidebarWidth(240)
	split.SetSidebarWidthFraction(0.22)
	w.split = split

	// The button that opens it while it is an overlay. It is hidden the rest of
	// the time: with the sidebar already on screen there is nothing to toggle.
	w.menuBtn = gtk.NewToggleButton()
	w.menuBtn.SetIconName("atlas-menu-symbolic")
	w.menuBtn.SetTooltipText("Show the sidebar")
	w.menuBtn.SetVisible(false)
	w.menuBtn.ConnectToggled(func() { split.SetShowSidebar(w.menuBtn.Active()) })
	split.NotifyProperty("show-sidebar", func() {
		if active := split.ShowSidebar(); active != w.menuBtn.Active() {
			w.menuBtn.SetActive(active)
		}
	})

	bin := adw.NewBreakpointBin()
	bin.SetChild(split)
	// BreakpointBin refuses to shrink below its own minimum, so it is told one
	// small enough for the breakpoint to be reachable at all.
	bin.SetSizeRequest(360, 320)

	bp := adw.NewBreakpoint(adw.NewBreakpointConditionLength(
		adw.BreakpointConditionMaxWidth, narrowWidth, adw.LengthUnitPx))
	bp.ConnectApply(func() {
		split.SetCollapsed(true)
		w.menuBtn.SetVisible(true)
	})
	bp.ConnectUnapply(func() {
		split.SetCollapsed(false)
		split.SetShowSidebar(true)
		w.menuBtn.SetVisible(false)
	})
	bin.AddBreakpoint(bp)

	// Reopen on the page the user left, unless ATLAS_VIEW overrides it for
	// development. A page that no longer exists — a disk that was unplugged —
	// falls back to CPU.
	initial := "cpu"
	if _, ok := w.views[w.settings.LastView]; ok {
		initial = w.settings.LastView
	}
	if name := os.Getenv("ATLAS_VIEW"); name != "" {
		if _, ok := w.views[name]; ok {
			initial = name
		}
	}
	if initial == "assistant" && !w.settings.AIEnabled {
		initial = "cpu"
	}
	w.selectView(initial)

	// And again once the window is on screen. GTK gives initial focus to the
	// first focusable widget, which is the first sidebar row, and a GtkListBox
	// selects the row that receives focus — after Build has run. Without this
	// the hardware list came up with CPU highlighted whatever page was open,
	// which is what anyone reopening Atlas on Apps or Services saw, since it
	// restores the page you left. Re-asserting from an idle callback runs after
	// focus has landed, so the content stays the thing that decides.
	bin.ConnectMap(func() {
		glib.IdleAdd(func() { w.sidebar.selectView(w.active) })
	})
	return bin
}

// SetAIEnabled shows or hides the Assistant entry. With AI off the page is
// never built at all — no widgets, no Ollama probe, no systemd bus connection —
// which is what makes turning the assistant off a real saving rather than a
// hidden row.
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

// StartRefresh installs the UI tick that updates the active view, at whatever
// interval the settings ask for.
func (w *Window) StartRefresh() {
	w.lastTrim = time.Now()
	w.installTick()
}

// SetRefreshInterval re-times the UI tick and both collectors after the
// interval is changed in Settings.
func (w *Window) SetRefreshInterval(d time.Duration) {
	w.col.SetInterval(d)
	w.proc.SetInterval(d)
	if w.tick != 0 {
		w.installTick()
	}
}

// installTick replaces the GLib timeout driving the refresh.
func (w *Window) installTick() {
	if w.tick != 0 {
		glib.SourceRemove(w.tick)
		w.tick = 0
	}
	every := w.refreshInterval()
	w.tick = glib.TimeoutAdd(uint(every/time.Millisecond), func() bool {
		if !w.visible {
			return true
		}
		w.reorderNets()
		if w.sidebar != nil {
			w.col.Read(w.sidebar.update)
		}
		w.tickActive()
		if time.Since(w.lastTrim) >= trimInterval {
			w.lastTrim = time.Now()
			go sysmem.Trim() // off the main loop; malloc_trim walks the heap
		}
		return true
	})
}

// refreshInterval is the configured sampling period.
func (w *Window) refreshInterval() time.Duration {
	return time.Duration(config.NormalizeRefresh(w.settings.RefreshSeconds)) * time.Second
}

// ActiveView is the page currently on screen, saved so Atlas reopens on it.
func (w *Window) ActiveView() string { return w.active }

// MenuButton is the sidebar toggle, for the window's header bar. It shows
// itself only when the sidebar has collapsed into an overlay.
func (w *Window) MenuButton() *gtk.ToggleButton { return w.menuBtn }

// narrowWidth is where the sidebar stops fitting beside the content. Below it
// the window is mostly sidebar, so it becomes an overlay instead.
const narrowWidth = 700

// SetVisible pauses/resumes collection and refresh based on window visibility.
func (w *Window) SetVisible(visible bool) {
	w.visible = visible
	if visible {
		w.col.Resume()
		if needsProcs(w.active) {
			w.proc.Start()
		}
		w.tickActive()
		return
	}
	w.col.Pause()
	w.proc.Stop()
	// Hidden in the tray or another workspace: hand back everything the last
	// visible second allocated, so a backgrounded Atlas costs almost nothing.
	go sysmem.Release()
}

func (w *Window) addView(name string, build func() View) {
	w.views[name] = &lazyView{build: build}
}

func (w *Window) selectView(name string) {
	lv := w.views[name]
	if lv == nil {
		return
	}
	if lv.view == nil {
		lv.view = lv.build()
		w.stack.AddNamed(lv.view.Root(), name)
	}
	w.active = name
	w.stack.SetVisibleChildName(name)
	if w.split != nil && w.split.Collapsed() {
		w.split.SetShowSidebar(false)
	}
	if w.sidebar != nil {
		w.sidebar.selectView(name)
	}
	// The per-process collector is expensive, so only run it where it is used:
	// the Apps table and the Assistant (which reports top processes).
	if needsProcs(name) {
		w.proc.Start()
	} else {
		w.proc.Stop()
	}
	w.tickActive()
}

// needsProcs reports whether a view consumes the per-process collector.
func needsProcs(name string) bool {
	return name == "apps" || name == "assistant"
}

func (w *Window) tickActive() {
	if lv := w.views[w.active]; lv != nil && lv.view != nil {
		lv.view.Update()
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
	icon := "atlas-network-symbolic"
	if active != "" {
		if _, err := os.Stat("/sys/class/net/" + active + "/wireless"); err == nil {
			icon = "atlas-wifi-symbolic"
		}
	}
	w.netExp.SetIconName(icon)
}
