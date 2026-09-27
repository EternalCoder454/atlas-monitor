package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/config"
	"atlas-monitor/internal/format"
	"atlas-monitor/internal/gfx"
	"atlas-monitor/internal/sysmem"
	"atlas-monitor/internal/theme"
)

// SettingsHooks are the app-level callbacks the Settings dialog needs.
type SettingsHooks struct {
	OnChange    func()                                                        // a setting was saved
	ApplyUpdate func(done func(ok bool))                                      // pull the channel, reinstall, relaunch; done reports a failure
	CheckUpdate func(channel string) (available bool, info string, err error) // git fetch + compare (no restart)
	Version     string
	Location    string // install / source location, for display
	// ManagedBy names the package manager that owns this install, when one does.
	// The Update button then says so, because "Update" on its own promises
	// something Atlas is not the one doing.
	ManagedBy string
}

// ShowSettings presents the settings dialog over parent: a sidebar with three
// sections — Model & Prompt, Quick Prompts, and App.
func ShowSettings(parent gtk.Widgetter, s *config.Settings, h SettingsHooks) {
	dlg := adw.NewDialog()
	dlg.SetTitle("Settings")
	dlg.SetContentWidth(640)
	dlg.SetContentHeight(620)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(adw.NewHeaderBar())

	// One page, so no navigation. The full build has three and needs a sidebar
	// to move between them; here it would be a 200px column holding a single
	// highlighted row that goes nowhere, next to a dialog made narrower to make
	// room for it.
	ap := newAppPage(s, h)
	ap.page.SetHExpand(true)
	ap.page.SetVExpand(true)
	toolbar.SetContent(ap.page)
	dlg.SetChild(toolbar)

	dlg.ConnectClosed(func() {
		_ = config.Save(*s)
		fire(h.OnChange)
	})

	dlg.Present(parent)
}

type appPage struct {
	page *adw.PreferencesPage
}

func newAppPage(s *config.Settings, h SettingsHooks) *appPage {
	p := &appPage{page: adw.NewPreferencesPage()}
	version := h.Version
	if version == "" {
		version = "unknown"
	}

	p.page.Add(themeGroup(s, h))
	p.page.Add(perfGroup(s, h))

	updGroup := adw.NewPreferencesGroup()
	updGroup.SetTitle("Updates")
	updGroup.SetDescription("Atlas updates by pulling its branch from GitHub and reinstalling. " +
		"Update checks first and only restarts if there is something newer.")

	// No channel picker. The full application lives on main and beta, and
	// offering either here would let someone who installed the build without an
	// assistant update their way back into the one with it — silently, since an
	// update just pulls a branch and rebuilds. This build follows its own branch
	// and says so.
	channel := adw.NewActionRow()
	channel.SetTitle("Channel")
	channel.SetSubtitle("Minimal — this build follows the branch it was made from")

	status := adw.NewActionRow()
	status.SetTitle("Status")
	status.SetSubtitle(fmt.Sprintf("On %s · version %s", channelName(s.UpdateChannel), version))
	status.SetSubtitleSelectable(true)
	// Checking on launch is on by default: an update nobody hears about is not
	// much use. It is one switch to stop, and stopping it leaves the manual
	// Update button below working exactly as before.
	autoCheck := adw.NewSwitchRow()
	autoCheck.SetTitle("Check for updates on launch")
	autoCheck.SetSubtitle("Asks GitHub once, shortly after Atlas opens, and only speaks up if there is something newer")
	autoCheck.SetActive(s.UpdateCheck)
	autoCheck.NotifyProperty("active", func() {
		if s.UpdateCheck == autoCheck.Active() {
			return
		}
		s.UpdateCheck = autoCheck.Active()
		_ = config.Save(*s)
		fire(h.OnChange)
	})

	updGroup.Add(channel)
	updGroup.Add(autoCheck)
	updGroup.Add(status)

	update := adw.NewButtonRow()
	update.SetTitle("Update")
	if h.ManagedBy != "" {
		// An install pacman owns is not updated from here, and the button should
		// not imply otherwise: it checks, and then hands over the one command
		// that does the work.
		update.SetTitle("Check for updates")
	}
	update.SetStartIconName("atlas-update-symbolic")
	update.AddCSSClass("suggested-action")
	update.ConnectActivated(func() {
		if h.CheckUpdate == nil {
			return
		}
		channelNow := s.UpdateChannel
		update.SetSensitive(false)
		status.SetSubtitle("Checking " + channelName(channelNow) + " for updates…")
		go func() {
			available, info, err := h.CheckUpdate(channelNow)
			glib.IdleAdd(func() {
				switch {
				case err != nil:
					update.SetSensitive(true)
					status.SetSubtitle("Couldn't check: " + err.Error())
				case available:
					// The button stays disabled while the install runs: it ends in
					// a restart, and a dialog of its own shows how it is getting on.
					// It comes back only if the update did not happen after all.
					status.SetSubtitle(info + " — updating…")
					if h.ApplyUpdate == nil {
						update.SetSensitive(true)
						break
					}
					h.ApplyUpdate(func(ok bool) {
						if ok {
							return
						}
						update.SetSensitive(true)
						if h.ManagedBy != "" {
							// Not a failure. A packaged install is updated by
							// its package manager, and ApplyUpdate has just
							// shown the command that does it — so leave the
							// summary saying what is available rather than
							// claiming something went wrong.
							status.SetSubtitle(info)
							return
						}
						status.SetSubtitle("The update didn't finish — see the message for details.")
					})
				default:
					update.SetSensitive(true)
					status.SetSubtitle(info)
				}
			})
		}()
	})
	updGroup.Add(update)
	p.page.Add(updGroup)

	aboutGroup := adw.NewPreferencesGroup()
	aboutGroup.SetTitle("About")
	ver := adw.NewActionRow()
	ver.SetTitle("Version")
	ver.SetSubtitle(version)
	ver.SetSubtitleSelectable(true)
	aboutGroup.Add(ver)
	loc := adw.NewActionRow()
	loc.SetTitle("Location")
	loc.SetSubtitle(nonEmpty(h.Location, "unknown"))
	loc.SetSubtitleSelectable(true)
	aboutGroup.Add(loc)
	p.page.Add(aboutGroup)
	return p
}

// themeGroup builds the colour theme picker: one circle per theme, split between
// the window background and the accent.
//
// Circles rather than a dropdown because the thing being chosen is a colour, and a
// list of names makes you pick one to find out what it looks like. Split circles
// rather than single ones because a theme is two decisions — what the window is and
// what stands out against it — and either one alone is a misleading preview.
//
// There is no circle for "follow the desktop". It is the state Atlas starts in and
// it is not a palette, so it sits underneath as a plain button, and only when there
// is something to go back from.
func themeGroup(s *config.Settings, h SettingsHooks) *adw.PreferencesGroup {
	g := adw.NewPreferencesGroup()
	g.SetTitle("Theme")

	row := gtk.NewBox(gtk.OrientationHorizontal, 18)
	row.SetHAlign(gtk.AlignCenter)
	row.SetMarginTop(6)
	row.SetMarginBottom(6)

	// A small, quiet button. Added to the group's own box rather than to the
	// group directly: a bare button there picks up the styling meant for a
	// section heading and comes out bold and full width, which for the way back
	// from a choice is much too loud.
	follow := gtk.NewButtonWithLabel("Follow the desktop instead")
	follow.SetHAlign(gtk.AlignCenter)
	follow.AddCSSClass("flat")
	follow.AddCSSClass("am-quiet-button")

	// Every button is held so that choosing one can clear the others. A GtkCheckButton
	// group would do that itself, but its indicator cannot be styled into a disc.
	var buttons []*gtk.ToggleButton

	describe := func() {
		if theme.IsFollowing(s.Theme) {
			g.SetDescription("Atlas is following the desktop's light and dark setting. " +
				"Choose a theme to set it here instead.")
			follow.SetVisible(false)
			return
		}
		if t, ok := theme.ByID(s.Theme); ok {
			g.SetDescription(t.Name + " — " + t.Summary)
		}
		follow.SetVisible(true)
	}

	// sync marks the chosen one and leaves the rest clear. The guard is for the
	// notify that setting Active fires: without it, clearing the others would
	// re-enter this through their own handlers.
	syncing := false
	sync := func() {
		syncing = true
		for i, b := range buttons {
			b.SetActive(theme.Themes[i].ID == s.Theme)
		}
		syncing = false
		describe()
	}

	for _, t := range theme.Themes {
		t := t

		swatch := gtk.NewToggleButton()
		swatch.AddCSSClass("am-swatch")
		swatch.AddCSSClass(theme.SwatchClass(t.ID))
		swatch.SetTooltipText(t.Name + " — " + t.Summary)
		// The button has no label, so without this a screen reader would announce
		// an unnamed toggle five times over.
		swatch.SetName(t.Name)
		swatch.Widget.SetTooltipText(t.Name + " — " + t.Summary)

		name := gtk.NewLabel(t.Name)
		name.AddCSSClass("caption")

		cell := gtk.NewBox(gtk.OrientationVertical, 6)
		cell.SetHAlign(gtk.AlignCenter)
		cell.Append(swatch)
		cell.Append(name)
		row.Append(cell)

		swatch.ConnectToggled(func() {
			if syncing {
				return
			}
			if !swatch.Active() {
				// Clicking the chosen one again would otherwise turn the theme
				// off and leave nothing selected. It stays chosen.
				swatch.SetActive(true)
				return
			}
			s.Theme = t.ID
			_ = config.Save(*s)
			sync()
			fire(h.OnChange)
		})
		buttons = append(buttons, swatch)
	}

	follow.ConnectClicked(func() {
		s.Theme = theme.Follow
		_ = config.Save(*s)
		sync()
		fire(h.OnChange)
	})

	content := gtk.NewBox(gtk.OrientationVertical, 4)
	content.Append(row)
	content.Append(follow)
	g.Add(content)
	sync()
	return g
}

// perfGroup builds the rendering-mode selector and the live self-memory
// readout. Rendering is the single biggest influence on how much memory Atlas
// uses, so it gets a first-class setting rather than an environment variable.
func perfGroup(s *config.Settings, h SettingsHooks) *adw.PreferencesGroup {
	g := adw.NewPreferencesGroup()
	g.SetTitle("Performance")
	g.SetDescription("Atlas draws its charts on the CPU by default, which keeps the graphics driver stack — " +
		"Mesa, the Vulkan loader and LLVM — out of the process entirely. Loading it costs memory: on the " +
		"machine this was measured on, about 27 MiB pinned to one card, or about 63 MiB if GTK is left to " +
		"load every driver installed. Switch to GPU if you want smoother window resizing on a high-refresh " +
		"display.")

	labels := make([]string, len(gfx.Modes))
	selected := 0
	for i, m := range gfx.Modes {
		labels[i] = m.Label
		if m.Value == s.RenderMode {
			selected = i
		}
	}
	render := adw.NewComboRow()
	render.SetTitle("Rendering")
	render.SetSubtitle(gfx.Modes[selected].Detail)
	render.SetModel(gtk.NewStringList(labels))
	render.SetSelected(uint(selected))
	render.NotifyProperty("selected", func() {
		idx := int(render.Selected())
		if idx < 0 || idx >= len(gfx.Modes) || gfx.Modes[idx].Value == s.RenderMode {
			return
		}
		s.RenderMode = gfx.Modes[idx].Value
		render.SetSubtitle(gfx.Modes[idx].Detail + " · restart Atlas to apply")
		_ = config.Save(*s)
		fire(h.OnChange)
	})
	g.Add(render)

	// Text rendering. Separate from the renderer above: this one decides
	// whether GTK may skip hinting, which is only obvious on a 1x display.
	textLabels := make([]string, len(gfx.TextModes))
	textSel := 0
	currentText := gfx.NormalizeText(s.TextRendering)
	for i, m := range gfx.TextModes {
		textLabels[i] = m.Label
		if m.Value == currentText {
			textSel = i
		}
	}
	text := adw.NewComboRow()
	text.SetTitle("Font rendering")
	text.SetSubtitle(gfx.TextModes[textSel].Detail)
	text.SetModel(gtk.NewStringList(textLabels))
	text.SetSelected(uint(textSel))
	text.NotifyProperty("selected", func() {
		idx := int(text.Selected())
		if idx < 0 || idx >= len(gfx.TextModes) || gfx.TextModes[idx].Value == s.TextRendering {
			return
		}
		s.TextRendering = gfx.TextModes[idx].Value
		text.SetSubtitle(gfx.TextModes[idx].Detail + " · restart Atlas to apply")
		_ = config.Save(*s)
		fire(h.OnChange)
	})
	g.Add(text)

	// Sampling interval. Slower is cheaper, and stretches the graphs: they hold
	// 60 samples whatever the rate.
	intervals := make([]string, len(config.RefreshChoices))
	chosen := 0
	current := config.NormalizeRefresh(s.RefreshSeconds)
	for i, sec := range config.RefreshChoices {
		intervals[i] = refreshLabel(sec)
		if sec == current {
			chosen = i
		}
	}
	refresh := adw.NewComboRow()
	refresh.SetTitle("Refresh interval")
	refresh.SetSubtitle(refreshDetail(current))
	refresh.SetModel(gtk.NewStringList(intervals))
	refresh.SetSelected(uint(chosen))
	refresh.NotifyProperty("selected", func() {
		idx := int(refresh.Selected())
		if idx < 0 || idx >= len(config.RefreshChoices) {
			return
		}
		s.RefreshSeconds = config.RefreshChoices[idx]
		refresh.SetSubtitle(refreshDetail(s.RefreshSeconds))
		_ = config.Save(*s)
		fire(h.OnChange)
	})
	g.Add(refresh)

	usage := adw.NewActionRow()
	usage.SetTitle("Memory used by Atlas")
	usage.SetSubtitle(selfMemory())
	usage.SetSubtitleSelectable(true)
	g.Add(usage)

	release := adw.NewButtonRow()
	release.SetTitle("Release idle memory now")
	release.SetStartIconName("atlas-trash-symbolic")
	release.ConnectActivated(func() {
		sysmem.Release()
		usage.SetSubtitle(selfMemory())
	})
	g.Add(release)
	return g
}

// refreshLabel names an interval in the dropdown.
func refreshLabel(seconds int) string {
	if seconds == 1 {
		return "Every second"
	}
	return "Every " + strconv.Itoa(seconds) + " seconds"
}

// refreshDetail explains what the choice costs and buys.
func refreshDetail(seconds int) string {
	if seconds == 1 {
		return "Graphs cover the last minute"
	}
	return "Lighter on the CPU · graphs cover the last " +
		strconv.Itoa(seconds) + " minutes"
}

// selfMemory reports this process's resident set, read straight from
// /proc/self/statm — the same figure a task manager shows for Atlas.
func selfMemory() string {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return "unavailable"
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return "unavailable"
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return "unavailable"
	}
	return format.Bytes(pages*uint64(os.Getpagesize())) + " resident"
}

func channelName(string) string { return "Minimal" }

// --- shared helpers ---------------------------------------------------------

func fire(f func()) {
	if f != nil {
		f()
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
