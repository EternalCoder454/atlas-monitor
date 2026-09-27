//go:build unix

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Permission bits are a Unix idea, so the test that checks them lives here.
//
// Save opens the file 0600 on every platform, but Windows does not apply Unix mode
// bits at all: os.Stat reports 0666 for anything writable there, whatever it was
// created with. The protection on Windows is the ACL the file inherits from
// %AppData%, which is already user-only — so there is nothing for this test to
// assert there, and asserting it anyway just failed on a file that was not actually
// exposed.

// TestSaveIsNotWorldReadable checks the settings file's mode. It carries the
// assistant's endpoint and system prompt, nothing else needs to read it, and on
// a shared machine there is no reason for other users to be able to.
func TestSaveIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "atlas-monitor", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("settings.json is mode %#o; group and other should have no access", mode)
	}

	// A rewrite must not widen it either — the rename has to carry the mode of
	// the file that was written, not of whatever was there before.
	if err := Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(filepath.Join(dir, "atlas-monitor", "settings.json"))
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("after a second save settings.json is mode %#o", mode)
	}
}
