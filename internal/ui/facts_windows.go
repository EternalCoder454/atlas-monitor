package ui

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The machine's own description on Windows, for the assistant's context.
//
// There is no load average here, and that is not a gap to fill: Windows does not
// have the concept, and inventing something from processor usage would be handing
// the assistant a number that does not mean what it says. The line is left out.

// currentVersionKey is where Windows records what it is.
const currentVersionKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`

// osName is the edition, with the release it is on — "Windows 11 Pro 24H2".
func osName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, currentVersionKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()

	name, _, err := k.GetStringValue("ProductName")
	if err != nil {
		return ""
	}
	// ProductName still says "Windows 10" on Windows 11, which Microsoft has not
	// changed; the build number is what actually distinguishes them, and
	// kernelVersion carries it. DisplayVersion is the marketing release, and is
	// the more useful half of the answer here.
	if v, _, err := k.GetStringValue("DisplayVersion"); err == nil && v != "" {
		return name + " " + v
	}
	return name
}

// kernelVersion is the build, which is what identifies a Windows release
// unambiguously.
func kernelVersion() string {
	major, minor, build := windows.RtlGetNtVersionNumbers()
	return itoa(int(major)) + "." + itoa(int(minor)) + "." + itoa(int(build))
}

// userName is who is logged in. Windows sets USERNAME rather than USER.
func userName() string { return os.Getenv("USERNAME") }

// uptime is how long the machine has been running, from the tick count — which is
// unaffected by the clock being changed, unlike a boot-time subtraction.
func uptime() (time.Duration, bool) {
	ms := windows.GetTickCount64()
	if ms == 0 {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

// loadAverage has no Windows equivalent. See the note above.
func loadAverage() string { return "" }

// itoa keeps this file from importing strconv for three small numbers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
