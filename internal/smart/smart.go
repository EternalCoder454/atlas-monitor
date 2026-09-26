// Package smart reads a drive's own opinion of its condition.
//
// The battery page has said "75% of original · worn" for a while, which is the
// most useful line on it: the figure that tells you whether to replace the
// thing. Disks had capacity and throughput and nothing about wear, even though
// every SSD keeps exactly that number.
//
// It comes from udisks2 over the system bus rather than from the device
// directly. Reading SMART from a block device needs root; udisks2 is already
// running on any desktop, already has the privilege, and hands the values over
// for free. A machine without it simply has no health section.
package smart

import (
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	dest       = "org.freedesktop.UDisks2"
	blockPath  = "/org/freedesktop/UDisks2/block_devices/"
	propsIf    = "org.freedesktop.DBus.Properties"
	blockIf    = "org.freedesktop.UDisks2.Block"
	driveIf    = "org.freedesktop.UDisks2.Drive"
	nvmeIf     = "org.freedesktop.UDisks2.NVMe.Controller"
	ataIf      = "org.freedesktop.UDisks2.Drive.Ata"
	kelvinZero = 273.15
)

// Health is what a drive says about itself. Fields that the drive does not
// report are left at zero and Has* says so, because "0 °C" and "no reading" are
// very different things to put on a page.
type Health struct {
	// Wear is how much of the drive's rated life is gone, 0..100. NVMe reports
	// it directly; ATA drives mostly do not.
	Wear    int
	HasWear bool

	// Spare is the percentage of replacement blocks left, and SpareLow is true
	// once the drive itself considers that too few.
	Spare    int
	HasSpare bool
	SpareLow bool

	TemperatureC   float64
	HasTemperature bool

	PowerOnHours uint64
	WrittenBytes uint64
	PowerCycles  uint64

	UnsafeShutdowns uint64
	MediaErrors     uint64

	// Failing is the drive's own verdict: it expects to fail. Warnings carries
	// whatever it said alongside.
	Failing  bool
	Warnings []string
}

// Client talks to udisks2. A nil Client reports nothing, so callers on a
// machine without it need no special case.
type Client struct {
	conn *dbus.Conn
}

// New connects to the system bus. It returns nil when udisks2 is not there.
func New() *Client {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil
	}
	c := &Client{conn: conn}
	// A name check rather than a call: asking a bus that has nobody listening
	// blocks until it times out.
	var owned bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, dest).Store(&owned)
	if err != nil || !owned {
		return nil
	}
	return c
}

// Read returns what the drive behind a kernel device name says about itself.
// The second value is false when there is nothing to report, which is the
// ordinary case for a USB stick, a loop device or an older disk.
func (c *Client) Read(device string) (Health, bool) {
	if c == nil || c.conn == nil {
		return Health{}, false
	}
	drive, ok := c.driveOf(device)
	if !ok {
		return Health{}, false
	}

	obj := c.conn.Object(dest, drive)
	h := Health{}
	found := false

	// NVMe first: it is what anything recent is, and it reports wear directly.
	if warn, ok := c.stringsProp(obj, nvmeIf, "SmartCriticalWarning"); ok {
		found = true
		h.Warnings = warn
		h.Failing = len(warn) > 0
		if v, ok := c.uint64Prop(obj, nvmeIf, "SmartPowerOnHours"); ok {
			h.PowerOnHours = v
		}
		if v, ok := c.uint16Prop(obj, nvmeIf, "SmartTemperature"); ok && v > 0 {
			h.TemperatureC, h.HasTemperature = float64(v)-kelvinZero, true
		}
		c.readNVMeAttributes(obj, &h)
	}

	// ATA drives report a pass/fail verdict and a temperature, and little else
	// without privileges.
	if failing, ok := c.boolProp(obj, ataIf, "SmartFailing"); ok {
		found = true
		h.Failing = h.Failing || failing
		if v, ok := c.float64Prop(obj, ataIf, "SmartTemperature"); ok && v > 0 {
			h.TemperatureC, h.HasTemperature = v-kelvinZero, true
		}
		if v, ok := c.uint64Prop(obj, ataIf, "SmartPowerOnSeconds"); ok {
			h.PowerOnHours = v / 3600
		}
	}
	return h, found
}

// readNVMeAttributes pulls the detail out of the SMART log.
func (c *Client) readNVMeAttributes(obj dbus.BusObject, h *Health) {
	var attrs map[string]dbus.Variant
	if err := obj.Call(nvmeIf+".SmartGetAttributes", 0, map[string]dbus.Variant{}).Store(&attrs); err != nil {
		return
	}
	if v, ok := attrs["percent_used"]; ok {
		if n, ok := asUint(v); ok {
			h.Wear, h.HasWear = int(n), true
			if h.Wear > 100 {
				h.Wear = 100 // a drive past its rating keeps counting
			}
		}
	}
	if v, ok := attrs["avail_spare"]; ok {
		if n, ok := asUint(v); ok {
			h.Spare, h.HasSpare = int(n), true
		}
	}
	if v, ok := attrs["spare_thresh"]; ok {
		if n, ok := asUint(v); ok && h.HasSpare && h.Spare <= int(n) {
			h.SpareLow = true
		}
	}
	for key, field := range map[string]*uint64{
		"total_data_written": &h.WrittenBytes,
		"power_cycles":       &h.PowerCycles,
		"unsafe_shutdowns":   &h.UnsafeShutdowns,
		"media_errors":       &h.MediaErrors,
	} {
		if v, ok := attrs[key]; ok {
			if n, ok := asUint(v); ok {
				*field = n
			}
		}
	}
}

// driveOf maps a kernel device name onto the udisks2 drive behind it.
func (c *Client) driveOf(device string) (dbus.ObjectPath, bool) {
	device = strings.TrimPrefix(device, "/dev/")
	obj := c.conn.Object(dest, dbus.ObjectPath(blockPath+device))
	var v dbus.Variant
	if err := obj.Call(propsIf+".Get", 0, blockIf, "Drive").Store(&v); err != nil {
		return "", false
	}
	path, ok := v.Value().(dbus.ObjectPath)
	if !ok || path == "" || path == "/" {
		return "", false
	}
	return path, true
}

func (c *Client) prop(obj dbus.BusObject, iface, name string) (dbus.Variant, bool) {
	var v dbus.Variant
	if err := obj.Call(propsIf+".Get", 0, iface, name).Store(&v); err != nil {
		return dbus.Variant{}, false
	}
	return v, true
}

func (c *Client) stringsProp(obj dbus.BusObject, iface, name string) ([]string, bool) {
	v, ok := c.prop(obj, iface, name)
	if !ok {
		return nil, false
	}
	s, ok := v.Value().([]string)
	return s, ok
}

func (c *Client) boolProp(obj dbus.BusObject, iface, name string) (bool, bool) {
	v, ok := c.prop(obj, iface, name)
	if !ok {
		return false, false
	}
	b, ok := v.Value().(bool)
	return b, ok
}

func (c *Client) uint64Prop(obj dbus.BusObject, iface, name string) (uint64, bool) {
	v, ok := c.prop(obj, iface, name)
	if !ok {
		return 0, false
	}
	return asUint(v)
}

func (c *Client) uint16Prop(obj dbus.BusObject, iface, name string) (uint16, bool) {
	v, ok := c.prop(obj, iface, name)
	if !ok {
		return 0, false
	}
	n, ok := asUint(v)
	return uint16(n), ok
}

func (c *Client) float64Prop(obj dbus.BusObject, iface, name string) (float64, bool) {
	v, ok := c.prop(obj, iface, name)
	if !ok {
		return 0, false
	}
	f, ok := v.Value().(float64)
	return f, ok
}

// asUint accepts whichever width the property happens to use: udisks2 reports
// bytes, uint16s and uint64s across the same set of attributes.
func asUint(v dbus.Variant) (uint64, bool) {
	switch n := v.Value().(type) {
	case uint8:
		return uint64(n), true
	case uint16:
		return uint64(n), true
	case uint32:
		return uint64(n), true
	case uint64:
		return n, true
	case int16:
		return uint64(n), true
	case int32:
		return uint64(n), true
	case int64:
		return uint64(n), true
	default:
		return 0, false
	}
}
