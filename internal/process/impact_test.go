package process

import "testing"

func TestImpactRatings(t *testing.T) {
	const mb = 1 << 20
	tests := []struct {
		name string
		p    Proc
		want Impact
	}{
		{"idle", Proc{GPU: -1}, ImpactVeryLow},
		{"a trickle of CPU", Proc{CPU: 0.4, GPU: -1}, ImpactVeryLow},
		{"a few percent", Proc{CPU: 5, GPU: -1}, ImpactLow},
		{"a quarter core", Proc{CPU: 25, GPU: -1}, ImpactModerate},
		{"a whole core", Proc{CPU: 100, GPU: -1}, ImpactHigh},
		{"a parallel build", Proc{CPU: 780, GPU: -1}, ImpactHigh},
		{"video playback", Proc{CPU: 15, GPU: 40}, ImpactModerate},
		{"a game", Proc{CPU: 60, GPU: 95}, ImpactHigh},
		{"a big file copy", Proc{CPU: 8, GPU: -1, DiskRead: 200 * mb, DiskWrite: 200 * mb}, ImpactModerate},
		{"a quiet download", Proc{CPU: 1, GPU: -1, NetIn: 2 * mb}, ImpactLow},
	}
	for _, tt := range tests {
		if got := tt.p.Impact(); got != tt.want {
			t.Errorf("%s: Impact() = %v (score %.1f), want %v", tt.name, got, tt.p.PowerScore(), tt.want)
		}
	}
}

// TestImpactIgnoresAbsentGPU: -1 means "holds no GPU handle" and must not be
// read as negative usage that drags the score down.
func TestImpactIgnoresAbsentGPU(t *testing.T) {
	withGPU := Proc{CPU: 30, GPU: 0}
	without := Proc{CPU: 30, GPU: -1}
	if withGPU.PowerScore() != without.PowerScore() {
		t.Errorf("an idle GPU client (%.2f) scores differently from a non-client (%.2f)",
			withGPU.PowerScore(), without.PowerScore())
	}
}

// TestImpactSaturates: a process doing enormous I/O must not outrank a process
// pinning several cores.
func TestImpactSaturates(t *testing.T) {
	const gb = 1 << 30
	io := Proc{CPU: 1, GPU: -1, DiskRead: 8 * gb, DiskWrite: 8 * gb, NetIn: 4 * gb}
	cpu := Proc{CPU: 400, GPU: -1}
	if io.PowerScore() >= cpu.PowerScore() {
		t.Errorf("saturated I/O (%.1f) outranks four busy cores (%.1f)", io.PowerScore(), cpu.PowerScore())
	}
}

func TestImpactStrings(t *testing.T) {
	for i, want := range map[Impact]string{
		ImpactVeryLow:  "Very low",
		ImpactLow:      "Low",
		ImpactModerate: "Moderate",
		ImpactHigh:     "High",
	} {
		if got := i.String(); got != want {
			t.Errorf("Impact(%d).String() = %q, want %q", i, got, want)
		}
	}
}

// TestImpactOrdersForSorting: the constants must ascend so a column sort by
// rating reads low-to-high.
func TestImpactOrdersForSorting(t *testing.T) {
	if !(ImpactVeryLow < ImpactLow && ImpactLow < ImpactModerate && ImpactModerate < ImpactHigh) {
		t.Error("Impact constants are not in ascending order")
	}
}
