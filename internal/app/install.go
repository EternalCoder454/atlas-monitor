package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// Installing an update.
//
// The install is a git pull and a full rebuild: a minute or two on a warm build
// cache, longer on a cold one. It used to be fired off detached, with the window
// closing the instant the user said yes — from the outside that is
// indistinguishable from the app crashing, and a build that failed brought the
// old version silently back as though nothing had been asked for. The work now
// runs where the app can watch it, so something is on screen while it builds,
// there is an explanation when it fails, and the restart happens only once the
// new version is actually installed.

// restartPause leaves the "restarting" line up long enough to be read before the
// window goes. The relaunch itself waits a further second (see relaunch).
const restartPause = 700

// maxLogTail caps how much of a failed build's log the dialog shows. It is the
// last thing make said that matters, not the whole run.
const maxLogTail = 360

// errNoCheckout is the honest answer for a copy that was unpacked rather than
// installed from source: there is nothing to pull and nothing to build.
var errNoCheckout = errors.New("this copy of Atlas wasn't installed from a source checkout, so it can't update itself")

// startUpdate installs the waiting version and restarts into it, reporting
// progress in a dialog of its own.
//
// done, when given, is called if the update did not happen, so a caller that
// disabled a button on the way in can put it back. A successful update never
// calls it: by then the app is on its way out.
func (a *App) startUpdate(done func(ok bool)) {
	if a.updating {
		return // one at a time — two builds in the same checkout would fight
	}
	a.updating = true

	dlg := adw.NewAlertDialog("Updating Atlas Monitor", "")

	body := gtk.NewBox(gtk.OrientationVertical, 12)
	spinner := gtk.NewSpinner()
	spinner.SetSizeRequest(28, 28)
	spinner.Start()
	body.Append(spinner)

	status := gtk.NewLabel("")
	status.SetJustify(gtk.JustifyCenter)
	status.SetWrap(true)
	status.SetMaxWidthChars(42)
	body.Append(status)

	// Where the end of a failed build goes. It is compiler output rather than
	// prose: centred and proportional it is unreadable, so it gets the left
	// edge and a fixed width, and it stays hidden unless there is something to
	// put in it.
	detail := gtk.NewLabel("")
	detail.SetXAlign(0)
	detail.SetWrap(true)
	detail.SetWrapMode(pango.WrapWordChar)
	detail.SetMaxWidthChars(46)
	detail.SetSelectable(true) // so it can be pasted into a bug report
	// ...but not focusable with it. A selectable label selects all of itself
	// when it takes focus, and as the only focusable thing in the dialog it
	// took it on open: the failure appeared with the whole log highlighted, as
	// though the dialog had gone wrong as well as the build. Dragging across it
	// still selects.
	detail.SetCanFocus(false)
	detail.AddCSSClass("monospace")
	detail.AddCSSClass("caption")
	detail.SetVisible(false)
	body.Append(detail)

	dlg.SetExtraChild(body)
	// Closing the dialog only puts it away; the build carries on, and the app
	// still restarts into the new version when it is done.
	dlg.AddResponse("close", "Close")
	dlg.SetCloseResponse("close")
	// Close takes the focus. Without this it lands on the log below, which is
	// selectable, and a label selects all of itself when focused — the failure
	// came up with the whole build log highlighted as though something had gone
	// wrong with the dialog too.
	dlg.SetDefaultResponse("close")
	if a.win != nil {
		dlg.Present(a.win)
	}

	a.installUpdate(
		status.SetText,
		func(err error) {
			spinner.Stop()
			a.updating = false
			if err != nil {
				if errors.Is(err, errNoCheckout) {
					status.SetText(err.Error() + "\nDownload the newer version from the project page instead.")
				} else {
					status.SetText("The update didn't finish — Atlas Monitor is still on v" + a.version + ".")
					if tail := updateLogTail(maxLogTail); tail != "" {
						detail.SetText(tail)
						detail.SetVisible(true)
					}
				}
				if done != nil {
					done(false)
				}
				return
			}
			status.SetText("Updated — restarting Atlas Monitor…")
			glib.TimeoutAdd(restartPause, func() bool {
				a.relaunch()
				return false
			})
		},
	)
}

// installUpdate runs the update script for the configured channel off the UI
// thread. status is called with something to show while it works; finished is
// called exactly once, with the error or nil, when it is over. Both run on the
// main loop.
func (a *App) installUpdate(status func(string), finished func(error)) {
	script := ""
	if src := sourceDir(); src != "" {
		p := filepath.Join(src, "scripts", "update.sh")
		if _, err := os.Stat(p); err == nil {
			script = p
		}
	}
	if script == "" {
		finished(errNoCheckout)
		return
	}
	branch := a.settings.UpdateChannel
	if branch != "main" && branch != "beta" {
		branch = "main"
	}

	status("Downloading and building the new version…\nThis takes a minute or two.")

	go func() {
		// The script path and the branch are arguments to bash, never text
		// pasted into a command for a shell to re-read. An argument cannot
		// become syntax whatever it contains, so a checkout path holding
		// $(...), a backtick, or just a '$' is data — see
		// TestUpdateTreatsPathsAsData.
		err := exec.Command("bash", script, branch).Run()
		glib.IdleAdd(func() { finished(err) })
	}()
}

// relaunch starts a fresh instance and quits this one.
//
// The helper is detached with setsid so it outlives this process, and its sleep
// gives the single-instance lock time to release: without it the new instance
// finds this one still holding the bus name and hands its activation to a window
// that is already leaving.
func (a *App) relaunch() {
	// $0 names the shell in any error it prints; $1 is the desktop ID. Nothing
	// here comes from the user or from disk.
	const helper = `sleep 1; gtk-launch "$1"`
	_ = exec.Command("setsid", "bash", "-c", helper, "atlas-monitor-restart", AppID).Start()
	a.app.Quit()
}

// updateLogTail returns the end of the update script's log, or "" if there is
// none to read. n is a byte budget rather than a line count; the result is
// trimmed forward to a character, and then to a line, so it never opens on half
// of either.
func updateLogTail(n int) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	b, err := os.ReadFile(filepath.Join(base, "atlas-monitor", "update.log"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < 80 {
		s = s[i+1:]
	}
	return "…\n" + s
}
