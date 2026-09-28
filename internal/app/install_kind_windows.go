package app

// Updating on Windows.
//
// Atlas cannot update itself here, and it is worth being precise about why rather
// than treating it as a missing feature.
//
// The Linux path is to pull the source and rebuild, which needs make, a C
// compiler and a POSIX shell. A Windows machine that downloaded a packaged exe
// has no reason to have any of them. The other obvious route — download the new
// build and write it over this one — does not work either: Windows holds an
// executable's file locked while it is running, so a process cannot replace its
// own exe. Doing it properly means a helper that waits for Atlas to exit and then
// swaps the files, which is an installer, and an installer is a different piece of
// software from a system monitor.
//
// So the check works — it reads VERSION off the channel over HTTPS like every
// other install — and what it offers is the download page.

// canSelfInstall is false: see above.
const canSelfInstall = false

// platformWording replaces the package-manager and permission explanations, which
// describe a machine this is not.
func platformWording(Install) (heading, body string, ok bool) {
	return "Update from the website",
		"Atlas Monitor cannot replace itself on Windows: the file is locked while " +
			"it is running. Download the new version and unpack it over this one, " +
			"with Atlas closed.", true
}

// downloadPage is where the new build is. The dialog offers to open it.
//
// Not /releases/latest. GitHub resolves that to the newest release of either
// build, which is usually the full application's — so a minimal install would be
// sent to download the one with the assistant in it, the same trap the update
// channel is pinned against (see config.MinimalChannel). Minimal releases are
// tagged vX.Y.Z-minimal, and a search for them lists the newest first.
func downloadPage() string { return repoURL + "/releases?q=minimal&expanded=true" }
