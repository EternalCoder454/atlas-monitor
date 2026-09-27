package stats

// Disks on Linux: block devices under /sys/block, sector counters from
// /proc/diskstats, and statfs for free space.

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"atlas-monitor/internal/sysfs"
)

// sectorSize is the fixed unit used by /proc/diskstats sector counters. It is
// 512 whatever the device's real sector size is.
const sectorSize = 512

// discoverDisks enumerates whole block devices from /sys/block (skipping
// loop/ram pseudo-devices) and maps each to its mounted partitions.
func (c *Collector) discoverDisks() {
	c.diskStat = sysfs.OpenSize("/proc/diskstats", 8192)
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
		if v, ok := sysfs.ReadUint(filepath.Join("/sys/block", name, "size")); ok {
			d.SizeBytes = v * sectorSize
		}
		if strings.HasPrefix(name, "zram") {
			d.IsSwap = true
		} else if model := sysfs.ReadString(filepath.Join("/sys/block", name, "device", "model")); model != "" {
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

	// The collector goroutine keeps its own handle on the list so it can read
	// each disk's mountpoints — fixed at discovery — without the lock.
	c.disks = disks
	c.diskSpace = make([][2]uint64, len(disks))
	c.write(func(s *Stats) { s.Disks = disks })
}

// readDiskBytes returns name -> [bytesRead, bytesWritten], reusing the
// collector's buffer and map so a tick allocates nothing.
//
// /proc/diskstats counts in 512-byte sectors regardless of the device's real
// sector size, so the conversion is a fixed multiplication and happens here —
// the shared rate arithmetic works in bytes.
func (c *Collector) readDiskBytes() map[string][2]uint64 {
	out := c.diskStats
	clear(out)
	data, ok := c.diskStat.Bytes()
	if !ok {
		return out
	}
	for len(data) > 0 {
		var line []byte
		line, data = nextLine(data)
		name := field(line, 2)
		rd, wr := field(line, 5), field(line, 9)
		if name == nil || rd == nil || wr == nil {
			continue
		}
		// The device name is the only allocation, and only for a device we have
		// not seen before — the map key is reused on every later tick.
		key := string(name)
		out[key] = [2]uint64{parseUintBytes(rd) * sectorSize, parseUintBytes(wr) * sectorSize}
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
