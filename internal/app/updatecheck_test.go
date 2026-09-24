package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// git runs a git command in dir, failing the test if it errors.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newUpstream builds a repository with two releases in it and returns its path.
func newUpstream(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("VERSION", "1.0.0\n")
	write("CHANGELOG.md", "# What's new\n\n## 1.0.0\n\n- The first one\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "1.0.0")

	write("VERSION", "1.1.0\n")
	write("CHANGELOG.md", "# What's new\n\n## 1.1.0\n\n- Something people can read\n- And a second thing\n\n## 1.0.0\n\n- The first one\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "1.1.0")
	return dir
}

// checkoutAt clones upstream and parks it on the given revision, so the clone is
// behind its own origin — which is exactly the state a user who has not updated
// is in.
func checkoutAt(t *testing.T, upstream, rev string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout")
	if out, err := exec.Command("git", "clone", "-q", upstream, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	gitIn(t, dir, "reset", "--hard", "-q", rev)
	return dir
}

// pointSourceAt writes the file `make install` leaves behind, which is how the
// app finds its own checkout.
func pointSourceAt(t *testing.T, checkout string) {
	t.Helper()
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "atlas-monitor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "atlas-monitor", "source"), []byte(checkout+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", data)
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// TestCheckUpdateFindsTheNewerRelease is the whole feature end to end, short of
// the dialog: a checkout that is a release behind must report the update, name
// the version, and carry the changelog people actually read.
func TestCheckUpdateFindsTheNewerRelease(t *testing.T) {
	requireGit(t)
	up := newUpstream(t)
	old := gitIn(t, up, "rev-parse", "HEAD~1")
	pointSourceAt(t, checkoutAt(t, up, old))

	var a App
	info, err := a.CheckUpdate("main")
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !info.Available {
		t.Fatal("no update reported from a checkout a release behind")
	}
	if info.Version != "1.1.0" {
		t.Errorf("Version = %q, want 1.1.0", info.Version)
	}
	want := []string{"Something people can read", "And a second thing"}
	if len(info.Changes) != len(want) {
		t.Fatalf("got %d changelog bullets, want %d: %q", len(info.Changes), len(want), info.Changes)
	}
	for i := range want {
		if info.Changes[i] != want[i] {
			t.Errorf("bullet %d = %q, want %q", i, info.Changes[i], want[i])
		}
	}
	if info.Summary == "" {
		t.Error("Summary is empty; the Settings row would show nothing")
	}
}

// TestCheckUpdateSaysNothingWhenCurrent covers the common case. An up-to-date
// machine must not be interrupted, so Available has to be false.
func TestCheckUpdateSaysNothingWhenCurrent(t *testing.T) {
	requireGit(t)
	up := newUpstream(t)
	pointSourceAt(t, checkoutAt(t, up, "HEAD"))

	var a App
	info, err := a.CheckUpdate("main")
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if info.Available {
		t.Error("an up-to-date checkout was told there is an update")
	}
	if !strings.Contains(info.Summary, "Up to date") {
		t.Errorf("Summary = %q, want it to say up to date", info.Summary)
	}
}

// TestCheckUpdateWithoutACheckout covers an install from the release tarball.
// There is nothing to pull, so the check has to fail quietly rather than
// pestering someone who cannot act on it.
func TestCheckUpdateWithoutACheckout(t *testing.T) {
	requireGit(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // no source file at all

	var a App
	info, err := a.CheckUpdate("main")
	if err == nil {
		t.Error("expected an error when the source location is unknown")
	}
	if info.Available {
		t.Error("an update was offered with no checkout to update")
	}
}

// TestCheckUpdateOnSomethingThatIsNotARepository covers a source path that
// exists but was, say, unpacked from a zip.
func TestCheckUpdateOnSomethingThatIsNotARepository(t *testing.T) {
	requireGit(t)
	pointSourceAt(t, t.TempDir())

	var a App
	info, err := a.CheckUpdate("main")
	if err == nil {
		t.Error("expected an error for a source directory that is not a checkout")
	}
	if info.Available {
		t.Error("an update was offered from a non-repository")
	}
}
