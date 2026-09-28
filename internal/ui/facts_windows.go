package ui

import (
	"os"
	"strconv"
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
	return strconv.Itoa(int(major)) + "." + strconv.Itoa(int(minor)) + "." + strconv.Itoa(int(build))
}

// userName is who is logged in. Windows sets USERNAME rather than USER.
func userName() string { return os.Getenv("USERNAME") }

// GetTickCount64 is not wrapped by x/sys/windows, so it is bound here. It has been
// present since Vista; the 32-bit GetTickCount it replaced wrapped after 49 days,
// which is exactly the kind of uptime worth reporting correctly.
var (
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procGetTickCount64 = kernel32.NewProc("GetTickCount64")
)

// uptime is how long the machine has been running, from the tick count — which is
// unaffected by the clock being changed, unlike a boot-time subtraction.
func uptime() (time.Duration, bool) {
	ms, _, _ := procGetTickCount64.Call()
	if ms == 0 {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

// loadAverage has no Windows equivalent. See the note above.
func loadAverage() string { return "" }
