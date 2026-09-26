package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug these cover: on Arch, Atlas told the user "source location unknown —
// install with `make install`" and would not update. Every part of that was
// wrong. pacman had installed it, it was working, and running `make install`
// would have built a second copy into ~/.local that shadows the packaged binary
// on PATH and is never updated by anything again.

// TestUpdateCommandPerManager checks that each package manager is given a command
// that manager actually understands.
func TestUpdateCommandPerManager(t *testing.T) {
	for _, c := range []struct {
		name    string
		manager string
		pkg     string
		helper  string
		want    string
	}{
		{"pacman with an AUR helper", "pacman", "atlas-monitor", "paru",
			"paru -Syu atlas-monitor"},
		{"apt", "apt", "atlas-monitor", "",
			"sudo apt update && sudo apt install --only-upgrade atlas-monitor"},
		{"dnf", "dnf", "atlas-monitor", "", "sudo dnf upgrade atlas-monitor"},
		{"zypper", "zypper", "atlas-monitor", "", "sudo zypper update atlas-monitor"},
		{"yum", "yum", "atlas-monitor", "", "sudo yum update atlas-monitor"},
	} {
		if got := updateCommand(c.manager, c.pkg, c.helper); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPacmanWithoutAHelperDoesNotSuggestAnImpossibleCommand: Atlas is built from
// its own PKGBUILD, so unless it came from the AUR, `pacman -Syu atlas-monitor`
// answers "target not found". Handing someone that command is the same kind of
// mistake as telling them to run `make install`.
func TestPacmanWithoutAHelperDoesNotSuggestAnImpossibleCommand(t *testing.T) {
	got := updateCommand("pacman", "atlas-monitor", "")
	if strings.Contains(got, "pacman -Syu") {
		t.Errorf("got %q, which pacman cannot do for a package with no repository", got)
	}
	for _, want := range []string{"makepkg", "git clone"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to mention %q", got, want)
		}
	}
}

// TestNoCommandSuggestsMakeInstall is the regression that started all of this.
// `make install` is correct advice for a checkout and actively harmful for
// anything a package manager owns.
func TestNoCommandSuggestsMakeInstall(t *testing.T) {
	for _, mgr := range []string{"pacman", "apt", "dnf", "zypper", "yum", "unknown-mgr"} {
		for _, helper := range []string{"", "paru", "yay"} {
			got := updateCommand(mgr, "atlas-monitor", helper)
			if strings.Contains(got, "make install") {
				t.Errorf("%s (helper %q) was told to run make install: %q", mgr, helper, got)
			}
		}
	}
}

// TestUpdateCommandRejectsAnUnsafePackageName: the command can be handed to a
// terminal to run, so a name that a shell would read as syntax never reaches it.
// No real package manager emits one — this is so that none of them has to be
// trusted not to.
func TestUpdateCommandRejectsAnUnsafePackageName(t *testing.T) {
	for _, bad := range []string{
		"atlas; rm -rf ~",
		"atlas$(id)",
		"atlas`id`",
		"atlas && curl evil.example",
		"atlas\nrm -rf /",
		"atlas|tee /tmp/x",
		"",
	} {
		got := updateCommand("dnf", bad, "")
		if got != "sudo dnf upgrade atlas-monitor" {
			t.Errorf("package name %q produced %q; it should have fallen back to the known name", bad, got)
		}
	}
	// ...while the names packages really have are passed through.
	for _, good := range []string{"atlas-monitor", "atlas-monitor-git", "gtk4", "libadwaita", "go1.26", "a_b.c+d"} {
		if !safePackageName(good) {
			t.Errorf("safePackageName(%q) = false, want true", good)
		}
	}
}

// TestSelfUpdatableNeverIncludesAPackagedInstall: writing over /usr/bin behind
// pacman's back leaves its database describing a file that is no longer there.
func TestSelfUpdatableNeverIncludesAPackagedInstall(t *testing.T) {
	in := Install{Kind: FromPackage, Manager: "pacman", Package: "atlas-monitor", Binary: "/usr/bin/atlas-monitor"}
	if in.SelfUpdatable() {
		t.Error("a packaged install reported that Atlas may overwrite it")
	}
	if !in.Managed() {
		t.Error("a packaged install is managed by definition")
	}
}

// TestStandaloneIsSelfUpdatableWhereItCanWrite: a tarball unpacked into a home
// directory can be replaced in place; one unpacked into /usr/local by root cannot.
func TestStandaloneIsSelfUpdatableWhereItCanWrite(t *testing.T) {
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	in := Install{Kind: Standalone, Binary: filepath.Join(bin, "atlas-monitor")}
	if in.Prefix() != prefix {
		t.Fatalf("Prefix() = %q, want %q", in.Prefix(), prefix)
	}
	if !in.SelfUpdatable() {
		t.Error("a writable install was reported as not updatable")
	}

	// The same install with nowhere to write.
	if err := os.Chmod(bin, 0o500); err != nil {
		t.Skip("cannot make a directory read-only here")
	}
	t.Cleanup(func() { os.Chmod(bin, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory, so there is nothing to check")
	}
	if in.SelfUpdatable() {
		t.Error("an install with no write permission was reported as updatable")
	}
}

// TestPrefixIsTheInstallRoot: the rebuild installs back into this, so getting it
// wrong leaves two copies of Atlas and lets PATH decide which one runs.
func TestPrefixIsTheInstallRoot(t *testing.T) {
	for _, c := range []struct{ binary, want string }{
		{"/usr/bin/atlas-monitor", "/usr"},
		{"/usr/local/bin/atlas-monitor", "/usr/local"},
		{"/home/someone/.local/bin/atlas-monitor", "/home/someone/.local"},
		{"", ""},
	} {
		if got := (Install{Binary: c.binary}).Prefix(); got != c.want {
			t.Errorf("Prefix() for %q = %q, want %q", c.binary, got, c.want)
		}
	}
}

// TestWhereSaysWhoInstalledIt: "/usr/bin/atlas-monitor" on its own does not tell
// anyone that pacman put it there and pacman will replace it, which is the one
// thing the About section needs to convey.
func TestWhereSaysWhoInstalledIt(t *testing.T) {
	packaged := Install{Kind: FromPackage, Manager: "pacman", Binary: "/usr/bin/atlas-monitor"}
	where := packaged.Where()
	if !strings.Contains(where, "pacman") || !strings.Contains(where, "/usr/bin/atlas-monitor") {
		t.Errorf("Where() = %q, want it to name pacman and the path", where)
	}
	if strings.Contains(where, "make install") {
		t.Errorf("Where() = %q, which tells a packaged install to run make install", where)
	}

	source := Install{Kind: FromSource, Source: "/home/someone/src/atlas-monitor"}
	if source.Where() != "/home/someone/src/atlas-monitor" {
		t.Errorf("Where() = %q, want the checkout path", source.Where())
	}
	if (Install{Kind: Standalone}).Where() != "unknown" {
		t.Error("an install with no path at all should say unknown, not be blank")
	}
}

// TestManagedWordingExplainsItself: the dialog replaces an error, so it has to
// say who updates this copy and must not repeat the advice that caused the bug.
func TestManagedWordingExplainsItself(t *testing.T) {
	heading, body := managedWording(Install{
		Kind: FromPackage, Manager: "pacman", Binary: "/usr/bin/atlas-monitor",
	})
	if !strings.Contains(heading, "pacman") {
		t.Errorf("heading = %q, want it to name pacman", heading)
	}
	if !strings.Contains(body, "pacman") {
		t.Errorf("body = %q, want it to name pacman", body)
	}
	for _, phrase := range []string{"make install", "source location unknown", "can't update itself"} {
		if strings.Contains(heading+body, phrase) {
			t.Errorf("the wording still contains %q", phrase)
		}
	}

	// An install nobody owns but that cannot be written to is a different reason,
	// and says so rather than blaming a package manager that is not involved.
	_, body = managedWording(Install{Kind: Standalone, Binary: "/usr/local/bin/atlas-monitor"})
	if !strings.Contains(body, "/usr/local") {
		t.Errorf("body = %q, want it to name the prefix it cannot write to", body)
	}
	if strings.Contains(body, "package") {
		t.Errorf("body = %q, but no package manager is involved", body)
	}
}

// TestOwnerParsersReadRealOutput: each package manager says it differently, and a
// parser that gets it wrong makes a packaged install look unmanaged — straight
// back to the original bug.
func TestOwnerParsersReadRealOutput(t *testing.T) {
	for _, c := range []struct {
		tool, out, want string
	}{
		{"pacman", "/usr/bin/atlas-monitor is owned by atlas-monitor 0.11.0-1", "atlas-monitor"},
		{"dpkg-query", "atlas-monitor: /usr/bin/atlas-monitor", "atlas-monitor"},
		{"rpm", "atlas-monitor", "atlas-monitor"},
		{"rpm", "atlas-monitor\n", "atlas-monitor"},
		// Nothing recognisable must not become a package name.
		{"pacman", "error: No package owns /usr/bin/atlas-monitor", ""},
		{"pacman", "", ""},
	} {
		var parse func(string) string
		for _, q := range ownerQueries {
			if q.tool == c.tool {
				parse = q.name
			}
		}
		if parse == nil {
			t.Fatalf("no parser registered for %s", c.tool)
		}
		if got := parse(strings.TrimSpace(c.out)); got != c.want {
			t.Errorf("%s parsing %q gave %q, want %q", c.tool, c.out, got, c.want)
		}
	}
}

// TestStaleSourcePathIsNotBelieved: `make install` records the checkout once and
// nothing corrects it afterwards, so a checkout that was later deleted leaves a
// path pointing at nothing. Building there would fail; falling back to the
// version check still tells the user what is available.
func TestStaleSourcePathIsNotBelieved(t *testing.T) {
	if looksLikeCheckout(filepath.Join(t.TempDir(), "gone")) {
		t.Error("a path that does not exist was accepted as a checkout")
	}
	if looksLikeCheckout(t.TempDir()) {
		t.Error("an empty directory was accepted as a checkout")
	}

	withMakefile := t.TempDir()
	if err := os.WriteFile(filepath.Join(withMakefile, "Makefile"), []byte("all:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !looksLikeCheckout(withMakefile) {
		t.Error("a source tree with a Makefile was not accepted")
	}

	withGit := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withGit, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !looksLikeCheckout(withGit) {
		t.Error("a git checkout was not accepted")
	}
}

// TestTerminalPassesTheCommandAsAnArgument: the command must arrive as one
// argument, never spliced into a string that a second shell re-parses.
func TestTerminalPassesTheCommandAsAnArgument(t *testing.T) {
	cmd := "sudo dnf upgrade atlas-monitor"
	argv := terminal{"konsole", []string{"-e"}}.argv(cmd)

	if argv[0] != "-e" {
		t.Errorf("argv = %q, want it to start with the terminal's own flag", argv)
	}
	found := false
	for _, a := range argv {
		if a == cmd {
			found = true
		}
	}
	if !found {
		t.Errorf("argv = %q, want the command present as a single argument", argv)
	}
	// A terminal that takes the command with no flag at all still gets it.
	if argv := (terminal{"kitty", nil}).argv(cmd); argv[0] != "bash" {
		t.Errorf("argv = %q, want it to start with bash when there is no flag", argv)
	}
}
