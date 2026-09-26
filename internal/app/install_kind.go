package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// How this copy of Atlas got onto the machine, and therefore what "update"
// means for it.
//
// This used to be a yes/no question — is there a source checkout to pull and
// rebuild in? — and everything that was not a checkout got the same answer:
// "install with `make install`". On an Arch machine that answer is worse than
// unhelpful. The copy was installed by pacman, it is not broken, and following
// the advice would build a second one into ~/.local that shadows the packaged
// binary on PATH and is never updated again by anything.
//
// So the three cases are told apart, and each gets the update that is actually
// correct for it: pull and rebuild, hand it to the package manager, or fetch the
// source first because there is none yet.

// InstallKind is one of the three ways Atlas can be installed.
type InstallKind int

const (
	// FromSource is a checkout that `make install` recorded. Updating it means
	// pulling and rebuilding, which is what scripts/update.sh does.
	FromSource InstallKind = iota

	// FromPackage means a distribution package owns the binary. Updating is the
	// package manager's job: writing over /usr/bin ourselves would leave its
	// database describing a file that is no longer there, and the next time the
	// real package was upgraded or removed it would act on the wrong thing.
	FromPackage

	// Standalone is a release tarball, or a copy built and moved by hand.
	// Nothing owns it and nothing recorded where it came from, so updating means
	// fetching the source before anything else can happen.
	Standalone
)

// Install describes this copy of Atlas: how it got here, and what updating it
// involves.
type Install struct {
	Kind InstallKind

	// Source is the checkout to pull and rebuild in, when there is one.
	Source string
	// Binary is the running executable with symlinks resolved, which is what the
	// package managers are asked about.
	Binary string

	// Manager is the command that owns updates when Kind is FromPackage —
	// "pacman", "apt", "dnf", "zypper" — and Package is the name it knows this
	// install by.
	Manager string
	Package string
}

// Managed says whether something other than Atlas is responsible for updating
// it. The Update button turns into instructions rather than a build.
func (in Install) Managed() bool { return in.Kind == FromPackage }

// Where is a short phrase for the About section: how this copy got here, not
// just a path. A bare "/usr/bin/atlas-monitor" does not tell anyone that pacman
// put it there and pacman will replace it.
func (in Install) Where() string {
	switch in.Kind {
	case FromSource:
		return in.Source
	case FromPackage:
		return "installed by " + in.Manager + " — " + in.Binary
	default:
		if in.Binary == "" {
			return "unknown"
		}
		return in.Binary
	}
}

// Prefix is the install root the binary sits under — /usr for a packaged copy,
// ~/.local for the usual `make install`, /usr/local for a system-wide tarball.
// It is what a rebuild has to install back into: building a new version into a
// different prefix leaves two copies, and which one launches then depends on the
// order of PATH.
func (in Install) Prefix() string {
	if in.Binary == "" {
		return ""
	}
	// .../bin/atlas-monitor → ...
	return filepath.Dir(filepath.Dir(in.Binary))
}

// SelfUpdatable says whether Atlas can install over itself: it needs somewhere
// it is allowed to write, and no package manager keeping track of what is there.
func (in Install) SelfUpdatable() bool {
	switch in.Kind {
	case FromSource:
		return true
	case FromPackage:
		return false
	default:
		return writable(filepath.Join(in.Prefix(), "bin"))
	}
}

// writable reports whether we may create files in a directory. Permission bits
// are not enough to go on — a read-only mount, or a directory owned by root on a
// system where this user is not in a group that matters, both look writable — so
// it is settled by trying.
func writable(dir string) bool {
	if dir == "" {
		return false
	}
	f, err := os.CreateTemp(dir, ".atlas-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// UpdateCommand is what the user should run to update a packaged install, or ""
// when Atlas updates itself.
//
// For everything except Arch this is one line, because the package is in a
// repository the system already tracks. Arch is the awkward one: Atlas is built
// from its own PKGBUILD, so `pacman -Syu` only knows about it if it came from
// the AUR. An AUR helper is used when one is installed, and otherwise the recipe
// that always works — fetch the newer PKGBUILD and build it — is spelled out
// rather than a command that would answer "target not found".
func (in Install) UpdateCommand() string {
	if in.Kind != FromPackage {
		return ""
	}
	return updateCommand(in.Manager, in.Package, which(aurHelpers...))
}

// updateCommand is UpdateCommand with the machine taken out of it, so that what
// each package manager is told to do can be checked without that manager being
// installed. aurHelper is the AUR wrapper that is available, or "".
func updateCommand(manager, pkg, aurHelper string) string {
	if !safePackageName(pkg) {
		// The name is composed into a command line the user can have run in a
		// terminal, so anything that is not a plain package name is replaced by
		// the one we already know rather than passed along. No real package
		// manager produces such a name; this is here so that none of them has to
		// be trusted not to.
		pkg = binaryName
	}
	switch manager {
	case "pacman":
		if aurHelper != "" {
			return aurHelper + " -Syu " + pkg
		}
		// Plain pacman cannot upgrade a package it does not have a repository
		// for, and `pacman -Syu atlas-monitor-minimal` would answer "target not
		// found". Rebuilding from the newer PKGBUILD is what actually works.
		return packageRecipe
	case "apt":
		return "sudo apt update && sudo apt install --only-upgrade " + pkg
	case "dnf":
		return "sudo dnf upgrade " + pkg
	case "yum":
		return "sudo yum update " + pkg
	case "zypper":
		return "sudo zypper update " + pkg
	case "":
		return ""
	default:
		return manager + " " + pkg
	}
}

// binaryName is what the executable is called. The package is
// atlas-monitor-minimal, but pacman tells us that itself.
const binaryName = "atlas-monitor"

// packageRecipe rebuilds and reinstalls the Arch package from source, for a copy
// pacman owns but has no repository for.
//
// The branch is named explicitly: this is the minimal build, and the PKGBUILD on
// the default branch builds the full application, so cloning without -b would
// quietly replace a minimal install with one that has the assistant in it.
const packageRecipe = "git clone -b minimal " + repoURL +
	".git && cd atlas-monitor/packaging && makepkg -si"

// safePackageName accepts the characters package names are actually made of.
// Everything a shell would read as syntax is therefore rejected.
func safePackageName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '+':
		default:
			return false
		}
	}
	return true
}

// aurHelpers are the wrappers that can upgrade an AUR package, in the order
// they are preferred. Plain pacman cannot: an AUR package is built locally and
// is in no repository pacman syncs.
var aurHelpers = []string{"paru", "yay", "pikaur", "trizen", "aura"}

// install returns how this copy was installed, working it out once.
//
// The answer cannot change while the process runs — the binary is already open
// and the recorded checkout is read at startup — and finding it out runs two or
// three package-manager queries, so it is not worth doing twice. It is asked
// both from the main loop and from the goroutine the Settings dialog checks for
// updates on, hence the Once: it also gives readers the happens-before they need
// to see a fully written value.
func (a *App) install() Install {
	a.installOnce.Do(func() { a.installed = detectInstall() })
	return a.installed
}

// pinInstall fixes the answer without detecting it, for tests that must not
// depend on where the test binary happens to live.
func (a *App) pinInstall(in Install) {
	a.installed = in
	a.installOnce.Do(func() {})
}

// looksLikeCheckout reports whether a recorded path is still a source tree.
//
// `make install` writes the path once and nothing updates it afterwards, so a
// checkout that has since been moved or deleted leaves a path pointing at
// nothing. Believing it would send the updater to build in a directory that is
// not there; disbelieving it falls back to the version check, which still works.
func looksLikeCheckout(dir string) bool {
	// scripts/update.sh first: it is the thing the updater actually runs, so a
	// tree that has it is usable whether or not the rest looks familiar.
	for _, marker := range []string{filepath.Join("scripts", "update.sh"), "Makefile", ".git"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func detectInstall() Install {
	var in Install

	// The binary is found first and kept whatever the answer turns out to be: a
	// rebuild has to install back over the copy that is running, so its prefix
	// matters even for a checkout that knows where its source is.
	if exe, err := os.Executable(); err == nil {
		// Resolved, because the package managers know the real path: on a distro
		// that puts /bin behind a symlink to /usr/bin, asking about the
		// unresolved path gets "no package owns this".
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		in.Binary = exe
	}

	// A recorded checkout wins, but only if it is still there: `make install`
	// writes the path once and nothing updates it if the directory is later
	// moved or deleted, and a stale path would send the updater to build in a
	// directory that is not there any more.
	if src := sourceDir(); src != "" && looksLikeCheckout(src) {
		in.Kind, in.Source = FromSource, src
		return in
	}

	if in.Binary != "" {
		if mgr, pkg, ok := packageOwner(in.Binary); ok {
			in.Kind, in.Manager, in.Package = FromPackage, mgr, pkg
			return in
		}
	}

	in.Kind = Standalone
	return in
}

// packageOwner asks whichever package managers are installed whether any of them
// owns a path.
//
// Every one of these is the local database lookup the manager already has, so it
// is a few milliseconds and never touches the network. The query tool and the
// command that performs updates are not always the same program — rpm answers
// for dnf, yum and zypper alike — so ownership and the update command are found
// separately.
func packageOwner(path string) (manager, pkg string, ok bool) {
	for _, q := range ownerQueries {
		if which(q.tool) == "" {
			continue
		}
		out, err := exec.Command(q.tool, append(q.args, path)...).Output()
		if err != nil {
			continue // not owned by this one, or it could not say
		}
		name := q.name(strings.TrimSpace(string(out)))
		if name == "" {
			continue
		}
		return q.updater(), name, true
	}
	return "", "", false
}

var ownerQueries = []struct {
	tool string
	args []string
	// name pulls the package name out of what the tool printed.
	name func(string) string
	// updater is the command that actually performs updates on this system,
	// which may not be the tool that answered.
	updater func() string
}{
	{
		// "/usr/bin/atlas-monitor is owned by atlas-monitor 0.11.0-1"
		tool: "pacman", args: []string{"-Qo"},
		name: func(s string) string {
			_, after, found := strings.Cut(s, " is owned by ")
			if !found {
				return ""
			}
			return firstField(after)
		},
		updater: func() string { return "pacman" },
	},
	{
		// "atlas-monitor: /usr/bin/atlas-monitor"
		tool: "dpkg-query", args: []string{"-S"},
		name: func(s string) string {
			before, _, found := strings.Cut(s, ":")
			if !found {
				return ""
			}
			// A path can be listed by more than one package; the first line is
			// the one that owns this file.
			return firstField(before)
		},
		updater: func() string { return "apt" },
	},
	{
		// The name on its own, because that is what is asked for.
		tool: "rpm", args: []string{"-qf", "--queryformat", "%{NAME}"},
		name: func(s string) string { return firstField(s) },
		// rpm is the database for several distributions with different front
		// ends, so the updater is whichever of them is installed.
		updater: func() string {
			for _, m := range []string{"dnf", "zypper", "yum"} {
				if which(m) != "" {
					return m
				}
			}
			return "rpm"
		},
	},
}

// firstField is the first whitespace-separated word, or "".
func firstField(s string) string {
	if i := strings.IndexFunc(strings.TrimSpace(s), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n'
	}); i >= 0 {
		return strings.TrimSpace(s)[:i]
	}
	return strings.TrimSpace(s)
}

// which returns the first of names that is on PATH, or "".
func which(names ...string) string {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return n
		}
	}
	return ""
}
