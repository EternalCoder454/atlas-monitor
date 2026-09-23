package process

// Impact is a coarse power-usage rating for a process, in the spirit of
// Task Manager's "Power usage" column.
//
// It is a heuristic, not a measurement. Linux exposes package-level energy
// through RAPL but nothing per process, so — like Windows — this blends the
// activity that actually costs battery: time on a core, time on the GPU
// engines, and the I/O that keeps the storage bus and the radio awake.
type Impact int

// The ratings, ordered so they sort naturally.
const (
	ImpactVeryLow Impact = iota
	ImpactLow
	ImpactModerate
	ImpactHigh
)

// String is the label shown in the table.
func (i Impact) String() string {
	switch i {
	case ImpactHigh:
		return "High"
	case ImpactModerate:
		return "Moderate"
	case ImpactLow:
		return "Low"
	default:
		return "Very low"
	}
}

// Weights for the score. CPU is the unit: one fully-busy core scores 100, and
// the other terms are expressed as how much of a core's worth of battery they
// are judged to cost when saturated.
const (
	gpuWeight    = 0.8      // a fully-busy GPU engine ≈ 0.8 of a busy core
	diskWeight   = 25.0     // sustained I/O at diskSaturate ≈ a quarter core
	netWeight    = 15.0     // a saturated radio ≈ 0.15 of a core
	diskSaturate = 50 << 20 // bytes/sec at which the disk term maxes out
	netSaturate  = 20 << 20 // bytes/sec at which the network term maxes out
)

// Thresholds between the ratings, in the same units as PowerScore.
const (
	highScore     = 60 // ≈ two thirds of a core, sustained
	moderateScore = 20
	lowScore      = 2
)

// PowerScore is the weighted activity behind Impact. It is exported so the
// table can sort by the underlying number rather than by the label, which would
// otherwise order alphabetically.
func (p Proc) PowerScore() float64 {
	score := p.CPU // already a percentage of one core, and may exceed 100
	if p.GPU > 0 {
		score += gpuWeight * p.GPU
	}
	score += diskWeight * saturating(p.DiskRead+p.DiskWrite, diskSaturate)
	score += netWeight * saturating(p.NetIn+p.NetOut, netSaturate)
	return score
}

// Impact buckets the score into the label shown to the user.
func (p Proc) Impact() Impact {
	switch s := p.PowerScore(); {
	case s >= highScore:
		return ImpactHigh
	case s >= moderateScore:
		return ImpactModerate
	case s >= lowScore:
		return ImpactLow
	default:
		return ImpactVeryLow
	}
}

// saturating maps a rate onto 0..1, flattening out at full.
func saturating(rate, full float64) float64 {
	if rate <= 0 {
		return 0
	}
	if rate >= full {
		return 1
	}
	return rate / full
}
