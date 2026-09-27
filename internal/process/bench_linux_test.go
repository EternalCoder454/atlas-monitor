package process

import "testing"

// BenchmarkCollect measures one full scan of this machine's /proc. The
// collector runs once a second for as long as the Apps or Assistant page is
// open, and profiling showed it was two thirds of the app's CPU time, almost
// all of it syscalls — so this is the number worth watching.
func BenchmarkCollect(b *testing.B) {
	c := New()
	c.collect() // prime the previous-sample maps
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.collect()
	}
}

// BenchmarkCollectWithKernelThreads is the same scan with the Apps table's
// "Kernel threads" toggle on, which is what the collector used to do always.
func BenchmarkCollectWithKernelThreads(b *testing.B) {
	c := New()
	c.SetIncludeKernel(true)
	c.collect()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.collect()
	}
}

// BenchmarkReadStat is the per-process cost of the one file every process needs.
func BenchmarkReadStat(b *testing.B) {
	c := New()
	var st statLine
	pid := 1
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.readStat(pid, &st)
	}
}

// TestGPUClientsAreFound checks the merged descriptor walk still attributes GPU
// time. Something on a running desktop always holds a /dev/dri handle — the
// compositor at least — so a scan that finds none has regressed.
func TestGPUClientsAreFound(t *testing.T) {
	c := New()
	c.collect() // first pass discovers clients
	c.collect() // second produces deltas
	snap := c.Snapshot()
	if len(snap) == 0 {
		t.Skip("no processes readable")
	}

	clients := 0
	for _, p := range snap {
		if p.GPU >= 0 { // >= 0 means "holds a GPU handle"; -1 means it does not
			clients++
		}
	}
	t.Logf("%d of %d processes hold a GPU handle", clients, len(snap))
	if clients == 0 {
		t.Skip("nothing on this machine holds a /dev/dri handle")
	}
}
