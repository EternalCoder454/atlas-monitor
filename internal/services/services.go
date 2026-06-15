// Package services is a thin systemd client over the system D-Bus
// (org.freedesktop.systemd1). Privileged actions (start/stop/enable/disable)
// go through systemd, which enforces polkit authentication.
package services

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	dest    = "org.freedesktop.systemd1"
	objPath = "/org/freedesktop/systemd1"
	mgrIf   = "org.freedesktop.systemd1.Manager"
)

// Status is the derived run state shown as a coloured dot.
type Status int

const (
	Stopped Status = iota
	Running
	Failed
)

// Service is one systemd unit for the list view.
type Service struct {
	Name        string
	Description string
	Active      string // ActiveState: active / inactive / failed
	Sub         string // SubState: running / dead / exited
	Enabled     string // enabled / disabled / static / ...
	Status      Status
}

// Client wraps a system-bus connection to systemd.
type Client struct {
	conn *dbus.Conn
	mgr  dbus.BusObject
}

// dbusUnit mirrors the struct returned by Manager.ListUnits (a(ssssssouso)).
type dbusUnit struct {
	Name        string
	Description string
	LoadState   string
	ActiveState string
	SubState    string
	Followed    string
	Path        dbus.ObjectPath
	JobID       uint32
	JobType     string
	JobPath     dbus.ObjectPath
}

// dbusUnitFile mirrors a row of Manager.ListUnitFiles (a(ss)).
type dbusUnitFile struct {
	Path  string
	State string
}

// NewClient connects to the system bus.
func NewClient() (*Client, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, mgr: conn.Object(dest, objPath)}, nil
}

// Close releases the bus connection.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// List returns the units. When servicesOnly is true, only *.service units are
// returned; otherwise all unit types are included.
func (c *Client) List(servicesOnly bool) ([]Service, error) {
	var units []dbusUnit
	if err := c.mgr.Call(mgrIf+".ListUnits", 0).Store(&units); err != nil {
		return nil, err
	}
	enabled := c.unitFileStates()

	out := make([]Service, 0, len(units))
	for _, u := range units {
		if servicesOnly && !strings.HasSuffix(u.Name, ".service") {
			continue
		}
		s := Service{
			Name:        u.Name,
			Description: u.Description,
			Active:      u.ActiveState,
			Sub:         u.SubState,
			Enabled:     enabled[u.Name],
			Status:      statusOf(u.ActiveState, u.SubState),
		}
		if s.Enabled == "" {
			s.Enabled = "—"
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// unitFileStates maps unit name -> install state (enabled/disabled/static).
func (c *Client) unitFileStates() map[string]string {
	var files []dbusUnitFile
	if err := c.mgr.Call(mgrIf+".ListUnitFiles", 0).Store(&files); err != nil {
		return map[string]string{}
	}
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[filepath.Base(f.Path)] = f.State
	}
	return m
}

func statusOf(active, sub string) Status {
	switch {
	case active == "failed" || sub == "failed":
		return Failed
	case active == "active" && sub == "running":
		return Running
	default:
		return Stopped
	}
}

// Start, Stop, Restart, Enable, Disable apply privileged operations. systemd
// prompts for polkit authorization when the caller is unprivileged.

func (c *Client) Start(name string) error {
	return c.mgr.Call(mgrIf+".StartUnit", 0, name, "replace").Err
}

func (c *Client) Stop(name string) error {
	return c.mgr.Call(mgrIf+".StopUnit", 0, name, "replace").Err
}

func (c *Client) Restart(name string) error {
	return c.mgr.Call(mgrIf+".RestartUnit", 0, name, "replace").Err
}

func (c *Client) Enable(name string) error {
	return c.mgr.Call(mgrIf+".EnableUnitFiles", 0, []string{name}, false, true).Err
}

func (c *Client) Disable(name string) error {
	return c.mgr.Call(mgrIf+".DisableUnitFiles", 0, []string{name}, false).Err
}
