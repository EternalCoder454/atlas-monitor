package process

import (
	"os"
	"testing"
)

// The two tests here are about the contract, not the mechanism: what a priority
// reads as, and what a pid that has gone reads as. Both platforms have to answer
// the same way, so they are not Linux-only — see nice_linux_test.go for the
// /proc/[pid]/stat parsing.

// TestNiceReadsOurOwn checks the field arithmetic against a process whose
// priority this test controls.
func TestNiceReadsOurOwn(t *testing.T) {
	got, ok := Nice(os.Getpid())
	if !ok {
		t.Fatal("could not read our own nice value")
	}
	if got < -20 || got > 19 {
		t.Errorf("nice = %d, which is outside the range the kernel allows", got)
	}
}

// TestNiceOnAMissingProcess: a pid that has gone should report that rather
// than a zero that reads as "normal priority".
func TestNiceOnAMissingProcess(t *testing.T) {
	if _, ok := Nice(1 << 30); ok {
		t.Error("a pid that does not exist reported a priority")
	}
}
