package process

import "golang.org/x/sys/windows"

// ExecutablePath is the full path to a process's program file, or "" if it cannot
// be read.
//
// QueryFullProcessImageName is the one that works with the limited query right, so
// this does not need to ask for more access than looking at a process requires.
func ExecutablePath(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
