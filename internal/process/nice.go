package process

import (
	"os"
	"strconv"
	"syscall"
)

// Priority, for the Energy Saver page.
//
// Linux lets any process lower its own priority, and lets you lower another of
// your own. Raising one back again needs CAP_SYS_NICE, which Atlas does not
// have and should not ask for — so easing a program off is a one-way door until
// it restarts, and the page says so rather than offering a switch that would
// fail on the way back.

// EasedNice is where "ease off" puts a program: clearly behind everything at
// the default of zero, without starving it the way 19 would.
const EasedNice = 10

// Nice reads a process's current priority. Field 19 of /proc/[pid]/stat, which
// is after the comm field, so the line is split from the last ')' as elsewhere.
func Nice(pid int) (int, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	close := lastIndexByte(b, ')')
	if close < 0 || close+2 >= len(b) {
		return 0, false
	}
	// After "pid (comm) " the fields are state, ppid, pgrp, session, tty_nr,
	// tpgid, flags, minflt, cminflt, majflt, cmajflt, utime, stime, cutime,
	// cstime, priority, nice — nice being the seventeenth of them.
	rest := b[close+2:]
	field, start := 0, 0
	for i := 0; i <= len(rest); i++ {
		if i == len(rest) || rest[i] == ' ' {
			field++
			if field == 17 {
				n, err := strconv.Atoi(string(rest[start:i]))
				return n, err == nil
			}
			start = i + 1
		}
	}
	return 0, false
}

// SetNice changes a process's priority. Raising it again will fail without
// privileges, which is why the caller only ever lowers.
func SetNice(pid, nice int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, pid, nice)
}

func lastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}
