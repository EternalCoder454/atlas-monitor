package stats

// The part of disk reporting that is the same everywhere: turning two cumulative
// byte counters into a rate, deciding which disk to show first, and throttling
// how often free space is re-measured.
//
// Where the numbers come from is not shared at all. Linux enumerates block
// devices under /sys/block, reads sector counts from /proc/diskstats and asks
// statfs about each mountpoint. Windows lists drive letters and asks each volume
// for its own counters. Both end up filling the same DiskStats.

import "time"

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

// spaceEvery is how often free space is re-measured, in ticks. Throughput has
// to be sampled every tick to be a rate at all, but capacity moves slowly and
// each check is a statfs per mounted filesystem.
const spaceEvery = 5

// collectDisks updates throughput and free space from whatever the platform
// reports — see readDiskBytes and diskSpace, which are the only parts of this
// that differ between Linux and Windows.
//
// Measuring free space deliberately runs before the lock is taken. It can block
// for as long as the filesystem takes to answer — indefinitely, on a network
// mount or an unreachable share — and holding the stats lock across that would
// freeze every reader, which means the whole UI.
func (c *Collector) collectDisks() {
	now := time.Now()
	dt := now.Sub(c.diskLast).Seconds()
	if c.diskLast.IsZero() || dt <= 0 {
		dt = 1
	}
	c.diskLast = now

	stats := c.readDiskBytes()

	c.diskTick++
	measureSpace := c.diskTick%spaceEvery == 1
	if measureSpace {
		for i, d := range c.disks {
			c.diskSpace[i][0], c.diskSpace[i][1] = diskSpace(d.mounts)
		}
	}

	c.write(func(s *Stats) {
		for i, d := range s.Disks {
			ds, ok := stats[d.Name]
			if ok {
				rd, wr := ds[0], ds[1]
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
			if measureSpace && i < len(c.diskSpace) {
				d.Used, d.Free = c.diskSpace[i][0], c.diskSpace[i][1]
			}
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
