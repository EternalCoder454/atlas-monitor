package process

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Acting on a process on Windows.
//
// There is no signal mechanism, so each of the four actions is a different call.
// Terminate is the one worth care: Windows has nothing that asks a program to exit
// the way SIGTERM does, so the closest honest equivalent is what Task Manager's
// "End task" does — post a close request to the program's own windows and let it
// decide what to do about it, including putting up a "save your work?" prompt.
// Only a process with no windows to ask falls through to being terminated.
var (
	ntdll                = windows.NewLazySystemDLL("ntdll.dll")
	procNtSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	procNtResumeProcess  = ntdll.NewProc("NtResumeProcess")

	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procPostMessage              = user32.NewProc("PostMessageW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
)

// wmClose is the close request a window manager sends when the X is clicked.
const wmClose = 0x0010

// Signal carries out an action on a process.
func Signal(pid int, a Action) error {
	switch a {
	case Terminate:
		if askWindowsToClose(pid) > 0 {
			return nil
		}
		// Nothing to ask: a background process, or one whose windows are on
		// another desktop. Terminating is then the only thing "end" can mean.
		return terminate(pid)
	case Kill:
		return terminate(pid)
	case Suspend:
		return suspendResume(pid, procNtSuspendProcess, "suspend")
	case Resume:
		return suspendResume(pid, procNtResumeProcess, "resume")
	default:
		return nil
	}
}

// Supported reports whether an action can be carried out here. All four can,
// though Terminate means something slightly different — see above.
func Supported(Action) bool { return true }

// terminate stops a process immediately.
func terminate(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("opening process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	// 1 rather than 0: an exit code of zero would claim the program finished
	// successfully, which is not what happened to it.
	return windows.TerminateProcess(h, 1)
}

// askWindowsToClose posts a close request to each visible top-level window the
// process owns, and returns how many it asked.
func askWindowsToClose(pid int) int {
	asked := 0
	cb := windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadProcessID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&owner)))
		if int(owner) != pid {
			return 1 // not ours; keep enumerating
		}
		// Invisible windows include the message-only ones many programs keep for
		// their own bookkeeping, and closing those achieves nothing.
		if visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); visible == 0 {
			return 1
		}
		if r, _, _ := procPostMessage.Call(uintptr(hwnd), wmClose, 0, 0); r != 0 {
			asked++
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return asked
}

// suspendResume freezes or thaws a process.
//
// NtSuspendProcess and NtResumeProcess are not in the documented API, which is
// worth saying out loud. They are what every task manager on Windows uses,
// including Process Explorer, and have been present and unchanged since Windows
// XP — but they are reached through ntdll rather than a supported header, so a
// missing export is handled rather than assumed away.
func suspendResume(pid int, proc *windows.LazyProc, what string) error {
	if err := proc.Find(); err != nil {
		return fmt.Errorf("this version of Windows cannot %s a process: %w", what, err)
	}
	h, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("opening process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	// An NTSTATUS: zero is success.
	if r, _, _ := proc.Call(uintptr(h)); r != 0 {
		return fmt.Errorf("could not %s process %d: status %#x", what, pid, r)
	}
	return nil
}
