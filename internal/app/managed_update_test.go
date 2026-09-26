package app

import (
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// TestManagedDialogBuilds exercises the widget code the dialog is made of.
//
// The whole point of this path is that it appears where an error used to, so it
// has to actually come up. The dialog itself is not presented — there is no
// window in a test — but every widget is built, which is where an API misuse
// would show.
func TestManagedDialogBuilds(t *testing.T) {
	if !gtk.InitCheck() {
		t.Skip("no display")
	}
	for _, in := range []Install{
		{Kind: FromPackage, Manager: "pacman", Package: "atlas-monitor", Binary: "/usr/bin/atlas-monitor"},
		{Kind: FromPackage, Manager: "dnf", Package: "atlas-monitor", Binary: "/usr/bin/atlas-monitor"},
		// A package manager that answered but that we have no command for: the
		// dialog must still build, just without a command to offer.
		{Kind: FromPackage, Manager: "", Package: "", Binary: "/usr/bin/atlas-monitor"},
		// Nobody owns it, but it cannot be written to either.
		{Kind: Standalone, Binary: "/usr/local/bin/atlas-monitor"},
	} {
		a := &App{version: "0.11.0"}
		a.pinInstall(in)
		a.showManagedUpdate(in) // win is nil, so nothing is presented
	}
}

// TestManagedDialogShowsTheCommand checks the one thing the user came for is in
// there, and that it is the command for their manager.
func TestManagedDialogShowsTheCommand(t *testing.T) {
	in := Install{Kind: FromPackage, Manager: "dnf", Package: "atlas-monitor", Binary: "/usr/bin/atlas-monitor"}
	cmd := in.UpdateCommand()
	if !strings.Contains(cmd, "dnf") || !strings.Contains(cmd, "atlas-monitor") {
		t.Fatalf("UpdateCommand() = %q, want a dnf command for atlas-monitor", cmd)
	}
	// A manager with nothing sensible to suggest offers no command rather than a
	// wrong one, and the dialog leaves the section out entirely.
	if got := (Install{Kind: FromPackage, Manager: ""}).UpdateCommand(); got != "" {
		t.Errorf("an unknown manager produced %q, want no command at all", got)
	}
}
