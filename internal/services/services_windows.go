package services

import (
	"errors"
	"fmt"
	"sort"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

// Services on Windows: the Service Control Manager.
//
// The important difference from systemd is about permission. systemd is asked to
// start or stop something and hands the decision to polkit, which prompts the
// user; the Service Control Manager checks the rights on the handle and refuses
// if they are not there. Atlas does not run elevated and should not ask to, so
// this lists and reports faithfully and says plainly that it cannot change
// anything — rather than offering buttons that fail.
//
// The connection is therefore opened read-only. mgr.Connect in x/sys asks for
// SC_MANAGER_ALL_ACCESS, which fails outright for a normal user, so the manager
// is opened here with the two rights that listing actually needs.

// Client is a read-only connection to the Service Control Manager.
type Client struct {
	m *mgr.Mgr
}

// errNeedsAdmin is what every changing operation returns. It names the reason
// rather than passing along "access is denied", which on its own tells the user
// nothing about what to do.
var errNeedsAdmin = errors.New("changing a Windows service needs administrator rights, " +
	"which Atlas does not have — use Services in Windows, or the sc command from an " +
	"elevated prompt")

// NewClient opens the Service Control Manager for reading.
func NewClient() (*Client, error) {
	h, err := windows.OpenSCManager(nil, nil,
		windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, fmt.Errorf("opening the service manager: %w", err)
	}
	return &Client{m: &mgr.Mgr{Handle: h}}, nil
}

// Close releases the connection.
func (c *Client) Close() {
	if c == nil || c.m == nil {
		return
	}
	c.m.Disconnect()
	c.m = nil
}

// enumerate returns every Win32 service with its current status, in one call.
//
// This is the cheap path, and the only one the alert badge uses: it asks the
// manager for all of them at once rather than opening each service in turn. The
// buffer is sized by asking, which is how every Windows enumeration works.
func (c *Client) enumerate() ([]windows.ENUM_SERVICE_STATUS_PROCESS, error) {
	if c == nil || c.m == nil {
		return nil, errors.New("not connected")
	}
	var bytesNeeded, returned uint32
	var buf []byte
	for {
		var p *byte
		if len(buf) > 0 {
			p = &buf[0]
		}
		err := windows.EnumServicesStatusEx(c.m.Handle, windows.SC_ENUM_PROCESS_INFO,
			windows.SERVICE_WIN32, windows.SERVICE_STATE_ALL,
			p, uint32(len(buf)), &bytesNeeded, &returned, nil, nil)
		if err == nil {
			break
		}
		if err != syscall.ERROR_MORE_DATA {
			return nil, err
		}
		if bytesNeeded <= uint32(len(buf)) {
			return nil, err // it wants more but not more than we gave: give up
		}
		buf = make([]byte, bytesNeeded)
	}
	if returned == 0 {
		return nil, nil
	}
	// The strings the entries point at live in buf, so it has to outlive them —
	// which it does: the slice aliases it and the caller copies what it keeps.
	return unsafe.Slice(
		(*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), int(returned)), nil
}

// Failed returns the names of services that look like they have gone wrong.
//
// Windows has no "failed" state. A service that could not start, or that stopped
// because it crashed, is simply stopped — with a non-zero exit code, which is the
// only evidence there is. So that is what this looks for, and it deliberately
// ignores the code that means "this has never been started", which is the
// ordinary state of a great many services on a healthy machine.
func (c *Client) Failed() ([]string, error) {
	list, err := c.enumerate()
	if err != nil {
		return nil, err
	}
	var out []string
	for i := range list {
		s := &list[i]
		if statusOfWindows(&s.ServiceStatusProcess) == Failed {
			out = append(out, windows.UTF16PtrToString(s.ServiceName))
		}
	}
	sort.Strings(out)
	return out, nil
}

// List returns the services for the list view.
//
// servicesOnly is accepted for the shared interface but changes nothing here: the
// enumeration already asks for SERVICE_WIN32, so drivers are never in the answer.
// Windows has no equivalent of the timers, sockets and mounts that systemd also
// calls units.
//
// The start-up type is not part of the enumeration, so it takes one query per
// service. That is a few hundred calls against a local database, which is fine for
// something a person opened a page to see — but it is why Failed above does not
// use this.
func (c *Client) List(servicesOnly bool) ([]Service, error) {
	list, err := c.enumerate()
	if err != nil {
		return nil, err
	}

	out := make([]Service, 0, len(list))
	for i := range list {
		e := &list[i]
		name := windows.UTF16PtrToString(e.ServiceName)
		if name == "" {
			continue
		}
		s := Service{
			Name:        name,
			Description: windows.UTF16PtrToString(e.DisplayName),
			Active:      activeOfWindows(e.ServiceStatusProcess.CurrentState),
			Status:      statusOfWindows(&e.ServiceStatusProcess),
			Enabled:     c.startType(name),
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// startType reads how a service is set to start.
//
// Some services will not say. WaaSMedicSvc is the one that found this: it is
// protected, so even opening it for a configuration query is refused, and the
// column came back blank — which reads as though Atlas forgot to fill it in rather
// than as the machine declining to answer. "Unknown" is the honest word for it, and
// startupLabel passes anything it does not recognise through unchanged.
func (c *Client) startType(name string) string {
	s, err := c.openService(name, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return "Unknown"
	}
	defer s.Close()

	cfg, err := s.Config()
	if err != nil {
		return "Unknown"
	}
	switch cfg.StartType {
	case windows.SERVICE_BOOT_START:
		return "At boot"
	case windows.SERVICE_SYSTEM_START:
		return "At startup"
	case windows.SERVICE_AUTO_START:
		if cfg.DelayedAutoStart {
			return "On, delayed"
		}
		// The vocabulary startupLabel already knows, so it reads as "On".
		return "enabled"
	case windows.SERVICE_DEMAND_START:
		// systemd's "static" shows as "As needed", which is exactly what a
		// demand-start service is.
		return "static"
	case windows.SERVICE_DISABLED:
		return "disabled"
	default:
		return "Unknown"
	}
}

// openService opens one service with the given rights.
func (c *Client) openService(name string, access uint32) (*mgr.Service, error) {
	if c == nil || c.m == nil {
		return nil, errors.New("not connected")
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenService(c.m.Handle, p, access)
	if err != nil {
		return nil, err
	}
	return &mgr.Service{Name: name, Handle: h}, nil
}

// activeOfWindows maps a service state onto the word systemd would use, because
// that is the vocabulary the UI reads.
func activeOfWindows(state uint32) string {
	switch state {
	case windows.SERVICE_RUNNING:
		return "active"
	case windows.SERVICE_START_PENDING:
		return "activating"
	case windows.SERVICE_STOP_PENDING:
		return "deactivating"
	case windows.SERVICE_PAUSED, windows.SERVICE_PAUSE_PENDING,
		windows.SERVICE_CONTINUE_PENDING:
		return "paused"
	default:
		return "inactive"
	}
}

// errServiceNeverStarted is the exit code a service carries when it has simply
// never run. It is not a failure, and on a normal machine most stopped services
// have it.
const errServiceNeverStarted = 1077

// statusOfWindows derives the coloured dot.
func statusOfWindows(st *windows.SERVICE_STATUS_PROCESS) Status {
	if st.CurrentState == windows.SERVICE_RUNNING ||
		st.CurrentState == windows.SERVICE_START_PENDING {
		return Running
	}
	// Stopped with something to say about why.
	if st.Win32ExitCode != 0 && st.Win32ExitCode != errServiceNeverStarted {
		return Failed
	}
	if st.ServiceSpecificExitCode != 0 {
		return Failed
	}
	return Stopped
}

// Start, Stop, Restart, Enable and Disable all need administrator rights.
//
// They are not attempted and then reported as denied: the handle would have to be
// opened with rights a normal user cannot get, so the answer is known before
// asking. Saying so up front is better than a round trip that ends in
// "access is denied".

func (c *Client) Start(name string) error   { return errNeedsAdmin }
func (c *Client) Stop(name string) error    { return errNeedsAdmin }
func (c *Client) Restart(name string) error { return errNeedsAdmin }
func (c *Client) Enable(name string) error  { return errNeedsAdmin }
func (c *Client) Disable(name string) error { return errNeedsAdmin }

// CanControl reports whether the changing operations above would work. The
// Services page uses it to leave the buttons out rather than show ones that
// cannot do anything.
func (c *Client) CanControl() bool { return false }
