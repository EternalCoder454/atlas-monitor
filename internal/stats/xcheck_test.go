package stats

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"atlas-monitor/internal/gpu"
)

// sample runs the real collectors for long enough to produce n samples and
// hands the caller the resulting Stats under the read lock.
func sample(t *testing.T, n int, f func(*Stats)) {
	t.Helper()
	c := New(gpu.NewReader())
	c.Start()
	defer c.Stop()
	time.Sleep(time.Duration(n)*time.Second + 300*time.Millisecond)
	c.Read(f)
}

// ---------------------------------------------------------------- memory

// freeFigures runs `free -b` and returns its Mem and Swap rows by column name.
func freeFigures(t *testing.T) (mem, swap map[string]uint64) {
	t.Helper()
	bin, err := exec.LookPath("free")
	if err != nil {
		t.Skip("free not installed")
	}
	out, err := exec.Command(bin, "-b").Output()
	if err != nil {
		t.Skipf("free failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Skip("unexpected free output")
	}
	cols := strings.Fields(lines[0]) // total used free shared buff/cache available
	parse := func(line string) map[string]uint64 {
		f := strings.Fields(line)
		if len(f) < 2 {
			return nil
		}
		m := make(map[string]uint64, len(cols))
		for i, col := range cols {
			if i+1 < len(f) {
				v, err := strconv.ParseUint(f[i+1], 10, 64)
				if err == nil {
					m[col] = v
				}
			}
		}
		return m
	}
	for _, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "Mem:"):
			mem = parse(line)
		case strings.HasPrefix(line, "Swap:"):
			swap = parse(line)
		}
	}
	if mem == nil {
		t.Skip("free printed no Mem row")
	}
	return mem, swap
}

// TestMemoryAgreesWithFree checks the Memory page against free(1).
//
// Note the definition of Used: we report Total-MemAvailable, the memory that
// cannot be reclaimed without evicting something, which is what Windows' Task
// Manager calls "In use". free(1) instead prints Total-Free-Buffers-Cache in
// its "used" column, which is a different and smaller number. The two are not
// interchangeable, so this test compares Total and Available — the figures both
// tools read straight out of /proc/meminfo — and then checks our Used against
// free's *available* column, not its used column.
func TestMemoryAgreesWithFree(t *testing.T) {
	// Bracket free(1) between two of our own reads. On a machine that is
	// actively allocating — a parallel build, say — MemAvailable can move by a
	// gigabyte between two reads of /proc/meminfo, and that is the machine
	// moving rather than a disagreement.
	var first, second MemStats
	sample(t, 2, func(s *Stats) { first = s.Mem })
	mem, swap := freeFigures(t)
	sample(t, 2, func(s *Stats) { second = s.Mem })

	if first.Total != mem["total"] {
		t.Errorf("Mem.Total = %d, free says %d", first.Total, mem["total"])
	}
	// Total is fixed; the moving figures are checked against the range our two
	// reads saw, with a little slack outside it.
	const slack = 64 << 20
	within := func(name string, lo, hi, want uint64) {
		t.Helper()
		if lo > hi {
			lo, hi = hi, lo
		}
		if want+slack < lo || want > hi+slack {
			t.Errorf("%s: free says %d, outside the [%d, %d] range our reads bracketed", name, want, lo, hi)
		}
	}
	within("Available", first.Available, second.Available, mem["available"])
	within("Cached", first.Cached, second.Cached, mem["buff/cache"])
	within("Used", first.Used, second.Used, mem["total"]-mem["available"])
	if swap != nil {
		if first.SwapTotal != swap["total"] {
			t.Errorf("Mem.SwapTotal = %d, free says %d", first.SwapTotal, swap["total"])
		}
		within("SwapUsed", first.SwapUsed, second.SwapUsed, swap["used"])
	}
	t.Logf("total=%d used=%d avail=%d cached=%d swap=%d/%d",
		first.Total, first.Used, first.Available, first.Cached, first.SwapUsed, first.SwapTotal)
}

// TestMemoryInvariants checks the figures are internally consistent, which the
// Memory page's bars and percentages depend on.
func TestMemoryInvariants(t *testing.T) {
	var m MemStats
	sample(t, 2, func(s *Stats) { m = s.Mem })

	if m.Total == 0 {
		t.Fatal("Mem.Total is 0")
	}
	if m.Used > m.Total {
		t.Errorf("Used %d > Total %d", m.Used, m.Total)
	}
	if m.Available > m.Total {
		t.Errorf("Available %d > Total %d", m.Available, m.Total)
	}
	if m.Free > m.Total {
		t.Errorf("Free %d > Total %d", m.Free, m.Total)
	}
	if m.Free > m.Available+(64<<20) {
		t.Errorf("Free %d exceeds Available %d", m.Free, m.Available)
	}
	if m.Used+m.Available != m.Total {
		t.Errorf("Used+Available = %d, Total = %d (must be exact by construction)", m.Used+m.Available, m.Total)
	}
	if m.SwapUsed > m.SwapTotal {
		t.Errorf("SwapUsed %d > SwapTotal %d", m.SwapUsed, m.SwapTotal)
	}
}

// ---------------------------------------------------------------- disks

// dfRow is one `df` line.
type dfRow struct {
	dev, mount  string
	used, avail uint64
}

// readDF runs df and returns its rows for real block devices only.
func readDF(t *testing.T) []dfRow {
	t.Helper()
	bin, err := exec.LookPath("df")
	if err != nil {
		t.Skip("df not installed")
	}
	out, err := exec.Command(bin, "-B1", "--output=source,target,used,avail").Output()
	if err != nil {
		t.Skipf("df failed: %v", err)
	}
	var rows []dfRow
	for _, line := range strings.Split(string(out), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		used, err1 := strconv.ParseUint(f[2], 10, 64)
		avail, err2 := strconv.ParseUint(f[3], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rows = append(rows, dfRow{dev: filepath.Base(f[0]), mount: f[1], used: used, avail: avail})
	}
	return rows
}

// TestDiskSpaceAgreesWithDF checks the Storage page's used/free against df.
//
// A disk's figures are the sum over its mounted partitions, counting each
// device once: btrfs subvolumes put the same partition at several mountpoints
// (/ and /home here), and summing those would double-count the whole
// filesystem. That de-duplication is the thing most worth guarding.
func TestDiskSpaceAgreesWithDF(t *testing.T) {
	rows := readDF(t)
	if len(rows) == 0 {
		t.Skip("df listed no block devices")
	}

	var disks []*DiskStats
	sample(t, 2, func(s *Stats) { disks = append(disks, s.Disks...) })

	// Expected per whole disk: one row per device, first mount wins, which is
	// how /proc/mounts is read.
	seen := make(map[string]bool)
	wantUsed := make(map[string]uint64)
	wantAvail := make(map[string]uint64)
	for _, r := range rows {
		if seen[r.dev] {
			continue
		}
		seen[r.dev] = true
		for _, d := range disks {
			if d.Name == r.dev || isPartitionOf(d.Name, r.dev) {
				wantUsed[d.Name] += r.used
				wantAvail[d.Name] += r.avail
			}
		}
	}

	var checked int
	for _, d := range disks {
		wu, ok := wantUsed[d.Name]
		if !ok {
			continue // unmounted disk: no space figures to check
		}
		checked++
		// 1% or 64 MiB: writes land between df's statfs and ours.
		tol := wu / 100
		if tol < 64<<20 {
			tol = 64 << 20
		}
		if diffU(d.Used, wu) > tol {
			t.Errorf("%s Used = %d, df says %d (diff %d)", d.Name, d.Used, wu, diffU(d.Used, wu))
		}
		if diffU(d.Free, wantAvail[d.Name]) > tol {
			t.Errorf("%s Free = %d, df says %d (diff %d)", d.Name, d.Free, wantAvail[d.Name], diffU(d.Free, wantAvail[d.Name]))
		}
		if d.SizeBytes > 0 && d.Used+d.Free > d.SizeBytes {
			t.Errorf("%s Used+Free = %d exceeds device size %d", d.Name, d.Used+d.Free, d.SizeBytes)
		}
		t.Logf("%-10s used=%d free=%d size=%d root=%v swap=%v", d.Name, d.Used, d.Free, d.SizeBytes, d.IsRoot, d.IsSwap)
	}
	if checked == 0 {
		t.Skip("no mounted disks matched df's devices")
	}
}

// TestRootDiskIdentified checks the disk hosting / is singled out, since the
// Storage page sorts on it and the sidebar names it the primary drive.
//
// Whether any disk can be flagged at all depends on the machine: a container
// usually has / on an overlayfs with no /dev/ device behind it, so there is
// nothing to flag and flagging something would be the bug. The strict check
// therefore runs only where /proc/mounts really does show a block device at /.
func TestRootDiskIdentified(t *testing.T) {
	var roots, total int
	var name string
	sample(t, 1, func(s *Stats) {
		total = len(s.Disks)
		for _, d := range s.Disks {
			if d.IsRoot {
				roots++
				name = d.Name
			}
		}
	})
	if total == 0 {
		t.Skip("no disks discovered")
	}
	// More than one primary drive is wrong everywhere.
	if roots > 1 {
		t.Fatalf("%d disks flagged IsRoot, want at most 1 (of %d disks)", roots, total)
	}
	if !blockDeviceAtRoot(t) {
		if roots != 0 {
			t.Errorf("%q flagged IsRoot, but no block device is mounted at /", name)
		}
		t.Skip("/ is not backed by a /dev/ device (overlayfs?), nothing to identify")
	}
	if roots != 1 {
		t.Errorf("%d disks flagged IsRoot, want exactly 1 (of %d disks)", roots, total)
	} else {
		t.Logf("root disk is %s (of %d disks)", name, total)
	}
}

// blockDeviceAtRoot reports whether /proc/mounts shows a /dev/ device mounted at
// /, which is what discoverDisks needs in order to flag one.
func blockDeviceAtRoot(t *testing.T) bool {
	t.Helper()
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "/" && strings.HasPrefix(f[0], "/dev/") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- network

// TestNetCountersAgreeWithProc checks the interface byte counters against an
// independent parse of /proc/net/dev. Counters only climb, so ours must be at
// least what we read before the sample and no more than what we read after.
func TestNetCountersAgreeWithProc(t *testing.T) {
	before := parseNetDevForTest(t)

	var nets []*NetStats
	sample(t, 2, func(s *Stats) { nets = append(nets, s.Nets...) })

	after := parseNetDevForTest(t)
	if len(nets) == 0 {
		t.Skip("no interfaces discovered")
	}

	var checked int
	for _, n := range nets {
		lo, ok1 := before[n.Name]
		hi, ok2 := after[n.Name]
		if !ok1 || !ok2 {
			continue
		}
		checked++
		if n.RxTotal < lo[0] || n.RxTotal > hi[0] {
			t.Errorf("%s RxTotal = %d, outside the [%d, %d] window /proc/net/dev bracketed", n.Name, n.RxTotal, lo[0], hi[0])
		}
		if n.TxTotal < lo[1] || n.TxTotal > hi[1] {
			t.Errorf("%s TxTotal = %d, outside the [%d, %d] window /proc/net/dev bracketed", n.Name, n.TxTotal, lo[1], hi[1])
		}
		if n.RxRate < 0 || n.TxRate < 0 {
			t.Errorf("%s negative rate: rx=%.1f tx=%.1f", n.Name, n.RxRate, n.TxRate)
		}
	}
	if checked == 0 {
		t.Skip("no interface appeared in both /proc/net/dev reads")
	}
	t.Logf("checked %d interfaces against /proc/net/dev", checked)
}

// parseNetDevForTest is a deliberately naive /proc/net/dev parser, independent
// of the byte-scanning one in net.go, so a bug in field indexing shows up as a
// disagreement rather than being reproduced identically on both sides.
func parseNetDevForTest(t *testing.T) map[string][2]uint64 {
	t.Helper()
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		t.Skipf("cannot read /proc/net/dev: %v", err)
	}
	out := make(map[string][2]uint64)
	for _, line := range strings.Split(string(data), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64) // bytes received
		tx, err2 := strconv.ParseUint(f[8], 10, 64) // bytes transmitted
		if err1 != nil || err2 != nil {
			continue
		}
		out[name] = [2]uint64{rx, tx}
	}
	return out
}

// TestNetAddressesAgreeWithIP checks the addresses shown on the Network page
// against `ip addr`.
func TestNetAddressesAgreeWithIP(t *testing.T) {
	bin, err := exec.LookPath("ip")
	if err != nil {
		t.Skip("ip not installed")
	}
	out, err := exec.Command(bin, "-4", "-o", "addr", "show").Output()
	if err != nil {
		t.Skipf("ip failed: %v", err)
	}
	want := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[2] != "inet" {
			continue
		}
		addr, _, _ := strings.Cut(f[3], "/")
		if _, seen := want[f[1]]; !seen {
			want[f[1]] = addr
		}
	}

	var nets []*NetStats
	sample(t, 1, func(s *Stats) { nets = append(nets, s.Nets...) })

	var checked int
	for _, n := range nets {
		w, ok := want[n.Name]
		if !ok {
			if n.IPv4 != "" {
				t.Errorf("%s reports IPv4 %q but ip shows no v4 address", n.Name, n.IPv4)
			}
			continue
		}
		checked++
		if n.IPv4 != w {
			t.Errorf("%s IPv4 = %q, ip says %q", n.Name, n.IPv4, w)
		}
	}
	if checked == 0 {
		t.Skip("no interface had an IPv4 address in both views")
	}
	t.Logf("checked %d IPv4 addresses against ip(8)", checked)
}

// TestActiveNetIsRoutable checks the interface the sidebar highlights is the one
// the kernel routes through.
func TestActiveNetIsRoutable(t *testing.T) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		t.Skipf("cannot read /proc/net/route: %v", err)
	}
	var active string
	sample(t, 1, func(s *Stats) { active = s.ActiveNet })
	if active == "" {
		t.Skip("no default route detected")
	}
	// The default route's destination is 00000000.
	var found bool
	for _, line := range strings.Split(string(data), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == active && f[1] == "00000000" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ActiveNet = %q but it has no default route in /proc/net/route", active)
	} else {
		t.Logf("ActiveNet = %q, confirmed as the default route", active)
	}
}

// ---------------------------------------------------------------- cpu

// TestCPUUsageAgreesWithProcStat brackets a run of the collector with two
// direct /proc/stat reads and checks the samples average out to the same busy
// percentage the kernel's own counters imply over that window.
func TestCPUUsageAgreesWithProcStat(t *testing.T) {
	if testing.Short() {
		t.Skip("takes several seconds")
	}
	idle0, total0, ok := procStatTotals()
	if !ok {
		t.Skip("cannot read /proc/stat")
	}
	start := time.Now()

	c := New(gpu.NewReader())
	c.Start()
	defer c.Stop()

	const samples = 10
	var sum float64
	var n int
	for i := 0; i < samples; i++ {
		time.Sleep(time.Second)
		c.Read(func(s *Stats) {
			if s.CPU.Usage >= 0 {
				sum += s.CPU.Usage
				n++
			}
		})
	}
	elapsed := time.Since(start)
	idle1, total1, _ := procStatTotals()

	if n == 0 || total1 <= total0 {
		t.Skip("no usable samples")
	}
	want := 100 * (1 - float64(idle1-idle0)/float64(total1-total0))
	got := sum / float64(n)
	// Our sample mean is a mean of instantaneous readings over the same window,
	// so it tracks the window average — but only as well as sampling allows. On
	// a machine whose load is swinging between idle and pegged, ten point
	// samples cannot reconstruct the average, so the tolerance widens with how
	// busy the window was. This stays tight on a quiet machine, which is where
	// a real disagreement would show.
	tol := 8.0
	if want > 50 {
		tol = 20.0
	}
	if d := got - want; d > tol || d < -tol {
		t.Errorf("mean CPU usage %.2f%% over %v, /proc/stat implies %.2f%% (diff %.2f, tolerance %.0f)", got, elapsed.Round(time.Millisecond), want, d, tol)
	} else {
		t.Logf("mean CPU usage %.2f%% over %v, /proc/stat implies %.2f%% (diff %.2f)", got, elapsed.Round(time.Millisecond), want, d)
	}
}

// procStatTotals reads the aggregate cpu line with a parser independent of
// cpu.go's.
func procStatTotals() (idle, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		for i, tok := range strings.Fields(line)[1:] {
			v, err := strconv.ParseUint(tok, 10, 64)
			if err != nil {
				continue
			}
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		return idle, total, total > 0
	}
	return 0, 0, false
}

// TestCPUTopologyAgreesWithSysfs checks the core grid and the static header
// against the machine's real topology.
func TestCPUTopologyAgreesWithSysfs(t *testing.T) {
	var cpu CPUStats
	sample(t, 2, func(s *Stats) {
		cpu = s.CPU
		cpu.Cores = append([]CoreStat(nil), s.CPU.Cores...)
	})

	if cpu.Logical != runtime.NumCPU() {
		t.Errorf("Logical = %d, runtime.NumCPU() = %d", cpu.Logical, runtime.NumCPU())
	}
	if len(cpu.Cores) != cpu.Logical {
		t.Errorf("core grid has %d entries, Logical = %d", len(cpu.Cores), cpu.Logical)
	}
	if cpu.PhysCores > cpu.Logical {
		t.Errorf("PhysCores %d > Logical %d", cpu.PhysCores, cpu.Logical)
	}
	if cpu.Sockets < 1 {
		t.Errorf("Sockets = %d, want at least 1", cpu.Sockets)
	}
	if cpu.Model == "" {
		t.Error("Model is empty")
	}
	for i, core := range cpu.Cores {
		if core.Usage < 0 || core.Usage > 100.1 {
			t.Errorf("core %d usage %.2f%% outside [0,100]", i, core.Usage)
		}
	}
	if cpu.Usage < 0 || cpu.Usage > 100.1 {
		t.Errorf("aggregate usage %.2f%% outside [0,100]", cpu.Usage)
	}
	// The aggregate should sit near the mean of the per-core figures.
	if len(cpu.Cores) > 0 {
		var mean float64
		for _, core := range cpu.Cores {
			mean += core.Usage
		}
		mean /= float64(len(cpu.Cores))
		if d := cpu.Usage - mean; d > 10 || d < -10 {
			t.Errorf("aggregate usage %.2f%% is far from the core mean %.2f%%", cpu.Usage, mean)
		}
	}
	t.Logf("%s: %d socket(s), %d physical, %d logical, base=%.0fMHz cur=%.0fMHz temp=%.1f°C",
		cpu.Model, cpu.Sockets, cpu.PhysCores, cpu.Logical, cpu.BaseFreq, cpu.CurFreq, cpu.Temp)
}

// ---------------------------------------------------------------- helpers

// diffU is the absolute difference between two unsigned values.
func diffU(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}
