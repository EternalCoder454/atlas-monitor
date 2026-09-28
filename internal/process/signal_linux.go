package process

import "syscall"

// Signal carries out an action on a process.
//
// Each of the four is a Unix signal, which is the vocabulary this was written in
// before Windows needed the same menu to work.
func Signal(pid int, a Action) error {
	var sig syscall.Signal
	switch a {
	case Terminate:
		sig = syscall.SIGTERM
	case Kill:
		sig = syscall.SIGKILL
	case Suspend:
		sig = syscall.SIGSTOP
	case Resume:
		sig = syscall.SIGCONT
	default:
		return nil
	}
	return syscall.Kill(pid, sig)
}

// Supported reports whether an action can be carried out here. All four can.
func Supported(Action) bool { return true }
