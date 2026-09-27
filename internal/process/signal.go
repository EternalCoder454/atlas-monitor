package process

// Acting on a process, in terms both platforms can honour.
//
// The Apps table's context menu used Unix signals directly, which meant the UI
// spoke in SIGTERM and SIGSTOP — names Windows has no equivalent of, and in the
// case of SIGSTOP does not even define a constant for. So the menu asks for one of
// four things and each platform decides how to do it.

// Action is something to do to a process.
type Action int

const (
	// Terminate asks a program to exit and lets it clean up. On Linux that is
	// SIGTERM; on Windows it is a close request to the program's own windows,
	// which is what Task Manager's "End task" does.
	Terminate Action = iota

	// Kill stops a process immediately, with no chance to save anything.
	Kill

	// Suspend freezes a process, and Resume lets it continue.
	Suspend
	Resume
)

// String is for error messages, not for the menu — the UI has its own wording.
func (a Action) String() string {
	switch a {
	case Terminate:
		return "end"
	case Kill:
		return "kill"
	case Suspend:
		return "suspend"
	case Resume:
		return "resume"
	default:
		return "act on"
	}
}
