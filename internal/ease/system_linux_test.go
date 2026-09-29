package ease

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"atlas-monitor/internal/desktop"
)

// TestAgainstThisSession drives the real pieces — the cgroup tree, the user's
// service manager, PipeWire — with a throwaway application unit that plays
// silence. It is skipped wherever any of them is missing, which includes CI.
func TestAgainstThisSession(t *testing.T) {
	slice, err := AppSlice()
	if err != nil {
		t.Skip(err)
	}
	w, err := newSystemdWeights()
	if err != nil {
		t.Skip(err)
	}
	player, err := exec.LookPath("paplay")
	if err != nil {
		t.Skip("no paplay to make a stream with")
	}
	unit := "app-atlastest-" + strconv.Itoa(os.Getpid()) + ".scope"
	cmd := exec.Command("systemd-run", "--user", "--scope", "-q", "-u", unit,
		player, "--raw", "--rate", "48000", "--channels", "2", "--format", "s16le", "/dev/zero")
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start a scope:", err)
	}
	t.Cleanup(func() {
		exec.Command("systemctl", "--user", "stop", unit).Run()
		cmd.Wait()
	})
	cgroup := filepath.Join(slice, unit)
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(cgroup); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	usage, err := cgroupUnits{base: slice}.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := usage[unit]; !ok {
		t.Fatalf("the test unit is not among the %d units read", len(usage))
	}

	// Whatever it reads now is what has to come back. It is not asserted: an
	// untouched unit reads as unset on some systemd versions and as 100 on
	// others, and on Fedora uresourced may already have raised it.
	before, err := w.Weight(unit)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetWeight(unit, EasedWeight); err != nil {
		t.Fatal(err)
	}
	if got := readWeight(t, cgroup); got != "10" {
		t.Fatalf("cpu.weight after easing = %s, want 10", got)
	}
	if err := w.SetWeight(unit, before); err != nil {
		t.Fatal(err)
	}
	if after, err := w.Weight(unit); err != nil || after != before {
		t.Fatalf("weight after restoring = %d, %v; want the %d it had", after, err, before)
	}
	if err := w.SetWeight("pipewire.service", EasedWeight); err == nil {
		t.Fatal("a weight was set on a unit that is not an application's")
	}

	// And the stream: paplay speaks PulseAudio, so this is the path that has to
	// check the pid it reports against the program it names.
	var aud map[string]bool
	for i := 0; i < 30; i++ {
		if aud, err = (pipewireAudio{}).AudibleApps(); err == nil && aud["atlastest"] {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !aud["atlastest"] {
		t.Fatalf("the playing test unit was not found audible: %v", aud)
	}
	if id, ok := desktop.AppID(unit); !ok || id != "atlastest" {
		t.Fatalf("AppID(%s) = %q", unit, id)
	}
}

func readWeight(t *testing.T, cgroup string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cgroup, "cpu.weight"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b[:len(b)-1])
}

// BenchmarkSample is one background sample on this machine: every application
// unit's CPU time and weight. It runs every five seconds whether or not Atlas's
// window is open, so this is what Energy Saver costs while nobody is looking.
func BenchmarkSample(b *testing.B) {
	slice, err := AppSlice()
	if err != nil {
		b.Skip(err)
	}
	u := cgroupUnits{base: slice}
	s, _ := u.Sample()
	b.Logf("%d units", len(s))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		u.Sample()
	}
}

// BenchmarkTick is a whole controller tick over this machine's units, with the
// identity lookups, minus the service manager and the sound check, which only
// happen when something is busy.
func BenchmarkTick(b *testing.B) {
	slice, err := AppSlice()
	if err != nil {
		b.Skip(err)
	}
	c := New(cgroupUnits{base: slice}, &nopWeights{}, &nopAudio{}, DesktopIdentity, "")
	c.Tick()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Tick()
	}
}

type nopWeights struct{}

func (nopWeights) Weight(string) (uint64, error)  { return Unset, nil }
func (nopWeights) SetWeight(string, uint64) error { return nil }

type nopAudio struct{}

func (nopAudio) AudibleApps() (map[string]bool, error) { return nil, nil }
