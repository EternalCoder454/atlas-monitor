// Package autostart reads and edits the programs that start when you log in.
//
// The two platforms keep this in completely different places — XDG desktop entry
// files on Linux, the registry and the Startup folders on Windows — but the
// question is the same one, and so is the shape of the answer. That shape is here.
package autostart

// Entry is one program that starts at login.
type Entry struct {
	Name    string // human name: the desktop entry's Name, or the registry value's
	Comment string
	Exec    string // the command line, as written
	File    string // where this came from: a file path, or a registry key

	// System is true for an entry installed for every user rather than by this
	// one. On Linux that is /etc/xdg/autostart; on Windows, HKEY_LOCAL_MACHINE or
	// the shared Startup folder.
	System bool
	// Plumbing is an entry that does not want to be listed in a settings window —
	// the desktop's own background pieces. Linux reads it from NoDisplay; Windows
	// has no equivalent and leaves it false.
	Plumbing bool
	// Disabled is true when the entry is switched off, by us or by something else.
	Disabled bool
	// RunsHere is false when the entry's own rules exclude the current desktop, so
	// it is listed as something that will not actually run. Always true on
	// Windows, which has no such rules.
	RunsHere bool

	// hasName records whether the source actually carried a name. A file written
	// only to switch something off carries nothing else, and the list should still
	// show the program's real name rather than its filename.
	hasName bool
	// systemFile is the packaged file this entry shadows, if any. Switching the
	// entry back on removes the override and hands it back, rather than leaving a
	// copy behind to shadow every future update of it.
	systemFile string
	// approvalKey is the Windows registry value that records whether this entry is
	// switched off. Unused on Linux, where the override is a file.
	approvalKey string
}
