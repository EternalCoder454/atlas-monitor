package app

import (
	"fmt"
	"os/exec"
	"strings"
)

// UpdateInfo is what the launch-time check found: whether the channel has
// something newer, which version that is, and what changed in words a person
// who does not build the app can read.
type UpdateInfo struct {
	Available bool
	Version   string   // "0.8.2", empty when it could not be read
	Changes   []string // changelog bullets for that version
	Summary   string   // one line, e.g. "Release update available: a5ffed5 → 762074b"
}

// maxChangelogBullets caps how much the update prompt shows. A release with
// thirty entries would fill the screen and stop being readable, and the point of
// the list is to answer "is this worth doing now".
const maxChangelogBullets = 8

// CheckUpdate reports what the channel is offering.
//
// How it finds out depends on how Atlas was installed. A source checkout is
// compared commit by commit, which is exact and notices work that has not been
// given a version number yet. Anything else — a packaged install, or a tarball —
// has no checkout to compare in, and asks the channel for its version instead.
// Both change nothing on disk.
func (a *App) CheckUpdate(channel string) (UpdateInfo, error) {
	in := a.install()
	if in.Kind != FromSource {
		return a.checkRemote(channel)
	}

	var info UpdateInfo
	src := in.Source
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", src}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("rev-parse", "--git-dir"); err != nil {
		// A recorded directory that is not a checkout can still be asked about
		// by version, which is better than refusing to look.
		return a.checkRemote(channel)
	}
	if _, err := git("fetch", "--quiet", "origin", channel); err != nil {
		return info, fmt.Errorf("couldn't reach GitHub")
	}

	local, _ := git("rev-parse", "--short", "HEAD")
	remote, _ := git("rev-parse", "--short", "origin/"+channel)
	name := channelName(channel)

	// Up to date when origin/<channel> is already contained in HEAD.
	if exec.Command("git", "-C", src, "merge-base", "--is-ancestor", "origin/"+channel, "HEAD").Run() == nil {
		info.Summary = fmt.Sprintf("Up to date on %s (%s)", name, local)
		return info, nil
	}

	info.Available = true
	info.Summary = fmt.Sprintf("%s update available: %s → %s", name, local, remote)
	info.Version, _ = git("show", "origin/"+channel+":VERSION")
	info.Version = strings.TrimSpace(info.Version)
	if md, err := git("show", "origin/"+channel+":CHANGELOG.md"); err == nil {
		info.Changes = ChangelogFor(md, info.Version)
	}
	return info, nil
}

// ChangelogFor pulls the bullets for one version out of the changelog.
//
// The format is the one CHANGELOG.md uses: "## <version>" starts a section and
// lines beginning with "- " are its entries, which may wrap onto the next line.
// Anything it cannot make sense of yields no bullets rather than a guess — the
// prompt then simply shows the version, which is still useful.
func ChangelogFor(markdown, version string) []string {
	if version == "" {
		return nil
	}
	var out []string
	inSection := false
	for _, raw := range strings.Split(markdown, "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.HasPrefix(line, "## ") {
			if inSection {
				break // the next version's section: we are done
			}
			inSection = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == version
			continue
		}
		if !inSection {
			continue
		}
		switch {
		case strings.HasPrefix(line, "- "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		case strings.HasPrefix(line, "  ") && len(out) > 0 && strings.TrimSpace(line) != "":
			// A continuation of the previous bullet, wrapped in the source.
			out[len(out)-1] += " " + strings.TrimSpace(line)
		}
		if len(out) == maxChangelogBullets {
			break
		}
	}
	return out
}
