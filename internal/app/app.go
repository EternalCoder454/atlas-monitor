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
	col      *stats.Collector
	settings config.Settings
	aiClient *ai.Client
	content  *ui.Window
}

// New creates the application. css is the embedded stylesheet contents.
func New(css string) *App {
	a := &App{
		app: adw.NewApplication(AppID, gio.ApplicationFlagsNone),
		css: css,
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
	header.SetTitleWidget(adw.NewWindowTitle("Atlas Monitor", ""))

	gear := gtk.NewButtonFromIconName("emblem-system-symbolic")
	gear.SetTooltipText("Settings")
	gear.ConnectClicked(func() {
		ui.ShowSettings(win, &a.settings, a.onSettingsChanged, a.onRestart)
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
			ui.ShowSettings(win, &a.settings, a.onSettingsChanged, a.onRestart)
			return false
		})
	}
}

// onSettingsChanged applies saved settings to the running app.
func (a *App) onSettingsChanged() {
	a.aiClient.SetConfig(a.settings.OllamaURL, a.settings.Model)
	a.content.SetAIEnabled(a.settings.AIEnabled)
}

// onRestart relaunches a fresh instance and quits this one. If the source
// checkout is known (recorded by `make install`), it first rebuilds and
// reinstalls from there. The helper is detached with setsid so it survives this
// process exiting; the sleep lets the single-instance lock release before the
// new instance registers.
func (a *App) onRestart() {
	rebuild := ""
	if src := sourceDir(); src != "" {
		script := filepath.Join(src, "scripts", "auto-reinstall.sh")
		if _, err := os.Stat(script); err == nil {
			rebuild = fmt.Sprintf("bash %q >/tmp/atlas-monitor-reinstall.log 2>&1; ", script)
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
