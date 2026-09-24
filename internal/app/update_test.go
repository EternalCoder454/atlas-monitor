package app

import (
	"os"
	"strings"
	"testing"
)

const sampleChangelog = `# What's new

Some preamble that is not part of any version.

## 0.8.2

- Atlas now tells you when an update is available
- You can turn that check off in Settings

## 0.8.1

- "End Task" can no longer close the wrong program by mistake
- Settings now warns you if the assistant would send your system details to
  another machine
- Your settings file is no longer readable by other users

## 0.8.0

- Fixed a memory leak on the Apps page
`

func TestChangelogFor(t *testing.T) {
	got := ChangelogFor(sampleChangelog, "0.8.2")
	want := []string{
		"Atlas now tells you when an update is available",
		"You can turn that check off in Settings",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d bullets, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bullet %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestChangelogJoinsWrappedBullets covers an entry written across two lines in
// the file: the prompt has to show it as one sentence, not two fragments.
func TestChangelogJoinsWrappedBullets(t *testing.T) {
	got := ChangelogFor(sampleChangelog, "0.8.1")
	if len(got) != 3 {
		t.Fatalf("got %d bullets, want 3: %q", len(got), got)
	}
	const want = "Settings now warns you if the assistant would send your system details to another machine"
	if got[1] != want {
		t.Errorf("wrapped bullet = %q, want %q", got[1], want)
	}
}

// TestChangelogStopsAtTheNextVersion is the bug worth guarding: a section must
// not bleed into the one below it, or the prompt would list changes the user
// already has.
func TestChangelogStopsAtTheNextVersion(t *testing.T) {
	for _, v := range []string{"0.8.2", "0.8.1", "0.8.0"} {
		for _, line := range ChangelogFor(sampleChangelog, v) {
			if v == "0.8.2" && strings.Contains(line, "End Task") {
				t.Errorf("%s picked up a bullet from 0.8.1", v)
			}
			if v == "0.8.1" && strings.Contains(line, "memory leak") {
				t.Errorf("%s picked up a bullet from 0.8.0", v)
			}
		}
	}
}

// TestChangelogHandlesWhatItCannotParse checks the prompt degrades to showing
// just the version rather than inventing something.
func TestChangelogHandlesWhatItCannotParse(t *testing.T) {
	cases := []struct{ name, md, version string }{
		{"unknown version", sampleChangelog, "9.9.9"},
		{"empty version", sampleChangelog, ""},
		{"empty file", "", "0.8.2"},
		{"no bullets", "## 0.8.2\n\nJust prose, no list.\n", "0.8.2"},
		{"not markdown", "\x00\x01 binary", "0.8.2"},
	}
	for _, c := range cases {
		if got := ChangelogFor(c.md, c.version); len(got) != 0 {
			t.Errorf("%s: got %q, want nothing", c.name, got)
		}
	}
}

// TestChangelogIsCapped stops one enormous release from filling the screen.
func TestChangelogIsCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("## 1.0.0\n\n")
	for i := 0; i < 40; i++ {
		b.WriteString("- change\n")
	}
	if got := ChangelogFor(b.String(), "1.0.0"); len(got) != maxChangelogBullets {
		t.Errorf("got %d bullets, want the cap of %d", len(got), maxChangelogBullets)
	}
}

// TestShippedChangelogParses checks the file in the repository actually yields
// bullets for the version being shipped — the prompt is only as good as this.
func TestShippedChangelogParses(t *testing.T) {
	md, err := readRepoFile("../../CHANGELOG.md")
	if err != nil {
		t.Skipf("cannot read CHANGELOG.md: %v", err)
	}
	version, err := readRepoFile("../../VERSION")
	if err != nil {
		t.Skipf("cannot read VERSION: %v", err)
	}
	v := strings.TrimSpace(version)
	got := ChangelogFor(md, v)
	if len(got) == 0 {
		t.Errorf("CHANGELOG.md has no entry for the current version %q; the update prompt would show an empty list", v)
	}
	for _, line := range got {
		if len(line) > 110 {
			t.Errorf("changelog line is %d characters, too long for the prompt: %q", len(line), line)
		}
	}
	t.Logf("version %s: %d bullets", v, len(got))
}

// readRepoFile reads a file relative to this package, for the tests that check
// the repository's own changelog rather than a fixture.
func readRepoFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
