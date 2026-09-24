// Package app wires together the AdwApplication, the main window, and the
// fixed two-pane layout (sidebar + content area).
package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/gfx"
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
	win      *adw.ApplicationWindow
	// updateOffered keeps the launch prompt to once per run.
	updateOffered bool
}

// New creates the application. css is the embedded stylesheet contents and
// version is the embedded VERSION string.
func New(css, version string) *App {
	settings := config.Load()
	// The GSK renderer and the graphics libraries GDK loads have to be settled
	// before GTK opens the display, so this happens here rather than in
	// activate. It is by far the biggest single influence on how much memory
	// the process ends up using.
	gfx.Apply(settings.RenderMode)

	a := &App{
		app:      adw.NewApplication(AppID, gio.ApplicationFlagsNone),
		css:      css,
		version:  version,
		settings: settings,
	}
	a.app.ConnectActivate(a.activate)
	a.app.ConnectShutdown(func() {
		// Backstop for quits that never reach the window's close-request —
		// "Update and restart", or the session ending. The geometry recorded
		// there is reused; only the open page can still be read here.
		if a.content != nil {
			if v := a.content.ActiveView(); v != "" && v != a.settings.LastView {
				a.settings.LastView = v
				_ = config.Save(a.settings)
			}
		}
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
	// activate fires again every time Atlas is launched while it is already
	// running: GApplication hands the request to the existing process rather
	// than starting a second one. Without this guard each launch built another
	// window and another full set of collectors, overwriting a.col and leaving
	// the previous one running — around fifty held descriptors and six
	// goroutines stranded per launch, for the life of the process.
	if a.win != nil {
		a.win.Present()
		return
	}
	a.loadCSS()
	applyTextRendering(a.settings.TextRendering)
	a.aiClient = ai.New(a.settings.OllamaURL, a.settings.Model)

	a.col = stats.New(gpu.NewReader())
	a.col.Start()

	a.content = ui.NewWindow(a.col, a.aiClient, &a.settings)
	root := a.content.Build()

	win := adw.NewApplicationWindow(&a.app.Application)
	a.win = win
	win.SetTitle("Atlas Monitor")
	win.SetDefaultSize(a.settings.WindowWidth, a.settings.WindowHeight)
	win.SetSizeRequest(config.MinWindowWidth, config.MinWindowHeight)
	if a.settings.WindowMaximized {
		win.Maximize()
	}

	header := adw.NewHeaderBar()
	subtitle := ""
	if a.version != "" {
		subtitle = "v" + a.version
	}
	header.SetTitleWidget(adw.NewWindowTitle("Atlas Monitor", subtitle))

	gear := gtk.NewButtonFromIconName("atlas-settings-symbolic")
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

	// Remember where the window was and what it was showing. close-request is
	// the last point at which the window can still be measured.
	win.ConnectCloseRequest(func() bool {
		a.saveWindowState(win)
		return false // let the close proceed
	})

	a.content.StartRefresh()
	win.Present()

	a.startUpdateCheck(win)

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
	a.content.SetRefreshInterval(
		time.Duration(config.NormalizeRefresh(a.settings.RefreshSeconds)) * time.Second)
}

// saveWindowState records the geometry and the open page so the next launch
// picks up where this one left off. A maximized window keeps the size it had
// before being maximized, which is what the user gets back on un-maximize.
func (a *App) saveWindowState(win *adw.ApplicationWindow) {
	a.settings.WindowMaximized = win.IsMaximized()
	if !a.settings.WindowMaximized {
		if w, h := win.DefaultSize(); w >= config.MinWindowWidth && h >= config.MinWindowHeight {
			a.settings.WindowWidth, a.settings.WindowHeight = w, h
		}
	}
	if v := a.content.ActiveView(); v != "" {
		a.settings.LastView = v
	}
	_ = config.Save(a.settings)
}

// onRestart relaunches a fresh instance and quits this one. If the source
// checkout is known (recorded by `make install`), it first runs update.sh to
// pull the selected channel (Release=main / Beta=beta) from GitHub and reinstall.
// The helper is detached with setsid so it survives this process exiting; the
// sleep lets the single-instance lock release before the new instance registers.
func (a *App) onRestart() {
	// The script path and the branch are passed to bash as arguments rather
	// than pasted into the command it runs. Go's %q is not shell quoting: it
	// leaves $(...) and backticks intact, and inside double quotes the shell
	// still expands them, so a checkout path containing either would have been
	// executed. Positional arguments cannot be re-read as syntax whatever they
	// contain — which also means a perfectly ordinary path with a '$' in it now
	// updates instead of breaking.
	script := ""
	if src := sourceDir(); src != "" {
		p := filepath.Join(src, "scripts", "update.sh")
		if _, err := os.Stat(p); err == nil {
			script = p
		}
	}
	branch := a.settings.UpdateChannel
	if branch != "main" && branch != "beta" {
		branch = "main"
	}

	// $0 names the shell for errors; $1..$3 are the values.
	const helper = `if [ -n "$1" ]; then bash "$1" "$2"; fi; sleep 1; gtk-launch "$3"`
	_ = exec.Command("setsid", "bash", "-c", helper,
		"atlas-monitor-restart", script, branch, AppID).Start()
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

// checkUpdate adapts CheckUpdate to what the Settings dialog wants: a yes/no
// and one line to show. The detail it drops — the version and the changelog —
// is only needed by the prompt at launch.
func (a *App) checkUpdate(channel string) (available bool, info string, err error) {
	u, err := a.CheckUpdate(channel)
	return u.Available, u.Summary, err
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

// applyTextRendering decides whether GTK may trade glyph quality for speed.
//
// GTK's own default, gtk-font-rendering "automatic", lets it skip hinting — and
// when it does, stems land between pixels. On a HiDPI display there are enough
// pixels that nobody notices; at 1x the same text is visibly soft, which is why
// Atlas looked worse on a 1080p monitor than on a 4K one beside it. "manual"
// means GTK rasterises the way the desktop's own font settings ask, which is
// what every other toolkit on the machine already does.
//
// Only the automatic/manual choice is made here. The hint style itself is left
// alone: that is the user's setting, and Atlas has no business overriding it.
//
// Note that gtk-xft-hintstyle and gtk-xft-rgba have no effect while this is
// "automatic" — GTK ignores them — and that GTK4 does no subpixel antialiasing
// at all, so gtk-xft-rgba does nothing either way.
func applyTextRendering(mode string) {
	if gfx.NormalizeText(mode) != gfx.TextSharp {
		return // leave GTK's own default in place
	}
	settings := gtk.SettingsGetDefault()
	if settings == nil {
		return
	}
	settings.SetObjectProperty("gtk-font-rendering", fontRenderingManual)
}

// fontRenderingManual is GTK_FONT_RENDERING_MANUAL. The enum is not bound by
// gotk4, and the property takes the integer value.
const fontRenderingManual = 1

// startUpdateCheck looks for a newer version once, shortly after the window is
// up, and offers it.
//
// The check runs off the UI thread because it ends in a `git fetch`, which talks
// to GitHub and can take seconds or hang on a bad connection; doing that on the
// main loop would freeze the window before the user has seen it. It is delayed a
// little so the app finishes drawing first, and it is silent about everything
// except finding something: no dialog when up to date, none when offline, none
// when Atlas was installed from a tarball and has no checkout to pull.
func (a *App) startUpdateCheck(win *adw.ApplicationWindow) {
	if !a.settings.UpdateCheck || a.updateOffered {
		return
	}
	channel := a.settings.UpdateChannel
	if channel != "main" && channel != "beta" {
		channel = "main"
	}
	glib.TimeoutAdd(1500, func() bool {
		go func() {
			info, err := a.CheckUpdate(channel)
			if err != nil || !info.Available {
				return // nothing to say, and nothing worth interrupting for
			}
			glib.IdleAdd(func() { a.offerUpdate(win, info) })
		}()
		return false
	})
}

// offerUpdate shows what is waiting and lets the user take it or leave it.
func (a *App) offerUpdate(win *adw.ApplicationWindow, info UpdateInfo) {
	if a.updateOffered {
		return // once per run: an update prompt is not something to repeat
	}
	a.updateOffered = true

	heading := "Update Found"
	if info.Version != "" {
		heading = "Update Found — v" + info.Version
	}

	body := "A newer version of Atlas Monitor is available."
	if len(info.Changes) > 0 {
		var b strings.Builder
		b.WriteString("What's new:\n")
		for _, c := range info.Changes {
			b.WriteString("\n•  ")
			b.WriteString(c)
		}
		body = b.String()
	}

	dlg := adw.NewAlertDialog(heading, body)
	dlg.AddResponse("later", "Update Later")
	dlg.AddResponse("now", "Update Now")
	dlg.SetResponseAppearance("now", adw.ResponseSuggested)
	dlg.SetDefaultResponse("now")
	dlg.SetCloseResponse("later")
	dlg.ConnectResponse(func(response string) {
		if response == "now" {
			a.onRestart()
		}
	})
	dlg.Present(win)
}
