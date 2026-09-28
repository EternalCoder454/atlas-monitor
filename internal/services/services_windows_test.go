package services

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestFailedNeedsMoreThanBeingStopped is the whole difficulty of this mapping.
//
// Windows has no failed state. Most stopped services on a healthy machine have
// simply never run, and treating "stopped" as failed would light the alert badge
// permanently — which is the same class of mistake as the swap warning that fired
// on a machine with 23 GiB free.
func TestFailedNeedsMoreThanBeingStopped(t *testing.T) {
	for _, c := range []struct {
		name string
		st   windows.SERVICE_STATUS_PROCESS
		want Status
	}{
		{"running", windows.SERVICE_STATUS_PROCESS{CurrentState: windows.SERVICE_RUNNING}, Running},
		{"starting", windows.SERVICE_STATUS_PROCESS{CurrentState: windows.SERVICE_START_PENDING}, Running},
		{
			"stopped, never started",
			windows.SERVICE_STATUS_PROCESS{
				CurrentState:  windows.SERVICE_STOPPED,
				Win32ExitCode: errServiceNeverStarted,
			},
			Stopped,
		},
		{
			"stopped cleanly",
			windows.SERVICE_STATUS_PROCESS{CurrentState: windows.SERVICE_STOPPED},
			Stopped,
		},
		{
			"stopped with an error",
			windows.SERVICE_STATUS_PROCESS{CurrentState: windows.SERVICE_STOPPED, Win32ExitCode: 5},
			Failed,
		},
		{
			"stopped with its own error",
			windows.SERVICE_STATUS_PROCESS{
				CurrentState:            windows.SERVICE_STOPPED,
				ServiceSpecificExitCode: 42,
			},
			Failed,
		},
	} {
		st := c.st
		if got := statusOfWindows(&st); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestActiveUsesSystemdsVocabulary: the UI reads these strings, so a near-miss
// word silently changes what the page says.
func TestActiveUsesSystemdsVocabulary(t *testing.T) {
	for _, c := range []struct {
		state uint32
		want  string
	}{
		{windows.SERVICE_RUNNING, "active"},
		{windows.SERVICE_STOPPED, "inactive"},
		{windows.SERVICE_START_PENDING, "activating"},
		{windows.SERVICE_STOP_PENDING, "deactivating"},
		{windows.SERVICE_PAUSED, "paused"},
	} {
		if got := activeOfWindows(c.state); got != c.want {
			t.Errorf("state %d: got %q, want %q", c.state, got, c.want)
		}
	}
}

// TestControlSaysWhyItCannot: the five changing operations refuse before asking,
// and the message has to tell the user what to do instead of just "denied".
func TestControlSaysWhyItCannot(t *testing.T) {
	var c *Client
	for name, err := range map[string]error{
		"Start": c.Start("x"), "Stop": c.Stop("x"), "Restart": c.Restart("x"),
		"Enable": c.Enable("x"), "Disable": c.Disable("x"),
	} {
		if err == nil {
			t.Errorf("%s reported success without administrator rights", name)
			continue
		}
		if !strings.Contains(err.Error(), "administrator") {
			t.Errorf("%s: %q does not say what is missing", name, err)
		}
	}
	if c.CanControl() {
		t.Error("CanControl said yes")
	}
}
