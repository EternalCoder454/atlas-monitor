package process

import (
	"os"
	"testing"
)

func TestUnitFromCgroup(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"unified, app scope",
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.chromium.Chromium-662486.scope\n",
			"app-org.chromium.Chromium-662486.scope"},
		{"unified, app service with an instance",
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.kde.dolphin@765eead4667549d0ab239e9e204534a3.service\n",
			"app-org.kde.dolphin@765eead4667549d0ab239e9e204534a3.service"},
		// A unit's own sub-cgroups belong to it: the deepest *unit* wins, not the
		// deepest directory.
		{"cgroups below the unit",
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-flatpak-com.discordapp.Discord-3262276743.scope/sandbox/renderer\n",
			"app-flatpak-com.discordapp.Discord-3262276743.scope"},
		{"system service", "0::/system.slice/sshd.service\n", "sshd.service"},
		{"the user manager itself", "0::/user.slice/user-1000.slice/user@1000.service/init.scope\n", "init.scope"},
		{"root", "0::/\n", ""},
		{"nothing", "", ""},
		// Hybrid: v1 controllers first, the unified line last. The unified line
		// is the one to believe.
		{"hybrid",
			"12:cpuset:/\n1:name=systemd:/system.slice/old.service\n0::/system.slice/cups.service\n",
			"cups.service"},
		// Legacy only: the systemd controller's line.
		{"legacy", "5:cpu,cpuacct:/\n1:name=systemd:/user.slice/user-1000.slice/session-2.scope\n", "session-2.scope"},
		{"slices only", "0::/user.slice/user-1000.slice\n", ""},
	}
	for _, c := range cases {
		if got := string(UnitFromCgroup([]byte(c.in))); got != c.want {
			t.Errorf("%s: UnitFromCgroup = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestUnitsAreCollected checks the scan reports the unit this test process is
// actually in. Skipped where there is no unit to find, as in a container that
// puts everything at the cgroup root.
func TestUnitsAreCollected(t *testing.T) {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Skip("no /proc/self/cgroup")
	}
	want := string(UnitFromCgroup(b))
	if want == "" {
		t.Skip("this process is not in a systemd unit")
	}
	c := New()
	defer c.closePlatform()
	c.collect()
	for _, p := range c.Snapshot() {
		if p.PID == os.Getpid() {
			if p.Unit != want {
				t.Fatalf("Unit = %q, want %q", p.Unit, want)
			}
			return
		}
	}
	t.Fatal("this process was not in the snapshot")
}
