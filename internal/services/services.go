// Package services lists and controls the machine's background services.
//
// The two platforms have nothing in common underneath. Linux talks to systemd
// over D-Bus; Windows talks to the Service Control Manager. What they share is
// the shape of the answer, which is this file: a list of named services, each
// with a run state and a start-up setting.
//
// The one thing worth knowing about the Windows side is that it is read-only
// unless Atlas is running elevated. Starting, stopping and changing a service's
// start-up type all need administrator rights there, where systemd hands the
// question to polkit and lets the user authenticate. See services_windows.go.
package services

// Status is the derived run state shown as a coloured dot.
type Status int

const (
	Stopped Status = iota
	Running
	Failed
)

// Service is one service for the list view.
//
// Active and Sub are systemd's two state strings. Windows has one state, so it
// fills Active and leaves Sub empty — which statusOf on each side accounts for.
// Enabled is the start-up setting, in the vocabulary startupLabel in the UI knows
// how to word; anything it does not recognise is shown as it stands.
type Service struct {
	Name        string
	Description string
	Active      string // ActiveState: active / inactive / failed
	Sub         string // SubState: running / dead / exited
	Enabled     string // enabled / disabled / static / ...
	Status      Status
}
