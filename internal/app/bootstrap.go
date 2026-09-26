package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Fetching the source for a copy of Atlas that arrived without any.
//
// A release tarball unpacks a built binary, a stylesheet and a pile of icons.
// There is no checkout, so the updater had nothing to pull and told the user to
// run `make install` — advice that, followed, installs a second Atlas beside the
// first. Cloning the repository once turns that copy into one that updates
// itself from then on, which is the point of having the button at all.
//
// Rebuilding needs a toolchain, and the machine that unpacked a prebuilt tarball
// is exactly the machine that may not have one. That is checked before anything
// is downloaded, and what is missing is named along with the command that
// installs it, rather than letting the user discover it as a page of compiler
// errors in the failure log.

// bootstrapSource clones the source for an install that has none and records it
// as the checkout to update from, returning its path.
func bootstrapSource(channel string, status func(string)) (string, error) {
	if missing := missingBuildTools(); len(missing) > 0 {
		return "", missingToolsError(missing)
	}

	dir := filepath.Join(userDataDir(), "src")
	if isCheckout(dir) {
		// A previous update already fetched it; update.sh does the rest.
		return dir, recordSource(dir)
	}
	// Anything else in the way is not a checkout and cannot be pulled into one.
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return "", fmt.Errorf("couldn't clear %s: %w", dir, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}

	status("Fetching the source…\nThis happens once; later updates are quicker.")
	cmd := exec.Command("git", "clone", "--branch", channel, repoURL+".git", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir) // a half-finished clone would be taken for a checkout
		return "", fmt.Errorf("couldn't download the source: %s", lastLine(string(out)))
	}
	return dir, recordSource(dir)
}

// isCheckout reports whether dir is a git working tree we can pull in.
func isCheckout(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !(fi.IsDir() || fi.Mode().IsRegular()) {
		return false
	}
	return exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run() == nil
}

// recordSource writes the checkout path where sourceDir reads it, so the next
// update — and the About section — find it without cloning again.
func recordSource(dir string) error {
	base := userDataDir()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, "source"), []byte(dir+"\n"), 0o644)
}

// buildTool is one thing a rebuild needs, and how to tell whether it is there.
type buildTool struct {
	// label is what the user is told is missing.
	label string
	// alternatives are commands that would satisfy it; any one will do.
	alternatives []string
	// packages names it per package manager, for the install hint.
	packages map[string]string
}

// buildTools is what building Atlas needs beyond the source. cgo is not
// optional — the GTK4 bindings are cgo, and so is the malloc_trim the memory
// page calls — so a C compiler and the development packages are as necessary as
// the Go compiler.
var buildTools = []buildTool{
	{"git", []string{"git"}, map[string]string{
		"pacman": "git", "apt": "git", "dnf": "git", "zypper": "git", "yum": "git",
	}},
	{"the Go compiler", []string{"go"}, map[string]string{
		"pacman": "go", "apt": "golang", "dnf": "golang", "zypper": "go", "yum": "golang",
	}},
	{"a C compiler", []string{"cc", "gcc", "clang"}, map[string]string{
		"pacman": "base-devel", "apt": "build-essential", "dnf": "gcc",
		"zypper": "gcc", "yum": "gcc",
	}},
	{"pkg-config", []string{"pkg-config", "pkgconf"}, map[string]string{
		"pacman": "pkgconf", "apt": "pkg-config", "dnf": "pkgconf-pkg-config",
		"zypper": "pkg-config", "yum": "pkgconfig",
	}},
}

// devLibraries are the libraries Atlas links against, named as pkg-config knows
// them. A prebuilt tarball runs against the runtime packages alone, so these are
// routinely absent on exactly the installs that need bootstrapping.
var devLibraries = []struct {
	pc       string
	label    string
	packages map[string]string
}{
	{"gtk4", "the GTK 4 development files", map[string]string{
		"pacman": "gtk4", "apt": "libgtk-4-dev", "dnf": "gtk4-devel",
		"zypper": "gtk4-devel", "yum": "gtk4-devel",
	}},
	{"libadwaita-1", "the libadwaita development files", map[string]string{
		"pacman": "libadwaita", "apt": "libadwaita-1-dev", "dnf": "libadwaita-devel",
		"zypper": "libadwaita-devel", "yum": "libadwaita-devel",
	}},
}

// missingBuildTools returns what is needed and not present, as label/package
// pairs. An empty result means a build can be attempted.
func missingBuildTools() []buildTool {
	var missing []buildTool
	for _, t := range buildTools {
		if which(t.alternatives...) == "" {
			missing = append(missing, t)
		}
	}
	// Without pkg-config there is no point asking it about libraries; it is
	// already on the list and the hint will name it.
	if which("pkg-config", "pkgconf") == "" {
		return missing
	}
	pc := which("pkg-config", "pkgconf")
	for _, l := range devLibraries {
		if exec.Command(pc, "--exists", l.pc).Run() != nil {
			missing = append(missing, buildTool{label: l.label, packages: l.packages})
		}
	}
	return missing
}

// setupError is something the user has to install before an update can be
// built. It is not a failure of the update — nothing was changed and nothing is
// broken — so the dialog shows it as instructions rather than as an error with a
// build log folded underneath.
type setupError struct{ msg string }

func (e *setupError) Error() string { return e.msg }

// missingToolsError says what is missing and how to get it, in one message the
// update dialog can show as it stands.
func missingToolsError(missing []buildTool) error {
	labels := make([]string, len(missing))
	for i, m := range missing {
		labels[i] = m.label
	}

	msg := "Atlas builds the new version from source, and this system is missing " +
		joinWords(labels) + "."

	if cmd := installHint(missing); cmd != "" {
		msg += "\n\nInstall them with:\n" + cmd
	}
	return &setupError{msg}
}

// installHint is the command that installs the missing packages, for whichever
// package manager this system has. It returns "" when none is recognised, and
// the message then simply says what is missing.
func installHint(missing []buildTool) string {
	for _, m := range installers {
		tool := m.key
		if tool == "apt" {
			tool = "apt-get"
		}
		if which(m.key, tool) == "" {
			continue
		}
		return installHintFor(m.key, missing)
	}
	return ""
}

// installers are the package managers that can install the build dependencies,
// and the command that does it.
var installers = []struct {
	key string
	cmd string
}{
	{"pacman", "sudo pacman -S --needed"},
	{"apt", "sudo apt install"},
	{"dnf", "sudo dnf install"},
	{"zypper", "sudo zypper install"},
	{"yum", "sudo yum install"},
}

// installHintFor is installHint for a named manager, so the wording for each one
// can be checked on a machine that does not have it.
func installHintFor(manager string, missing []buildTool) string {
	var cmd string
	for _, m := range installers {
		if m.key == manager {
			cmd = m.cmd
			break
		}
	}
	if cmd == "" {
		return ""
	}
	var pkgs []string
	for _, want := range missing {
		if p := want.packages[manager]; p != "" && !contains(pkgs, p) {
			pkgs = append(pkgs, p)
		}
	}
	if len(pkgs) == 0 {
		return ""
	}
	return cmd + " " + strings.Join(pkgs, " ")
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// joinWords lists things the way a sentence would: "a, b and c".
func joinWords(s []string) string {
	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	case 2:
		return s[0] + " and " + s[1]
	default:
		return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
	}
}

// lastLine is the final non-blank line of command output, which is where git
// puts the reason it gave up.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return "no output"
}
