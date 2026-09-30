package process

import (
	"os"
	"testing"
)

// BenchmarkSnapshotIntoReuse is what a page does every tick with a buffer it keeps.
func BenchmarkSnapshotIntoReuse(b *testing.B) {
	c := New()
	c.collect()
	var buf []Proc
	b.ReportAllocs()
	for b.Loop() {
		buf = c.SnapshotInto(buf)
	}
}

// BenchmarkView reads the list in place, copying nothing.
func BenchmarkView(b *testing.B) {
	c := New()
	c.collect()
	var n int
	b.ReportAllocs()
	for b.Loop() {
		c.View(func(ps []Proc) {
			for i := range ps {
				if ps[i].CPU > 20 {
					n++
				}
			}
		})
	}
	_ = n
}

func TestStartTimeAndNiceReadSelf(t *testing.T) {
	pid := os.Getpid()
	st, ok := StartTime(pid)
	if !ok || st == 0 {
		t.Fatalf("StartTime(self) = %d, %v", st, ok)
	}
	if _, ok := Nice(pid); !ok {
		t.Fatal("Nice(self) unreadable")
	}
	if _, ok := StartTime(1 << 30); ok {
		t.Fatal("StartTime of a missing pid succeeded")
	}
}

func BenchmarkStartTime(b *testing.B) {
	pid := os.Getpid()
	b.ReportAllocs()
	for b.Loop() {
		StartTime(pid)
	}
}
