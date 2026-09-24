//go:build linux && cgo

package sysmem

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// This package reaches into glibc's allocator, so there is nothing to assert
// about return values — there are none. What can be checked is that the calls
// are safe to make from anywhere, repeatedly and concurrently, and that Release
// does what the window-hidden path claims it does.

func TestTuneIsSafeToCallRepeatedly(t *testing.T) {
	for i := 0; i < 10; i++ {
		Tune()
	}
}

func TestTrimIsSafeToCallRepeatedly(t *testing.T) {
	Tune()
	for i := 0; i < 50; i++ {
		Trim()
	}
}

// TestTrimFromManyGoroutines checks the documented promise that Trim is safe
// from any goroutine. malloc_trim takes the allocator's own lock, so this is
// really a check that nothing here deadlocks against itself.
func TestTrimFromManyGoroutines(t *testing.T) {
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				Trim()
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

// TestReleaseReturnsMemory allocates a large Go heap, drops it, and checks
// Release actually hands the pages back. This is what a minimised Atlas relies
// on to stop costing the system anything.
func TestReleaseReturnsMemory(t *testing.T) {
	Tune()

	// Build something big enough that the runtime must map fresh spans for it.
	const chunks, chunkSize = 64, 1 << 20 // 64 MiB
	held := make([][]byte, chunks)
	for i := range held {
		held[i] = make([]byte, chunkSize)
		for j := 0; j < chunkSize; j += 4096 {
			held[i][j] = byte(i) // touch every page so it is really resident
		}
	}
	peak := rssKB(t)
	held = nil
	runtime.KeepAlive(held)

	Release()
	after := rssKB(t)

	t.Logf("RSS %d kB at peak, %d kB after Release", peak, after)
	if after >= peak {
		t.Errorf("Release did not reduce RSS: %d kB -> %d kB", peak, after)
	}
	// The 64 MiB should be substantially gone, not merely dented.
	if freed := peak - after; freed < 32*1024 {
		t.Errorf("Release returned only %d kB of a 64 MiB allocation", freed)
	}
}

// TestReleaseIsSafeWhenThereIsNothingToRelease covers the common case: the
// window is hidden on an app that has been idle and has nothing to give back.
func TestReleaseIsSafeWhenThereIsNothingToRelease(t *testing.T) {
	Release()
	Release()
	Release()
}

// rssKB reads this process's resident set size.
func rssKB(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("cannot read /proc/self/status: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				break
			}
			n, err := strconv.Atoi(fields[0])
			if err != nil {
				break
			}
			return n
		}
	}
	t.Skip("VmRSS not reported")
	return 0
}
