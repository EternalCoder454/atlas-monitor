module atlas-monitor

go 1.24.0

require (
	github.com/diamondburned/gotk4-adwaita/pkg v0.0.0-20260808200908-d4aecaa0ff32
	github.com/diamondburned/gotk4/pkg v0.4.1
	github.com/godbus/dbus/v5 v5.2.2
	golang.org/x/sys v0.27.0
)

require golang.org/x/sync v0.0.0-20210220032951-036812b2e83c // indirect

replace github.com/diamondburned/gotk4-adwaita/pkg => ./third_party/gotk4-adwaita
