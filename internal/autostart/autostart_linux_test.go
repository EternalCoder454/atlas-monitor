package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func find(entries []Entry, name string) (Entry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

func TestListReadsBothDirectories(t *testing.T) {
	sys, user := t.TempDir(), t.TempDir()
	write(t, sys, "steam.desktop", "[Desktop Entry]\nType=Application\nName=Steam\nComment=Games\nExec=steam -silent\n")
	write(t, sys, "plumbing.desktop", "[Desktop Entry]\nName=Bus Launcher\nExec=/usr/libexec/bus\nNoDisplay=true\n")
	write(t, user, "backup.desktop", "[Desktop Entry]\nName=Backup\nExec=backup --daemon\n")

	got := New2(sys, user, "KDE").List()
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	steam, ok := find(got, "Steam")
	if !ok {
		t.Fatal("Steam is missing")
	}
	if steam.Exec != "steam -silent" || !steam.System || steam.Disabled || steam.Plumbing {
		t.Errorf("Steam read as %+v", steam)
	}
	if p, _ := find(got, "Bus Launcher"); !p.Plumbing {
		t.Error("a NoDisplay entry should be marked as plumbing")
	}
	if b, _ := find(got, "Backup"); b.System {
		t.Error("an entry from the user directory is not a system one")
	}
	// Sorted by the name people read, not by filename.
	if got[0].Name != "Backup" {
		t.Errorf("first entry is %q; the list should be in name order", got[0].Name)
	}
}

func TestOnlyShowInDecidesWhetherItRunsHere(t *testing.T) {
	sys, user := t.TempDir(), t.TempDir()
	write(t, sys, "gnome-only.desktop", "[Desktop Entry]\nName=Gnome Thing\nExec=g\nOnlyShowIn=GNOME;Unity;\n")
	write(t, sys, "kde-only.desktop", "[Desktop Entry]\nName=Kde Thing\nExec=k\nOnlyShowIn=KDE;\n")
	write(t, sys, "not-kde.desktop", "[Desktop Entry]\nName=Not Kde\nExec=n\nNotShowIn=KDE;\n")
	write(t, sys, "anywhere.desktop", "[Desktop Entry]\nName=Anywhere\nExec=a\n")

	got := New2(sys, user, "KDE").List()
	for name, want := range map[string]bool{
		"Gnome Thing": false,
		"Kde Thing":   true,
		"Not Kde":     false,
		"Anywhere":    true,
	} {
		e, ok := find(got, name)
		if !ok {
			t.Fatalf("%s is missing", name)
		}
		if e.RunsHere != want {
			t.Errorf("%s: RunsHere = %v, want %v on KDE", name, e.RunsHere, want)
		}
	}
}

// TestUserFileShadowsSystemFile is the mechanism the whole feature rests on.
func TestUserFileShadowsSystemFile(t *testing.T) {
	sys, user := t.TempDir(), t.TempDir()
	write(t, sys, "steam.desktop", "[Desktop Entry]\nName=Steam\nComment=Games\nExec=steam -silent\n")
	write(t, user, "steam.desktop", "[Desktop Entry]\nName=Steam\nComment=Games\nExec=steam -silent\nHidden=true\n")

	got := New2(sys, user, "KDE").List()
	if len(got) != 1 {
		t.Fatalf("the same basename in both directories gave %d entries, want 1", len(got))
	}
	if !got[0].Disabled {
		t.Error("the user's file says Hidden=true, so the entry is off")
	}
}

func TestDisableWritesAnOverrideAndLeavesTheSystemFileAlone(t *testing.T) {
	sys, user := t.TempDir(), t.TempDir()
	original := "[Desktop Entry]\nType=Application\nName=Steam\nExec=steam -silent\n"
	sysFile := write(t, sys, "steam.desktop", original)

	steam, _ := find(New2(sys, user, "KDE").List(), "Steam")
	if err := setEnabledAt(steam, false, user); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if body, _ := os.ReadFile(sysFile); string(body) != original {
		t.Error("the system file was modified; it belongs to a package and an update would undo this")
	}
	override, err := os.ReadFile(filepath.Join(user, "steam.desktop"))
	if err != nil {
		t.Fatalf("no override was written: %v", err)
	}
	if !strings.Contains(string(override), "Hidden=true") {
		t.Errorf("the override does not disable anything:\n%s", override)
	}
	if !strings.Contains(string(override), "Exec=steam -silent") {
		t.Errorf("the override lost the original contents:\n%s", override)
	}

	// ...and it reads back as off.
	steam, _ = find(New2(sys, user, "KDE").List(), "Steam")
	if !steam.Disabled {
		t.Error("after disabling, the entry still reads as on")
	}

	// Re-enabling removes the override and hands the entry back to the system.
	if err := setEnabledAt(steam, true, user); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(user, "steam.desktop")); !os.IsNotExist(err) {
		t.Error("the override should be gone once the entry is on again")
	}
	steam, _ = find(New2(sys, user, "KDE").List(), "Steam")
	if steam.Disabled {
		t.Error("the entry is still off after being switched on")
	}
}

// TestDisableAUserEntryEditsItInPlace: there is no system file to fall back to,
// so the entry's own file carries the flag.
func TestDisableAUserEntryEditsItInPlace(t *testing.T) {
	sys, user := t.TempDir(), t.TempDir()
	write(t, user, "backup.desktop", "[Desktop Entry]\nName=Backup\nExec=backup\n")

	e, _ := find(New2(sys, user, "KDE").List(), "Backup")
	if err := setEnabledAt(e, false, user); err != nil {
		t.Fatal(err)
	}
	e, _ = find(New2(sys, user, "KDE").List(), "Backup")
	if !e.Disabled {
		t.Fatal("the user's own entry did not switch off")
	}
	if err := setEnabledAt(e, true, user); err != nil {
		t.Fatal(err)
	}
	e, _ = find(New2(sys, user, "KDE").List(), "Backup")
	if e.Disabled {
		t.Error("the user's own entry did not switch back on")
	}
	// Switching twice must not leave two Hidden keys behind.
	body, _ := os.ReadFile(filepath.Join(user, "backup.desktop"))
	if n := strings.Count(string(body), "Hidden="); n != 1 {
		t.Errorf("file has %d Hidden keys, want 1:\n%s", n, body)
	}
	if !strings.Contains(string(body), "Exec=backup") {
		t.Errorf("rewriting lost the entry's own contents:\n%s", body)
	}
}

// TestAStubOverrideKeepsTheSystemDescription: desktops sometimes write a
// minimal file with nothing but Hidden=true. The list should still show the
// program's real name rather than a blank row.
func TestAStubOverrideKeepsTheSystemDescription(t *testing.T) {
	sys, user := t.TempDir(), t.TempDir()
	write(t, sys, "steam.desktop", "[Desktop Entry]\nName=Steam\nComment=Games\nExec=steam -silent\n")
	write(t, user, "steam.desktop", "[Desktop Entry]\nHidden=true\n")

	got, ok := find(New2(sys, user, "KDE").List(), "Steam")
	if !ok {
		t.Fatal("a stub override hid the entry's name entirely")
	}
	if got.Exec != "steam -silent" || !got.Disabled {
		t.Errorf("read as %+v", got)
	}
}

// New2 is a test shorthand for a single system directory and one desktop.
func New2(sys, user, desktop string) *Reader {
	return NewAt([]string{sys}, user, []string{desktop})
}
