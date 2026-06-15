package stats

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// diskRank orders disks: root (primary) first, swap last, others in between.
func diskRank(d *DiskStats) int {
	switch {
	case d.IsRoot:
		return 0
	case d.IsSwap:
		return 2
	default:
		return 1
	}
}

// sectorSize is the fixed unit used by /proc/diskstats sector counters.
const sectorSize = 512

// discoverDisks enumerates whole block devices from /sys/block (skipping
// loop/ram pseudo-devices) and maps each to its mounted partitions.
func (c *Collector) discoverDisks() {
	entries, _ := os.ReadDir("/sys/block")
	mounts := readMounts()

	var disks []*DiskStats
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}
		d := &DiskStats{
			Name:      name,
			ReadHist:  NewRingBuffer(),
			WriteHist: NewRingBuffer(),
		}
		if v, err := readUint(filepath.Join("/sys/block", name, "size")); err == nil {
			d.SizeBytes = v * sectorSize
		}
		if strings.HasPrefix(name, "zram") {
			d.IsSwap = true
		} else if model, err := readString(filepath.Join("/sys/block", name, "device", "model")); err == nil {
			d.Model = strings.Join(strings.Fields(model), " ") // collapse padding whitespace
		}
		d.mounts = mountsForDisk(name, mounts)
		for _, mp := range d.mounts {
			if mp == "/" {
				d.IsRoot = true
				break
			}
		}
		disks = append(disks, d)
	}

	// Primary disk (root filesystem) first, swap last, otherwise larger first.
	sort.SliceStable(disks, func(i, j int) bool {
		if ri, rj := diskRank(disks[i]), diskRank(disks[j]); ri != rj {
			return ri < rj
		}
		return disks[i].SizeBytes > disks[j].SizeBytes
	})

	c.write(func(s *Stats) { s.Disks = disks })
}

// collectDisks updates throughput (from /proc/diskstats) and space (statfs).
func (c *Collector) collectDisks() {
	now := time.Now()
	dt := now.Sub(c.diskLast).Seconds()
	if c.diskLast.IsZero() || dt <= 0 {
		dt = 1
	}
	c.diskLast = now

	stats := readDiskstats()

	c.write(func(s *Stats) {
		for _, d := range s.Disks {
			ds, ok := stats[d.Name]
			if ok {
				rd := ds[0] * sectorSize
				wr := ds[1] * sectorSize
				if d.havePrev {
					d.ReadRate = rateOf(rd, d.prevRead, dt)
					d.WriteRate = rateOf(wr, d.prevWrite, dt)
				}
				d.prevRead, d.prevWrite = rd, wr
				d.havePrev = true
				d.ReadTotal, d.WriteTotal = rd, wr
			}
			if d.ReadHist != nil {
				d.ReadHist.Push(d.ReadRate)
			}
			if d.WriteHist != nil {
				d.WriteHist.Push(d.WriteRate)
			}
			d.Used, d.Free = diskSpace(d.mounts)
		}
	})
}

// rateOf returns (cur-prev)/dt, guarding against counter resets.
func rateOf(cur, prev uint64, dt float64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

// readDiskstats returns name -> [sectorsRead, sectorsWritten].
func readDiskstats() map[string][2]uint64 {
	out := make(map[string][2]uint64)
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		name := fields[2]
		out[name] = [2]uint64{atou(fields[5]), atou(fields[9])}
	}
	return out
}

// readMounts returns device -> mountpoint for /dev-backed mounts.
func readMounts() map[string]string {
	out := make(map[string]string)
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}
		dev := filepath.Base(fields[0])
		if _, seen := out[dev]; !seen {
			out[dev] = fields[1]
		}
	}
	return out
}

// mountsForDisk returns mountpoints belonging to a whole disk: the disk itself
// or any of its partitions (e.g. nvme0n1 -> nvme0n1p1).
func mountsForDisk(disk string, mounts map[string]string) []string {
	var mps []string
	for dev, mp := range mounts {
		if dev == disk || isPartitionOf(disk, dev) {
			mps = append(mps, mp)
		}
	}
	return mps
}

// isPartitionOf reports whether dev is a partition of disk.
func isPartitionOf(disk, dev string) bool {
	if !strings.HasPrefix(dev, disk) || len(dev) <= len(disk) {
		return false
	}
	rest := dev[len(disk):]
	// nvme0n1p3 / mmcblk0p1 use a 'p' separator; sda1 does not.
	if rest[0] == 'p' {
		rest = rest[1:]
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(rest) > 0
}

// diskSpace sums used/free bytes across the given mountpoints via statfs.
func diskSpace(mounts []string) (used, free uint64) {
	for _, mp := range mounts {
		var st syscall.Statfs_t
		if syscall.Statfs(mp, &st) != nil {
			continue
		}
		bs := uint64(st.Bsize)
		free += st.Bavail * bs
		used += (st.Blocks - st.Bfree) * bs
	}
	return used, free
}
