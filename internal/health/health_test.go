package health

import (
	"strings"
	"testing"

	"atlas-monitor/internal/stats"
)

// healthy is a machine with nothing wrong: cool, plenty of memory, disks with
// room. Each test spoils exactly one thing, so a check that fires on the wrong
// input is visible.
func healthy() *stats.Stats {
	s := &stats.Stats{}
	s.CPU.Temp = 45
	s.Mem.Total = 32 << 30
	s.Mem.Used = 8 << 30
	s.Mem.Available = 22 << 30
	s.Disks = []*stats.DiskStats{{
		Name: "nvme0n1", SizeBytes: 1000 << 30, Used: 100 << 30, Free: 900 << 30,
	}}
	return s
}

func titles(alerts []Alert) string {
	var b strings.Builder
	for _, a := range alerts {
		b.WriteString(a.Title)
		b.WriteString("; ")
	}
	return b.String()
}

func TestHealthyMachineHasNothingToSay(t *testing.T) {
	if got := Check(healthy(), nil); len(got) != 0 {
		t.Errorf("a healthy machine produced %d alerts: %s", len(got), titles(got))
	}
	if got := Worst(nil); got != 0 {
		t.Errorf("Worst(nil) = %d, want 0", got)
	}
}

func TestEachProblemIsReportedOnce(t *testing.T) {
	for _, c := range []struct {
		name  string
		spoil func(*stats.Stats)
		want  string
		level Level
	}{
		{"hot cpu", func(s *stats.Stats) { s.CPU.Temp = 91 }, "Processor is running hot", Critical},
		{"hot gpu", func(s *stats.Stats) { s.GPU.Available, s.GPU.Temp = true, 90 }, "Graphics card is running hot", Critical},
		{"little memory left", func(s *stats.Stats) { s.Mem.Available = 1 << 30 }, "Running out of memory", Warning},
		{"most memory used", func(s *stats.Stats) { s.Mem.Used = 31 << 30 }, "Running out of memory", Warning},
		{"swapping under pressure", func(s *stats.Stats) {
			s.Mem.SwapTotal, s.Mem.SwapUsed = 8<<30, 4<<30
			s.Mem.Available = 4 << 30 // and nowhere left to put things
		}, "Swapping heavily", Warning},
		{"full disk", func(s *stats.Stats) { s.Disks[0].Free = 1 << 30 }, "Disk nearly full", Warning},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := healthy()
			c.spoil(s)
			got := Check(s, nil)
			if len(got) != 1 {
				t.Fatalf("got %d alerts, want 1: %s", len(got), titles(got))
			}
			if !strings.Contains(got[0].Title, c.want) {
				t.Errorf("title %q does not mention %q", got[0].Title, c.want)
			}
			if got[0].Level != c.level {
				t.Errorf("level = %d, want %d", got[0].Level, c.level)
			}
			if got[0].Detail == "" {
				t.Error("no detail, so the popover would show a bare title")
			}
		})
	}
}

// TestGPUTemperatureIsIgnoredWithoutAGPU guards the desktop-with-no-card case:
// GPU.Temp is zero there, but so is Available, and a machine should not be told
// its absent graphics card is fine either.
func TestGPUTemperatureIsIgnoredWithoutAGPU(t *testing.T) {
	s := healthy()
	s.GPU.Available, s.GPU.Temp = false, 120
	if got := Check(s, nil); len(got) != 0 {
		t.Errorf("an absent GPU produced %s", titles(got))
	}
}

// TestSwapIsIgnoredWithoutSwap covers machines with none configured, where
// SwapUsed and SwapTotal are both zero and the ratio is not a number.
func TestSwapIsIgnoredWithoutSwap(t *testing.T) {
	s := healthy()
	s.Mem.SwapTotal, s.Mem.SwapUsed = 0, 0
	if got := Check(s, nil); len(got) != 0 {
		t.Errorf("no swap produced %s", titles(got))
	}
}

// TestZramIsNotADiskToFill: the compressed-RAM swap device reports almost no
// free space by design, and warning that it is nearly full is noise.
func TestZramIsNotADiskToFill(t *testing.T) {
	s := healthy()
	s.Disks = append(s.Disks, &stats.DiskStats{
		Name: "zram0", IsSwap: true, SizeBytes: 8 << 30, Free: 0,
	})
	if got := Check(s, nil); len(got) != 0 {
		t.Errorf("zram produced %s", titles(got))
	}
}

func TestFailedServicesAreSummarised(t *testing.T) {
	one := Check(healthy(), []string{"nginx.service"})
	if len(one) != 1 || !strings.Contains(one[0].Detail, "nginx.service") {
		t.Fatalf("one failed service gave %+v", one)
	}
	many := Check(healthy(), []string{"a.service", "b.service", "c.service", "d.service", "e.service"})
	if len(many) != 1 {
		t.Fatalf("five failed services gave %d alerts", len(many))
	}
	if !strings.Contains(many[0].Title, "5") {
		t.Errorf("title %q does not say how many", many[0].Title)
	}
	// Listing all of them would fill the popover; three and a count is enough.
	if !strings.Contains(many[0].Detail, "and 2 more") {
		t.Errorf("detail %q should cut the list off", many[0].Detail)
	}
}

// TestCriticalComesFirst: a hot chip matters more than a full disk, and the
// badge takes its colour from the worst thing present.
func TestCriticalComesFirst(t *testing.T) {
	s := healthy()
	s.Disks[0].Free = 1 << 30
	s.CPU.Temp = 95
	got := Check(s, nil)
	if len(got) != 2 {
		t.Fatalf("got %d alerts, want 2: %s", len(got), titles(got))
	}
	if got[0].Level != Critical {
		t.Errorf("first alert is %q at level %d; the critical one should lead", got[0].Title, got[0].Level)
	}
	if Worst(got) != Critical {
		t.Errorf("Worst = %d, want Critical", Worst(got))
	}
}

// TestUnmountedDiskIsNotNearlyFull is the dual-boot case. A drive with no
// mounted filesystem reports no used and no free space; treated as a ratio
// that is nought percent free, and every machine with a Windows partition
// would have been told its disk was full.
func TestUnmountedDiskIsNotNearlyFull(t *testing.T) {
	s := healthy()
	s.Disks = append(s.Disks, &stats.DiskStats{
		Name: "nvme1n1", SizeBytes: 1000 << 30, Used: 0, Free: 0,
	})
	if got := Check(s, nil); len(got) != 0 {
		t.Errorf("an unmounted drive produced %s", titles(got))
	}
	// A genuinely full mounted disk still reports.
	s.Disks[1].Used, s.Disks[1].Free = 999<<30, 1<<30
	if got := Check(s, nil); len(got) != 1 {
		t.Errorf("a full mounted disk gave %d alerts, want 1: %s", len(got), titles(got))
	}
}

// TestZramInUseIsNotAProblem is the Fedora default: swap is compressed RAM,
// the system is built to use it, and a third of it being occupied says nothing
// about whether the machine is struggling. This warned that a machine with
// 23 GiB of 31 free would "feel slow".
func TestZramInUseIsNotAProblem(t *testing.T) {
	s := healthy()
	s.Mem.SwapTotal, s.Mem.SwapUsed = 8<<30, 2400<<20 // 29% of swap in use
	s.Mem.Available = 23 << 30                        // ...and plenty of room
	if got := Check(s, nil); len(got) != 0 {
		t.Errorf("a healthy machine using zram produced %s", titles(got))
	}
}
