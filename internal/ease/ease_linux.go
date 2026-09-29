package ease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"atlas-monitor/internal/desktop"
	"atlas-monitor/internal/process"
)

// System assembles a controller for this machine: the user's own app.slice for
// CPU time, the user's service manager for weights, PipeWire for sound. It
// returns an error saying why when any of them is missing, which the Energy
// Saver page shows instead of a switch that could never do anything.
func System(ident Identity) (*Controller, error) {
	slice, err := AppSlice()
	if err != nil {
		return nil, err
	}
	w, err := newSystemdWeights()
	if err != nil {
		return nil, err
	}
	audio := &pipewireAudio{}
	if !audio.available() {
		return nil, errors.New("telling what is playing needs pw-dump or pactl, and neither is installed")
	}
	self := ""
	if b, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		if id, _, _, ok := ident(string(process.UnitFromCgroup(b))); ok {
			self = id
		}
	}
	c := New(cgroupUnits{base: slice}, w, audio, ident, self)
	recoverFromCrash(w, slice)
	c.OnChange(saveState)
	return c, nil
}

// AppSlice is where the user's applications' cgroups are, provided the
// processor controller is enabled below it — without that, a weight is
// accepted by the service manager and has no effect at all.
func AppSlice() (string, error) {
	uid := strconv.Itoa(os.Getuid())
	slice := filepath.Join("/sys/fs/cgroup/user.slice", "user-"+uid+".slice", "user@"+uid+".service", "app.slice")
	ctl, err := os.ReadFile(filepath.Join(slice, "cgroup.subtree_control"))
	if err != nil {
		return "", errors.New("applications are not started in systemd units here, so there is nothing to give a weight to")
	}
	for _, c := range strings.Fields(string(ctl)) {
		if c == "cpu" {
			return slice, nil
		}
	}
	return "", errors.New("this system does not let desktop applications' share of the processor be changed without administrator rights")
}

// cgroupUnits reads each application unit's CPU time from its cgroup.
type cgroupUnits struct{ base string }

func (u cgroupUnits) Sample() (map[string]Sample, error) {
	out := map[string]Sample{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			switch {
			case strings.HasSuffix(name, ".scope"), strings.HasSuffix(name, ".service"):
				if us, ok := cpuUsage(filepath.Join(dir, name, "cpu.stat")); ok {
					out[name] = Sample{Usage: us, Weight: cgroupWeight(filepath.Join(dir, name, "cpu.weight"))}
				}
			case strings.HasSuffix(name, ".slice") && depth == 0:
				// Some desktops sort applications into slices of their own
				// below app.slice; one level is as deep as any goes.
				walk(filepath.Join(dir, name), depth+1)
			}
		}
	}
	walk(u.base, 0)
	return out, nil
}

// cgroupWeight reads the weight the kernel is applying to a cgroup, or 0.
func cgroupWeight(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(string(bytes.TrimSpace(b)), 10, 64)
	return n
}

// cpuUsage reads usage_usec from a cgroup's cpu.stat.
func cpuUsage(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	rest, ok := bytes.CutPrefix(b, []byte("usage_usec "))
	if !ok {
		return 0, false
	}
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	n, err := strconv.ParseUint(string(rest), 10, 64)
	return n, err == nil
}

// systemdWeights sets CPUWeight through the user's service manager, as a
// runtime property: it lasts until the unit ends, and nothing is written that
// would outlive the session.
type systemdWeights struct {
	conn *dbus.Conn
	mgr  dbus.BusObject
}

func newSystemdWeights() (*systemdWeights, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, errors.New("the session bus is not reachable")
	}
	var owner string
	if conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, "org.freedesktop.systemd1").Store(&owner) != nil {
		conn.Close()
		return nil, errors.New("there is no systemd user manager on this session")
	}
	return &systemdWeights{conn: conn, mgr: conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")}, nil
}

func (s *systemdWeights) Weight(unit string) (uint64, error) {
	var path dbus.ObjectPath
	if err := s.mgr.Call("org.freedesktop.systemd1.Manager.GetUnit", 0, unit).Store(&path); err != nil {
		return 0, err
	}
	iface := "org.freedesktop.systemd1.Service"
	if strings.HasSuffix(unit, ".scope") {
		iface = "org.freedesktop.systemd1.Scope"
	}
	v, err := s.conn.Object("org.freedesktop.systemd1", path).GetProperty(iface + ".CPUWeight")
	if err != nil {
		return 0, err
	}
	w, ok := v.Value().(uint64)
	if !ok {
		return 0, fmt.Errorf("CPUWeight of %s is a %T", unit, v.Value())
	}
	return w, nil
}

func (s *systemdWeights) SetWeight(unit string, w uint64) error {
	// Belt and braces: only ever an application's unit. The controller only
	// hands these over, but a weight on the session or a system service is not
	// something to get wrong by a bug somewhere else.
	if !strings.HasPrefix(unit, "app-") {
		return fmt.Errorf("refusing to set a weight on %s, which is not an application", unit)
	}
	type prop struct {
		Name  string
		Value dbus.Variant
	}
	return s.mgr.Call("org.freedesktop.systemd1.Manager.SetUnitProperties", 0,
		unit, true, []prop{{"CPUWeight", dbus.MakeVariant(w)}}).Err
}

// pipewireAudio asks PipeWire which streams are running, and whose they are.
type pipewireAudio struct{}

func (pipewireAudio) available() bool {
	for _, tool := range []string{"pw-dump", "pactl"} {
		if _, err := exec.LookPath(tool); err == nil {
			return true
		}
	}
	return false
}

// AudibleApps finds every application with a stream running: playing,
// recording, or with the camera open, which is a call. See streamsFromPwDump
// for how a stream is traced to its process, and appsOf for how carefully.
func (p pipewireAudio) AudibleApps() (map[string]bool, error) {
	var streams []stream
	var err error
	if _, e := exec.LookPath("pw-dump"); e == nil {
		var out []byte
		if out, err = run("pw-dump"); err == nil {
			streams, err = streamsFromPwDump(out)
		}
	} else {
		streams, err = streamsFromPactl()
	}
	if err != nil {
		return nil, err
	}
	return appsOf(streams, procFS{}), nil
}

func run(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// stream is a running stream and what is known about who owns it.
type stream struct {
	// trusted is a pid the server took from the socket, where there is one.
	trusted int
	// claimed is the pid the client reported for itself, and binary the
	// program it said it was. Inside a Flatpak sandbox the pid is the
	// sandbox's own numbering and names some other process out here.
	claimed int
	binary  string
	// appID is a Flatpak application's ID, where PipeWire recorded one.
	appID string
}

// streamsFromPwDump reads the running audio and video streams out of pw-dump's
// JSON. A stream node names its client; a native PipeWire client carries the
// pid the server took from its socket, while a PulseAudio client is reached
// through pipewire-pulse and only has the pid it reported.
func streamsFromPwDump(b []byte) ([]stream, error) {
	var objs []struct {
		ID   int    `json:"id"`
		Type string `json:"type"`
		Info *struct {
			State string         `json:"state"`
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(b, &objs); err != nil {
		return nil, err
	}
	clients := map[int]map[string]any{}
	for _, o := range objs {
		if o.Type == "PipeWire:Interface:Client" && o.Info != nil {
			clients[o.ID] = o.Info.Props
		}
	}
	var out []stream
	for _, o := range objs {
		if o.Type != "PipeWire:Interface:Node" || o.Info == nil || o.Info.State != "running" {
			continue
		}
		props := o.Info.Props
		class := str(props["media.class"])
		if class != "Stream/Output/Audio" && class != "Stream/Input/Audio" && class != "Stream/Input/Video" {
			continue
		}
		s := stream{claimed: num(props["application.process.id"]), binary: str(props["application.process.binary"]),
			appID: str(props["pipewire.access.portal.app_id"])}
		if c, ok := clients[num(props["client.id"])]; ok {
			if str(c["client.api"]) != "pipewire-pulse" && str(c["application.name"]) != "pipewire-pulse" {
				s.trusted = num(c["pipewire.sec.pid"])
			}
			if s.claimed == 0 {
				s.claimed = num(c["application.process.id"])
			}
			if s.binary == "" {
				s.binary = str(c["application.process.binary"])
			}
			if s.appID == "" {
				s.appID = str(c["pipewire.access.portal.app_id"])
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// streamsFromPactl is the fallback where pw-dump is not installed: the
// PulseAudio view of the same streams, uncorked ones only.
func streamsFromPactl() ([]stream, error) {
	var out []stream
	for _, what := range []string{"sink-inputs", "source-outputs"} {
		b, err := run("pactl", "-f", "json", "list", what)
		if err != nil {
			return nil, err
		}
		var list []struct {
			Corked     bool           `json:"corked"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		for _, s := range list {
			if s.Corked {
				continue
			}
			p := s.Properties
			out = append(out, stream{claimed: num(p["application.process.id"]),
				binary: str(p["application.process.binary"]), appID: str(p["pipewire.access.portal.app_id"])})
		}
	}
	return out, nil
}

// procs is the process table as appsOf needs it; procFS reads the real one.
type procs interface {
	unit(pid int) string
	exeName(pid int) string
	// all lists every process in an application unit, as pid → unit.
	all() map[int]string
}

// appsOf maps streams to applications, erring towards more of them: a stream
// that cannot be traced for certain marks every application running a program
// of that name. Leaving an application alone that could have been eased costs
// nothing; easing the one playing music is the thing this must not do.
func appsOf(streams []stream, p procs) map[string]bool {
	out := map[string]bool{}
	mark := func(unit string) {
		if id, ok := desktop.AppID(unit); ok {
			out[id] = true
		}
	}
	var unknown []string
	for _, s := range streams {
		switch {
		case s.appID != "":
			out[s.appID] = true
		case s.trusted > 0:
			mark(p.unit(s.trusted))
		case s.claimed > 0 && s.binary != "" && p.exeName(s.claimed) == s.binary:
			mark(p.unit(s.claimed))
		case s.binary != "":
			unknown = append(unknown, s.binary)
		case s.claimed > 0:
			mark(p.unit(s.claimed))
		}
	}
	if len(unknown) > 0 {
		for pid, unit := range p.all() {
			name := p.exeName(pid)
			for _, b := range unknown {
				if name == b {
					mark(unit)
				}
			}
		}
	}
	return out
}

type procFS struct{}

func (procFS) unit(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return ""
	}
	return string(process.UnitFromCgroup(b))
}

func (procFS) exeName(pid int) string {
	exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
}

func (procFS) all() map[int]string {
	out := map[int]string{}
	slice, err := AppSlice()
	if err != nil {
		return out
	}
	usage, _ := cgroupUnits{base: slice}.Sample()
	for unit := range usage {
		matches, _ := filepath.Glob(filepath.Join(slice, "*", unit, "cgroup.procs"))
		matches = append(matches, filepath.Join(slice, unit, "cgroup.procs"))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			for _, f := range strings.Fields(string(b)) {
				if pid, err := strconv.Atoi(f); err == nil {
					out[pid] = unit
				}
			}
		}
	}
	return out
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

// The crash-recovery file: which units Atlas eased automatically, and what to
// put back. Kept in the runtime directory, which lives exactly as long as the
// session — and so exactly as long as the eases it describes.
func stateFile() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "atlas-monitor", "eased.json")
}

func saveState(eased map[string]uint64) {
	path := stateFile()
	if path == "" {
		return
	}
	if len(eased) == 0 {
		os.Remove(path)
		return
	}
	b, err := json.Marshal(eased)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		os.WriteFile(path, b, 0o600)
	}
}

// recoverFromCrash puts back what a previous Atlas eased and did not live to
// restore. A unit that has since ended needs nothing; one whose weight is no
// longer ours has been changed by somebody else and is left as they set it.
func recoverFromCrash(w Weights, slice string) {
	path := stateFile()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var eased map[string]uint64
	if json.Unmarshal(b, &eased) == nil {
		for unit, prev := range eased {
			if cur, err := w.Weight(unit); err == nil && cur == EasedWeight {
				w.SetWeight(unit, prev)
			}
		}
	}
	os.Remove(path)
}
