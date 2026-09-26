package stats

import (
	"sort"
	"strings"

	"atlas-monitor/internal/winapi"
)

// Disks on Windows: one row per drive letter.
//
// Linux lists whole block devices — nvme0n1, sda — and gathers the mountpoints
// underneath each. Windows could be made to do the same through the physical
// drive numbers, but it would be the wrong answer: nobody thinks of their machine
// as having a PhysicalDrive0. They think of it as having a C: drive. So a volume
// is what a "disk" is here, and its letter is its name.
//
// That makes one figure behave differently from Linux, and it is worth being
// clear about which. On Linux a disk's throughput covers the whole device,
// partitions included. Here it is per volume, because that is the level the
// storage stack keeps counters at. Two volumes on one physical disk therefore
// report their own traffic separately rather than both showing the disk's total.

// discoverDisks enumerates the mounted volumes.
func (c *Collector) discoverDisks() {
	vols, err := winapi.ReadVolumes()
	if err != nil {
		return
	}

	var disks []*DiskStats
	for _, v := range vols {
		d := &DiskStats{
			Name:      v.Letter,
			SizeBytes: v.TotalBytes,
			ReadHist:  NewRingBuffer(),
			WriteHist: NewRingBuffer(),
			// The letter is the name, so the volume's own label becomes the
			// model line — "Windows", "Games" — which is what identifies it to
			// the person looking at it.
			Model: diskLabel(v),
			// The system volume stands in for Linux's root filesystem: it is the
			// one that gets listed first.
			IsRoot:   strings.EqualFold(v.Letter, systemDrive()),
			mounts:   []string{v.Root},
			isFixed:  v.Fixed,
			isRemote: v.Remote,
		}
		disks = append(disks, d)
	}

	// System volume first, then by size, the same ordering rule Linux uses.
	sort.SliceStable(disks, func(i, j int) bool {
		if ri, rj := diskRank(disks[i]), diskRank(disks[j]); ri != rj {
			return ri < rj
		}
		return disks[i].SizeBytes > disks[j].SizeBytes
	})

	c.disks = disks
	c.diskSpace = make([][2]uint64, len(disks))
	c.write(func(s *Stats) { s.Disks = disks })
}

// diskLabel is what to show beside the drive letter.
func diskLabel(v winapi.Volume) string {
	switch {
	case v.Label != "" && v.FileSystem != "":
		return v.Label + " · " + v.FileSystem
	case v.Label != "":
		return v.Label
	case v.Remote:
		return "Network drive"
	case !v.Fixed:
		return "Removable drive"
	case v.FileSystem != "":
		return v.FileSystem
	default:
		return ""
	}
}

// systemDrive is the volume Windows is installed on, `C:` on almost every
// machine but not guaranteed to be.
func systemDrive() string {
	if root := winapi.SystemDrive(); root != "" {
		return root
	}
	return "C:"
}

// readDiskBytes asks each volume for its own cumulative byte counters.
//
// A volume that will not answer is left out of the map rather than reported as
// zero: collectDisks only updates a disk that is present in the result, so an
// absent entry holds the last known rate instead of drawing a false drop to
// nothing.
func (c *Collector) readDiskBytes() map[string][2]uint64 {
	out := c.diskStats
	clear(out)
	for _, d := range c.disks {
		// Network shares are not asked. The counters belong to the local storage
		// stack and a share has none, and the query would go over the wire to
		// find that out — on a tick, once a second.
		if d.isRemote {
			continue
		}
		io, ok := winapi.ReadDiskIO(d.Name)
		if !ok {
			continue
		}
		out[d.Name] = [2]uint64{io.ReadBytes, io.WriteBytes}
	}
	return out
}

// diskSpace returns used and free bytes for a volume's root path.
//
// Unlike the Linux statfs version this takes the figures from the volume itself
// rather than summing mountpoints, because a Windows volume is exactly one of
// them.
func diskSpace(mounts []string) (used, free uint64) {
	for _, root := range mounts {
		total, avail, ok := winapi.VolumeSpace(root)
		if !ok {
			continue
		}
		free += avail
		if total > avail {
			used += total - avail
		}
	}
	return used, free
}
