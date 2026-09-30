package ui

import (
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/format"
	"atlas-monitor/internal/gfx"
	"atlas-monitor/internal/sysmem"
	"atlas-monitor/internal/theme"
)

const modelsURL = "https://ollama.com/library"

// SettingsHooks are the app-level callbacks the Settings page needs.
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

// settingsView is the Settings page: every setting on one scrolling page, under
// a few section headings, the way Windows 11's Task Manager lays out its own.
//
// It used to be a dialog with a list of three pages down its side, and the
// quick prompts folded away inside expander rows, so reaching a setting could
// take four clicks and changing one meant finding it first. Now each setting is
// a card of its own — an icon, a title, one line saying what it does, and the
// control at the right-hand end — and all of them are on screen, a scroll away
// at most. The options that belong to another, such as the assistant's model
// under the switch that turns the assistant on, sit in the same card beneath it,
// lined up with its title, and are always shown: there is nothing to expand.
//
// Text fields apply themselves, on Enter or when focus leaves them, instead of
// waiting for an apply button, and whatever is still being typed is applied
// when the page is left.
type settingsView struct {
	root  *gtk.ScrolledWindow
	usage *adw.ActionRow // Atlas's own memory, re-read while the page is open

	// flush applies text typed but not yet confirmed. Each field adds its own.
	flush []func()
}

// settingIconSize and the margins round it decide where a card's titles start,
// and settingIndent is that position, for the rows and blocks beneath a card's
// first row that have no icon of their own but should line up with its title.
// The 12 on either side of the margins is libadwaita's own: the row's header
// box starts 12px in, and puts 6px between its prefixes and the title.
const (
	settingIconSize     = 20
	settingIconStart    = 6
	settingIconEnd      = 10
	settingIndent       = 12 + settingIconStart + settingIconSize + settingIconEnd + 6
	settingTextWidth    = 26 // characters, for the text fields at a row's end
	settingPromptHeight = 140
)

func newSettingsView(s *config.Settings, h SettingsHooks) *settingsView {
	v := &settingsView{}

	page := gtk.NewBox(gtk.OrientationVertical, 6)
	page.AddCSSClass("am-settings")
	page.SetMarginTop(18)
	page.SetMarginBottom(24)
	page.SetMarginStart(24)
	page.SetMarginEnd(24)
	page.Append(newTitle("Settings"))

	settingsSection(page, "Appearance",
		themeCard(s, h),
		transparencyCard(s, h),
		settingsCard(fontRow(s, h)))

	v.usage = memoryRow()
	settingsSection(page, "Performance",
		settingsCard(refreshRow(s, h)),
		settingsCard(renderRow(s, h)),
		settingsCard(v.usage))

	if aiCompiledIn {
		settingsSection(page, "Assistant",
			v.assistantCard(s, h),
			v.promptCard(s, h),
			v.quickPromptsCard(s, h))
	}

	settingsSection(page, "Updates", updateCards(s, h)...)

	// Wide enough for Task Manager's long rows, but not so wide on a maximised
	// window that a title and its control end up a screen apart.
	clamp := adw.NewClamp()
	clamp.SetMaximumSize(1000)
	clamp.SetTighteningThreshold(800)
	clamp.SetChild(page)

	v.root = gtk.NewScrolledWindow()
	v.root.SetChild(clamp)
	v.root.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.root.SetHExpand(true)
	v.root.SetVExpand(true)
	// Leaving the page — for another page, or closing the window — applies
	// whatever was being typed, as leaving the field would have.
	v.root.ConnectUnmap(v.commit)
	return v
}

func (v *settingsView) Root() gtk.Widgetter { return v.root }

// Update refreshes the one reading on the page, Atlas's own memory, so it can
// be watched falling after Release idle memory without leaving and coming back.
func (v *settingsView) Update() { v.usage.SetSubtitle(selfMemory()) }

func (v *settingsView) commit() {
	for _, f := range v.flush {
		f()
	}
}

// --- Layout -----------------------------------------------------------------

// settingsSection appends a section to the page: its heading, then its cards.
func settingsSection(page *gtk.Box, title string, cards ...gtk.Widgetter) {
	heading := gtk.NewLabel(title)
	heading.AddCSSClass("am-settings-heading")
	heading.SetXAlign(0)
	heading.SetMarginTop(14)
	heading.SetMarginBottom(4)
	page.Append(heading)
	for _, c := range cards {
		page.Append(c)
	}
}

// settingsCard is one card on the page: its rows joined on one surface.
func settingsCard(rows ...gtk.Widgetter) *gtk.ListBox {
	lb := gtk.NewListBox()
	lb.SetSelectionMode(gtk.SelectionNone)
	lb.AddCSSClass("boxed-list")
	for _, r := range rows {
		lb.Append(r)
	}
	return lb
}

// withIcon makes row the first row of a card: its icon at the start, and the
// taller height Task Manager gives a setting's title and description.
func withIcon(row *adw.ActionRow, icon string) {
	img := gtk.NewImageFromIconName(icon)
	img.SetPixelSize(settingIconSize)
	img.SetMarginStart(settingIconStart)
	img.SetMarginEnd(settingIconEnd)
	row.AddPrefix(img)
	row.AddCSSClass("am-setting")
}

// indented makes row one that belongs to the row above it: no icon, and its
// title lined up with that row's.
func indented(row *adw.ActionRow) {
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetSizeRequest(settingIconSize, -1)
	spacer.SetMarginStart(settingIconStart)
	spacer.SetMarginEnd(settingIconEnd)
	row.AddPrefix(spacer)
}

// blockRow holds something that is not a row — the theme circles, the prompt's
// text — beneath a card's first row, starting where that row's title does.
func blockRow(child gtk.Widgetter) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	// The row is only a frame. Whatever is inside it takes focus; the row
	// itself would be one more stop on the way there.
	row.SetFocusable(false)
	w := gtk.BaseWidget(child)
	w.SetMarginStart(settingIndent)
	w.SetMarginEnd(12)
	w.SetMarginTop(6)
	w.SetMarginBottom(12)
	row.SetChild(child)
	return row
}

// textField is a text setting's entry. It applies itself on Enter and when focus
// leaves it; apply stores the trimmed text and returns what was stored, which
// the entry then shows — an emptied name comes back as the default, rather than
// staying blank while the default is what is used. Anything still pending when
// the page is left is applied then; see flush.
func (v *settingsView) textField(value string, apply func(text string) string) *gtk.Entry {
	e := gtk.NewEntry()
	e.SetText(value)
	e.SetWidthChars(settingTextWidth)
	e.SetVAlign(gtk.AlignCenter)
	applied := strings.TrimSpace(value)
	commit := func() {
		text := strings.TrimSpace(e.Text())
		if text == applied {
			return
		}
		applied = apply(text)
		if e.Text() != applied {
			e.SetText(applied)
		}
	}
	e.ConnectActivate(commit)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(commit)
	e.AddController(focus)
	v.flush = append(v.flush, commit)
	return e
}

// save writes the settings and tells the application they changed.
func save(s *config.Settings, h SettingsHooks) {
	_ = config.Save(*s)
	fire(h.OnChange)
}

// --- Appearance ---------------------------------------------------------------

// themeCard is the colour theme: one circle per theme, split between the window
// background and the accent, under a row whose button goes back to following
// the desktop.
//
// Circles rather than a dropdown because the thing being chosen is a colour, and a
// list of names makes you pick one to find out what it looks like. Split circles
// rather than single ones because a theme is two decisions — what the window is and
// what stands out against it — and either one alone is a misleading preview.
//
// There is no circle for following the desktop. It is the state Atlas starts in
// and it is not a palette, so it is the button at the end of the row, where Task
// Manager puts its own "Use system setting", and it stays pressed while it is the
// choice.
func themeCard(s *config.Settings, h SettingsHooks) *gtk.ListBox {
	head := adw.NewActionRow()
	head.SetTitle("App theme")
	withIcon(head, "atlas-theme-symbolic")

	system := gtk.NewToggleButtonWithLabel("Use system setting")
	system.SetVAlign(gtk.AlignCenter)
	system.AddCSSClass("am-choice")
	head.AddSuffix(system)

	// The circles in two halves, side by side where they fit and one above the
	// other where they do not: a line of ten, or two of five. Wrapping the ten
	// one by one left whatever did not fit alone on a second line — Contrast by
	// itself under the other nine, at a common window width.
	circles := gtk.NewFlowBox()
	circles.SetSelectionMode(gtk.SelectionNone)
	circles.SetActivateOnSingleClick(false)
	circles.SetMaxChildrenPerLine(2)
	circles.SetMinChildrenPerLine(1)
	circles.SetHomogeneous(true)
	circles.SetColumnSpacing(18)
	circles.SetRowSpacing(12)
	circles.SetHAlign(gtk.AlignStart)
	perHalf := (len(theme.Themes) + 1) / 2
	var halves [2]*gtk.Box
	for i := range halves {
		halves[i] = gtk.NewBox(gtk.OrientationHorizontal, 18)
		halves[i].SetHomogeneous(true)
		circles.Append(halves[i])
		// The FlowBox wraps each half in a child of its own that takes keyboard
		// focus, which would put a stop that does nothing in front of the
		// circles. Only the circles should take it.
		if child := circles.ChildAtIndex(i); child != nil {
			child.SetFocusable(false)
		}
	}

	// Every button is held so that choosing one can clear the others. A GtkCheckButton
	// group would do that itself, but its indicator cannot be styled into a disc.
	var buttons []*gtk.ToggleButton

	// sync marks the chosen one and leaves the rest clear. The guard is for the
	// notify that setting Active fires: without it, clearing the others would
	// re-enter this through their own handlers.
	syncing := false
	sync := func() {
		syncing = true
		for i, b := range buttons {
			b.SetActive(theme.Themes[i].ID == s.Theme)
		}
		following := theme.IsFollowing(s.Theme)
		system.SetActive(following)
		syncing = false
		if t, ok := theme.ByID(s.Theme); ok && !following {
			head.SetSubtitle(t.Name + " — " + t.Summary)
		} else {
			head.SetSubtitle("Follows the desktop's light and dark setting")
		}
	}

	for _, t := range theme.Themes {
		t := t

		swatch := gtk.NewToggleButton()
		// Centred, not filled. A button fills its cell by default, and the cell
		// is as wide as the name under it — so every theme with a name longer
		// than the circle ("Ember", "Dracula", "Solarized") was drawn as an oval,
		// and the ring round the chosen one with it.
		swatch.SetHAlign(gtk.AlignCenter)
		swatch.SetVAlign(gtk.AlignCenter)
		swatch.AddCSSClass("am-swatch")
		swatch.AddCSSClass(theme.SwatchClass(t.ID))
		swatch.SetTooltipText(t.Name + " — " + t.Summary)
		// The button has no label, so without this a screen reader would announce
		// an unnamed toggle ten times over.
		swatch.SetName(t.Name)

		name := gtk.NewLabel(t.Name)
		name.AddCSSClass("caption")

		cell := gtk.NewBox(gtk.OrientationVertical, 6)
		cell.Append(swatch)
		cell.Append(name)
		halves[len(buttons)/perHalf].Append(cell)

		swatch.ConnectToggled(func() {
			if syncing {
				return
			}
			if !swatch.Active() {
				// Clicking the chosen one again would otherwise turn the theme
				// off and leave nothing selected. It stays chosen, and nothing
				// has changed to save.
				syncing = true
				swatch.SetActive(true)
				syncing = false
				return
			}
			s.Theme = t.ID
			sync()
			save(s, h)
		})
		buttons = append(buttons, swatch)
	}

	system.ConnectToggled(func() {
		if syncing {
			return
		}
		if !system.Active() {
			// The same as a circle: pressing it again leaves it chosen.
			syncing = true
			system.SetActive(true)
			syncing = false
			return
		}
		s.Theme = theme.Follow
		sync()
		save(s, h)
	})

	sync()
	return settingsCard(head, blockRow(circles))
}

// transparencyCard is the transparency level with, beneath it, how the sidebar
// and title bar take it. The second row only means something while the window is
// see-through, so it is greyed the rest of the time, with the reason.
func transparencyCard(s *config.Settings, h SettingsHooks) *gtk.ListBox {
	level := transparencyRow(s, h)
	frame := frameRow(s, h)
	available, _ := TransparencyAvailable()
	sync := func() {
		on := available && config.NormalizeTransparency(s.WindowTransparency) != config.TransparencyOff
		frame.SetSensitive(on)
		if on {
			frame.SetSubtitle("Whether the desktop shows through them as well as the page")
		} else {
			frame.SetSubtitle("Takes effect while the window is see-through")
		}
	}
	level.NotifyProperty("selected", sync)
	sync()
	return settingsCard(level, frame)
}

// frameRow is how the sidebar and title bar look under transparency.
func frameRow(s *config.Settings, h SettingsHooks) *adw.ComboRow {
	labels := make([]string, len(frameStyles))
	selected := 0
	current := config.NormalizeFrame(s.FrameStyle)
	for i, f := range frameStyles {
		labels[i] = f.Label
		if f.Value == current {
			selected = i
		}
	}
	row := adw.NewComboRow()
	row.SetTitle("Sidebar and title bar")
	indented(&row.ActionRow)
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(selected))
	row.NotifyProperty("selected", func() {
		idx := int(row.Selected())
		if idx < 0 || idx >= len(frameStyles) || frameStyles[idx].Value == s.FrameStyle {
			return
		}
		s.FrameStyle = frameStyles[idx].Value
		save(s, h)
	})
	return row
}

// transparencyRow is the window transparency dropdown. Where transparency cannot
// work — see TransparencyAvailable — the row stays, greyed, with the reason in
// place of its description, so that the option is not simply missing with nothing
// to say why.
func transparencyRow(s *config.Settings, h SettingsHooks) *adw.ComboRow {
	labels := make([]string, len(transparencyLevels))
	selected := 0
	current := config.NormalizeTransparency(s.WindowTransparency)
	for i, l := range transparencyLevels {
		labels[i] = l.Label
		if l.Value == current {
			selected = i
		}
	}

	row := adw.NewComboRow()
	row.SetTitle("Window transparency")
	row.SetSubtitle("How much of the desktop shows through. Text and charts stay solid")
	withIcon(&row.ActionRow, "atlas-opacity-symbolic")
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(selected))
	if ok, why := TransparencyAvailable(); !ok {
		row.SetSubtitle(why)
		row.SetSensitive(false)
	}
	row.NotifyProperty("selected", func() {
		idx := int(row.Selected())
		if idx < 0 || idx >= len(transparencyLevels) || transparencyLevels[idx].Value == s.WindowTransparency {
			return
		}
		s.WindowTransparency = transparencyLevels[idx].Value
		save(s, h)
	})
	return row
}

// fontRow is text rendering. Separate from the renderer: this one decides
// whether GTK may skip hinting, which is only obvious on a 1x display.
func fontRow(s *config.Settings, h SettingsHooks) *adw.ComboRow {
	labels := make([]string, len(gfx.TextModes))
	selected := 0
	current := gfx.NormalizeText(s.TextRendering)
	for i, m := range gfx.TextModes {
		labels[i] = m.Label
		if m.Value == current {
			selected = i
		}
	}
	row := adw.NewComboRow()
	row.SetTitle("Font rendering")
	row.SetSubtitle(gfx.TextModes[selected].Detail)
	withIcon(&row.ActionRow, "atlas-text-symbolic")
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(selected))
	row.NotifyProperty("selected", func() {
		idx := int(row.Selected())
		if idx < 0 || idx >= len(gfx.TextModes) || gfx.TextModes[idx].Value == s.TextRendering {
			return
		}
		s.TextRendering = gfx.TextModes[idx].Value
		row.SetSubtitle(gfx.TextModes[idx].Detail + " Restart Atlas to apply.")
		save(s, h)
	})
	return row
}

// --- Performance --------------------------------------------------------------

// refreshRow is the sampling interval. Slower is cheaper, and stretches the
// graphs: they hold 60 samples whatever the rate.
func refreshRow(s *config.Settings, h SettingsHooks) *adw.ComboRow {
	labels := make([]string, len(config.RefreshChoices))
	selected := 0
	current := config.NormalizeRefresh(s.RefreshSeconds)
	for i, sec := range config.RefreshChoices {
		labels[i] = refreshLabel(sec)
		if sec == current {
			selected = i
		}
	}
	row := adw.NewComboRow()
	row.SetTitle("Refresh interval")
	row.SetSubtitle(refreshDetail(current))
	withIcon(&row.ActionRow, "atlas-timer-symbolic")
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(selected))
	row.NotifyProperty("selected", func() {
		idx := int(row.Selected())
		if idx < 0 || idx >= len(config.RefreshChoices) {
			return
		}
		s.RefreshSeconds = config.RefreshChoices[idx]
		row.SetSubtitle(refreshDetail(s.RefreshSeconds))
		save(s, h)
	})
	return row
}

// renderRow is the rendering mode. It is the single biggest influence on how
// much memory Atlas uses, so it is a setting rather than an environment
// variable; each mode's description says what it costs.
func renderRow(s *config.Settings, h SettingsHooks) *adw.ComboRow {
	labels := make([]string, len(gfx.Modes))
	selected := 0
	for i, m := range gfx.Modes {
		labels[i] = m.Label
		if m.Value == s.RenderMode {
			selected = i
		}
	}
	row := adw.NewComboRow()
	row.SetTitle("Rendering")
	row.SetSubtitle(gfx.Modes[selected].Detail)
	withIcon(&row.ActionRow, "atlas-gpu-symbolic")
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(selected))
	row.NotifyProperty("selected", func() {
		idx := int(row.Selected())
		if idx < 0 || idx >= len(gfx.Modes) || gfx.Modes[idx].Value == s.RenderMode {
			return
		}
		s.RenderMode = gfx.Modes[idx].Value
		row.SetSubtitle(gfx.Modes[idx].Detail + " Restart Atlas to apply.")
		save(s, h)
	})
	return row
}

// memoryRow is Atlas's own memory, with the button that hands back what it is
// holding and not using.
func memoryRow() *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle("Memory used by Atlas")
	row.SetSubtitle(selfMemory())
	row.SetSubtitleSelectable(true)
	withIcon(row, "atlas-memory-symbolic")
	release := gtk.NewButtonWithLabel("Release idle memory")
	release.SetVAlign(gtk.AlignCenter)
	release.ConnectClicked(func() {
		sysmem.Release()
		row.SetSubtitle(selfMemory())
	})
	row.AddSuffix(release)
	return row
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

// selfMemory reports this process's resident set — the same figure a task manager
// shows for Atlas. Where it comes from is per-platform; see internal/sysmem.
func selfMemory() string {
	n, ok := sysmem.Resident()
	if !ok {
		return "unavailable"
	}
	return format.Bytes(n) + " resident"
}

// --- Assistant ----------------------------------------------------------------

// assistantCard is the switch that turns the assistant on, with what it needs
// beneath it: its name, the model, and where Ollama is.
func (v *settingsView) assistantCard(s *config.Settings, h SettingsHooks) *gtk.ListBox {
	enable := adw.NewSwitchRow()
	enable.SetTitle("AI assistant")
	enable.SetSubtitle("A local assistant powered by Ollama, running entirely on this machine")
	withIcon(&enable.ActionRow, "atlas-assistant-symbolic")
	enable.SetActive(s.AIEnabled)
	enable.NotifyProperty("active", func() {
		if s.AIEnabled == enable.Active() {
			return
		}
		s.AIEnabled = enable.Active()
		save(s, h)
	})

	name := v.textRow("Name", "What the Assistant page calls it", s.AssistantTitle, func(text string) string {
		s.AssistantTitle = nonEmpty(text, config.Defaults().AssistantTitle)
		save(s, h)
		return s.AssistantTitle
	})

	// The link goes before the field rather than after it, so that the three
	// fields' right-hand edges still line up down the card.
	browse := gtk.NewLinkButtonWithLabel(modelsURL, "Browse models")
	browse.SetVAlign(gtk.AlignCenter)
	browse.SetTooltipText("The models Ollama can pull, on ollama.com/library")
	model := v.textRow("Model", "Any model Ollama has pulled", s.Model, func(text string) string {
		s.Model = text
		save(s, h)
		return s.Model
	}, browse)

	// A warning that appears only when the endpoint is off this machine. The
	// assistant's system prompt carries the hostname, the username, the running
	// processes and the enabled services, and the client speaks plaintext HTTP
	// only — https is refused — so a remote Ollama means all of that crosses the
	// network in the clear. Local is the default and the intended use; this just
	// makes the other case visible instead of silent.
	egress := gtk.NewLabel("This Ollama server is not on this machine. Each question sends a snapshot of " +
		"this system — hostname, user, running processes and services — to it over plain HTTP, " +
		"unencrypted.")
	egress.SetWrap(true)
	egress.SetXAlign(0)
	egress.AddCSSClass("am-warning")
	warning := blockRow(egress)
	warning.SetVisible(!ai.IsLocal(s.OllamaURL))

	url := v.textRow("Ollama URL", "Where Ollama is listening", s.OllamaURL, func(text string) string {
		s.OllamaURL = text
		warning.SetVisible(!ai.IsLocal(text))
		save(s, h)
		return s.OllamaURL
	})

	return settingsCard(enable, name, model, url, warning)
}

// textRow is a text setting beneath a card's first row: its title and
// description, and the field at the end, with anything in before just ahead of it.
func (v *settingsView) textRow(title, desc, value string, apply func(text string) string,
	before ...gtk.Widgetter) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(title)
	row.SetSubtitle(desc)
	indented(row)
	for _, w := range before {
		row.AddSuffix(w)
	}
	field := v.textField(value, apply)
	row.AddSuffix(field)
	// A click anywhere on the row puts the cursor in the field.
	row.SetActivatableWidget(field)
	return row
}

// promptCard is the system prompt, in full, with the button that puts back the
// one Atlas ships with.
func (v *settingsView) promptCard(s *config.Settings, h SettingsHooks) *gtk.ListBox {
	head := adw.NewActionRow()
	head.SetTitle("System prompt")
	head.SetSubtitle("How the assistant behaves. The live system data is always attached for you")
	withIcon(head, "atlas-document-symbolic")

	text := gtk.NewTextView()
	text.SetWrapMode(gtk.WrapWordChar)
	text.SetLeftMargin(8)
	text.SetRightMargin(8)
	text.SetTopMargin(8)
	text.SetBottomMargin(8)
	text.Buffer().SetText(s.SystemPrompt)

	applied := strings.TrimSpace(s.SystemPrompt)
	commit := func() {
		current := strings.TrimSpace(textViewText(text))
		if current == "" {
			current = config.DefaultSystemPrompt
			text.Buffer().SetText(current)
		}
		if current == applied {
			return
		}
		s.SystemPrompt = current
		applied = current
		save(s, h)
	}
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(commit)
	text.AddController(focus)
	v.flush = append(v.flush, commit)

	reset := gtk.NewButtonWithLabel("Reset to default")
	reset.SetVAlign(gtk.AlignCenter)
	reset.ConnectClicked(func() {
		text.Buffer().SetText(config.DefaultSystemPrompt)
		commit()
	})
	head.AddSuffix(reset)

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(text)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetMinContentHeight(settingPromptHeight)
	scroll.AddCSSClass("am-chat")

	return settingsCard(head, blockRow(scroll))
}

// quickPromptsCard is the three entries in the assistant's list of ready-made
// questions, each a name and what it asks, side by side.
func (v *settingsView) quickPromptsCard(s *config.Settings, h SettingsHooks) *gtk.ListBox {
	head := adw.NewActionRow()
	head.SetTitle("Quick prompts")
	head.SetSubtitle("The questions in the list button beside the assistant's message box")
	withIcon(head, "atlas-prompts-symbolic")

	rows := []gtk.Widgetter{head}
	def := config.DefaultQuickPrompts()
	for i := range s.QuickPrompts {
		i := i
		name := v.textField(s.QuickPrompts[i].Name, func(text string) string {
			s.QuickPrompts[i].Name = nonEmpty(text, def[i].Name)
			save(s, h)
			return s.QuickPrompts[i].Name
		})
		name.SetWidthChars(16)
		name.SetPlaceholderText("Name")
		name.SetTooltipText("The name it has in the list")

		prompt := v.textField(s.QuickPrompts[i].Prompt, func(text string) string {
			s.QuickPrompts[i].Prompt = nonEmpty(text, def[i].Prompt)
			save(s, h)
			return s.QuickPrompts[i].Prompt
		})
		prompt.SetHExpand(true)
		prompt.SetPlaceholderText("What it asks")
		prompt.SetTooltipText("What it asks the assistant")

		pair := gtk.NewBox(gtk.OrientationHorizontal, 8)
		pair.Append(name)
		pair.Append(prompt)
		row := blockRow(pair)
		gtk.BaseWidget(pair).SetMarginTop(8)
		gtk.BaseWidget(pair).SetMarginBottom(8)
		rows = append(rows, row)
	}
	return settingsCard(rows...)
}

// --- Updates ------------------------------------------------------------------

// updateCards are the version with its Update button, the channel it follows,
// whether it checks by itself, and where this copy lives.
func updateCards(s *config.Settings, h SettingsHooks) []gtk.Widgetter {
	version := nonEmpty(h.Version, "unknown")
	onChannel := func() string { return "On the " + channelName(s.UpdateChannel) + " channel" }

	status := adw.NewActionRow()
	status.SetTitle("Atlas " + version)
	status.SetSubtitle(onChannel())
	status.SetSubtitleSelectable(true)
	withIcon(status, "atlas-update-symbolic")

	update := gtk.NewButtonWithLabel("Update")
	if h.ManagedBy != "" {
		// An install pacman owns is not updated from here, and the button should
		// not imply otherwise: it checks, and then hands over the one command
		// that does the work.
		update.SetLabel("Check for updates")
	}
	update.SetVAlign(gtk.AlignCenter)
	update.AddCSSClass("suggested-action")
	update.ConnectClicked(func() {
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
	status.AddSuffix(update)

	// The channel is which Atlas this copy is: moving to another one is
	// choosing it here and pressing Update, and the description says in one
	// sentence what the chosen one is.
	current := config.NormalizeChannel(s.UpdateChannel)
	labels := make([]string, len(updateChannels))
	selected := 0
	for i, c := range updateChannels {
		labels[i] = c.Label
		if c.Value == current {
			selected = i
		}
	}
	channel := adw.NewComboRow()
	channel.SetTitle("Update channel")
	channel.SetSubtitle(updateChannels[selected].Detail)
	withIcon(&channel.ActionRow, "atlas-branch-symbolic")
	channel.SetModel(gtk.NewStringList(labels))
	channel.SetSelected(uint(selected))
	channel.NotifyProperty("selected", func() {
		idx := int(channel.Selected())
		if idx < 0 || idx >= len(updateChannels) || updateChannels[idx].Value == s.UpdateChannel {
			return
		}
		c := updateChannels[idx]
		s.UpdateChannel = c.Value
		channel.SetSubtitle(c.Detail)
		if c.Value == current {
			status.SetSubtitle(onChannel())
		} else {
			status.SetSubtitle("Press Update to switch to " + c.Label)
		}
		save(s, h)
	})

	// Checking on launch is on by default: an update nobody hears about is not
	// much use. It is one switch to stop, and stopping it leaves the Update
	// button above working exactly as before.
	autoCheck := adw.NewSwitchRow()
	autoCheck.SetTitle("Check for updates on launch")
	autoCheck.SetSubtitle("Asks GitHub once after Atlas opens, and only speaks up if there is something newer")
	withIcon(&autoCheck.ActionRow, "atlas-startup-symbolic")
	autoCheck.SetActive(s.UpdateCheck)
	autoCheck.NotifyProperty("active", func() {
		if s.UpdateCheck == autoCheck.Active() {
			return
		}
		s.UpdateCheck = autoCheck.Active()
		save(s, h)
	})

	loc := adw.NewActionRow()
	loc.SetTitle("Location")
	loc.SetSubtitle(nonEmpty(h.Location, "unknown"))
	loc.SetSubtitleSelectable(true)
	withIcon(loc, "atlas-folder-symbolic")

	return []gtk.Widgetter{
		settingsCard(status),
		settingsCard(channel),
		settingsCard(autoCheck),
		settingsCard(loc),
	}
}

// updateChannels are the channels Settings offers, each with what it is in one
// sentence.
var updateChannels = []struct{ Value, Label, Detail string }{
	{config.ChannelRelease, "Release", "The full Atlas with the assistant, in its stable version"},
	{config.ChannelBeta, "Beta", "The full Atlas with the newest features and fixes, before they reach Release"},
	{config.ChannelMinimal, "Minimal", "Atlas without the AI assistant: lighter, with nothing to set up"},
}

func channelName(ch string) string {
	for _, c := range updateChannels {
		if c.Value == ch {
			return c.Label
		}
	}
	return "Release"
}

// --- shared helpers ---------------------------------------------------------

func fire(f func()) {
	if f != nil {
		f()
	}
}

func textViewText(tv *gtk.TextView) string {
	buf := tv.Buffer()
	return buf.Text(buf.StartIter(), buf.EndIter(), false)
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
