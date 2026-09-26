package app

import (
	"os/exec"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// What a copy of Atlas that it must not overwrite offers instead of an update.
//
// The old behaviour was to start the build anyway and let it fail, which on an
// Arch machine produced "source location unknown — install with `make install`".
// Everything about that is wrong: nothing is unknown, nothing is broken, and the
// suggested command would install a second, unmanaged Atlas into ~/.local that
// wins on PATH and is never updated again.
//
// What a person needs here is one line they can run. So that is what this is: the
// command for their package manager, a button that copies it, and — when a
// terminal can be found — a button that runs it in front of them, where the
// package manager can ask for the password itself rather than Atlas asking on its
// behalf.

// showManagedUpdate explains who updates this copy, and hands over the command.
func (a *App) showManagedUpdate(in Install) {
	heading, bodyText := managedWording(in)
	dlg := adw.NewAlertDialog(heading, bodyText)

	body := gtk.NewBox(gtk.OrientationVertical, 12)

	cmd := in.UpdateCommand()
	if cmd != "" {
		line := gtk.NewLabel(cmd)
		line.SetXAlign(0)
		line.SetWrap(true)
		line.SetWrapMode(pango.WrapWordChar)
		line.SetMaxWidthChars(44)
		line.SetSelectable(true)
		// As the only focusable thing here it would take focus on open, and a
		// selectable label selects all of itself when it does — the dialog would
		// appear with the command highlighted as though something had gone wrong.
		line.SetCanFocus(false)
		line.AddCSSClass("monospace")
		body.Append(line)

		buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
		buttons.SetHAlign(gtk.AlignStart)

		copyBtn := gtk.NewButtonWithLabel("Copy command")
		copyBtn.ConnectClicked(func() {
			copyBtn.Clipboard().SetText(cmd)
			copyBtn.SetLabel("Copied")
		})
		buttons.Append(copyBtn)

		// Only offered when there is something to run it in. A button that
		// silently does nothing is worse than no button.
		if term := findTerminal(); term != nil {
			runBtn := gtk.NewButtonWithLabel("Run in Terminal")
			runBtn.AddCSSClass("suggested-action")
			runBtn.ConnectClicked(func() {
				if err := term.run(cmd); err != nil {
					runBtn.SetLabel("Couldn't open a terminal")
					runBtn.SetSensitive(false)
					return
				}
				runBtn.SetSensitive(false)
				dlg.Close()
			})
			buttons.Append(runBtn)
		}
		body.Append(buttons)

		note := gtk.NewLabel("Atlas Monitor will use the new version the next time you start it.")
		note.SetXAlign(0)
		note.SetWrap(true)
		note.SetMaxWidthChars(44)
		note.AddCSSClass("caption")
		note.AddCSSClass("dim-label")
		body.Append(note)
	}

	dlg.SetExtraChild(body)
	dlg.AddResponse("close", "Close")
	dlg.SetCloseResponse("close")
	dlg.SetDefaultResponse("close")
	if a.win != nil {
		dlg.Present(a.win)
	}
}

// managedWording is the heading and the explanation, which differ by why Atlas
// is staying out of it.
func managedWording(in Install) (heading, body string) {
	if in.Kind == FromPackage {
		mgr := in.Manager
		if mgr == "" {
			mgr = "your package manager"
		}
		return "Update with " + mgr,
			"Atlas Monitor was installed by " + mgr + ", so " + mgr + " updates it too. " +
				"Installing over it from here would leave the package database describing " +
				"a file that is no longer there."
	}
	return "Update needs permission",
		"Atlas Monitor is installed in " + in.Prefix() + ", which this account cannot " +
			"write to. Re-run the installer with administrator rights to update it."
}

// terminal is a terminal emulator and how it takes a command to run.
type terminal struct {
	cmd  string
	args []string // everything before the command itself
}

// run opens the terminal on a shell that runs cmd and then waits, so the output
// is still there to read when the package manager has finished.
//
// cmd reaches bash as an argument rather than being pasted into a command line
// for a shell to re-read, and it is built by UpdateCommand from a validated
// package name, so there is nothing in it a shell could take for syntax that we
// did not put there deliberately.
func (t terminal) run(cmd string) error {
	return exec.Command(t.cmd, t.argv(cmd)...).Start()
}

// argv is what the terminal is actually exec'd with, split out so that where the
// command ends up can be checked — it must arrive as one argument and never be
// spliced into a string another shell will parse. See
// TestTerminalPassesTheCommandAsAnArgument.
func (t terminal) argv(cmd string) []string {
	const wrapper = `"$@"; status=$?; printf '\n'; read -rsn1 -p 'Press any key to close…'; exit $status`
	args := append([]string{}, t.args...)
	return append(args, "bash", "-c", wrapper, "atlas-update", "bash", "-lc", cmd)
}

// terminals are the emulators worth trying, in order. x-terminal-emulator comes
// first because on a Debian-derived system it is whichever one the user chose;
// the rest are the defaults of the desktops Atlas is likely to be running on.
var terminals = []terminal{
	{"x-terminal-emulator", []string{"-e"}},
	{"ptyxis", []string{"--"}},
	{"kgx", []string{"--"}},
	{"gnome-terminal", []string{"--"}},
	{"konsole", []string{"-e"}},
	{"xfce4-terminal", []string{"-x"}},
	{"tilix", []string{"-e"}},
	{"alacritty", []string{"-e"}},
	{"kitty", nil},
	{"foot", nil},
	{"wezterm", []string{"start", "--"}},
	{"xterm", []string{"-e"}},
}

// findTerminal returns the first terminal emulator that is installed, or nil.
func findTerminal() *terminal {
	for _, t := range terminals {
		if which(t.cmd) != "" {
			found := t
			return &found
		}
	}
	return nil
}
