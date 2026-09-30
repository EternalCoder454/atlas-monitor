// Package app wires together the AdwApplication, the main window, and the
// fixed two-pane layout (sidebar + content area).
package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/ease"
	"atlas-monitor/internal/gfx"
	"atlas-monitor/internal/gpu"
	"atlas-monitor/internal/stats"
	"atlas-monitor/internal/theme"
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
	// updating is set while an install is building, so a second one cannot start.
	updating bool
	// installed is how this copy got onto the machine, worked out once: see
	// install(). The Settings page checks for updates on a goroutine of its
	// own, so this is settled through a sync.Once rather than a plain flag.
	installed   Install
	installOnce sync.Once

	// themeCSS carries the chosen theme's colour overrides. It is kept so that
	// changing theme can replace its contents instead of stacking another
	// provider on the display for every change.
	themeCSS       *gtk.CSSProvider
	loadedThemeCSS string // what themeCSS holds; see reloadThemeCSS
	// appliedTheme is the setting last put into effect, so applyTheme can tell
	// when there is nothing to do. See applyTheme for why that matters.
	appliedTheme string
	themeApplied bool

	// appliedGlass is the transparency classes last put on the window, space
	// separated, or "" for none, so applyTransparency can tell when there is
	// nothing to do. It has no separate "applied yet" flag: a window that has
	// been given nothing and one that has been told to have nothing are the
	// same window.
	appliedGlass string

	// easer is Energy Saver's automatic half, or nil where the system cannot
	// support it (easeErr says why). It ticks on a goroutine of its own, window
	// or no window: see startEaser.
	easer    *ease.Controller
	easeErr  error
	easeStop chan struct{}
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
			a.content.FlushSettings()
			if v := a.content.LastPage(); v != "" && v != a.settings.LastView {
				a.settings.LastView = v
				_ = config.Save(a.settings)
			}
		}
		if a.col != nil {
			a.col.Stop()
		}
		a.stopEaser()
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
	a.startEaser()
	a.content.SetEnergy(a.easer, a.easeErr)
	a.content.SetSettingsHooks(a.settingsHooks)
	root := a.content.Build()

	win := adw.NewApplicationWindow(&a.app.Application)
	a.win = win
	win.SetTitle("Atlas Monitor")
	// The window's own icon, set rather than left to GTK to guess, so the title
	// bar's icon slot (see brandBox) and the taskbar show it wherever they ask.
	win.SetIconName(AppID)
	// The window does not clip what it holds to its rounded corners; the few
	// surfaces that reach a corner are rounded themselves instead. See "The
	// frame" in assets/style.css.
	//
	// A clip round the whole window is nearly free on the GPU and the most
	// expensive thing on the default software renderer. There cairo clips every
	// shape drawn inside it against the rounded outline, re-flattening the corner
	// curves each time, and it has to do so for all of the window on every
	// frame: on Wayland GTK's software renderer repaints the whole window each
	// time anything changes, even one figure in the sidebar. Measured on a
	// captured frame, the clip was a third of the cost of drawing the Apps page
	// and more than half of the CPU page's.
	win.SetOverflow(gtk.OverflowVisible)
	win.AddCSSClass("am-main")
	// High contrast, as a class the stylesheet can see. Its cards replace
	// libadwaita's shadow and so its high-contrast one too; see "Cards" in
	// assets/style.css. A class, not an @media query, because GTK only reads
	// those from 4.20 and Atlas supports older.
	if mgr := adw.StyleManagerGetDefault(); mgr != nil {
		syncContrast := func() {
			if mgr.HighContrast() {
				win.AddCSSClass("am-hc")
			} else {
				win.RemoveCSSClass("am-hc")
			}
		}
		syncContrast()
		mgr.NotifyProperty("high-contrast", syncContrast)
	}
	win.SetDefaultSize(a.settings.WindowWidth, a.settings.WindowHeight)
	win.SetSizeRequest(config.MinWindowWidth, config.MinWindowHeight)
	if a.settings.WindowMaximized {
		win.Maximize()
	}

	// The title bar the way Windows 11 draws Task Manager's: the name at the
	// start rather than centred, on the same surface as the sidebar, so the two
	// read as one frame around the page. The centred title is replaced by an
	// empty widget; see brandBox.
	header := adw.NewHeaderBar()
	header.AddCSSClass("am-titlebar")
	header.SetTitleWidget(gtk.NewBox(gtk.OrientationHorizontal, 0))

	// Sidebar toggle first, where a hamburger belongs. It hides itself whenever
	// the sidebar is on screen in its own right.
	if btn := a.content.MenuButton(); btn != nil {
		header.PackStart(btn)
	}
	header.PackStart(brandBox(a.version))
	// The alert badge sits on the right: it is a notice rather than navigation,
	// and it is not there at all while the machine is fine.
	if btn := a.content.AlertButton(); btn != nil {
		header.PackEnd(btn)
	}

	toolbar := adw.NewToolbarView()
	toolbar.AddCSSClass("am-frame")
	toolbar.AddTopBar(header)
	toolbar.SetContent(root)
	win.SetContent(toolbar)
	a.applyTransparency()

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
}

// brandBox is the application's name at the start of the title bar: its icon,
// the name, and the version, quieter, after it.
//
// The icon only where the title bar does not already show one. A desktop can
// ask for the window's icon at the start of every title bar — KDE does, with
// the decoration layout "icon:minimize,maximize,close" — and GTK then draws it
// itself, which put two side by side. The layout can change while Atlas runs,
// so the choice is made again when it does. The icon is also left out when the
// icon theme does not have it: a build run from the source tree has not
// installed it, and GTK's stand-in for a missing icon would be worse than none.
func brandBox(version string) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("am-brand")
	if d := gdk.DisplayGetDefault(); d != nil && gtk.IconThemeGetForDisplay(d).HasIcon(AppID) {
		icon := gtk.NewImageFromIconName(AppID)
		icon.SetPixelSize(18)
		box.Append(icon)
		if st := gtk.SettingsGetDefault(); st != nil {
			sync := func() {
				layout, _ := st.ObjectProperty("gtk-decoration-layout").(string)
				icon.SetVisible(!decorationShowsIcon(layout))
			}
			sync()
			st.NotifyProperty("gtk-decoration-layout", sync)
		}
	}
	name := gtk.NewLabel("Atlas Monitor")
	name.AddCSSClass("am-brand-name")
	box.Append(name)
	if version != "" {
		v := gtk.NewLabel("v" + version)
		v.AddCSSClass("am-brand-version")
		box.Append(v)
	}
	return box
}

// decorationShowsIcon reports whether a GTK decoration layout puts the window's
// icon at the start of the title bar, where Atlas's name goes: "icon" among the
// buttons before the colon.
func decorationShowsIcon(layout string) bool {
	start, _, _ := strings.Cut(layout, ":")
	for _, b := range strings.Split(start, ",") {
		if strings.TrimSpace(b) == "icon" {
			return true
		}
	}
	return false
}

// onSettingsChanged applies saved settings to the running app.
func (a *App) onSettingsChanged() {
	a.applyTheme()
	a.aiClient.SetConfig(a.settings.OllamaURL, a.settings.Model)
	a.content.SetAIEnabled(a.settings.AIEnabled)
	a.content.RefreshQuickPrompts()
	a.content.SetRefreshInterval(
		time.Duration(config.NormalizeRefresh(a.settings.RefreshSeconds)) * time.Second)
	a.applyTransparency()
}

// saveWindowState records the geometry and the open page so the next launch
// picks up where this one left off. A maximized window keeps the size it had
// before being maximized, which is what the user gets back on un-maximize.
func (a *App) saveWindowState(win *adw.ApplicationWindow) {
	// Text still being typed in Settings goes in with everything else, while the
	// window it would update is still there.
	a.content.FlushSettings()
	a.settings.WindowMaximized = win.IsMaximized()
	if !a.settings.WindowMaximized {
		if w, h := win.DefaultSize(); w >= config.MinWindowWidth && h >= config.MinWindowHeight {
			a.settings.WindowWidth, a.settings.WindowHeight = w, h
		}
	}
	if v := a.content.LastPage(); v != "" {
		a.settings.LastView = v
	}
	_ = config.Save(a.settings)
}

// sourceDir returns the source checkout recorded by `make install`
// (in $XDG_DATA_HOME/atlas-monitor/source), or "" if it is unknown.
func sourceDir() string {
	b, err := os.ReadFile(filepath.Join(userDataDir(), "source"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// userDataDir is Atlas's own directory under the user's data home, where
// `make install` records the source checkout and the build flavour.
func userDataDir() string { return atlasDir("XDG_DATA_HOME", filepath.Join(".local", "share")) }

// atlasDir resolves one of the XDG directories to Atlas's folder inside it.
//
// The variable wins wherever it is set, which is the whole of the Linux story and
// on Windows lets a portable install keep its state beside itself. Failing that
// there is a per-platform default: the XDG path under the home directory on Unix,
// and %LocalAppData% on Windows, which is where a Windows program is supposed to
// put things it wrote itself. Falling back to "$HOME/.local/share" on a machine
// where HOME is unset — which is most Windows machines — used to produce a relative
// path, so Atlas wrote a .local folder into whatever directory it was started from.
func atlasDir(envVar, unixSuffix string) string {
	if base := os.Getenv(envVar); base != "" {
		return filepath.Join(base, "atlas-monitor")
	}
	if base := localAppData(); base != "" {
		return filepath.Join(base, "atlas-monitor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "atlas-monitor"
	}
	return filepath.Join(home, unixSuffix, "atlas-monitor")
}

// settingsHooks bundles the callbacks the Settings page needs.
func (a *App) settingsHooks() ui.SettingsHooks {
	return ui.SettingsHooks{
		OnChange:    a.onSettingsChanged,
		ApplyUpdate: a.startUpdate,
		CheckUpdate: a.checkUpdate,
		Version:     a.version,
		Location:    a.location(),
		ManagedBy:   a.install().Manager,
	}
}

// location is where this copy lives, said in a way that also explains who
// updates it: a checkout path, or "installed by pacman — /usr/bin/atlas-monitor".
func (a *App) location() string { return a.install().Where() }

// checkUpdate adapts CheckUpdate to what the Settings page wants: a yes/no
// and one line to show. The detail it drops — the version and the changelog —
// is only needed by the prompt at launch.
func (a *App) checkUpdate(channel string) (available bool, info string, err error) {
	u, err := a.CheckUpdate(channel)
	return u.Available, u.Summary, err
}

// easeEvery is how often the automatic Energy Saver samples. It reads one small
// file per application, so running it while the window is closed — which is the
// point of it — costs next to nothing.
const easeEvery = 5 * time.Second

// startEaser sets up Energy Saver's automatic half and starts it sampling. It
// runs whatever the window is doing: this is the one part of Atlas that has to
// keep working when nobody is looking at it.
func (a *App) startEaser() {
	c, err := ease.System(ease.DesktopIdentity)
	a.easer, a.easeErr = c, err
	if c == nil {
		return
	}
	c.SetNever(a.settings.EnergyNever)
	c.SetAutomatic(a.settings.EnergyAuto)
	a.easeStop = make(chan struct{})
	go func(stop <-chan struct{}) {
		t := time.NewTicker(easeEvery)
		defer t.Stop()
		c.Tick()
		for {
			select {
			case <-t.C:
				c.Tick()
			case <-stop:
				return
			}
		}
	}(a.easeStop)
}

// stopEaser stops the sampling and puts back everything eased automatically:
// with Atlas gone, nothing would notice an eased application start playing.
func (a *App) stopEaser() {
	if a.easer == nil {
		return
	}
	close(a.easeStop)
	a.easer.Shutdown()
	a.easer = nil
}

// loadCSS installs the embedded stylesheet and the theme's colours.
//
// Three providers, in order of increasing priority: the stylesheet, the picker's
// circles, and the chosen theme's colour overrides. The overrides have to come
// last, because they redefine the very names the stylesheet is written in terms of.
func (a *App) loadCSS() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	add := func(css string, priority uint) *gtk.CSSProvider {
		if css == "" {
			return nil
		}
		p := gtk.NewCSSProvider()
		p.LoadFromString(css)
		gtk.StyleContextAddProviderForDisplay(display, p, priority)
		return p
	}

	add(a.css, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	// The circles are generated from the theme table, so one cannot be added
	// without its colours. See theme.SwatchCSS.
	add(theme.SwatchCSS(), gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	// Empty to begin with and filled by applyTheme, which runs next and also
	// runs again whenever the choice changes.
	a.themeCSS = gtk.NewCSSProvider()
	// APPLICATION+1, not USER. USER is where GTK loads the person's own
	// gtk.css, and an application that installs itself at the same priority is
	// competing with their customisations on nothing but load order.
	gtk.StyleContextAddProviderForDisplay(
		display, a.themeCSS, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION+1)

	// The table colours are worked out from the window colours and the accent
	// (see theme.CSS), and following the desktop both of those can change under
	// Atlas: the desktop switches to dark in the evening, or the accent is
	// changed in its settings. Rather than rely on a CSS media query — GTK only
	// has prefers-color-scheme from 4.20, and libadwaita 1.6, which Atlas
	// supports, runs on older — they are recomputed here when either changes.
	if mgr := adw.StyleManagerGetDefault(); mgr != nil {
		mgr.NotifyProperty("dark", a.reloadThemeCSS)
		mgr.NotifyProperty("accent-color-rgba", a.reloadThemeCSS)
	}
	a.applyTheme()
}

// reloadThemeCSS loads the CSS for the theme in effect, with the desktop's
// current accent colour. applyTheme calls it when the choice changes; the
// style manager calls it when the desktop's scheme or accent does.
func (a *App) reloadThemeCSS() {
	mgr := adw.StyleManagerGetDefault()
	if mgr == nil || a.themeCSS == nil {
		return
	}
	accent := ""
	if c := mgr.AccentColorRGBA(); c != nil {
		accent = theme.HexOf(c.Red(), c.Green(), c.Blue())
	}
	css := theme.FollowCSS(mgr.Dark(), accent)
	if !theme.IsFollowing(a.settings.Theme) {
		css = theme.Resolve(a.settings.Theme, mgr.Dark()).CSS(accent)
	}
	// Reloading a provider restyles every widget in the window, and this is
	// called more often than the CSS changes: forcing a theme's scheme fires
	// notify::dark, and then applyTheme calls it again.
	if css == a.loadedThemeCSS {
		return
	}
	a.loadedThemeCSS = css
	a.themeCSS.LoadFromString(css)
}

// applyTheme puts the chosen theme into effect, live.
//
// Two separate things, and both matter. The colour scheme decides whether
// libadwaita draws its light or dark widgets, and — because internal/graph works
// out which side it is on from the foreground colour — whether the chart palette
// is the one drawn for a dark background or the darkened one for a light background.
// The overrides then supply the palette itself.
//
// An unset or unrecognised setting leaves the scheme on Default, which is
// libadwaita following the desktop. That is what Atlas did before it had themes and
// is still what it does until somebody picks one.
func (a *App) applyTheme() {
	mgr := adw.StyleManagerGetDefault()
	if mgr == nil {
		return
	}

	// Only when the choice has changed. This runs on every settings change —
	// every switch flipped, and closing the dialog — and reloading a CSS
	// provider invalidates the style of every widget in the window, whether or
	// not a single colour in it is different. Reloading the same theme is a full
	// restyle that changes nothing.
	want := a.settings.Theme
	if theme.IsFollowing(want) {
		want = theme.Follow
	}
	if a.themeApplied && want == a.appliedTheme {
		return
	}
	a.themeApplied, a.appliedTheme = true, want

	if theme.IsFollowing(a.settings.Theme) {
		mgr.SetColorScheme(adw.ColorSchemeDefault)
	} else if theme.Resolve(a.settings.Theme, mgr.Dark()).Dark {
		mgr.SetColorScheme(adw.ColorSchemeForceDark)
	} else {
		mgr.SetColorScheme(adw.ColorSchemeForceLight)
	}
	a.reloadThemeCSS()
}

// glassClasses are the style classes window transparency uses: the general one,
// which the stylesheet turns the backgrounds see-through with, and one per level
// for how far. They are all listed so that a change of level can clear the old
// one without remembering what it was.
var glassClasses = []string{"am-glass", "am-glass-subtle", "am-glass-medium", "am-glass-strong",
	"am-sidebar-solid", "am-frame-solid"}

// glassClassesFor is every class the window should wear for a transparency level
// and frame style: none at all when transparency is off, whatever the frame
// style says, since a solid frame on an opaque window is simply the window.
func glassClassesFor(level, frame string) []string {
	lc := glassLevelClass(level)
	if lc == "" {
		return nil
	}
	out := []string{"am-glass", lc}
	switch config.NormalizeFrame(frame) {
	case config.FrameSolidSidebar:
		out = append(out, "am-sidebar-solid")
	case config.FrameSolid:
		out = append(out, "am-frame-solid")
	}
	return out
}

// glassLevelClass is the class for one level, or "" for off and for anything
// unrecognised.
func glassLevelClass(level string) string {
	switch config.NormalizeTransparency(level) {
	case config.TransparencySubtle:
		return "am-glass-subtle"
	case config.TransparencyMedium:
		return "am-glass-medium"
	case config.TransparencyStrong:
		return "am-glass-strong"
	default:
		return ""
	}
}

// applyTransparency makes the main window see-through to the chosen level, or
// opaque again.
//
// It is all done with style classes on the window; the stylesheet decides what
// each one means. Nothing is applied when the setting is off, and nothing where
// the display cannot show it — the saved choice is left alone in that case, so it
// takes effect again on a desktop that can.
//
// Like applyTheme it does nothing when the effective level has not changed. This
// runs on every settings change, and adding or removing a class restyles the
// whole window whether or not the result is any different.
func (a *App) applyTransparency() {
	if a.win == nil {
		return
	}
	classes := glassClassesFor(a.settings.WindowTransparency, a.settings.FrameStyle)
	if ok, _ := ui.TransparencyAvailable(); !ok {
		classes = nil
	}
	want := strings.Join(classes, " ")
	if want == a.appliedGlass {
		return
	}
	a.appliedGlass = want

	for _, c := range glassClasses {
		a.win.RemoveCSSClass(c)
	}
	for _, c := range classes {
		a.win.AddCSSClass(c)
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
	channel := config.NormalizeChannel(a.settings.UpdateChannel)
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

	dlg := adw.NewAlertDialog(heading, "")
	if len(info.Changes) > 0 {
		dlg.SetExtraChild(changelogList(info.Changes))
	} else {
		dlg.SetBody("A newer version of Atlas Monitor is available.")
	}
	dlg.AddResponse("later", "Update Later")
	// "Update Now" would be a promise Atlas cannot keep on a copy it must not
	// overwrite: what happens next there is a command for the package manager,
	// so the button says that instead.
	now := "Update Now"
	if !a.install().SelfUpdatable() {
		now = "How to Update"
	}
	dlg.AddResponse("now", now)
	dlg.SetResponseAppearance("now", adw.ResponseSuggested)
	dlg.SetDefaultResponse("now")
	dlg.SetCloseResponse("later")
	dlg.ConnectResponse(func(response string) {
		if response == "now" {
			a.startUpdate(nil)
		}
	})
	dlg.Present(win)
}

// changelogList lays the release notes out as a list instead of as dialog prose.
// An AlertDialog centres its body, and a centred bullet list gives every line a
// different left edge for the eye to find — which is exactly the thing a list is
// meant to save you from.
func changelogList(changes []string) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 8)

	intro := gtk.NewLabel("What's new:")
	intro.SetXAlign(0)
	box.Append(intro)

	for _, c := range changes {
		row := gtk.NewBox(gtk.OrientationHorizontal, 8)
		bullet := gtk.NewLabel("•")
		bullet.SetVAlign(gtk.AlignStart)
		row.Append(bullet)

		text := gtk.NewLabel(c)
		text.SetXAlign(0)
		text.SetWrap(true)
		text.SetMaxWidthChars(46)
		row.Append(text)

		box.Append(row)
	}
	return box
}
