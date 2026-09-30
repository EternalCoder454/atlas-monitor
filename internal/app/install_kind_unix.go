//go:build unix

package app

// canSelfInstall is true here: pulling the source and running
// scripts/update.sh is exactly how a Linux install of Atlas updates itself.
const canSelfInstall = true

// platformWording has nothing to add on Linux; the package manager or the
// read-only prefix is the whole story. See managedWording.
func platformWording(Install) (heading, body string, ok bool) { return "", "", false }

// downloadPage is empty on Linux: there is always either a package manager to
// name or a rebuild to offer, so nothing needs to send the user to a browser.
func downloadPage(string) string { return "" }
