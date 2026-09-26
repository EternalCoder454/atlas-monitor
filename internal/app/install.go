package app

import (
	"errors"
	"fmt"
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

	// A packaged copy is not ours to overwrite, and one installed somewhere this
	// user cannot write is not ours to try. Both get the command that does work
	// instead of a build that would fail — or, worse, a build that succeeds into
	// the wrong prefix and leaves two Atlases on PATH.
	if in := a.install(); !in.SelfUpdatable() {
		a.showManagedUpdate(in)
		if done != nil {
			done(false)
		}
		return
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

	// Where the end of a failed build goes, folded away.
	//
	// It is compiler output rather than prose, so it gets the left edge, a
	// fixed width and a monospace face. It is also not for the person being
	// told the update failed — leading with a make error is alarming in a way
	// the situation does not warrant, since nothing is broken and the old
	// version is still running. The dialog says that in words; the log waits
	// behind a disclosure for whoever is going to report it.
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

	copyBtn := gtk.NewButtonWithLabel("Copy log")
	copyBtn.SetHAlign(gtk.AlignStart)
	copyBtn.ConnectClicked(func() {
		copyBtn.Clipboard().SetText(detail.Text())
		copyBtn.SetLabel("Copied")
	})

	detailBox := gtk.NewBox(gtk.OrientationVertical, 8)
	detailBox.SetMarginTop(8)
	detailBox.Append(detail)
	detailBox.Append(copyBtn)

	expander := gtk.NewExpander("Technical details")
	expander.SetVisible(false)
	expander.SetChild(detailBox)
	body.Append(expander)

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
				var setup *setupError
				if errors.As(err, &setup) {
					// Something the user has to install first, named along with
					// the command that installs it. This is not a build failure
					// and has no log worth showing.
					status.SetText(setup.Error())
				} else {
					status.SetText("The update couldn't be installed.\nAtlas Monitor is still on v" +
						a.version + " and running normally.")
					if tail := updateLogTail(maxLogTail); tail != "" {
						detail.SetText(tail)
						expander.SetVisible(true)
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
	in := a.install()
	branch := a.settings.UpdateChannel
	if branch != "main" && branch != "beta" {
		branch = "main"
	}
	// The new version installs back over the one that is running, whatever
	// prefix that is under. Left to the Makefile's default a copy installed in
	// /usr/local would be rebuilt into ~/.local, and which of the two launched
	// afterwards would come down to the order of PATH.
	prefix := in.Prefix()

	status("Downloading and building the new version…\nThis takes a minute or two.")

	go func() {
		fail := func(err error) { glib.IdleAdd(func() { finished(err) }) }
		say := func(text string) { glib.IdleAdd(func() { status(text) }) }

		src := in.Source
		if src == "" {
			// Unpacked rather than built: fetch the source once, and this copy
			// updates itself like any other from here on.
			var err error
			if src, err = bootstrapSource(branch, say); err != nil {
				fail(err)
				return
			}
			say("Building the new version…\nThis takes a minute or two.")
		}

		script := filepath.Join(src, "scripts", "update.sh")
		if _, err := os.Stat(script); err != nil {
			fail(fmt.Errorf("the update script is missing from %s", src))
			return
		}

		// The script path, the branch and the prefix are arguments to bash,
		// never text pasted into a command for a shell to re-read. An argument
		// cannot become syntax whatever it contains, so a checkout path holding
		// $(...), a backtick, or just a '$' is data — see
		// TestUpdateTreatsPathsAsData.
		err := exec.Command("bash", script, branch, prefix).Run()
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
