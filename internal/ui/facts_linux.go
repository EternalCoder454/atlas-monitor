package ui

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// The machine's own description on Linux, for the assistant's context.

// osName is the distribution's pretty name.
func osName() string { return osReleaseName() }

// kernelVersion is the running kernel's release string.
func kernelVersion() string {
	k, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(k))
}

// userName is who is logged in.
func userName() string { return os.Getenv("USER") }

// uptime is how long the machine has been running.
func uptime() (time.Duration, bool) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0, false
	}
	secs, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// loadAverage is the 1/5/15-minute figures, already formatted.
func loadAverage() string {
	la, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(la))
	if len(f) < 3 {
		return ""
	}
	return f[0] + " " + f[1] + " " + f[2] + " (1/5/15 min)"
}
func osReleaseName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, "\"")
		}
	}
	return ""
}
