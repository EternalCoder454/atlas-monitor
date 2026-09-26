package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallHintNamesTheRightPackages: a tarball install is exactly the machine
// with no toolchain on it, so the difference between a useful message and a
// useless one is whether it names the packages for the distribution in front of
// the user. gtk4-devel on Fedora is libgtk-4-dev on Debian and plain gtk4 on Arch.
func TestInstallHintNamesTheRightPackages(t *testing.T) {
	missing := []buildTool{
		{label: "the Go compiler", packages: map[string]string{
			"pacman": "go", "apt": "golang", "dnf": "golang",
		}},
		{label: "the GTK 4 development files", packages: map[string]string{
			"pacman": "gtk4", "apt": "libgtk-4-dev", "dnf": "gtk4-devel",
		}},
	}
	for _, c := range []struct{ manager, want string }{
		{"pacman", "sudo pacman -S --needed go gtk4"},
		{"apt", "sudo apt install golang libgtk-4-dev"},
		{"dnf", "sudo dnf install golang gtk4-devel"},
		{"nonesuch", ""},
	} {
		if got := installHintFor(c.manager, missing); got != c.want {
			t.Errorf("%s: got %q, want %q", c.manager, got, c.want)
		}
	}
}

// TestInstallHintDoesNotRepeatAPackage: base-devel covers the compiler and more,
// so two missing tools that come from one package must not ask for it twice.
func TestInstallHintDoesNotRepeatAPackage(t *testing.T) {
	missing := []buildTool{
		{label: "a C compiler", packages: map[string]string{"pacman": "base-devel"}},
		{label: "make", packages: map[string]string{"pacman": "base-devel"}},
	}
	if got := installHintFor("pacman", missing); got != "sudo pacman -S --needed base-devel" {
		t.Errorf("got %q, want base-devel named once", got)
	}
}

// TestMissingToolsReadsAsInstructions: this replaces a page of compiler errors, so
// it has to name what is missing and what to run, and be recognisable as setup
// rather than as a build that failed — the dialog shows the two differently.
func TestMissingToolsReadsAsInstructions(t *testing.T) {
	err := missingToolsError([]buildTool{
		{label: "the Go compiler", packages: map[string]string{"dnf": "golang"}},
		{label: "a C compiler", packages: map[string]string{"dnf": "gcc"}},
		{label: "the GTK 4 development files", packages: map[string]string{"dnf": "gtk4-devel"}},
	})

	var setup *setupError
	if !errors.As(err, &setup) {
		t.Fatalf("got %T, want a *setupError so the dialog can tell setup from a build failure", err)
	}
	msg := err.Error()
	for _, want := range []string{"the Go compiler", "a C compiler", "the GTK 4 development files"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q:\n%s", want, msg)
		}
	}
	// Listed as a sentence, not as a Go slice.
	if strings.Contains(msg, "[") || !strings.Contains(msg, " and ") {
		t.Errorf("the list does not read as prose:\n%s", msg)
	}
	if strings.Contains(msg, "make install") {
		t.Errorf("the message tells the user to run make install:\n%s", msg)
	}
}

func TestJoinWords(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"go"}, "go"},
		{[]string{"go", "gcc"}, "go and gcc"},
		{[]string{"go", "gcc", "gtk4"}, "go, gcc and gtk4"},
	} {
		if got := joinWords(c.in); got != c.want {
			t.Errorf("joinWords(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLastLine: git puts the reason it gave up at the end of its output, and that
// one line is what the dialog shows.
func TestLastLine(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Cloning into 'x'...\nfatal: could not read from remote repository\n", "fatal: could not read from remote repository"},
		{"one line", "one line"},
		{"trailing blanks\n\n\n", "trailing blanks"},
		{"", "no output"},
		{"\n\n", "no output"},
	} {
		if got := lastLine(c.in); got != c.want {
			t.Errorf("lastLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRecordSourceIsReadBackBySourceDir closes the loop that makes a bootstrapped
// install self-updating: what the clone writes has to be what the next launch
// reads, or it would clone again every single time.
func TestRecordSourceIsReadBackBySourceDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	const checkout = "/home/someone/.local/share/atlas-monitor/src"
	if err := recordSource(checkout); err != nil {
		t.Fatalf("recordSource: %v", err)
	}
	if got := sourceDir(); got != checkout {
		t.Errorf("sourceDir() = %q, want %q", got, checkout)
	}
}

// TestIsCheckoutRejectsAHalfFinishedClone: a directory that is not a working tree
// must not be taken for one, or the updater would try to pull in it forever.
func TestIsCheckoutRejectsAHalfFinishedClone(t *testing.T) {
	if isCheckout(filepath.Join(t.TempDir(), "nothing-here")) {
		t.Error("a missing directory was taken for a checkout")
	}
	empty := t.TempDir()
	if isCheckout(empty) {
		t.Error("an empty directory was taken for a checkout")
	}
	// A .git that is not a repository: what an interrupted clone can leave.
	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isCheckout(broken) {
		t.Error("a .git directory with nothing in it was taken for a checkout")
	}
}

// TestBootstrapReusesAnExistingCheckout: the clone happens once. A second update
// must find the source already there and go straight to pulling it, or every
// update would re-download the whole repository.
func TestBootstrapReusesAnExistingCheckout(t *testing.T) {
	requireGit(t)
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	src := filepath.Join(data, "atlas-monitor", "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, src, "init", "-q", "-b", "main")

	got, err := bootstrapSource("main", func(string) {
		t.Error("an existing checkout should not be downloaded again")
	})
	if err != nil {
		t.Fatalf("bootstrapSource: %v", err)
	}
	if got != src {
		t.Errorf("got %q, want the existing checkout %q", got, src)
	}
	// ...and it is recorded, so the next launch does not look for it again.
	if sourceDir() != src {
		t.Errorf("sourceDir() = %q, want %q", sourceDir(), src)
	}
}
