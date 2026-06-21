package ui

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/config"
)

const modelsURL = "https://ollama.com/library"

// SettingsHooks are the app-level callbacks the Settings dialog needs.
type SettingsHooks struct {
	OnChange    func()                                                        // a setting was saved
	ApplyUpdate func()                                                        // pull the channel, reinstall, relaunch
	CheckUpdate func(channel string) (available bool, info string, err error) // git fetch + compare (no restart)
	Version     string
	Location    string // install / source location, for display
}

// ShowSettings presents the settings dialog over parent: a sidebar with three
// sections — Model & Prompt, Quick Prompts, and App.
func ShowSettings(parent gtk.Widgetter, s *config.Settings, h SettingsHooks) {
	dlg := adw.NewDialog()
	dlg.SetTitle("Settings")
	dlg.SetContentWidth(840)
	dlg.SetContentHeight(620)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(adw.NewHeaderBar())

	stack := gtk.NewStack()
	stack.SetHExpand(true)
	stack.SetVExpand(true)
	stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	stack.SetTransitionDuration(120)

	mp := newModelPromptPage(s, h)
	qp := newQuickPromptsPage(s, h)
	ap := newAppPage(s, h)
	stack.AddNamed(mp.page, "model")
	stack.AddNamed(qp.page, "prompts")
	stack.AddNamed(ap.page, "app")

	sidebar := gtk.NewListBox()
	sidebar.AddCSSClass("navigation-sidebar")
	sidebar.SetVExpand(true)
	addSettingsRow(sidebar, "Model & Prompt", "model")
	addSettingsRow(sidebar, "Quick Prompts", "prompts")
	addSettingsRow(sidebar, "App", "app")
	sidebar.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row != nil {
			stack.SetVisibleChildName(row.Name())
		}
	})
	sidebar.SelectRow(sidebar.RowAtIndex(0))

	sidebarScroll := gtk.NewScrolledWindow()
	sidebarScroll.SetChild(sidebar)
	sidebarScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sidebarScroll.SetSizeRequest(200, -1)

	hbox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	hbox.Append(sidebarScroll)
	hbox.Append(gtk.NewSeparator(gtk.OrientationVertical))
	hbox.Append(stack)
	toolbar.SetContent(hbox)
	dlg.SetChild(toolbar)

	// Persist apply-rows that weren't explicitly confirmed when the dialog closes.
	dlg.ConnectClosed(func() {
		s.AssistantTitle = nonEmpty(strings.TrimSpace(mp.name.Text()), config.Defaults().AssistantTitle)
		s.Model = strings.TrimSpace(mp.model.Text())
		s.OllamaURL = strings.TrimSpace(mp.url.Text())
		s.SystemPrompt = nonEmpty(strings.TrimSpace(textViewText(mp.prompt)), config.DefaultSystemPrompt)
		def := config.DefaultQuickPrompts()
		for i := range s.QuickPrompts {
			s.QuickPrompts[i].Name = nonEmpty(strings.TrimSpace(qp.names[i].Text()), def[i].Name)
			s.QuickPrompts[i].Prompt = nonEmpty(strings.TrimSpace(qp.prompts[i].Text()), def[i].Prompt)
		}
		_ = config.Save(*s)
		fire(h.OnChange)
	})

	dlg.Present(parent)
}

// addSettingsRow adds a sidebar row whose widget name is the stack page it
// selects.
func addSettingsRow(list *gtk.ListBox, title, page string) {
	row := gtk.NewListBoxRow()
	row.SetName(page)
	lbl := gtk.NewLabel(title)
	lbl.SetXAlign(0)
	lbl.SetMarginTop(10)
	lbl.SetMarginBottom(10)
	lbl.SetMarginStart(12)
	lbl.SetMarginEnd(12)
	row.SetChild(lbl)
	list.Append(row)
}

// --- Model & Prompt ---------------------------------------------------------

type modelPromptPage struct {
	page             *adw.PreferencesPage
	name, model, url *adw.EntryRow
	prompt           *gtk.TextView
}

func newModelPromptPage(s *config.Settings, h SettingsHooks) *modelPromptPage {
	p := &modelPromptPage{page: adw.NewPreferencesPage()}

	aiGroup := adw.NewPreferencesGroup()
	aiGroup.SetTitle("AI Assistant")
	aiGroup.SetDescription("A local assistant powered by Ollama, running entirely on your machine. " +
		"Turn this off to hide the Assistant view and stop all AI activity.")

	enable := adw.NewSwitchRow()
	enable.SetTitle("Enable AI assistant")
	enable.SetActive(s.AIEnabled)
	enable.NotifyProperty("active", func() {
		s.AIEnabled = enable.Active()
		_ = config.Save(*s)
		fire(h.OnChange)
	})
	aiGroup.Add(enable)

	p.name = newApplyRow("Assistant name", s.AssistantTitle, func(text string) {
		s.AssistantTitle = nonEmpty(text, config.Defaults().AssistantTitle)
		_ = config.Save(*s)
		fire(h.OnChange)
	})
	aiGroup.Add(p.name)

	p.model = newApplyRow("Model", s.Model, func(text string) {
		s.Model = text
		_ = config.Save(*s)
		fire(h.OnChange)
	})
	aiGroup.Add(p.model)

	models := adw.NewActionRow()
	models.SetTitle("Browse models")
	models.SetSubtitle("Find a model name to use above")
	link := gtk.NewLinkButtonWithLabel(modelsURL, "ollama.com/library")
	link.SetVAlign(gtk.AlignCenter)
	models.AddSuffix(link)
	aiGroup.Add(models)

	p.url = newApplyRow("Ollama URL", s.OllamaURL, func(text string) {
		s.OllamaURL = text
		_ = config.Save(*s)
		fire(h.OnChange)
	})
	aiGroup.Add(p.url)
	p.page.Add(aiGroup)

	promptGroup := adw.NewPreferencesGroup()
	promptGroup.SetTitle("System prompt")
	promptGroup.SetDescription("Instructions sent to the model before the live system data (which is always " +
		"appended automatically). Edit this to change how the assistant behaves.")

	p.prompt = gtk.NewTextView()
	p.prompt.SetWrapMode(gtk.WrapWordChar)
	p.prompt.SetLeftMargin(8)
	p.prompt.SetRightMargin(8)
	p.prompt.SetTopMargin(8)
	p.prompt.SetBottomMargin(8)
	p.prompt.Buffer().SetText(s.SystemPrompt)

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(p.prompt)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetMinContentHeight(160)
	scroll.AddCSSClass("am-chat")
	promptGroup.Add(scroll)

	reset := adw.NewButtonRow()
	reset.SetTitle("Reset prompt to default")
	reset.ConnectActivated(func() { p.prompt.Buffer().SetText(config.DefaultSystemPrompt) })
	promptGroup.Add(reset)
	p.page.Add(promptGroup)
	return p
}

// --- Quick Prompts ----------------------------------------------------------

type quickPromptsPage struct {
	page    *adw.PreferencesPage
	names   [3]*adw.EntryRow
	prompts [3]*adw.EntryRow
}

func newQuickPromptsPage(s *config.Settings, h SettingsHooks) *quickPromptsPage {
	p := &quickPromptsPage{page: adw.NewPreferencesPage()}
	group := adw.NewPreferencesGroup()
	group.SetTitle("Quick prompts")
	group.SetDescription("The three entries in the assistant's dropdown (the list button next to the message box). " +
		"Rename them and edit what each one asks.")

	def := config.DefaultQuickPrompts()
	for i := range s.QuickPrompts {
		i := i
		exp := adw.NewExpanderRow()
		exp.SetTitle(s.QuickPrompts[i].Name)

		p.names[i] = newApplyRow("Name", s.QuickPrompts[i].Name, func(text string) {
			s.QuickPrompts[i].Name = nonEmpty(text, def[i].Name)
			exp.SetTitle(s.QuickPrompts[i].Name)
			_ = config.Save(*s)
			fire(h.OnChange)
		})
		p.prompts[i] = newApplyRow("Prompt", s.QuickPrompts[i].Prompt, func(text string) {
			s.QuickPrompts[i].Prompt = nonEmpty(text, def[i].Prompt)
			_ = config.Save(*s)
			fire(h.OnChange)
		})
		exp.AddRow(p.names[i])
		exp.AddRow(p.prompts[i])
		group.Add(exp)
	}
	p.page.Add(group)
	return p
}

// --- App --------------------------------------------------------------------

type appPage struct {
	page *adw.PreferencesPage
}

func newAppPage(s *config.Settings, h SettingsHooks) *appPage {
	p := &appPage{page: adw.NewPreferencesPage()}
	version := h.Version
	if version == "" {
		version = "unknown"
	}

	updGroup := adw.NewPreferencesGroup()
	updGroup.SetTitle("Updates")
	updGroup.SetDescription("Atlas updates by pulling the selected channel from GitHub and reinstalling. " +
		"Update checks first and only restarts if there is something newer.")

	channelIDs := []string{"main", "beta"}
	channel := adw.NewComboRow()
	channel.SetTitle("Channel")
	channel.SetSubtitle("Release is the stable main branch; Beta has the newest features and fixes")
	channel.SetModel(gtk.NewStringList([]string{"Release (main)", "Beta (beta)"}))
	if s.UpdateChannel == "beta" {
		channel.SetSelected(1)
	} else {
		channel.SetSelected(0)
	}

	status := adw.NewActionRow()
	status.SetTitle("Status")
	status.SetSubtitle(fmt.Sprintf("On %s · version %s", channelName(s.UpdateChannel), version))
	status.SetSubtitleSelectable(true)

	channel.NotifyProperty("selected", func() {
		if idx := int(channel.Selected()); idx >= 0 && idx < len(channelIDs) {
			s.UpdateChannel = channelIDs[idx]
			_ = config.Save(*s)
			status.SetSubtitle(fmt.Sprintf("On %s · version %s", channelName(s.UpdateChannel), version))
			fire(h.OnChange)
		}
	})
	updGroup.Add(channel)
	updGroup.Add(status)

	update := adw.NewButtonRow()
	update.SetTitle("Update")
	update.SetStartIconName("software-update-available-symbolic")
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
					// Leave the button disabled — ApplyUpdate quits and relaunches.
					status.SetSubtitle(info + " — updating…")
					fire(h.ApplyUpdate)
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
	loc.SetSubtitle(nonEmpty(h.Location, "unknown (install with `make install`)"))
	loc.SetSubtitleSelectable(true)
	aboutGroup.Add(loc)
	p.page.Add(aboutGroup)
	return p
}

func channelName(ch string) string {
	if ch == "beta" {
		return "Beta"
	}
	return "Release"
}

// --- shared helpers ---------------------------------------------------------

func fire(f func()) {
	if f != nil {
		f()
	}
}

// newApplyRow builds an entry row with a visible apply (✓) button. apply runs
// with the trimmed text when the user confirms.
func newApplyRow(title, value string, apply func(text string)) *adw.EntryRow {
	row := adw.NewEntryRow()
	row.SetTitle(title)
	row.SetText(value)
	row.SetShowApplyButton(true)
	row.ConnectApply(func() {
		text := strings.TrimSpace(row.Text())
		apply(text)
		row.SetText(text)
	})
	return row
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
