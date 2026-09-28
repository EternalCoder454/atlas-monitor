package autostart

import (
	"strings"
	"testing"
)

// TestApprovalBitDecidesEnabled: Windows records whether a startup entry is
// switched off in the low bit of the first byte. Reading it backwards would show
// every enabled program as disabled — and the switches would then all be in the
// wrong position, which looks like a working feature rather than a bug.
func TestApprovalBitDecidesEnabled(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []byte
		want bool
	}{
		{"the enabled value Windows writes", []byte{0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false},
		{"the disabled value Windows writes", []byte{0x03, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, true},
		// Higher bits set, which does occur: only the low one is the flag.
		{"enabled with other bits set", []byte{0x06}, false},
		{"disabled with other bits set", []byte{0x07}, true},
		{"nothing recorded", nil, false},
		{"empty value", []byte{}, false},
	} {
		if got := isDisabled(c.in); got != c.want {
			t.Errorf("%s: isDisabled(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// TestSetEnabledRefusesOnlyTheMachineRegistry: what decides whether this user can
// change an entry is where its approval lives, not where the entry does. A shared
// Startup folder shortcut is approved per-user, so it can be switched off; a
// machine-wide Run value cannot.
func TestSetEnabledRefusesOnlyTheMachineRegistry(t *testing.T) {
	machineRun := Entry{System: true, Name: "Thing", approvalKey: runApprovedKey}
	if err := SetEnabled(machineRun, false); err == nil {
		t.Error("a machine-wide Run entry was accepted")
	} else if !strings.Contains(err.Error(), "administrator") {
		t.Errorf("%q does not say what is missing", err)
	}

	// An entry with no approval setting at all is refused too, but for its own
	// reason rather than by claiming a permission problem.
	if err := SetEnabled(Entry{Name: "Thing"}, false); err == nil {
		t.Error("an entry with no approval key was accepted")
	} else if strings.Contains(err.Error(), "administrator") {
		t.Errorf("%q blames permissions for a missing approval key", err)
	}
}

// TestStartupDirsAreUnderTheRightRoots: the per-user folder belongs under APPDATA
// and the shared one under PROGRAMDATA. Swapping them would list one twice and the
// other never.
func TestStartupDirsAreUnderTheRightRoots(t *testing.T) {
	t.Setenv("APPDATA", `C:\Users\someone\AppData\Roaming`)
	t.Setenv("PROGRAMDATA", `C:\ProgramData`)

	user, shared := startupDir(false), startupDir(true)
	if !strings.HasPrefix(user, `C:\Users\someone\AppData\Roaming`) {
		t.Errorf("user startup dir = %q", user)
	}
	if !strings.HasPrefix(shared, `C:\ProgramData`) {
		t.Errorf("shared startup dir = %q", shared)
	}
	for _, d := range []string{user, shared} {
		if !strings.HasSuffix(d, `Start Menu\Programs\Startup`) {
			t.Errorf("%q does not end at the Startup folder", d)
		}
	}

	// With the variable missing there is no folder to read, and "" is what
	// folderEntries skips on.
	t.Setenv("APPDATA", "")
	if got := startupDir(false); got != "" {
		t.Errorf("with APPDATA unset, got %q, want empty", got)
	}
}
