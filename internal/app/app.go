// Package app wires together the AdwApplication, the main window, and the
// fixed two-pane layout (sidebar + content area).
package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/gpu"
	"atlas-monitor/internal/stats"
	"atlas-monitor/internal/ui"
)

// AppID is the D-Bus / desktop application identifier.
const AppID = "com.atlas.Monitor"

// App owns the AdwApplication and the top-level wiring.
type App struct {
	app      *adw.Application
	css      string
	version  string
	col      *stats.Collector
	settings config.Settings
	aiClient *ai.Client
	content  *ui.Window
}

// New creates the application. css is the embedded stylesheet contents and
// version is the embedded VERSION string.
func New(css, version string) *App {
	a := &App{
		app:     adw.NewApplication(AppID, gio.ApplicationFlagsNone),
		css:     css,
		version: version,
	}
	a.app.ConnectActivate(a.activate)
	a.app.ConnectShutdown(func() {
		if a.col != nil {
			a.col.Stop()
		}
	})
	return a
}

// Run starts the GTK main loop. Returns the process exit code.
func (a *App) Run(args []string) int {
	return a.app.Run(args)
}

func (a *App) activate() {
	a.loadCSS()

	a.settings = config.Load()
	a.aiClient = ai.New(a.settings.OllamaURL, a.settings.Model)

	a.col = stats.New(gpu.NewReader())
	a.col.Start()

	a.content = ui.NewWindow(a.col, a.aiClient, &a.settings)
	root := a.content.Build()

	win := adw.NewApplicationWindow(&a.app.Application)
	win.SetTitle("Atlas Monitor")
	win.SetDefaultSize(1100, 720)
	win.SetSizeRequest(900, 600)

	header := adw.NewHeaderBar()
	subtitle := ""
	if a.version != "" {
		subtitle = "v" + a.version
	}
	header.SetTitleWidget(adw.NewWindowTitle("Atlas Monitor", subtitle))

	gear := gtk.NewButtonFromIconName("emblem-system-symbolic")
	gear.SetTooltipText("Settings")
	gear.ConnectClicked(func() {
		ui.ShowSettings(win, &a.settings, a.settingsHooks())
	})
	header.PackEnd(gear)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(header)
	toolbar.SetContent(root)
	win.SetContent(toolbar)

	// Pause/resume all collection based on window visibility (0% CPU hidden).
	win.ConnectMap(func() { a.content.SetVisible(true) })
	win.ConnectUnmap(func() { a.content.SetVisible(false) })

	a.content.StartRefresh()
	win.Present()

	// Dev aid: ATLAS_OPEN_SETTINGS=1 opens the settings dialog at startup.
	if os.Getenv("ATLAS_OPEN_SETTINGS") == "1" {
		glib.TimeoutAdd(400, func() bool {
			ui.ShowSettings(win, &a.settings, a.settingsHooks())
			return false
		})
	}
}

// onSettingsChanged applies saved settings to the running app.
func (a *App) onSettingsChanged() {
	a.aiClient.SetConfig(a.settings.OllamaURL, a.settings.Model)
	a.content.SetAIEnabled(a.settings.AIEnabled)
	a.content.RefreshQuickPrompts()
}

// onRestart relaunches a fresh instance and quits this one. If the source
// checkout is known (recorded by `make install`), it first runs update.sh to
// pull the selected channel (Release=main / Beta=beta) from GitHub and reinstall.
// The helper is detached with setsid so it survives this process exiting; the
// sleep lets the single-instance lock release before the new instance registers.
func (a *App) onRestart() {
	rebuild := ""
	if src := sourceDir(); src != "" {
		script := filepath.Join(src, "scripts", "update.sh")
		if _, err := os.Stat(script); err == nil {
			branch := a.settings.UpdateChannel
			if branch != "main" && branch != "beta" {
				branch = "main"
			}
			rebuild = fmt.Sprintf("bash %q %q; ", script, branch)
		}
	}
	helper := rebuild + fmt.Sprintf("sleep 1; gtk-launch %s", AppID)
	_ = exec.Command("setsid", "bash", "-c", helper).Start()
	a.app.Quit()
}

// sourceDir returns the source checkout recorded by `make install`
// (in $XDG_DATA_HOME/atlas-monitor/source), or "" if it is unknown.
func sourceDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	b, err := os.ReadFile(filepath.Join(base, "atlas-monitor", "source"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// settingsHooks bundles the callbacks the Settings dialog needs.
func (a *App) settingsHooks() ui.SettingsHooks {
	return ui.SettingsHooks{
		OnChange:    a.onSettingsChanged,
		ApplyUpdate: a.onRestart,
		CheckUpdate: a.checkUpdate,
		Version:     a.version,
		Location:    a.location(),
	}
}

// location is the source checkout (where updates are pulled), falling back to
// the running binary's path.
func (a *App) location() string {
	if src := sourceDir(); src != "" {
		return src
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return ""
}

// checkUpdate fetches origin and reports whether the given channel has commits
// the local checkout does not. It changes nothing on disk, so the Settings
// "Update" action can skip the rebuild/restart when already up to date.
func (a *App) checkUpdate(channel string) (available bool, info string, err error) {
	src := sourceDir()
	if src == "" {
		return false, "", fmt.Errorf("source location unknown — install with `make install`")
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", src}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("rev-parse", "--git-dir"); err != nil {
		return false, "", fmt.Errorf("source is not a git checkout")
	}
	if _, err := git("fetch", "--quiet", "origin", channel); err != nil {
		return false, "", fmt.Errorf("couldn't reach GitHub")
	}
	local, _ := git("rev-parse", "--short", "HEAD")
	remote, _ := git("rev-parse", "--short", "origin/"+channel)
	name := "Release"
	if channel == "beta" {
		name = "Beta"
	}
	// Up to date when origin/<channel> is already contained in HEAD.
	if exec.Command("git", "-C", src, "merge-base", "--is-ancestor", "origin/"+channel, "HEAD").Run() == nil {
		return false, fmt.Sprintf("Up to date on %s (%s)", name, local), nil
	}
	return true, fmt.Sprintf("%s update available: %s → %s", name, local, remote), nil
}

// loadCSS installs the embedded stylesheet for the default display.
func (a *App) loadCSS() {
	if a.css == "" {
		return
	}
	provider := gtk.NewCSSProvider()
	provider.LoadFromString(a.css)
	if display := gdk.DisplayGetDefault(); display != nil {
		gtk.StyleContextAddProviderForDisplay(
			display, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}
}
