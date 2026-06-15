package ui

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/config"
)

// ShowSettings presents the settings dialog over parent. onChange runs whenever
// a setting is saved; onRestart rebuilds-and-restarts the app.
func ShowSettings(parent gtk.Widgetter, s *config.Settings, onChange, onRestart func(), version string) {
	dlg := adw.NewPreferencesDialog()
	dlg.SetTitle("Settings")
	page := adw.NewPreferencesPage()

	// --- AI assistant ---
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
		onChange()
	})
	aiGroup.Add(enable)

	name := newApplyRow("Assistant name", s.AssistantTitle, func(text string) {
		s.AssistantTitle = nonEmpty(text, config.Defaults().AssistantTitle)
		_ = config.Save(*s)
		onChange()
	})
	aiGroup.Add(name)

	model := newApplyRow("Model", s.Model, func(text string) {
		s.Model = text
		_ = config.Save(*s)
		onChange()
	})
	aiGroup.Add(model)

	url := newApplyRow("Ollama URL", s.OllamaURL, func(text string) {
		s.OllamaURL = text
		_ = config.Save(*s)
		onChange()
	})
	aiGroup.Add(url)
	page.Add(aiGroup)

	// --- System prompt ---
	promptGroup := adw.NewPreferencesGroup()
	promptGroup.SetTitle("System prompt")
	promptGroup.SetDescription("Instructions sent to the model before the live system data (which is always appended " +
		"automatically). Edit this to change how the assistant behaves.")

	promptView := gtk.NewTextView()
	promptView.SetWrapMode(gtk.WrapWordChar)
	promptView.SetLeftMargin(8)
	promptView.SetRightMargin(8)
	promptView.SetTopMargin(8)
	promptView.SetBottomMargin(8)
	promptView.Buffer().SetText(s.SystemPrompt)

	promptScroll := gtk.NewScrolledWindow()
	promptScroll.SetChild(promptView)
	promptScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	promptScroll.SetMinContentHeight(150)
	promptScroll.AddCSSClass("am-chat")
	promptGroup.Add(promptScroll)

	reset := adw.NewButtonRow()
	reset.SetTitle("Reset prompt to default")
	reset.ConnectActivated(func() { promptView.Buffer().SetText(config.DefaultSystemPrompt) })
	promptGroup.Add(reset)
	page.Add(promptGroup)

	// --- Application ---
	appGroup := adw.NewPreferencesGroup()
	appGroup.SetTitle("Application")
	appGroup.SetDescription("Download and install the latest version from GitHub on the selected channel, " +
		"then restart. Your own local edits, if any, are never overwritten.")

	if version == "" {
		version = "unknown"
	}
	versionRow := adw.NewActionRow()
	versionRow.SetTitle("Version")
	versionRow.SetSubtitle(version)
	appGroup.Add(versionRow)

	// Update channel: index 0 = main (Release), 1 = beta (newest).
	channelIDs := []string{"main", "beta"}
	channel := adw.NewComboRow()
	channel.SetTitle("Update channel")
	channel.SetSubtitle("Release is the stable main branch; Beta has the newest features and fixes")
	channel.SetModel(gtk.NewStringList([]string{"Release (main)", "Beta (beta)"}))
	if s.UpdateChannel == "beta" {
		channel.SetSelected(1)
	} else {
		channel.SetSelected(0)
	}
	channel.NotifyProperty("selected", func() {
		if idx := int(channel.Selected()); idx >= 0 && idx < len(channelIDs) {
			s.UpdateChannel = channelIDs[idx]
			_ = config.Save(*s)
			onChange()
		}
	})
	appGroup.Add(channel)

	update := adw.NewButtonRow()
	update.SetTitle("Update and restart")
	update.AddCSSClass("suggested-action")
	update.ConnectActivated(func() {
		if onRestart != nil {
			onRestart()
		}
	})
	appGroup.Add(update)
	page.Add(appGroup)

	dlg.Add(page)

	// Persist everything (including the prompt) when the dialog closes.
	dlg.ConnectClosed(func() {
		s.AssistantTitle = nonEmpty(strings.TrimSpace(name.Text()), config.Defaults().AssistantTitle)
		s.Model = strings.TrimSpace(model.Text())
		s.OllamaURL = strings.TrimSpace(url.Text())
		s.SystemPrompt = nonEmpty(strings.TrimSpace(textViewText(promptView)), config.DefaultSystemPrompt)
		_ = config.Save(*s)
		onChange()
	})

	dlg.Present(parent)
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
