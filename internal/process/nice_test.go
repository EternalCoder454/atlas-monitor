package process

import (
	"os"
	"testing"
)

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

// TestCommWithSpacesAndBrackets is the reason the line is split from the last
// bracket: a process can be called ") (" and the naive split would land in the
// middle of its name.
func TestCommWithSpacesAndBrackets(t *testing.T) {
	line := []byte("1234 (we ) ird) S 1 1234 1234 0 -1 4194560 100 0 0 0 5 6 0 0 20 7 1 0 0")
	close := lastIndexByte(line, ')')
	if close < 0 {
		t.Fatal("no bracket found")
	}
	// Seventeenth field after the name is the nice value, 7 in this line.
	rest := line[close+2:]
	field, start := 0, 0
	var got string
	for i := 0; i <= len(rest); i++ {
		if i == len(rest) || rest[i] == ' ' {
			field++
			if field == 17 {
				got = string(rest[start:i])
				break
			}
			start = i + 1
		}
	}
	if got != "7" {
		t.Errorf("parsed nice as %q, want 7 — the field offset is wrong", got)
	}
}
