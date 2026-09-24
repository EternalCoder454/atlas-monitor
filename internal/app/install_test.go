package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"atlas-monitor/internal/config"
)

// TestUpdateTreatsPathsAsData is the regression for a shell injection in the
// updater. The command used to be assembled with fmt.Sprintf("%q"), which is Go
// quoting rather than shell quoting: $(...) and backticks survive it, and inside
// double quotes the shell expands both. The checkout path comes from a file in
// the user's data directory, so exploiting it needed write access to the home
// directory already — but a legitimate path containing '$' broke updating too.
//
// The script is now an argument to bash rather than text bash re-reads, so this
// drives the real install with a checkout whose name is shaped like the attack
// and checks that nothing but the script itself ran.
func TestUpdateTreatsPathsAsData(t *testing.T) {
	home := t.TempDir()
	proof := filepath.Join(home, "INJECTED")
	ran := filepath.Join(home, "RAN")

	// A checkout directory whose name is a command substitution.
	src := filepath.Join(home, "checkout$(touch "+proof+")")
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\ntouch '" + ran + "'\n"
	if err := os.WriteFile(filepath.Join(src, "scripts", "update.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// sourceDir reads the checkout location from the data directory.
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	if err := os.MkdirAll(filepath.Join(data, "atlas-monitor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "atlas-monitor", "source"), []byte(src+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := &App{settings: config.Settings{UpdateChannel: "main"}}
	a.installUpdate(func(string) {}, func(error) {})

	// The completion callback goes through the main loop, which is not running
	// here, so wait on what the script itself does.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ran); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the update script never ran; the path was not passed through intact")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := os.Stat(proof); err == nil {
		t.Fatal("the checkout path was executed as shell syntax: the updater is injectable")
	}
}

// TestUpdateWithoutACheckoutSaysSo covers the copy that was unpacked rather than
// installed from source. There is nothing to pull and nothing to build, and the
// dialog has to say so rather than spin forever on a build that never started.
func TestUpdateWithoutACheckoutSaysSo(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	var got error
	called := 0
	a := &App{}
	a.installUpdate(func(string) {}, func(err error) { got = err; called++ })

	if called != 1 {
		t.Fatalf("finished called %d times, want exactly 1", called)
	}
	if !errors.Is(got, errNoCheckout) {
		t.Errorf("got %v, want errNoCheckout", got)
	}
}

// TestUpdateLogTailIsReadable checks the failure text. The script sends all of
// its output to a log, so this is the only thing the dialog can show when a
// build fails — and showing it means cutting into the middle of a file.
func TestUpdateLogTailIsReadable(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if got := updateLogTail(200); got != "" {
		t.Errorf("no log at all should give %q, got %q", "", got)
	}

	dir := filepath.Join(state, "atlas-monitor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("building something… £ € — with multi-byte characters\n")
	}
	b.WriteString("make: *** [Makefile:12: install] Error 2\n")
	if err := os.WriteFile(filepath.Join(dir, "update.log"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	tail := updateLogTail(200)
	if !utf8.ValidString(tail) {
		t.Error("the tail was cut through the middle of a character")
	}
	if !strings.Contains(tail, "Error 2") {
		t.Errorf("the last line is the one that matters, and it is missing:\n%s", tail)
	}
	if len(tail) > 200+len("…\n") {
		t.Errorf("tail is %d bytes, over the %d budget", len(tail), 200)
	}
	// Whatever is shown starts at the beginning of a line.
	first := strings.SplitN(strings.TrimPrefix(tail, "…\n"), "\n", 2)[0]
	if first != "" && !strings.HasPrefix(first, "building") && !strings.HasPrefix(first, "make:") {
		t.Errorf("the tail opens mid-line: %q", first)
	}
}
