// Package health turns a snapshot of the machine into the short list of things
// actually wrong with it.
//
// These checks already existed, buried in the assistant: it computed them in Go
// because a small model cannot reliably compare figures to thresholds, then
// handed them to the model as a line of text. So Atlas knew the disk was nearly
// full and only ever told a chatbot — turn the assistant off, or run the build
// that has none, and the knowledge went nowhere.
//
// They live here so that anything can ask. The thresholds are the ones the
// assistant used, unchanged, because the badge in the window and the answer the
// model gives have to agree about whether the machine is healthy.
package health

import (
	"fmt"

	"atlas-monitor/internal/format"
	"atlas-monitor/internal/stats"
)

// Level says how loud an alert should be.
type Level int

const (
	// Warning is something worth knowing about that is not hurting yet.
	Warning Level = iota + 1
	// Critical is something doing damage or about to.
	Critical
)

// Alert is one problem, in the words the user reads.
type Alert struct {
	Level  Level
	Title  string // short, for a list: "Disk nearly full"
	Detail string // the figures behind it
}

// Thresholds, named so the numbers are not scattered through the checks. They
// came from the assistant's own alert block and are deliberately unchanged.
const (
	hotDegrees     = 85 // °C, for the processor and the graphics card
	lowMemoryBytes = 2 << 30
	highMemoryUsed = 0.90 // of total
	heavySwapUsed  = 0.25 // of total, and only with memory short as well
	// swapPressureAvail is how little memory has to be left before swap use
	// means anything. Above it the machine has somewhere to put things.
	swapPressureAvail = 0.25 // available, of total
	nearlyFullDisk    = 0.05 // free, of total
	// wornDrive is how much of an SSD's rated life has to be gone before it is
	// worth mentioning. Below this it is ordinary ageing.
	wornDrive = 90 // percent of rated life used
)

// Drive is one disk's own verdict on itself, from the SMART log. It is passed
// in rather than read here because it comes over D-Bus and is polled slowly.
type Drive struct {
	Name     string // the label a person reads, not the kernel name
	Failing  bool
	SpareLow bool
	Wear     int // percentage of rated life used
	HasWear  bool
}

// Check reports everything wrong with the machine right now, worst first.
//
// It must be called with the snapshot held — from inside a Collector.Read — as
// it reads the pointer fields. failedServices and drives come from elsewhere:
// both are D-Bus questions, asked on their own slower timers.
func Check(s *stats.Stats, failedServices []string, drives ...Drive) []Alert {
	var critical, warning []Alert

	if s.CPU.Temp > hotDegrees {
		critical = append(critical, Alert{
			Level:  Critical,
			Title:  "Processor is running hot",
			Detail: fmt.Sprintf("%.0f °C — above %d °C the chip starts slowing itself down", s.CPU.Temp, hotDegrees),
		})
	}
	if s.GPU.Available && s.GPU.Temp > hotDegrees {
		critical = append(critical, Alert{
			Level:  Critical,
			Title:  "Graphics card is running hot",
			Detail: fmt.Sprintf("%.0f °C", s.GPU.Temp),
		})
	}

	if m := s.Mem; m.Total > 0 && (m.Available < lowMemoryBytes || float64(m.Used)/float64(m.Total) > highMemoryUsed) {
		warning = append(warning, Alert{
			Level: Warning,
			Title: "Running out of memory",
			Detail: fmt.Sprintf("%s free of %s — programs may start closing",
				format.GiB(m.Available), format.GiB(m.Total)),
		})
	}
	// Swap in use is only a problem when memory is actually short.
	//
	// The inherited threshold fired on swap residency alone, and on Fedora —
	// where swap is zram, compressed RAM that the system is designed to use —
	// that is the normal state of a perfectly healthy machine. It warned that
	// "the machine will feel slow" on this one while it had 23 GiB of 31 free.
	// Pages parked in swap hours ago cost nothing; what hurts is swapping while
	// there is nothing left to swap into.
	if m := s.Mem; m.SwapTotal > 0 && m.Total > 0 &&
		float64(m.SwapUsed)/float64(m.SwapTotal) > heavySwapUsed &&
		float64(m.Available)/float64(m.Total) < swapPressureAvail {
		warning = append(warning, Alert{
			Level: Warning,
			Title: "Swapping heavily",
			Detail: fmt.Sprintf("%s of %s swap in use — the machine will feel slow",
				format.GiB(m.SwapUsed), format.GiB(m.SwapTotal)),
		})
	}

	for _, d := range s.Disks {
		if d.IsSwap || d.SizeBytes == 0 {
			continue
		}
		// A drive with nothing mounted reports no used and no free space, which
		// reads as nought percent free and would have warned that every
		// dual-boot machine's Windows partition was full. Atlas cannot measure
		// what is not mounted, and it should not invent a problem out of that.
		if d.Used+d.Free == 0 {
			continue
		}
		if float64(d.Free)/float64(d.SizeBytes) < nearlyFullDisk {
			warning = append(warning, Alert{
				Level:  Warning,
				Title:  "Disk nearly full: " + d.Label(),
				Detail: fmt.Sprintf("%s free of %s", format.Bytes(d.Free), format.Bytes(d.SizeBytes)),
			})
		}
	}

	// A drive that says it is failing is the loudest thing here: everything
	// else is a machine being busy, and this is one that will stop working.
	for _, d := range drives {
		switch {
		case d.Failing:
			critical = append(critical, Alert{
				Level:  Critical,
				Title:  "Drive is failing: " + d.Name,
				Detail: "The drive reports that it expects to fail. Back up anything on it that matters.",
			})
		case d.SpareLow:
			warning = append(warning, Alert{
				Level:  Warning,
				Title:  "Drive is nearly worn out: " + d.Name,
				Detail: "It has run down its spare blocks, which is how an SSD says it is near the end.",
			})
		case d.HasWear && d.Wear >= wornDrive:
			warning = append(warning, Alert{
				Level:  Warning,
				Title:  "Drive is wearing out: " + d.Name,
				Detail: fmt.Sprintf("%d%% of its rated life is gone", d.Wear),
			})
		}
	}

	switch n := len(failedServices); {
	case n == 1:
		warning = append(warning, Alert{
			Level:  Warning,
			Title:  "A background service has failed",
			Detail: failedServices[0],
		})
	case n > 1:
		warning = append(warning, Alert{
			Level:  Warning,
			Title:  fmt.Sprintf("%d background services have failed", n),
			Detail: joinUpTo(failedServices, 3),
		})
	}

	return append(critical, warning...)
}

// Worst returns the loudest level present, or 0 for a healthy machine.
func Worst(alerts []Alert) Level {
	worst := Level(0)
	for _, a := range alerts {
		if a.Level > worst {
			worst = a.Level
		}
	}
	return worst
}

// joinUpTo lists the first n names and says how many more there are, so a
// machine with twenty broken units does not fill the popover with them.
func joinUpTo(names []string, n int) string {
	if len(names) <= n {
		out := ""
		for i, s := range names {
			if i > 0 {
				out += ", "
			}
			out += s
		}
		return out
	}
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ", "
		}
		out += names[i]
	}
	return fmt.Sprintf("%s and %d more", out, len(names)-n)
}
