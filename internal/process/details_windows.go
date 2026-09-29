package process

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"

	"atlas-monitor/internal/winapi"
)

// Details reads what Windows will say about one process to a program that is
// not an administrator. Most of it comes from the same one-call process table
// the scan uses; the path, start time and owner need a handle, which the limited
// query right is enough for on anything but protected processes.
//
// The command line is not read: it lives in the target's own memory, and getting
// it means an undocumented query this port has no way to test.
func Details(pid int) (Info, error) {
	procs, err := winapi.ReadProcesses()
	if err != nil {
		return Info{}, err
	}
	in := Info{PID: pid}
	found := false
	for _, p := range procs {
		if p.PID == pid {
			found = true
			in.Name = processName(p)
			in.PPID = p.ParentPID
			in.Threads = int(p.Threads)
			in.RSS = p.WorkingSet
			in.Private = p.Private
			in.CPUTime = time.Duration(p.CPUTicks) * 100
		}
	}
	if !found {
		return Info{}, errors.New("the process has exited")
	}
	for _, p := range procs {
		if in.PPID > 0 && p.PID == in.PPID {
			in.ParentName = processName(p)
		}
	}
	in.Exe = ExecutablePath(pid)

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return in, nil
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) == nil {
		in.Started = time.Unix(0, creation.Nanoseconds())
	}
	var tok windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok) == nil {
		defer tok.Close()
		if tu, err := tok.GetTokenUser(); err == nil {
			if account, domain, _, err := tu.User.Sid.LookupAccount(""); err == nil {
				in.User = account
				if domain != "" {
					in.User = domain + `\` + account
				}
			}
		}
	}
	return in, nil
}
