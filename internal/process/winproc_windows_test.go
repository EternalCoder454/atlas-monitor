package process

import "atlas-monitor/internal/winapi"

// winProc builds a process record for the tests.
func winProc(name string, pid int) winapi.Process {
	return winapi.Process{PID: pid, Name: name}
}
