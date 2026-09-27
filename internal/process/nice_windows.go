package process

import (
	"golang.org/x/sys/windows"
)

// Priority on Windows.
//
// Windows has no nice value. It has six priority classes, and a process belongs
// to one of them. So the number the rest of Atlas speaks in is mapped onto the
// class that means the same thing, in both directions.
//
// The other difference is that this is not a one-way door. Lowering and raising
// the priority of a process you own both work without any special privilege,
// where Linux needs CAP_SYS_NICE to undo it.

// EaseOffReversible is true here: a program that has been eased off can be put
// back. The Energy Saver page words itself from this.
const EaseOffReversible = true

// Priority classes, lowest to highest.
const (
	idlePriority        = 0x00000040
	belowNormalPriority = 0x00004000
	normalPriority      = 0x00000020
	aboveNormalPriority = 0x00008000
	highPriority        = 0x00000080
	realtimePriority    = 0x00000100
)

// niceOfClass maps a priority class onto the nice value that behaves like it.
// The numbers are chosen so that the comparison the Energy Saver page makes —
// "is this at or below EasedNice?" — gives the right answer.
var niceOfClass = map[uint32]int{
	idlePriority:        19,
	belowNormalPriority: EasedNice,
	normalPriority:      0,
	aboveNormalPriority: -5,
	highPriority:        -10,
	realtimePriority:    -20,
}

// Nice reads a process's priority as a nice-equivalent value.
func Nice(pid int) (int, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h)

	class, err := windows.GetPriorityClass(h)
	if err != nil {
		return 0, false
	}
	n, ok := niceOfClass[class]
	return n, ok
}

// SetNice moves a process to the priority class matching a nice value.
//
// Only the classes at or below normal are reachable from here, because easing off
// is the only thing that asks for this. Anything above normal would be Atlas
// handing a program more of the processor than the system gave it, which is not
// something a monitor should be doing.
func SetNice(pid, nice int) error {
	class := uint32(normalPriority)
	switch {
	case nice >= 19:
		class = idlePriority
	case nice > 0:
		class = belowNormalPriority
	}

	// SET_INFORMATION rather than the query-only right: this one changes something.
	h, err := windows.OpenProcess(windows.PROCESS_SET_INFORMATION, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.SetPriorityClass(h, class)
}
